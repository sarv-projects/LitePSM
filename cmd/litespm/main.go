package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sarv-projects/litespm/internal/agent"
	"github.com/sarv-projects/litespm/internal/bridge"
	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/discover"
	"github.com/sarv-projects/litespm/internal/doctor"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/install"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/resolver"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
	"github.com/sarv-projects/litespm/internal/update"
)

// Version is the canonical LiteSPM version. Release builds override it via
// -ldflags "-X main.Version=<v>" (see scripts/build-release.sh). Keep this
// value in sync with npm/package.json — the npm postinstall downloads the
// release asset named after the npm package version.
var Version = "0.3.0"

const (
	ProtocolVersion = "2026-07-28"
)

func main() {
	if len(os.Args) < 2 {
		runInteractiveWizard()
		return
	}

	command := os.Args[1]

	switch command {
	case "version", "--version", "-v":
		fmt.Printf("LiteSPM v%s (Protocol %s, %s/%s, %s)\n", Version, ProtocolVersion, runtime.GOOS, runtime.GOARCH, runtime.Version())

	case "setup", "init":
		runInteractiveWizard()

	case "self-update", "update":
		runSelfUpdate(os.Args[2:])

	case "doctor":
		if code := runDoctor(os.Args[2:]); code != 0 {
			os.Exit(code)
		}

	case "search":
		query := ""
		if len(os.Args) >= 3 {
			query = strings.Join(os.Args[2:], " ")
		}
		runSearch(query)

	case "install", "add", "i":
		runInstall(os.Args[2:])

	case "catalog":
		if len(os.Args) >= 3 {
			switch os.Args[2] {
			case "sync":
				runCatalogSync()
			case "build":
				runCatalogBuild(os.Args[3:])
			default:
				fmt.Println("Usage: litespm catalog [sync|build]")
				os.Exit(1)
			}
		} else {
			fmt.Println("Usage: litespm catalog [sync|build]")
			os.Exit(1)
		}

	case "daemon":
		if len(os.Args) < 3 || os.Args[2] != "serve" {
			fmt.Println("Usage: litespm daemon serve")
			os.Exit(1)
		}
		runDaemonServe()

	case "bridge":
		runBridge(os.Args[2:])

	case "host":
		if len(os.Args) < 3 {
			fmt.Println("Usage: litespm host [list|detect|setup <host-id>|remove <host-id>|remove --all]")
			os.Exit(1)
		}
		runHostCommand(os.Args[2:])

	case "uninstall":
		runUninstall(context.Background(), os.Args[2:])

	case "agent":
		runAgentCommand(os.Args[2:])

	case "capabilities", "caps":
		runCapabilities(os.Args[2:])

	case "invoke":
		runInvoke(os.Args[2:])

	case "skills":
		if len(os.Args) < 3 {
			fmt.Println("Usage: litespm skills [add <source> | list | update <name>... --source <src> | remove <name>|--all]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "add":
			runSkillsAdd(os.Args[3:])
		case "list":
			runSkillsList(os.Args[3:])
		case "update":
			runSkillsUpdate(os.Args[3:])
		case "remove":
			runSkillsRemove(os.Args[3:])
		default:
			fmt.Printf("Unknown skills subcommand: %s\n", os.Args[2])
			fmt.Println("Usage: litespm skills [add <source> | list | update <name>... --source <src> | remove <name>|--all]")
			os.Exit(1)
		}

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`LiteSPM - The Lightweight Skill & Package Manager for AI Agents

Usage:
  litespm                     Run interactive agent setup wizard
  litespm <command> [args]    Execute specific subcommand

Available Commands:
  setup                       Interactive setup wizard for AI agent hosts
  search <query>              Search global catalog of MCP servers, skills, and plugins
  install <id>                Install a capability (skills install now; MCP/plugin pending artifact wiring)
  catalog sync                Synchronize latest catalog release from upstream
  catalog build               Build the static /v1 release tree from the dataset
  bridge stdio [--host h]     Launch stateless stdio MCP bridge shim for host agent
  host [list|detect|setup]    Manage agent host adapters (Codex, Claude, OpenCode, Cline, Pi, Grok)
  agent list [--json]         List installable ACP agents from the registry
  agent resolve <id>          Resolve an ACP agent launch spec for this host
  skills add <source>         Install SKILL.md skills (owner/repo, git URL, local dir)
  skills update <name>...     Update installed skills from a source (--source, --ref, --dry-run)
  skills remove <name>|--all  Remove installed skills recorded in the install ledger
  doctor [--repair]           Run 10-check diagnostic verification & optional auto-repair
  self-update [--force]       Check for and apply binary updates
  daemon serve                Start the LiteSPM background supervisor and IPC engine
  version                     Print version and build details
  help                        Show this help text

Documentation & Architecture:
  https://github.com/sarv-projects/litespm
`)
}

// defaultUpdateDownloadLimit bounds the binary download when the configuration
// does not supply a smaller limit. It mirrors the 64 MiB order of magnitude the
// updater uses for its own manifest reads and is intentionally generous enough
// for any supported platform binary.
const defaultUpdateDownloadLimit = 64 << 20

func selfUpdateUsage() {
	fmt.Println(`Usage: litespm self-update [--force]

Check the published release manifest for a newer binary. When one is available
it is downloaded from the release asset URL (never the release HTML page), its
SHA-256 checksum is verified, and the running executable is replaced atomically.

Flags:
  --force     reinstall or downgrade even when the manifest version is not newer
  --help, -h  show this help text`)
}

func runSelfUpdate(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		selfUpdateUsage()
		return
	}
	force := hasFlag(args, "--force")

	ctx := context.Background()
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	// The release manifest is the GitHub Releases API document published by the
	// release workflow. The catalog registry origin is a different service
	// (catalog metadata) and is never consulted for binaries. Configuration may
	// still bound the download size.
	maxDownload := int64(defaultUpdateDownloadLimit)
	if cfg, cfgErr := config.LoadConfig(""); cfgErr == nil && cfg != nil && cfg.Network.MaxDownloadSizeBytes > 0 {
		maxDownload = cfg.Network.MaxDownloadSizeBytes
	}

	u := update.NewUpdater(update.DefaultReleaseManifestURL)
	fmt.Printf("Checking for LiteSPM updates (current: v%s)...\n", Version)
	status, info, err := u.CheckForUpdateWithOptions(ctx, Version, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update check failed: %v\n", err)
		os.Exit(1)
	}

	if !status.UpdateAvailable {
		fmt.Printf("LiteSPM is already up to date (v%s is latest).\n", Version)
		return
	}

	// ReleaseURL is the human-facing release page; the binary and its checksum
	// live on the per-asset download URLs. Fail closed when either is missing
	// rather than downloading HTML bytes as if they were a binary.
	binName := update.TargetBinaryName()
	downloadURL, expectedChecksum, err := releaseDownloadTarget(info, binName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update aborted: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("New version available: v%s (current: v%s)\n", status.LatestVersion, Version)
	fmt.Printf("Release URL: %s\n", info.ReleaseURL)
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to locate the running executable: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Target binary: %s\n", execPath)

	stagingDir := paths.StagingPath()
	fmt.Printf("Downloading %s...\n", downloadURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create update request: %v\n", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download update: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Update download returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	payload, err := readBounded(resp.Body, maxDownload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read update payload: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Verifying SHA-256 checksum and applying atomic update...")
	if err := u.ApplyUpdate(ctx, payload, expectedChecksum, execPath, stagingDir); err != nil {
		fmt.Fprintf(os.Stderr, "Self-update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("LiteSPM updated to v%s.\n", status.LatestVersion)
}

// releaseDownloadTarget selects the platform binary URL and its mandatory
// checksum from a release document. Both are required: an update with no
// checksum must never be applied.
func releaseDownloadTarget(info *update.ReleaseInfo, binName string) (downloadURL, checksum string, err error) {
	if info == nil {
		return "", "", fmt.Errorf("release manifest returned no release information")
	}
	downloadURL = strings.TrimSpace(info.DownloadURLs[binName])
	if downloadURL == "" {
		return "", "", fmt.Errorf("release %s publishes no binary asset for %s", info.Version, binName)
	}
	checksum = strings.TrimSpace(info.ChecksumsSHA256[binName])
	if checksum == "" {
		return "", "", fmt.Errorf("release %s publishes no SHA-256 checksum for %s", info.Version, binName)
	}
	return downloadURL, checksum, nil
}

// readBounded reads at most limit bytes and refuses an oversized payload rather
// than truncating it into a corrupt binary.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid download limit %d", limit)
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download exceeds the %d byte limit", limit)
	}
	return data, nil
}

func runInteractiveWizard() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	w := NewWizard(os.Stdin, os.Stdout, paths)
	if err := w.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		os.Exit(1)
	}
}

func runBridge(args []string) {
	hostID := "generic"
	for i := 0; i < len(args); i++ {
		if args[i] == "--host" && i+1 < len(args) {
			hostID = args[i+1]
			i++
		}
	}

	paths, err := config.ResolvePlatformPaths()
	var client *ipc.Client
	if err != nil {
		// Standalone mode has no capability data; say so instead of letting a
		// silent path failure masquerade as a working bridge.
		fmt.Fprintf(os.Stderr, "litespm bridge: platform path resolution failed: %v; running standalone with no capabilities\n", err)
	} else {
		c, dialErr := ipc.Dial(paths.IPCEndpoint())
		if dialErr != nil {
			fmt.Fprintf(os.Stderr, "litespm bridge: daemon dial failed: %v; running standalone with no capabilities (start the daemon with 'litespm daemon serve')\n", dialErr)
		} else {
			client = c
			defer client.Close()
		}
	}

	shim := bridge.NewShim(hostID, client, os.Stdin, os.Stdout)
	if err := shim.Serve(context.Background()); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "Bridge error: %v\n", err)
		os.Exit(1)
	}
}

