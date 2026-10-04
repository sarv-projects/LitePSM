package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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

	"github.com/sarv-projects/litepsm/internal/agent"
	"github.com/sarv-projects/litepsm/internal/bridge"
	"github.com/sarv-projects/litepsm/internal/catalog"
	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/doctor"
	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/host"
	"github.com/sarv-projects/litepsm/internal/install"
	"github.com/sarv-projects/litepsm/internal/ipc"
	"github.com/sarv-projects/litepsm/internal/policy"
	"github.com/sarv-projects/litepsm/internal/provider"
	"github.com/sarv-projects/litepsm/internal/resolver"
	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/skills"
	"github.com/sarv-projects/litepsm/internal/state"
	"github.com/sarv-projects/litepsm/internal/update"
)

// Version is the canonical LitePSM version. Release builds override it via
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
		fmt.Printf("LitePSM v%s (Protocol %s, %s/%s, %s)\n", Version, ProtocolVersion, runtime.GOOS, runtime.GOARCH, runtime.Version())

	case "setup", "init":
		runInteractiveWizard()

	case "self-update", "update":
		runSelfUpdate(os.Args[2:])

	case "doctor":
		runDoctor(os.Args[2:])

	case "search":
		query := ""
		if len(os.Args) >= 3 {
			query = strings.Join(os.Args[2:], " ")
		}
		runSearch(query)

	case "install", "add", "i":
		runInstall(os.Args[2:])

	case "catalog":
		if len(os.Args) >= 3 && os.Args[2] == "sync" {
			runCatalogSync()
		} else {
			fmt.Println("Usage: litepsm catalog sync")
			os.Exit(1)
		}

	case "daemon":
		if len(os.Args) < 3 || os.Args[2] != "serve" {
			fmt.Println("Usage: litepsm daemon serve")
			os.Exit(1)
		}
		runDaemonServe()

	case "bridge":
		runBridge(os.Args[2:])

	case "host":
		if len(os.Args) < 3 {
			fmt.Println("Usage: litepsm host [list|detect|setup <host-id>|remove <host-id>|remove --all]")
			os.Exit(1)
		}
		runHostCommand(os.Args[2:])

	case "uninstall":
		runUninstall(context.Background(), os.Args[2:])

	case "agent":
		runAgentCommand(os.Args[2:])

	case "skills":
		if len(os.Args) < 3 {
			fmt.Println("Usage: litepsm skills [add <source> | list | remove <name>|--all]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "add":
			runSkillsAdd(os.Args[3:])
		case "list":
			runSkillsList(os.Args[3:])
		case "remove":
			runSkillsRemove(os.Args[3:])
		default:
			fmt.Printf("Unknown skills subcommand: %s\n", os.Args[2])
			fmt.Println("Usage: litepsm skills [add <source> | list | remove <name>|--all]")
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
	fmt.Printf(`LitePSM - Universal Package & Capability Manager for AI Coding Agents

Usage:
  litepsm                     Run interactive agent setup wizard
  litepsm <command> [args]    Execute specific subcommand

Available Commands:
  setup                       Interactive setup wizard for AI agent hosts
  search <query>              Search global catalog of MCP servers, skills, and plugins
  install <id>                Install and verify a capability into the local CAS store
  catalog sync                Synchronize latest catalog release from upstream
  bridge stdio [--host h]     Launch stateless stdio MCP bridge shim for host agent
  host [list|detect|setup]    Manage agent host adapters (Codex, Claude, OpenCode, Cline, Pi, Grok)
  agent list [--json]         List installable ACP agents from the registry
  agent resolve <id>          Resolve an ACP agent launch spec for this host
  skills add <source>         Install SKILL.md skills (owner/repo, git URL, local dir)
  doctor [--repair]           Run 10-check diagnostic verification & optional auto-repair
  self-update                 Check for and apply binary updates
  daemon serve                Start the LitePSM background supervisor and IPC engine
  version                     Print version and build details
  help                        Show this help text

Documentation & Architecture:
  https://github.com/sarv-projects/litepsm
`)
}

func runSelfUpdate(args []string) {
	ctx := context.Background()
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	u := update.NewUpdater(regURL)
	fmt.Printf("Checking for LitePSM updates (current: v%s)...\n", Version)
	status, info, err := u.CheckForUpdate(ctx, Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update check failed: %v\n", err)
		os.Exit(1)
	}

	if !status.UpdateAvailable {
		fmt.Printf("✓ LitePSM is already up to date (v%s is latest).\n", Version)
		return
	}

	fmt.Printf("New version available: v%s (current: v%s)\n", status.LatestVersion, Version)
	fmt.Printf("Release URL: %s\n", info.ReleaseURL)
	execPath, _ := os.Executable()
	fmt.Printf("Target binary: %s\n", execPath)

	stagingDir := paths.StagingPath()
	fmt.Printf("Downloading binary update from %s...\n", info.ReleaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.ReleaseURL, nil)
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

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read update payload: %v\n", err)
		os.Exit(1)
	}

	binName := fmt.Sprintf("litepsm-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	expectedChecksum := info.ChecksumsSHA256[binName]

	fmt.Println("Verifying SHA-256 checksum and applying atomic update...")
	err = u.ApplyUpdate(ctx, payload, expectedChecksum, execPath, stagingDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Self-update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Successfully updated LitePSM to v%s!\n", status.LatestVersion)
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
		fmt.Fprintf(os.Stderr, "litepsm bridge: platform path resolution failed: %v; running standalone with no capabilities\n", err)
	} else {
		c, dialErr := ipc.Dial(paths.IPCEndpoint())
		if dialErr != nil {
			fmt.Fprintf(os.Stderr, "litepsm bridge: daemon dial failed: %v; running standalone with no capabilities (start the daemon with 'litepsm daemon serve')\n", dialErr)
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
			fmt.Println("Usage: litepsm host setup <host-id>")
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
		fmt.Println("Usage: litepsm host remove <host-id> | --all")
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
	fmt.Printf("✓ Removed the LitePSM bridge entry from %s\n", adapter.Descriptor().DisplayName)
	fmt.Printf("  • Config File: %s\n", result.ConfigPath)
	if result.BackupPath != "" {
		fmt.Printf("  • Backup:      %s\n", result.BackupPath)
	}
}

// runUninstall reverses everything LitePSM wrote: the bridge entry in every
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

	fmt.Println("Removing the LitePSM bridge entry from every agent host config…")
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
	fmt.Println("  • Skill directories copied by `litepsm skills add`. They live inside each")
	fmt.Println("    agent's own skills tree and may hold files you added yourself, so this")
	fmt.Println("    command does not delete them. See what is tracked, then remove it:")
	fmt.Println("        litepsm skills list")
	fmt.Println("        litepsm skills remove --all")
	fmt.Println("  • Backups written next to each config. They are your restore points;")
	fmt.Println("    delete them yourself once you are satisfied.")
}

func runAgentCommand(args []string) {
	ctx := context.Background()

	if len(args) == 0 {
		fmt.Println("Usage: litepsm agent [list|resolve <id>] [--registry <url>] [--file <path>] [--json]")
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
			fmt.Println("Usage: litepsm agent resolve <id>")
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
	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	results := catClient.Search(query, catalog.SearchOptions{Limit: 25})

	if len(results) == 0 {
		if catClient.Count() == 0 {
			fmt.Printf("No capabilities found: the local catalog index is empty.\n")
			fmt.Println("Run 'litepsm catalog sync' to populate it from the registry.")
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

// installFlags holds the parsed command line for `litepsm install`.
type installFlags struct {
	listingID   string
	version     string
	scope       domain.InstallScope
	workspaceID string
	showHelp    bool
}

const installUsage = "Usage: litepsm install <listing-id> [--version <ver>] [--scope user|project] [--workspace <id>]"

// parseInstallFlags parses `litepsm install` arguments strictly: unknown
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

	installEngine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to initialize install engine: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// The CLI has no upstream artifact endpoint yet: it packages a locally
	// generated archive so the real staging/CAS/journal path can be exercised.
	// The success line below says exactly that — it never claims a remote
	// package was fetched or verified.
	artifactData := createSyntheticPackageArtifact(flags.listingID)

	fmt.Printf("Resolving and installing %s...\n", flags.listingID)
	rec, err := installEngine.Execute(ctx, install.InstallOptions{
		ListingID:   flags.listingID,
		Version:     flags.version,
		Scope:       flags.scope,
		WorkspaceID: flags.workspaceID,
		ArchiveSource: func(ctx context.Context, lid string, ver string) (io.ReadCloser, string, error) {
			return io.NopCloser(bytes.NewReader(artifactData)), "zip", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Installed (local synthetic package; remote resolve/verify not yet wired)\n")
	fmt.Printf("  • Listing ID:  %s\n", flags.listingID)
	fmt.Printf("  • Install ID:  %s\n", rec.InstallID)
	fmt.Printf("  • Version:     %s\n", rec.Version)
	fmt.Printf("  • Scope:       %s\n", rec.Scope)
	fmt.Printf("  • CAS Digest:  %s\n", rec.TreeDigest)
	fmt.Printf("  • Status:      %s\n", rec.Status)
}

func runCatalogSync() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := "https://registry.litepsm.dev"
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

func createSyntheticPackageArtifact(listingID string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Add package.json or SKILL.md
	f, _ := zw.Create("SKILL.md")
	f.Write([]byte(fmt.Sprintf("---\nname: %s\ndescription: Package %s\n---\n# %s\nProgressive instruction workflow.\n", listingID, listingID, listingID)))

	zw.Close()
	return buf.Bytes()
}

func runDoctor(args []string) {
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
			Message:        fmt.Sprintf("the secure credential vault could not be opened: %v", storeErr),
			Recommendation: "Provide a functional OS credential vault; LitePSM refuses to store credentials in plaintext (ARCH/19).",
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

	fmt.Println("\nLitePSM Diagnostic Health Report")
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
			return
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
				return
			}
		}

		fmt.Println("\nExecuting Automated Repair Plan...")
		if err := doctor.ApplyRepairPlan(ctx, plan, paths, db); err != nil {
			fmt.Fprintf(os.Stderr, "Repair error: %v\n", err)
		}
		for _, act := range plan.Actions {
			if act.Applied {
				fmt.Printf("✓ Applied: %s\n", act.Description)
			} else if act.Error != "" {
				fmt.Printf("✗ Failed:  %s (%s)\n", act.Description, act.Error)
			}
		}
		fmt.Println("Repair cycle completed.")
	} else if report.FailCount > 0 || report.WarnCount > 0 {
		fmt.Println("\nTip: Run 'litepsm doctor --repair' to preview and apply automated corrective actions.")
	}
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

	// ARCH/19: without a functional credential vault LitePSM halts rather than
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

	regURL := "https://registry.litepsm.dev"
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

	fmt.Printf("[daemon] LitePSM Daemon v%s listening on %s (PID %d)\n", Version, endpoint, os.Getpid())

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
	// Truthfulness: LitePSM does not health-check or verify installed
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

		rec, err := installEngine.Execute(ctx, install.InstallOptions{
			ListingID:  req.ListingID,
			Version:    req.Version,
			Scope:      domain.InstallScope(req.Scope),
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
	server.RegisterHandler("capabilities.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: capabilities.search is not wired yet; use catalog.search",
		}
	})

	// 11. capabilities.describe: not wired yet (canned schema/status removed).
	// Listing metadata over real data lives in catalog.get_item.
	server.RegisterHandler("capabilities.describe", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: capabilities.describe is not wired yet; use catalog.get_item",
		}
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
		return nil, &ipc.RPCError{
			Code: ipc.CodeMethodNotFound,
			Message: "not implemented: no capability rows are persisted to resolve a capabilityId against, " +
				"and the provider supervisor exposes no MCP session dispatch; tool execution is fail-closed until both exist",
		}
	})

	// 14. invocation.get: the daemon has no invocation registry to query.
	server.RegisterHandler("invocation.get", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: the daemon records no invocation registry, so no invocationId can be looked up",
		}
	})

	// 15. invocation.cancel: without an invocation registry there is nothing to
	// cancel; the supervisor only manages provider processes.
	server.RegisterHandler("invocation.cancel", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return nil, &ipc.RPCError{
			Code:    ipc.CodeMethodNotFound,
			Message: "not implemented: no invocation registry exists to cancel from (the supervisor only starts/stops provider processes)",
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
// the journal is returned so startup can halt instead of serving unreconciled
// state.
func runStartupRecovery(ctx context.Context, db *state.DB, stagingRoot string, treePath func(string) string) (state.RecoverySummary, error) {
	summary, err := db.RecoverIncompleteOperations(ctx, stagingRoot, treePath)
	if err != nil {
		return summary, fmt.Errorf("journal recovery could not complete: %w", err)
	}
	fmt.Printf("[daemon] startup recovery: examined=%d rolledBack=%d committed=%d failed=%d\n",
		summary.Examined, summary.RolledBack, summary.Committed, summary.Failed)
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
	case "LPSM-STATE-NOT-FOUND", "LPSM-DOMAIN-INVALID-ID", "LPSM-STATE-CONFLICT":
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