func runHostCommand(args []string) {
	ctx := context.Background()
	sub := args[0]

	switch sub {
	case "list":
		adapters := host.ListAdapters()
		fmt.Printf("Registered Agent Host Adapters (%d):\n\n", len(adapters))
		fmt.Printf("%-14s %-20s %-10s %s\n", "HOST ID", "DISPLAY NAME", "FORMAT", "CONFIG FILE")
		fmt.Println(strings.Repeat("-", 70))
		for _, a := range adapters {
			d := a.Descriptor()
			fmt.Printf("%-14s %-20s %-10s %s\n", d.HostID, d.DisplayName, d.ConfigFormat, d.DefaultConfigFileName)
		}

	case "detect":
		fmt.Println("Scanning for installed agent configurations...")
		verifs, err := host.DetectInstalledHosts(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Detection error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%-14s %-10s %-12s %s\n", "HOST ID", "STATUS", "REGISTERED", "CONFIG PATH")
		fmt.Println(strings.Repeat("-", 75))
		for _, v := range verifs {
			regStr := "no"
			if v.Registered {
				regStr = "yes"
			}
			fmt.Printf("%-14s %-10s %-12s %s\n", v.HostID, v.Status, regStr, v.ConfigPath)
		}

	case "setup":
		if len(args) < 2 {
			fmt.Println("Usage: litespm host setup <host-id>")
			os.Exit(1)
		}
		hostID := args[1]
		adapter, err := host.GetAdapter(hostID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		paths, _ := config.ResolvePlatformPaths()
		execPath, _ := os.Executable()
		plan, err := adapter.PlanSetup(ctx, execPath, paths.BackupsPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to plan setup: %v\n", err)
			os.Exit(1)
		}

		result, err := adapter.ApplySetup(ctx, plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to apply setup: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✓ Successfully configured %s:\n", adapter.Descriptor().DisplayName)
		fmt.Printf("  • Config File: %s\n", result.ConfigPath)
		if result.BackupPath != "" {
			fmt.Printf("  • Backup:      %s\n", result.BackupPath)
		}

	case "remove":
		runHostRemove(ctx, args[1:])

	default:
		fmt.Printf("Unknown host subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// runHostRemove takes the bridge entry back out of one host, or out of every
// host that currently has it. Installing is only defensible if uninstalling is
// equally easy.
func runHostRemove(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: litespm host remove <host-id> | --all")
		os.Exit(1)
	}

	paths, _ := config.ResolvePlatformPaths()

	if args[0] == "--all" || args[0] == "all" {
		results := host.RemoveFromAll(ctx, paths.BackupsPath())
		removed, absent, failed := 0, 0, 0
		for _, r := range results {
			switch {
			case r.Removed:
				removed++
				fmt.Printf("✓ removed from %s (%s)\n", r.HostID, r.ConfigPath)
			case r.Reason != "":
				absent++
				if r.ConfigPath == "" {
					failed++
					fmt.Printf("! %s: %s\n", r.HostID, r.Reason)
				}
			}
		}
		fmt.Printf("\n%d removed · %d had nothing to remove · %d could not be read\n",
			removed, absent-failed, failed)
		return
	}

	adapter, err := host.GetAdapter(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	result, err := host.RemoveSetup(ctx, adapter, paths.BackupsPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Removal failed: %v\n", err)
		os.Exit(1)
	}
	if result == nil || !result.Removed {
		reason := "nothing to do"
		if result != nil && result.Reason != "" {
			reason = result.Reason
		}
		fmt.Printf("○ %s was not modified: %s\n", adapter.Descriptor().DisplayName, reason)
		return
	}
	fmt.Printf("✓ Removed the LiteSPM bridge entry from %s\n", adapter.Descriptor().DisplayName)
	fmt.Printf("  • Config File: %s\n", result.ConfigPath)
	if result.BackupPath != "" {
		fmt.Printf("  • Backup:      %s\n", result.BackupPath)
	}
}

// runUninstall reverses everything LiteSPM wrote: the bridge entry in every
// host config it touched. Skill directories are reported rather than deleted,
// because a skills directory may contain files the user added alongside ours.
func runUninstall(ctx context.Context, args []string) {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" {
			dryRun = true
		}
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving paths: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Removing the LiteSPM bridge entry from every agent host config…")
	fmt.Println()

	// A dry run must not touch anything, so it plans without applying.
	var removed, untouched, failed int
	if dryRun {
		for _, adapter := range host.ListAdapters() {
			plan, result, err := host.PlanRemoval(ctx, adapter, paths.BackupsPath())
			switch {
			case err != nil:
				failed++
				fmt.Printf("  ! %-16s %v\n", adapter.Descriptor().HostID, err)
			case plan != nil && result != nil && result.Removed:
				removed++
				fmt.Printf("  - %-16s would remove from %s\n", adapter.Descriptor().HostID, result.ConfigPath)
			default:
				untouched++
			}
		}
	} else {
		for _, r := range host.RemoveFromAll(ctx, paths.BackupsPath()) {
			switch {
			case r.Removed:
				removed++
				fmt.Printf("  ✓ %-16s removed from %s\n", r.HostID, r.ConfigPath)
			case r.ConfigPath == "":
				failed++
				fmt.Printf("  ! %-16s %s\n", r.HostID, r.Reason)
			default:
				untouched++
			}
		}
	}

	fmt.Println()
	if dryRun {
		fmt.Printf("Dry run: %d host config(s) would be modified, %d had nothing to remove, %d could not be read.\n",
			removed, untouched, failed)
		fmt.Println("Re-run without --dry-run to apply.")
		return
	}
	fmt.Printf("Done: %d host config(s) cleaned, %d had nothing to remove, %d could not be read.\n",
		removed, untouched, failed)

	fmt.Println()
	fmt.Println("Still on disk:")
	fmt.Println("  • Skill directories copied by `litespm skills add`. They live inside each")
	fmt.Println("    agent's own skills tree and may hold files you added yourself, so this")
	fmt.Println("    command does not delete them. See what is tracked, then remove it:")
	fmt.Println("        litespm skills list")
	fmt.Println("        litespm skills remove --all")
	fmt.Println("  • Backups written next to each config. They are your restore points;")
	fmt.Println("    delete them yourself once you are satisfied.")
}

func runAgentCommand(args []string) {
	ctx := context.Background()

	if len(args) == 0 {
		fmt.Println("Usage: litespm agent [list|resolve <id>] [--registry <url>] [--file <path>] [--json]")
		os.Exit(1)
	}

	sub := args[0]
	registryURL := agent.RegistryURL
	filePath := ""
	jsonOut := false
	id := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--registry":
			if i+1 < len(args) {
				registryURL = args[i+1]
				i++
			}
		case "--file":
			if i+1 < len(args) {
				filePath = args[i+1]
				i++
			}
		case "--json":
			jsonOut = true
		default:
			if id == "" {
				id = args[i]
			}
		}
	}

	var (
		reg *agent.Registry
		err error
	)
	if filePath != "" {
		reg, err = agent.LoadRegistryFile(filePath)
	} else {
		reg, err = agent.FetchRegistry(ctx, nil, registryURL, agent.DefaultMaxRegistryBytes)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load ACP registry: %v\n", err)
		os.Exit(1)
	}

	adapter := agent.NewACPAdapter()
	target, hasTarget := agent.HostTarget()

	switch sub {
	case "list":
		if jsonOut {
			payload, _ := json.Marshal(reg.Agents)
			fmt.Println(string(payload))
			return
		}

		fmt.Printf("Installable ACP Agents (%d):\n\n", len(reg.Agents))
		fmt.Printf("%-22s %-22s %-12s %s\n", "ID", "NAME", "VERSION", "STRATEGY")
		fmt.Println(strings.Repeat("-", 76))
		for i := range reg.Agents {
			a := reg.Agents[i]
			strategy := "-"
			deprecated := agent.IsDeprecated(a.ID)
			if hasTarget {
				if spec, rerr := adapter.Resolve(a, target, ""); rerr == nil {
					strategy = spec.Strategy
				}
			}
			marker := ""
			if deprecated {
				marker = "  [deprecated]"
			}
			fmt.Printf("%-22s %-22s %-12s %s%s\n", a.ID, truncate(a.Name, 22), a.Version, strategy, marker)
		}

	case "resolve":
		if id == "" {
			fmt.Println("Usage: litespm agent resolve <id>")
			os.Exit(1)
		}
		a, ok := reg.FindAgent(id)
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown ACP agent: %s\n", id)
			os.Exit(1)
		}
		if !hasTarget {
			fmt.Fprintf(os.Stderr, "No ACP distribution target for %s/%s\n", runtime.GOOS, runtime.GOARCH)
			os.Exit(1)
		}

		paths, _ := config.ResolvePlatformPaths()
		binaryDir := filepath.Join(paths.DataRoot, "agents", a.ID)
		spec, rerr := adapter.Resolve(*a, target, binaryDir)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "Failed to resolve %s: %v\n", id, rerr)
			os.Exit(1)
		}

		if jsonOut {
			payload, _ := json.Marshal(spec)
			fmt.Println(string(payload))
			return
		}
		fmt.Printf("Agent:     %s (%s v%s)\n", a.Name, a.ID, a.Version)
		fmt.Printf("Target:    %s\n", target)
		fmt.Printf("Strategy:  %s\n", spec.Strategy)
		fmt.Printf("Executable:%s\n", spec.Executable)
		if len(spec.Args) > 0 {
			fmt.Printf("Args:      %s\n", strings.Join(spec.Args, " "))
		}
		if spec.Archive != "" {
			fmt.Printf("Archive:   %s\n", spec.Archive)
			fmt.Printf("SHA-256:   %s\n", spec.SHA256)
		}
		if len(spec.Env) > 0 {
			fmt.Printf("Env:       %d variable(s)\n", len(spec.Env))
		}
		if spec.Deprecated {
			fmt.Println("Warning:   this agent is deprecated upstream")
		}
		for _, note := range spec.Notes {
			fmt.Printf("Note:      %s\n", note)
		}

	default:
		fmt.Printf("Unknown agent subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func runSearch(query string) {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := config.DefaultRegistryURL
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	results := catClient.Search(query, catalog.SearchOptions{Limit: 25})

	if len(results) == 0 {
		if catClient.Count() == 0 {
			fmt.Printf("No capabilities found: the local catalog index is empty.\n")
			fmt.Println("Run 'litespm catalog sync' to populate it from the registry.")
			return
		}
		fmt.Printf("No capabilities found matching %q.\n", query)
		return
	}

	fmt.Printf("Found %d capabilities matching %q:\n\n", len(results), query)
	fmt.Printf("%-32s %-8s %-16s %s\n", "NAME / ID", "KIND", "PUBLISHER", "SUMMARY")
	fmt.Println(strings.Repeat("-", 80))
	for _, res := range results {
		publisher := res.Listing.PublisherClaim.Name
		if publisher == "" {
			publisher = "community"
		}
		summary := res.Listing.Summary
		if len(summary) > 40 {
			summary = summary[:37] + "..."
		}
		fmt.Printf("%-32s %-8s %-16s %s\n", res.Listing.Name, strings.ToUpper(string(res.Listing.Kind)), publisher, summary)
	}
}

// installFlags holds the parsed command line for `litespm install`.
type installFlags struct {
	listingID   string
	version     string
	scope       domain.InstallScope
	workspaceID string
	showHelp    bool
	// hosts names the agent hosts to register an MCP server with. Empty means
	// every host whose LiteSPM bridge verifies as registered.
	hosts []string
	// envNames are environment variable NAMES to forward to the server. Names
	// only, never values: the config gets a reference the host expands, so a
	// secret never passes through LiteSPM.
	envNames []string
	// force replaces an entry of the same name instead of refusing.
	force bool
}

const installUsage = "Usage: litespm install <listing-id> [--version <ver>] [--scope user|project] [--workspace <id>]\n" +
	"                     [--host <host-id>]... [--env <VAR>]... [--force]\n\n" +
	"  --host   register an MCP server with this agent host (repeatable). Default: every\n" +
	"           host where 'litespm host setup' has been run.\n" +
	"  --env    forward this environment VARIABLE NAME to the server (repeatable). The\n" +
	"           host config records a reference such as ${VAR}, never the value, so the\n" +
	"           secret stays in the environment your agent was started in. Export it\n" +
	"           before starting the agent; a value in the config is not needed.\n" +
	"  --force  replace an existing entry with the same name instead of refusing."

// parseInstallFlags parses `litespm install` arguments strictly: unknown
// flags, missing flag values, an out-of-range --scope, extra positional
// arguments, and a missing listing id are all errors (the caller exits 2).
// `--help`/`-h` is reported separately so it can exit 0.
func parseInstallFlags(args []string) (installFlags, error) {
	flags := installFlags{scope: domain.ScopeUser}
	positional := 0

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			flags.showHelp = true
			return flags, nil
		case "--version", "-v":
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			flags.version = args[i]
		case "--scope", "-s":
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			scope := domain.InstallScope(args[i])
			if scope != domain.ScopeUser && scope != domain.ScopeProject {
				return flags, fmt.Errorf("invalid --scope %q: must be %s or %s", args[i], domain.ScopeUser, domain.ScopeProject)
			}
			flags.scope = scope
		case "--workspace", "-w":
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			flags.workspaceID = args[i]
		case "--host":
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			flags.hosts = append(flags.hosts, args[i])
		case "--env":
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			flags.envNames = append(flags.envNames, args[i])
		case "--force", "-f":
			flags.force = true
		default:
			if strings.HasPrefix(arg, "-") {
				return flags, fmt.Errorf("unknown flag %q", arg)
			}
			positional++
			if positional > 1 {
				return flags, fmt.Errorf("unexpected argument %q", arg)
			}
			flags.listingID = arg
		}
	}

	if flags.listingID == "" {
		return flags, fmt.Errorf("missing <listing-id>")
	}
	return flags, nil
}

func runInstall(args []string) {
	flags, err := parseInstallFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Install error: %v\n", err)
		fmt.Println(installUsage)
		os.Exit(2)
	}
	if flags.showHelp {
		fmt.Println(installUsage)
		return
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to initialize storage directories: %v\n", err)
		os.Exit(1)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to open state database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	// Resolve the listing from the local catalog index so the install can route
	// by kind. A skill installs as files through the skills ledger; every other
	// kind needs an artifact the catalog does not carry yet and fails closed.
	cfg, _ := config.LoadConfig("")
	regURL := config.DefaultRegistryURL
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}
	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)

	fmt.Printf("Resolving and installing %s...\n", flags.listingID)
	listing, listingErr := catClient.GetListing(flags.listingID)
	if listingErr != nil {
		msg := fmt.Sprintf("listing %s is not in the local catalog index", flags.listingID)
		if catClient.Count() == 0 {
			msg += "; run 'litespm catalog sync' first"
		}
		fmt.Fprintf(os.Stderr, "Install failed: %s\n", msg)
		os.Exit(1)
	}

	if listing.Kind == domain.KindSkill {
		home, _ := os.UserHomeDir()
		project, _ := os.Getwd()
		outcome, err := installSkillFromListing(ctx, db, paths.DataRoot, project, home, listing, versionOrLatest(flags.version), flags.scope, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
			os.Exit(1)
		}
		printSkillInstall(outcome)
		return
	}

	if listing.Kind == domain.KindMCP {
		runtime, runtimeErr := catClient.RuntimeForListing(listing.ID, versionOrLatest(flags.version))
		if runtimeErr != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", domain.ErrArtifactUnavailable(flags.listingID, runtimeErr.Error()))
			os.Exit(1)
		}
		outcome, err := installMCPFromListing(ctx, db, paths.DataRoot, listing, versionOrLatest(flags.version), flags.scope, flags.hosts, flags.force, runtime, flags.envNames)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
			os.Exit(1)
		}
		printMCPInstall(outcome)
		return
	}

	fmt.Fprintf(os.Stderr, "Install failed: %v\n", domain.ErrArtifactUnavailable(flags.listingID,
		fmt.Sprintf("the %q install path is not wired yet: the published catalog carries no artifact locator for it", listing.Kind)))
	os.Exit(1)
}

// versionOrLatest mirrors the install engine's default: an empty requested
// version means "latest".
func versionOrLatest(v string) string {
	if strings.TrimSpace(v) == "" {
		return "latest"
	}
	return v
}

func runCatalogSync() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := config.DefaultRegistryURL
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	fmt.Printf("Synchronizing catalog from %s...\n", regURL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := catClient.Sync(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Catalog synchronization failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "No catalog data was written; the existing local cache was left unchanged.")
		os.Exit(1)
	}

	fmt.Printf("✓ Catalog synchronization complete (Release %s, %d capabilities indexed).\n", res.ReleaseID, res.ItemCount)
}

// catalogBuildOptions are the inputs of buildCatalogRelease. The zero value
// beyond dataset/out/prev is the "release mode" default: derive a fresh
// release id, advance the sequence, stamp the current time.
type catalogBuildOptions struct {
	datasetPath string // dataset JSON to convert
	outDir      string // directory that receives the /v1 tree
	prevPath    string // released pointer that owns sequence/id authority
	releaseID   string // "" => derive (release mode)
	sequence    int    // <0  => derive (release mode)
	createdAt   string // RFC3339, "" => now or SOURCE_DATE_EPOCH
	materialize bool   // reproduce the released pointer's tree exactly
}

// buildCatalogRelease is the single path every /v1 release tree comes from:
// the release step (dataset changed), the deploy step (-materialize, rebuilding
// the already-released pointer's bytes), and tests all call it.
//
// Release mode (default) reads sequence authority from the released pointer at
// opts.prevPath and always advances past it: sequence = previous+1, and a
// same-day default id has its -NN suffix bumped so it never collides. Release
// ids are immutable (the CDN caches /v1/releases/* forever), so an explicitly
// reused id fails instead of silently republishing under a cached name.
//
// Materialize mode (-materialize) refuses to invent anything: it requires the
// released pointer and reproduces its id, sequence, and creation time, so the
// rebuilt tree is byte-identical to the release the pointer digests.
func buildCatalogRelease(opts catalogBuildOptions) (*catalogbuild.BuildOutput, string, error) {
	datasetRaw, err := os.ReadFile(opts.datasetPath)
	if err != nil {
		return nil, "", fmt.Errorf("read dataset %s: %w", opts.datasetPath, err)
	}
	rows, err := catalogbuild.ParseDataset(datasetRaw)
	if err != nil {
		return nil, "", err
	}

	prev, err := readCurrentPointer(opts.prevPath)
	if err != nil {
		return nil, "", err
	}

	releaseID := opts.releaseID
	sequence := opts.sequence
	createdAt, err := parseBuildCreatedAt(opts.createdAt)
	if err != nil {
		return nil, "", err
	}

	switch {
	case opts.materialize:
		if prev == nil {
			return nil, "", fmt.Errorf("no released pointer at %s to materialize", opts.prevPath)
		}
		if releaseID != "" && releaseID != prev.ReleaseID {
			return nil, "", fmt.Errorf("-release-id %q contradicts released pointer %q", releaseID, prev.ReleaseID)
		}
		if opts.sequence >= 1 && opts.sequence != prev.Sequence {
			return nil, "", fmt.Errorf("-sequence %d contradicts released pointer sequence %d", opts.sequence, prev.Sequence)
		}
		if opts.createdAt != "" && opts.createdAt != prev.CreatedAt {
			return nil, "", fmt.Errorf("-created-at %q contradicts released pointer createdAt %q", opts.createdAt, prev.CreatedAt)
		}
		releaseID = prev.ReleaseID
		sequence = prev.Sequence
		createdAt, err = time.Parse(time.RFC3339, prev.CreatedAt)
		if err != nil {
			return nil, "", fmt.Errorf("released pointer createdAt %q is not RFC3339: %w", prev.CreatedAt, err)
		}

	default:
		if releaseID == "" {
			base := fmt.Sprintf("rel-%s", createdAt.Format("2006-01-02"))
			releaseID = base + "-01"
			// Same-day rebuilds must not reuse an already-published id.
			if prev != nil && strings.HasPrefix(prev.ReleaseID, base+"-") {
				if n, err := strconv.Atoi(strings.TrimPrefix(prev.ReleaseID, base+"-")); err == nil {
					releaseID = fmt.Sprintf("%s-%02d", base, n+1)
				}
			}
		}
		if sequence < 1 { // 0 (the zero value) and negatives mean "derive"
			sequence = 1
			if prev != nil {
				sequence = prev.Sequence + 1
			}
		}
		if prev != nil {
			if sequence <= prev.Sequence {
				return nil, "", fmt.Errorf("-sequence %d does not advance past released sequence %d (%s): catalog sequences must increase",
					sequence, prev.Sequence, opts.prevPath)
			}
			if releaseID == prev.ReleaseID {
				return nil, "", fmt.Errorf("release id %q is already published at sequence %d (%s): release ids are immutable because CDNs cache /v1/releases/* forever; omit -release-id to derive a fresh one",
					releaseID, prev.Sequence, opts.prevPath)
			}
		}
	}

	if err := domain.ValidateReleaseID(releaseID); err != nil {
		return nil, "", err
	}

	// Release time is pinned to second precision. The pointer stores
	// createdAt as RFC3339 seconds, and the same timestamp is embedded in
	// every listing's provenance and version record — if the embedded value
	// carried nanoseconds, materializing from the pointer could never
	// reproduce the released bytes, and the deployed tree would fail the
	// pointer digest it claims to satisfy.
	createdAt = createdAt.UTC().Truncate(time.Second)

	snapshotID := catalogbuild.DatasetSnapshotID(datasetRaw)
	listings, versions, err := catalogbuild.ConvertDataset(rows, releaseID, snapshotID, createdAt)
	if err != nil {
		return nil, "", err
	}

	output, err := catalogbuild.CompileRelease(releaseID, sequence, []string{snapshotID}, listings, versions, createdAt)
	if err != nil {
		return nil, "", err
	}
	if err := output.WriteToDirectory(opts.outDir); err != nil {
		return nil, "", fmt.Errorf("write release tree to %s: %w", opts.outDir, err)
	}
	return output, snapshotID, nil
}

// readCurrentPointer loads a release pointer, treating a missing file as
// "no previous release" but a corrupt one as an error: sequence authority
// must never be guessed.
func readCurrentPointer(path string) (*catalogbuild.CurrentPointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read previous pointer %s: %w", path, err)
	}
	var pointer catalogbuild.CurrentPointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return nil, fmt.Errorf("parse previous pointer %s: %w", path, err)
	}
	return &pointer, nil
}

// parseBuildCreatedAt resolves the release timestamp: an explicit RFC3339
// value, else SOURCE_DATE_EPOCH (reproducible builds), else the current time.
func parseBuildCreatedAt(explicit string) (time.Time, error) {
	if explicit != "" {
		parsed, err := time.Parse(time.RFC3339, explicit)
		if err != nil {
			return time.Time{}, fmt.Errorf("-created-at must be RFC3339: %w", err)
		}
		return parsed, nil
	}
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		n, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH must be unix seconds: %w", err)
		}
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Now(), nil
}

// syncReleaseStats merges the release identity (releaseId, sequence,
// manifestDigest, createdAt, counts) into the site's build-time stats bundle
// when, and only when, this build is the one that updates the authority
// pointer — i.e. prevPath lives under outDir. Deploy materializations and test
// builds write their tree elsewhere and leave the bundle alone.
//
// The bundle is read-modify-write: dataset stats (per-kind counts,
// hostCompatibility, datasetDigest) belong to scripts/build_full_catalog.py
// and are preserved, exactly as the builder's identity keys are preserved when
// that script runs. A missing bundle is skipped rather than created, because a
// bundle without dataset stats would render the site's counts as zero.
func syncReleaseStats(prevPath, outDir string, output *catalogbuild.BuildOutput) error {
	if filepath.Clean(filepath.Dir(filepath.Dir(prevPath))) != filepath.Clean(outDir) {
		return nil
	}
	statsPath := filepath.Join(filepath.Dir(filepath.Clean(outDir)), "data", "release.json")
	data, err := os.ReadFile(statsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read release stats %s: %w", statsPath, err)
	}
	stats := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &stats); err != nil {
		return fmt.Errorf("parse release stats %s: %w", statsPath, err)
	}
	for key, value := range map[string]any{
		"releaseId":         output.Manifest.ReleaseID,
		"sequence":          output.Current.Sequence,
		"manifestDigest":    output.ManifestDigest,
		"createdAt":         output.Manifest.CreatedAt,
		"itemCount":         output.Manifest.ItemCount,
		"totalCapabilities": output.Manifest.ItemCount,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode release stats %s: %w", key, err)
		}
		stats[key] = encoded
	}
	encoded, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release stats: %w", err)
	}
	if err := os.WriteFile(statsPath, append(encoded, '\n'), 0644); err != nil {
		return fmt.Errorf("write release stats %s: %w", statsPath, err)
	}
	return nil
}

// runCatalogBuild implements `litespm catalog build`: convert the dataset into
// the static /v1 release tree (manifest + listings + versions + current.json).
func runCatalogBuild(args []string) {
	fs := flag.NewFlagSet("litespm catalog build", flag.ExitOnError)
	dataset := fs.String("dataset", "web/data/catalog.json", "path to the catalog dataset JSON")
	outDir := fs.String("out", "web/public", "directory to write the /v1 release tree into")
	prevPath := fs.String("prev", "web/public/v1/current.json", "released pointer that owns sequence/id authority")
	releaseID := fs.String("release-id", "", "release id (default: rel-<date>-NN derived from build time, NN bumped past the released pointer)")
	sequence := fs.Int("sequence", -1, "release sequence (default: released sequence plus one; must increase)")
	created := fs.String("created-at", "", "RFC3339 creation time (default: SOURCE_DATE_EPOCH if set, else now)")
	materialize := fs.Bool("materialize", false, "reproduce the released pointer's tree byte-for-byte instead of cutting a new release")
	verifyDir := fs.String("verify", "", "verify an already-written tree in this directory against its own pointer, then exit (used by packaging)")
	_ = fs.Parse(args)

	// --verify is the packaging gate: the CDN caches /v1/releases/* immutably,
	// so a tree that disagrees with its pointer must fail the deploy, not ship.
	if *verifyDir != "" {
		pointer, manifest, err := catalogbuild.VerifyMaterializedTree(*verifyDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Catalog tree verification failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Catalog tree verified against its pointer\n")
		fmt.Printf("  Release:  %s (sequence %d, %d items)\n", manifest.ReleaseID, pointer.Sequence, manifest.ItemCount)
		fmt.Printf("  Manifest: %s\n", pointer.ManifestDigest)
		fmt.Printf("  Files:    %d verified byte-for-byte\n", len(manifest.Files))
		return
	}

	output, snapshotID, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: *dataset,
		outDir:      *outDir,
		prevPath:    *prevPath,
		releaseID:   *releaseID,
		sequence:    *sequence,
		createdAt:   *created,
		materialize: *materialize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Catalog build failed: %v\n", err)
		os.Exit(1)
	}
	if err := syncReleaseStats(*prevPath, *outDir, output); err != nil {
		fmt.Fprintf(os.Stderr, "Catalog build failed: %v\n", err)
		os.Exit(1)
	}

	manifest := output.Manifest
	fmt.Printf("✓ Catalog release built\n")
	fmt.Printf("  Release:  %s (sequence %d, %d items)\n", manifest.ReleaseID, output.Current.Sequence, manifest.ItemCount)
	fmt.Printf("  Snapshot: %s\n", snapshotID)
	fmt.Printf("  Manifest: %s\n", manifest.ContentDigest)
	fmt.Printf("  Pointer:  %s\n", filepath.Join(*outDir, "v1", "current.json"))
	fmt.Printf("  Tree:     %s\n", filepath.Join(*outDir, "v1", "releases", manifest.ReleaseID))
}

// ARCH/20 §2 CLI exit-code contract. Only the doctor-producible codes are
// defined here; the table also reserves 2 (USAGE_ERROR) and 1 (INTERNAL_FAILURE)
// for other command paths.
const (
	exitCatalogError   = 10
	exitResolveError   = 20
	exitApprovalDenied = 30
	exitInstallError   = 40
	exitProviderError  = 50
	exitHostError      = 60
	exitStateError     = 70
)

// doctorCategoryExitCode maps a failing check's category to the documented
// ARCH/20 §2 exit code. An uncategorized failure falls back to STATE_ERROR so
// older check producers keep the historical "a doctor FAIL is a recovery
// condition" behavior.
func doctorCategoryExitCode(category doctor.Category) int {
	switch category {
	case doctor.CategoryCatalog:
		return exitCatalogError
	case doctor.CategoryResolve:
		return exitResolveError
	case doctor.CategoryApproval:
		return exitApprovalDenied
	case doctor.CategoryInstall:
		return exitInstallError
	case doctor.CategoryProvider:
		return exitProviderError
	case doctor.CategoryHost:
		return exitHostError
	case doctor.CategoryState:
		return exitStateError
	default:
		return exitStateError
	}
}

// doctorExitCode maps a diagnostic report to the process exit code. Each FAIL
// returns its category's documented code; when several categories fail, the
// highest (most severe) code wins. Warnings alone never fail the command. A
// report with a nonzero FailCount but no inspectable failing check still exits
// STATE_ERROR rather than 0.
func doctorExitCode(report *doctor.DoctorReport) int {
	if report == nil {
		return 0
	}
	worst := 0
	for _, c := range report.Checks {
		if c.Status != doctor.StatusFail {
			continue
		}
		if code := doctorCategoryExitCode(c.Category); code > worst {
			worst = code
		}
	}
	if worst == 0 && report.FailCount > 0 {
		return exitStateError
	}
	return worst
}

func runDoctor(args []string) int {
	repairMode := false
	yesMode := false
	for _, a := range args {
		if a == "--repair" || a == "-r" {
			repairMode = true
		}
		if a == "--yes" || a == "-y" {
			yesMode = true
		}
	}

	ctx := context.Background()
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: path resolution failed: %v\n", err)
		os.Exit(1)
	}

	var db *state.DB
	if pdb, err := state.Open(paths.StateDBPath()); err == nil {
		db = pdb
		defer db.Close()
	}

	// The doctor must keep diagnosing even when the credential vault cannot be
	// opened (that is one of the conditions it has to report), so a failure here
	// is announced loudly and injected as a FAIL check instead of being
	// discarded: `secretStore, _ :=` used to hide it completely.
	secretStore, storeErr := secrets.OpenSecretStore()
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "WARNING: [%s] secure credential vault unavailable: %v\n",
			domain.CodeAuthVaultUnavailable, storeErr)
	}
	eng := doctor.NewEngine(paths, db, secretStore)
	report := eng.RunChecks(ctx)
	if storeErr != nil {
		report.Checks = append(report.Checks, doctor.CheckResult{
			ID:             "check_secrets_open",
			Name:           "Secret Vault Availability",
			Status:         doctor.StatusFail,
			Category:       doctor.CategoryProvider,
			Message:        fmt.Sprintf("the secure credential vault could not be opened: %v", storeErr),
			Recommendation: "Provide a functional OS credential vault; LiteSPM refuses to store credentials in plaintext (ARCH/19).",
		})
		// Re-aggregate so the printed summary matches the checks actually listed.
		report.PassedCount, report.WarnCount, report.FailCount = 0, 0, 0
		report.OverallStatus = doctor.StatusPass
		for _, c := range report.Checks {
			switch c.Status {
			case doctor.StatusPass:
				report.PassedCount++
			case doctor.StatusWarn:
				report.WarnCount++
			case doctor.StatusFail:
				report.FailCount++
			}
		}
		if report.FailCount > 0 {
			report.OverallStatus = doctor.StatusFail
		} else if report.WarnCount > 0 {
			report.OverallStatus = doctor.StatusWarn
		}
	}

	fmt.Println("\nLiteSPM Diagnostic Health Report")
	fmt.Println(strings.Repeat("=", 60))
	for _, c := range report.Checks {
		var statusIcon string
		switch c.Status {
		case doctor.StatusPass:
			statusIcon = "✓ [PASS]"
		case doctor.StatusWarn:
			statusIcon = "⚠ [WARN]"
		case doctor.StatusFail:
			statusIcon = "✗ [FAIL]"
		}
		fmt.Printf("%-9s %-32s: %s\n", statusIcon, c.Name, c.Message)
		if c.Recommendation != "" {
			fmt.Printf("          ➜ Recommendation: %s\n", c.Recommendation)
		}
	}
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Summary: %d Passed, %d Warnings, %d Failures (Overall: %s)\n",
		report.PassedCount, report.WarnCount, report.FailCount, strings.ToUpper(string(report.OverallStatus)))

	if repairMode {
		plan := doctor.BuildRepairPlan(report, paths)
		if len(plan.Actions) == 0 {
			fmt.Println("\nNo automated repair actions necessary.")
			return doctorExitCode(report)
		}
		fmt.Println("\nProposed Automated Repair Plan:")
		for i, act := range plan.Actions {
			fmt.Printf("  %d. [%s] %s\n", i+1, act.ActionKind, act.Description)
		}

		if !yesMode {
			fmt.Print("\nApply these repair actions? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Repair aborted by user.")
				// The report was computed before repair; failures still stand.
				return doctorExitCode(report)
			}
		}

		fmt.Println("\nExecuting Automated Repair Plan...")
		// The exit code reflects the PRE-repair diagnosis: doctor does not
		// re-run its checks after repair, so a run that found failures still
		// reports them even if every action applied. Re-run `litespm doctor`
		// to confirm a clean state. A failed action or a failed repair plan is
		// a recovery condition and forces STATE_ERROR regardless of the
		// pre-repair category.
		code := doctorExitCode(report)
		if err := doctor.ApplyRepairPlan(ctx, plan, paths, db); err != nil {
			fmt.Fprintf(os.Stderr, "Repair error: %v\n", err)
			code = exitStateError
		}
		for _, act := range plan.Actions {
			if act.Applied {
				fmt.Printf("✓ Applied: %s\n", act.Description)
			} else if act.Error != "" {
				fmt.Printf("✗ Failed:  %s (%s)\n", act.Description, act.Error)
				code = exitStateError
			}
		}
		fmt.Println("Repair cycle completed.")
		return code
	} else if report.FailCount > 0 || report.WarnCount > 0 {
		fmt.Println("\nTip: Run 'litespm doctor --repair' to preview and apply automated corrective actions.")
	}
	return doctorExitCode(report)
}

func runDaemonServe() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to create directories: %v\n", err)
		os.Exit(1)
	}

	// ARCH/19: without a functional credential vault LiteSPM halts rather than
	// storing credentials anywhere unprotected.
	secretStore, err := secrets.OpenSecretStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: [%s] %v\n", domain.CodeAuthVaultUnavailable, err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	lockFile, err := acquireLock(paths.DaemonLockPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: unable to acquire daemon single-instance lock: %v\n", err)
		os.Exit(1)
	}
	defer releaseLock(lockFile, paths.DaemonLockPath())

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to open SQLite database at %s: %v\n", paths.StateDBPath(), err)
		os.Exit(1)
	}
	defer db.Close()

	installEngine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to create install engine: %v\n", err)
		os.Exit(1)
	}

	// Crash recovery runs BEFORE the IPC listener is bound: an interrupted
	// install is resolved (committed or rolled back) before any client can ask
	// the daemon about state it has not yet reconciled. A journal failure here
	// halts startup instead of serving unreconciled state.
	if _, err := runStartupRecovery(context.Background(), db, paths.StagingPath(), func(digest string) string {
		p, pathErr := installEngine.TreePath(digest)
		if pathErr != nil {
			return ""
		}
		return p
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: startup recovery failed: %v\n", err)
		os.Exit(1)
	}

	regURL := config.DefaultRegistryURL
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}
	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)

	// Provider lifecycle: construct the supervisor, start what the state
	// database says should autostart, and stop everything on shutdown.
	supervisor := provider.NewSupervisor()
	configured, cfgErr := loadConfiguredProviders(context.Background(), db)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to read provider configuration: %v\n", cfgErr)
		os.Exit(1)
	}
	for _, rep := range provider.StartConfigured(context.Background(), supervisor, configured) {
		fmt.Printf("[daemon] provider %s: %s — %s\n", rep.ProviderID, rep.Action, rep.Detail)
	}
	defer supervisor.StopAll(context.Background())

	endpoint := paths.IPCEndpoint()
	listener, err := ipc.ListenIPC(endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to bind IPC listener on %s: %v\n", endpoint, err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("[daemon] LiteSPM Daemon v%s listening on %s (PID %d)\n", Version, endpoint, os.Getpid())

	server := ipc.NewServer(Version, ProtocolVersion)

	// Register Core Handlers
	registerCoreHandlers(server, db, catClient, installEngine, paths, secretStore, supervisor)

	// Signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()

	select {
	case sig := <-sigChan:
		fmt.Printf("\n[daemon] Received signal %v, shutting down gracefully...\n", sig)
		_ = server.Stop()
		_ = listener.Close()
	case err := <-serverErr:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Daemon listener error: %v\n", err)
		}
	}

	fmt.Println("[daemon] Shutdown complete.")
}

func registerCoreHandlers(server *ipc.Server, db *state.DB, catClient *catalog.Client, installEngine *install.Engine, paths *config.PlatformPaths, secretStore secrets.SecretStore, supervisor *provider.Supervisor) {
	policyEngine := policy.NewEngine(db, nil)
	installEngine.SetPolicy(policyEngine)

	// 1. tools.list returns installed capabilities & external detected tools.
	// Truthfulness: LiteSPM does not health-check or verify installed
	// capabilities at runtime, so Verified stays false and Status/Transport are
	// omitted (unknown) unless derivable from real data. Kind is parsed from
	// the canonical listing ID; external entries carry the kind detected in the
	// host's own config file.
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		var items []bridge.CapabilityItem
		for _, inst := range installs {
			item := bridge.CapabilityItem{
				ID:      inst.ListingID,
				Name:    inst.ListingID,
				Summary: fmt.Sprintf("Installed capability (v%s)", inst.Version),
			}
			if lid, err := domain.ParseListingID(inst.ListingID); err == nil {
				item.Kind = string(lid.Kind())
			}
			items = append(items, item)
		}

		// Detect external tools
		for _, ad := range host.ListAdapters() {
			if extComps, err := ad.DetectPreExistingComponents(ctx); err == nil {
				for _, ec := range extComps {
					items = append(items, bridge.CapabilityItem{
						ID:         "external:" + ad.Descriptor().HostID + ":" + ec.Name,
						Name:       ec.Name,
						Kind:       ec.Kind,
						Summary:    fmt.Sprintf("Pre-existing host tool from %s", ec.SourcePath),
						IsExternal: true,
					})
				}
			}
		}

		return map[string]any{
			"installs": items,
			"count":    len(items),
		}, nil
	})

	// 2. catalog.search performs live search over catalog index. The `kinds`
	// parameter is honored with a local client-side filter over the results.
	server.RegisterHandler("catalog.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			Query    string   `json:"query"`
			Kinds    []string `json:"kinds,omitempty"`
			Category string   `json:"category,omitempty"`
			Limit    int      `json:"limit,omitempty"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
			}
		}

		limit := req.Limit
		if limit <= 0 {
			limit = 20
		}

		kinds := make(map[string]struct{}, len(req.Kinds))
		for _, k := range req.Kinds {
			kinds[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
		}

		// When filtering by kind, over-fetch so `limit` applies after the
		// filter rather than truncating before it.
		searchLimit := limit
		if len(kinds) > 0 {
			searchLimit = 1 << 30
		}
		results := catClient.Search(req.Query, catalog.SearchOptions{
			Category: req.Category,
			Limit:    searchLimit,
		})

		if len(kinds) > 0 {
			filtered := make([]*catalog.SearchResult, 0, len(results))
			for _, r := range results {
				if _, ok := kinds[strings.ToLower(string(r.Listing.Kind))]; ok {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}
		if len(results) > limit {
			results = results[:limit]
		}

		return map[string]any{
			"count":   len(results),
			"results": results,
		}, nil
	})

	// 3. catalog.get_item retrieves full listing metadata by exact ID lookup
	server.RegisterHandler("catalog.get_item", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
		}

		listing, err := catClient.GetListing(req.ID)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: fmt.Sprintf("listing %s not found", req.ID)}
		}
		return listing, nil
	})

	// 4. resolver.prepare_plan performs real dependency resolution over the
	// local catalog index, builds an InstallPlan, seals it with a planHash and
	// persists it so install.execute can re-verify exactly this document.
	server.RegisterHandler("resolver.prepare_plan", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
			Scope   string `json:"scope,omitempty"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
		}
		if strings.TrimSpace(req.ID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params: id is required"}
		}
		scope := domain.InstallScope(req.Scope)
		if scope == "" {
			scope = domain.ScopeUser
		}
		if scope != domain.ScopeUser && scope != domain.ScopeProject {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams,
				Message: fmt.Sprintf("invalid scope %q: must be %q or %q", req.Scope, domain.ScopeUser, domain.ScopeProject)}
		}

		listing, err := catClient.GetListing(req.ID)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams,
				Message: fmt.Sprintf("listing %s not found in the local catalog index: %v", req.ID, err)}
		}

		plan, rpcErr := buildInstallPlan(ctx, catClient, req.ID, req.Version, scope, listing)
		if rpcErr != nil {
			return nil, rpcErr
		}

		if err := db.SavePlan(ctx, plan); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError,
				Message: fmt.Sprintf("failed to persist plan %s: %v", plan.PlanID, err)}
		}
		return plan, nil
	})

	// 5. install.execute installs a package. A planId (the shape the bridge
	// shim sends) binds the run to a persisted, hash-verified plan; bare
	// listing/version parameters still work. Every failure maps to a real RPC
	// error — there is no success path that skips verification.
	server.RegisterHandler("install.execute", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ListingID     string `json:"listingId,omitempty"`
			Version       string `json:"version,omitempty"`
			Scope         string `json:"scope,omitempty"`
			ApprovalID    string `json:"approvalId,omitempty"`
			PlanID        string `json:"planId,omitempty"`
			ApprovalToken string `json:"approvalToken,omitempty"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid install parameters"}
		}

		approvalID := req.ApprovalID
		if approvalID == "" {
			approvalID = req.ApprovalToken
		}

		// Resolve the listing so the install can route by kind. A skill listing
		// installs as files through the skills ledger; every other kind falls
		// through to the archive engine, which requires an artifact source the
		// daemon does not supply yet and therefore fails closed.
		listingID, version, scope := req.ListingID, req.Version, domain.InstallScope(req.Scope)
		if req.PlanID != "" {
			if plan, perr := db.GetPlan(ctx, req.PlanID); perr == nil {
				listingID = plan.Request.ListingID
				if version == "" {
					version = plan.Resolved.Version
				}
				if scope == "" {
					scope = plan.Request.TargetScope
				}
			}
		}
		if scope == "" {
			scope = domain.ScopeUser
		}
		if listing, lerr := catClient.GetListing(listingID); lerr == nil {
			if listing.Kind == domain.KindSkill {
				home, _ := os.UserHomeDir()
				project, _ := os.Getwd()
				outcome, ierr := installSkillFromListing(ctx, db, paths.DataRoot, project, home, listing, versionOrLatest(version), scope, nil)
				if ierr != nil {
					return nil, installRPCError(ierr)
				}
				result := map[string]any{
					"installId":    outcome.InstallID,
					"treeDigest":   outcome.ContentDigest,
					"status":       string(domain.InstallActive),
					"kind":         string(domain.KindSkill),
					"skill":        outcome.SkillName,
					"destinations": outcome.Destinations,
					"sourceRef":    outcome.SourceRef,
				}
				if req.PlanID != "" {
					result["planId"] = req.PlanID
				}
				return result, nil
			}
			if listing.Kind == domain.KindMCP {
				runtime, rerr := catClient.RuntimeForListing(listingID, versionOrLatest(version))
				if rerr != nil {
					return nil, installRPCError(domain.ErrArtifactUnavailable(listingID, rerr.Error()))
				}
				outcome, ierr := installMCPFromListing(ctx, db, paths.DataRoot, listing, versionOrLatest(version), scope, nil, false, runtime, nil)
				if ierr != nil {
					return nil, installRPCError(ierr)
				}
				hosts := make([]map[string]any, 0, len(outcome.Hosts))
				for _, h := range outcome.Hosts {
					hosts = append(hosts, map[string]any{
						"hostId":     h.HostID,
						"configPath": h.ConfigPath,
						"entryName":  h.Name,
						"replaced":   h.Replaced,
						"created":    h.Created,
					})
				}
				result := map[string]any{
					"installId":           outcome.InstallID,
					"status":              string(domain.InstallActive),
					"kind":                string(domain.KindMCP),
					"entryName":           outcome.Entry.Name,
					"command":             outcome.Entry.Command,
					"args":                outcome.Entry.Args,
					"hosts":               hosts,
					"configuredTransport": outcome.ClaimedTransport,
				}
				if req.PlanID != "" {
					result["planId"] = req.PlanID
				}
				return result, nil
			}
			return nil, installRPCError(domain.ErrArtifactUnavailable(listingID,
				fmt.Sprintf("the %q install path is not wired yet: the published catalog carries no artifact locator for it", listing.Kind)))
		}

		rec, err := installEngine.Execute(ctx, install.InstallOptions{
			ListingID:  listingID,
			Version:    version,
			Scope:      scope,
			ApprovalID: approvalID,
			PlanID:     req.PlanID,
		})
		if err != nil {
			return nil, installRPCError(err)
		}

		result := map[string]any{
			"installId":  rec.InstallID,
			"treeDigest": rec.TreeDigest,
			"status":     rec.Status,
		}
		if req.PlanID != "" {
			result["planId"] = req.PlanID
		}
		return result, nil
	})

	// 6. install.remove removes an installed package. A missing install is
	// reported as not-found instead of a fake `removed: true`.
	server.RegisterHandler("install.remove", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			InstallID string `json:"installId"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid parameters"}
		}
		if strings.TrimSpace(req.InstallID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params: installId is required"}
		}
		if err := db.DeleteInstall(ctx, req.InstallID); err != nil {
			var lpsmErr *domain.LPSMError
			if errors.As(err, &lpsmErr) && lpsmErr.Code == "LPSM-STATE-NOT-FOUND" {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: err.Error()}
			}
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		return map[string]any{"removed": true, "installId": req.InstallID}, nil
	})

	// 7. skills.list returns progressive disclosure index.
	// Level-1 summaries only: name/description/metadata. Instruction bodies and
	// raw SKILL.md content stay behind skills.load_body.
	server.RegisterHandler("skills.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		type loadedSkill struct {
			id  string
			pkg *skills.SkillPackage
		}
		var loaded []loadedSkill
		for _, inst := range installs {
			treePath, err := installEngine.TreePath(inst.TreeDigest)
			if err != nil {
				continue
			}
			if sp, err := skills.LoadSkillFromDirectory(treePath); err == nil {
				loaded = append(loaded, loadedSkill{id: inst.ListingID, pkg: sp})
			}
		}

		pkgs := make([]*skills.SkillPackage, 0, len(loaded))
		summaries := make([]map[string]any, 0, len(loaded))
		for _, l := range loaded {
			pkgs = append(pkgs, l.pkg)
			summary := map[string]any{
				"id":          l.id,
				"name":        l.pkg.Name,
				"description": l.pkg.Description,
			}
			if l.pkg.License != "" {
				summary["license"] = l.pkg.License
			}
			if l.pkg.Version != "" {
				summary["version"] = l.pkg.Version
			}
			if l.pkg.Author != "" {
				summary["author"] = l.pkg.Author
			}
			if len(l.pkg.Triggers) > 0 {
				summary["triggers"] = l.pkg.Triggers
			}
			if len(l.pkg.ToolsRequired) > 0 {
				summary["toolsRequired"] = l.pkg.ToolsRequired
			}
			summaries = append(summaries, summary)
		}

		return map[string]any{
			"skills":           summaries,
			"progressiveIndex": skills.RenderProgressiveIndex(pkgs),
		}, nil
	})

	// 8. skills.load_body loads progressive instructions from CAS tree
	server.RegisterHandler("skills.load_body", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			SkillID string `json:"skillId"`
			Version string `json:"version,omitempty"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
		}
		if strings.TrimSpace(req.SkillID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params: skillId is required"}
		}

		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		for _, inst := range installs {
			if inst.ListingID != req.SkillID {
				continue
			}
			treePath, err := installEngine.TreePath(inst.TreeDigest)
			if err != nil {
				return nil, &ipc.RPCError{
					Code:    ipc.CodeInternalError,
					Message: fmt.Sprintf("skill %s has an invalid CAS tree reference: %v", req.SkillID, err),
				}
			}
			sp, err := skills.LoadSkillFromDirectory(treePath)
			if err != nil {
				return nil, &ipc.RPCError{
					Code:    ipc.CodeInternalError,
					Message: fmt.Sprintf("skill %s is installed but its SKILL.md could not be read: %v", req.SkillID, err),
				}
			}
			return map[string]any{
				"skillId":      req.SkillID,
				"instructions": sp.Instructions,
			}, nil
		}

		return nil, &ipc.RPCError{
			Code:    ipc.CodeInvalidParams,
			Message: fmt.Sprintf("skill %s not found: not installed", req.SkillID),
		}
	})

	// 9. skills.read_resource reads a supporting file from skill CAS directory.
	// Fail-closed like skills.load_body: every failure path returns a real RPC
	// error (not-installed -> -32602, unreadable CAS/file -> -32603) instead of
	// fabricating a resource body. The path-traversal guards are unchanged.
	server.RegisterHandler("skills.read_resource", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			SkillID string `json:"skillId"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
		}
		if strings.TrimSpace(req.SkillID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params: skillId is required"}
		}

		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		for _, inst := range installs {
			if inst.ListingID != req.SkillID {
				continue
			}
			treePath, err := installEngine.TreePath(inst.TreeDigest)
			if err != nil {
				return nil, &ipc.RPCError{
					Code:    ipc.CodeInternalError,
					Message: fmt.Sprintf("skill %s has an invalid CAS tree reference: %v", req.SkillID, err),
				}
			}
			cleanRel := filepath.Clean(req.Path)
			if filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, "..") {
				return nil, &ipc.RPCError{Code: -32602, Message: "path traversal denied"}
			}
			filePath := filepath.Join(treePath, cleanRel)
			rel, err := filepath.Rel(treePath, filePath)
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				return nil, &ipc.RPCError{Code: -32602, Message: "path traversal denied"}
			}
			data, err := os.ReadFile(filePath)
			if err != nil {
				return nil, &ipc.RPCError{
					Code:    ipc.CodeInternalError,
					Message: fmt.Sprintf("skill %s resource %q could not be read: %v", req.SkillID, cleanRel, err),
				}
			}
			return map[string]any{"content": string(data)}, nil
		}

		return nil, &ipc.RPCError{
			Code:    ipc.CodeInvalidParams,
			Message: fmt.Sprintf("skill %s not found: not installed", req.SkillID),
		}
	})

	// 10. capabilities.search: not wired yet (canned results were removed).
	// Catalog discovery over real data lives in catalog.search.
	// 10. capabilities.search answers over the DISCOVERED tools of installed
	// providers, which is a different question from catalog.search: that one
	// searches what exists in the registry, this one searches what this machine
	// has actually installed and probed.
	server.RegisterHandler("capabilities.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			Query  string `json:"query"`
			Limit  int    `json:"limit"`
			Source string `json:"source"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
			}
		}
		rows, err := db.ListCapabilities(ctx, req.Source)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		query := strings.ToLower(strings.TrimSpace(req.Query))
		limit := req.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		matches := make([]map[string]any, 0, limit)
		for _, row := range rows {
			if query != "" &&
				!strings.Contains(strings.ToLower(row.Name), query) &&
				!strings.Contains(strings.ToLower(row.Description), query) {
				continue
			}
			if len(matches) >= limit {
				break
			}
			matches = append(matches, map[string]any{
				"capabilityId":      row.CapabilityID,
				"name":              row.Name,
				"description":       row.Description,
				"providerId":        row.ProviderID,
				"schemaFingerprint": row.SchemaFingerprint,
			})
		}
		return map[string]any{"capabilities": matches, "totalDiscovered": len(rows)}, nil
	})

	// 11. capabilities.describe: not wired yet (canned schema/status removed).
	// Listing metadata over real data lives in catalog.get_item.
	// 11. capabilities.describe returns one discovered tool's real input schema
	// and the provider command behind it, so an agent can call it correctly
	// instead of guessing argument names.
	server.RegisterHandler("capabilities.describe", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			CapabilityID string `json:"capabilityId"`
		}
		if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.CapabilityID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "capabilityId is required"}
		}
		record, err := db.GetCapability(ctx, req.CapabilityID)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams,
				Message: fmt.Sprintf("no discovered capability %q; run 'litespm capabilities refresh'", req.CapabilityID)}
		}
		provider, perr := db.GetProvider(ctx, record.ProviderID)
		if perr != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: perr.Error()}
		}
		return map[string]any{
			"capabilityId":      record.CapabilityID,
			"name":              record.Name,
			"description":       record.Description,
			"inputSchema":       json.RawMessage(record.InputSchemaJSON),
			"schemaFingerprint": record.SchemaFingerprint,
			"providerId":        record.ProviderID,
			"transport":         provider.Transport,
			"command":           provider.Command,
			"discoveredAt":      record.DiscoveredAt,
		}, nil
	})

	// 12. provider.probe reports the supervisor's real view of one provider:
	// tracked processes return live status/PID/uptime/stderr tail; anything the
	// supervisor is not tracking is an explicit not-found, never a guessed
	// status.
	server.RegisterHandler("provider.probe", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ProviderID string `json:"providerId"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
			}
		}
		if strings.TrimSpace(req.ProviderID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params: providerId is required"}
		}

		snap, err := supervisor.SnapshotProvider(req.ProviderID)
		if err != nil {
			var lpsmErr *domain.LPSMError
			if errors.As(err, &lpsmErr) && lpsmErr.Code == "LPSM-STATE-NOT-FOUND" {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams,
					Message: fmt.Sprintf("provider %s is not tracked by this daemon (never started or already reaped)", req.ProviderID)}
			}
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		return snap, nil
	})

	// 13. provider.invoke stays unimplemented with the concrete reason: the
	// daemon writes no capability rows (nothing to resolve a capabilityId
	// against) and the supervisor has no MCP session dispatch to call into.
	server.RegisterHandler("provider.invoke", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			CapabilityID string          `json:"capabilityId"`
			Arguments    json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.CapabilityID) == "" {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "capabilityId is required"}
		}
		result, err := discover.Invoke(ctx, db, req.CapabilityID, req.Arguments)
		if err != nil {
			// A tool that is not installed, not discovered, or whose schema
			// drifted is the caller's problem to fix, not an internal failure.
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		return map[string]any{"output": result.Output, "isError": result.IsError}, nil
	})

	// 14. invocation.get stays unimplemented, with the reason narrowed: the
	// asynchronous invocation registry is ARCH/34, still DESIGNED. provider.invoke
	// is synchronous by contract (ARCH/06 §4), so nothing here invents a row.
	server.RegisterHandler("invocation.get", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: there is no invocation registry to look up. provider.invoke is synchronous by contract (ARCH/06 §4); the asynchronous registry is ARCH/34, still DESIGNED",
		}
	})

	// 15. invocation.cancel: without an invocation registry there is nothing to
	// cancel; the supervisor only manages provider processes.
	server.RegisterHandler("invocation.cancel", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: there is no invocation registry to cancel from. provider.invoke is synchronous and runs to completion in the request (ARCH/06 §4); the cancellable registry is ARCH/34, still DESIGNED",
		}
	})

	// 16. host.detect_config probes configured agent hosts
	server.RegisterHandler("host.detect_config", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		verifs, err := host.DetectInstalledHosts(ctx)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		return verifs, nil
	})

	// 17. host.apply_setup configures a host
	server.RegisterHandler("host.apply_setup", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			HostID     string `json:"hostId"`
			BinaryPath string `json:"binaryPath"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid params"}
		}
		adapter, err := host.GetAdapter(req.HostID)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}

		binPath := req.BinaryPath
		if binPath == "" {
			binPath, _ = os.Executable()
		}
		plan, err := adapter.PlanSetup(ctx, binPath, paths.BackupsPath())
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		res, err := adapter.ApplySetup(ctx, plan)
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		return res, nil
	})

	// 18. doctor.run_checks executes diagnostic checks
	server.RegisterHandler("doctor.run_checks", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		docEng := doctor.NewEngine(paths, db, secretStore)
		report := docEng.RunChecks(ctx)
		return report, nil
	})

	// 19. system.status returns live daemon health
	server.RegisterHandler("system.status", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return map[string]any{
			"version":         Version,
			"protocolVersion": ProtocolVersion,
			"status":          "ready",
			"pid":             os.Getpid(),
		}, nil
	})
}

// runStartupRecovery sweeps the operation journal before the daemon starts
// serving and reports exactly what it did. Any failure to read or reconcile
// the journal — including a per-operation recovery failure — is returned so
// startup halts instead of proceeding on unreconciled state. The summary is
// always printed first for observability.
func runStartupRecovery(ctx context.Context, db *state.DB, stagingRoot string, treePath func(string) string) (state.RecoverySummary, error) {
	summary, err := db.RecoverIncompleteOperations(ctx, stagingRoot, treePath)
	fmt.Printf("[daemon] startup recovery: examined=%d rolledBack=%d committed=%d failed=%d\n",
		summary.Examined, summary.RolledBack, summary.Committed, summary.Failed)
	if err != nil {
		return summary, fmt.Errorf("journal recovery could not complete: %w", err)
	}
	return summary, nil
}

// loadConfiguredProviders reads every providers row and decodes its stored
// launch spec. Rows whose launch spec cannot be decoded are returned with
// SpecErr set rather than dropped, so startup reports them as failed.
func loadConfiguredProviders(ctx context.Context, db *state.DB) ([]provider.ConfiguredProvider, error) {
	rows, err := db.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ConfiguredProvider, 0, len(rows))
	for _, row := range rows {
		entry := provider.ConfiguredProvider{
			ProviderID: row.ProviderID,
			Mode:       row.Mode,
			Enabled:    row.Enabled,
			Autostart:  row.Autostart,
		}
		if strings.TrimSpace(row.LaunchSpecJSON) != "" {
			var spec provider.LaunchSpec
			if err := json.Unmarshal([]byte(row.LaunchSpecJSON), &spec); err != nil {
				entry.SpecErr = err.Error()
			} else {
				entry.Spec = &spec
			}
		} else {
			entry.SpecErr = "launch_spec_json is empty"
		}
		out = append(out, entry)
	}
	return out, nil
}

// planIDAlphabet is the Crockford base32 alphabet (32 symbols, no I/L/O/U).
const planIDAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newPlanID returns an id matching schemas/install-plan.schema.json
// (`^plan_[0-9A-Za-z]{26}$`). The 26 symbols come from crypto/rand masked to
// 5 bits; since 256 is a multiple of 32 the mask is unbiased.
func newPlanID() (string, error) {
	var buf [26]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("failed to generate plan id: %w", err)
	}
	for i, b := range buf {
		buf[i] = planIDAlphabet[b&0x1f]
	}
	return "plan_" + string(buf[:]), nil
}

// catalogResolutionProvider adapts the local catalog index to the
// resolver.ListingProvider interface. Dependency edges come back empty because
// the normalized index stores versions without a dependency list; the resolver
// therefore produces a single-node graph for index-only listings, which is
// reported in the plan rather than guessed.
type catalogResolutionProvider struct {
	client *catalog.Client
}

func (p *catalogResolutionProvider) GetListing(ctx context.Context, listingID string) (*domain.Listing, error) {
	return p.client.GetListing(listingID)
}

func (p *catalogResolutionProvider) GetListingVersions(ctx context.Context, listingID string) ([]resolver.ListingVersionMetadata, error) {
	listing, err := p.client.GetListing(listingID)
	if err != nil {
		return nil, err
	}
	metas := make([]resolver.ListingVersionMetadata, 0, len(listing.Versions))
	for _, v := range listing.Versions {
		metas = append(metas, resolver.ListingVersionMetadata{Version: v.Version})
	}
	return metas, nil
}

// buildInstallPlan resolves the requested listing with the real resolver and
// seals the outcome into a persisted InstallPlan. Resolution failures are
// returned as RPC errors carrying the resolver's LPSM-RESOLVE-* details.
func buildInstallPlan(ctx context.Context, catClient *catalog.Client, id, version string, scope domain.InstallScope, listing *domain.Listing) (*domain.InstallPlan, *ipc.RPCError) {
	r := resolver.NewResolver(&catalogResolutionProvider{client: catClient})
	res, err := r.Resolve(ctx, id, version)
	if err != nil {
		return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
	}

	planID, err := newPlanID()
	if err != nil {
		return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
	}

	now := time.Now().UTC()
	plan := &domain.InstallPlan{
		SchemaVersion: 2,
		PlanID:        planID,
		CreatedAt:     now,
		ExpiresAt:     now.Add(15 * time.Minute),
		Request: domain.PlanRequest{
			ListingID:        id,
			RequestedVersion: version,
			TargetScope:      scope,
		},
		Resolved: domain.PlanResolved{
			Version:   res.SelectedVersions[id],
			Artifacts: []domain.PlanArtifact{},
		},
		Effects: []string{"package.install"},
	}

	// Immutable ref of the selected version, sourced from the catalog index.
	selected := plan.Resolved.Version
	for _, v := range listing.Versions {
		if v.Version == selected && v.ImmutableRef != "" {
			plan.Resolved.ImmutableRefs = []string{v.ImmutableRef}
			break
		}
	}

	// Dependency graph minus the root listing, in deterministic topological order.
	for _, dep := range res.TopologicalOrder {
		if dep == id {
			continue
		}
		plan.Resolved.Dependencies = append(plan.Resolved.Dependencies, dep)
	}

	planHash, err := domain.ComputePlanHash(plan)
	if err != nil {
		return nil, &ipc.RPCError{Code: ipc.CodeInternalError,
			Message: fmt.Sprintf("failed to compute plan hash: %v", err)}
	}
	plan.PlanHash = planHash
	return plan, nil
}

// installRPCError maps install engine failures onto the JSON-RPC codes the
// contract reserves for them. Nothing is collapsed into a generic success.
func installRPCError(err error) *ipc.RPCError {
	switch domain.ErrorCode(err) {
	case "LPSM-PLAN-STALE", "LPSM-PLAN-EXPIRED":
		return &ipc.RPCError{Code: ipc.CodePlanStale, Message: err.Error()}
	case "LPSM-POLICY-UNAUTHORIZED", "LPSM-POLICY-APPROVAL-CONSUMED", "LPSM-POLICY-APPROVAL-EXPIRED":
		return &ipc.RPCError{Code: ipc.CodeUnauthorized, Message: err.Error()}
	// A name collision is the caller's input being wrong, not an internal
	// failure: the request named something that already exists.
	case "LPSM-STATE-NOT-FOUND", "LPSM-DOMAIN-INVALID-ID", "LPSM-STATE-CONFLICT", "LPSM-NAME-CONFLICT", "LPSM-INSTALL-TARGET-UNAVAILABLE":
		return &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: err.Error()}
	default:
		return &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
	}
}

func acquireLock(lockPath string) (*os.File, error) {
	if data, err := os.ReadFile(lockPath); err == nil {
		pidStr := string(data)
		if pid, err := strconv.Atoi(pidStr); err == nil {
			if processAlive(pid) {
				return nil, fmt.Errorf("active daemon process running with PID %d", pid)
			}
		}
		_ = os.Remove(lockPath)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, _ = file.WriteString(fmt.Sprintf("%d", os.Getpid()))
	_ = file.Sync()
	return file, nil
}

func releaseLock(f *os.File, lockPath string) {
	if f != nil {
		_ = f.Close()
	}
	_ = os.Remove(lockPath)
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS != "windows" {
		err = process.Signal(syscall.Signal(0))
		return err == nil
	}
	return true
}
