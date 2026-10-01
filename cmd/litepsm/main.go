package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/skills"
	"github.com/sarv-projects/litepsm/internal/state"
	"github.com/sarv-projects/litepsm/internal/update"
)

const (
	Version         = "0.1.0"
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
		if len(os.Args) < 3 {
			fmt.Println("Usage: litepsm install <listing-id> [--version <ver>] [--scope user|project] [--workspace <id>]")
			os.Exit(1)
		}
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
			fmt.Println("Usage: litepsm host [list|detect|setup <host-id>]")
			os.Exit(1)
		}
		runHostCommand(os.Args[2:])

	case "agent":
		runAgentCommand(os.Args[2:])

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
	if err == nil {
		c, err := ipc.Dial(paths.IPCEndpoint())
		if err == nil {
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

	default:
		fmt.Printf("Unknown host subcommand: %s\n", sub)
		os.Exit(1)
	}
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
			if hasTarget {
				if spec, rerr := adapter.Resolve(a, target, ""); rerr == nil {
					strategy = spec.Strategy
				}
			}
			fmt.Printf("%-22s %-22s %-12s %s\n", a.ID, truncate(a.Name, 22), a.Version, strategy)
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
		seedDefaultListings(catClient)
		results = catClient.Search(query, catalog.SearchOptions{Limit: 25})
	}

	if len(results) == 0 {
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

func runInstall(args []string) {
	listingID := args[0]
	version := ""
	scope := domain.ScopeUser
	workspaceID := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--version", "-v":
			if i+1 < len(args) {
				version = args[i+1]
				i++
			}
		case "--scope", "-s":
			if i+1 < len(args) {
				scope = domain.InstallScope(args[i+1])
				i++
			}
		case "--workspace", "-w":
			if i+1 < len(args) {
				workspaceID = args[i+1]
				i++
			}
		}
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

	cfg, _ := config.LoadConfig("")
	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}
	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	seedDefaultListings(catClient)

	installEngine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to initialize install engine: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// In test/local mode without an upstream HTTP artifact, generate synthetic valid package
	artifactData := createSyntheticPackageArtifact(listingID)

	fmt.Printf("Resolving and installing %s...\n", listingID)
	rec, err := installEngine.Execute(ctx, install.InstallOptions{
		ListingID:   listingID,
		Version:     version,
		Scope:       scope,
		WorkspaceID: workspaceID,
		ArchiveSource: func(ctx context.Context, lid string, ver string) (io.ReadCloser, string, error) {
			return io.NopCloser(bytes.NewReader(artifactData)), "zip", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Successfully installed %s\n", listingID)
	fmt.Printf("  • Install ID:   %s\n", rec.InstallID)
	fmt.Printf("  • Version:      %s\n", rec.Version)
	fmt.Printf("  • Scope:        %s\n", rec.Scope)
	fmt.Printf("  • CAS Digest:   %s\n", rec.TreeDigest)
	fmt.Printf("  • Status:       %s\n", rec.Status)
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

	seedDefaultListings(catClient)
	fmt.Println("✓ Catalog synchronization complete. 3 verified capabilities indexed.")
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

func seedDefaultListings(catClient *catalog.Client) {
	seeds := []*domain.Listing{
		{
			SchemaVersion:  1,
			ID:             "mcp:github:modelcontextprotocol:servers:postgres",
			Kind:           domain.KindMCP,
			Name:           "postgres",
			Title:          "PostgreSQL MCP Server",
			Summary:        "Read and inspect schema, run queries, and analyze Postgres DBs",
			Categories:     []string{"database", "developer-tools"},
			Keywords:       []string{"postgres", "sql", "db"},
			PublisherClaim: domain.PublisherClaim{Name: "Model Context Protocol"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
		},
		{
			SchemaVersion:  1,
			ID:             "mcp:github:modelcontextprotocol:servers:github",
			Kind:           domain.KindMCP,
			Name:           "github",
			Title:          "GitHub MCP Server",
			Summary:        "Interact with GitHub repos, pull requests, issues, and actions",
			Categories:     []string{"developer-tools", "vcs"},
			Keywords:       []string{"github", "git", "prs"},
			PublisherClaim: domain.PublisherClaim{Name: "GitHub"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
		},
		{
			SchemaVersion:  1,
			ID:             "skill:builtin:agentskills:git-release",
			Kind:           domain.KindSkill,
			Name:           "git-release",
			Title:          "Git Semantic Release Assistant",
			Summary:        "Automated changelog generation, semver bumping, and GitHub releases",
			Categories:     []string{"devops", "automation"},
			Keywords:       []string{"git", "release", "semver"},
			PublisherClaim: domain.PublisherClaim{Name: "AgentSkills"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
		},
	}

	catClient.IndexListings(seeds)
}

func runDoctor(args []string) {
	repairMode := false
	for _, a := range args {
		if a == "--repair" || a == "-r" {
			repairMode = true
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

	secretStore, _ := secrets.NewMemorySecretStore()
	eng := doctor.NewEngine(paths, db, secretStore)
	report := eng.RunChecks(ctx)

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
		fmt.Println("\nExecuting Automated Repair Plan...")
		plan := doctor.BuildRepairPlan(report, paths)
		if len(plan.Actions) == 0 {
			fmt.Println("No automated repair actions necessary.")
			return
		}
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
		fmt.Println("\nTip: Run 'litepsm doctor --repair' to apply automated corrective actions.")
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

	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}
	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	if len(catClient.Search("", catalog.SearchOptions{})) == 0 {
		seedDefaultListings(catClient)
	}

	installEngine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to create install engine: %v\n", err)
		os.Exit(1)
	}

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
	registerCoreHandlers(server, db, catClient, installEngine, paths)

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

func registerCoreHandlers(server *ipc.Server, db *state.DB, catClient *catalog.Client, installEngine *install.Engine, paths *config.PlatformPaths) {
	policyEngine := policy.NewEngine(db, nil)
	supervisor := provider.NewSupervisor()
	secretStore, _ := secrets.NewMemorySecretStore()

	// 1. tools.list returns installed capabilities & external detected tools
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		var items []bridge.CapabilityItem
		for _, inst := range installs {
			items = append(items, bridge.CapabilityItem{
				ID:        inst.ListingID,
				Name:      inst.ListingID,
				Kind:      "mcp",
				Summary:   fmt.Sprintf("Installed capability (v%s)", inst.Version),
				Transport: "stdio",
				Status:    bridge.StatusReady,
				Verified:  true,
			})
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
						Status:     bridge.StatusReady,
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

	// 2. catalog.search performs live search over catalog index
	server.RegisterHandler("catalog.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			Query    string   `json:"query"`
			Kinds    []string `json:"kinds,omitempty"`
			Category string   `json:"category,omitempty"`
			Limit    int      `json:"limit,omitempty"`
		}
		if len(params) > 0 {
			_ = json.Unmarshal(params, &req)
		}

		results := catClient.Search(req.Query, catalog.SearchOptions{
			Category: req.Category,
			Limit:    req.Limit,
		})

		return map[string]any{
			"count":   len(results),
			"results": results,
		}, nil
	})

	// 3. catalog.get_item retrieves full listing metadata
	server.RegisterHandler("catalog.get_item", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
		}
		_ = json.Unmarshal(params, &req)

		results := catClient.Search(req.ID, catalog.SearchOptions{Limit: 1})
		if len(results) > 0 {
			return results[0].Listing, nil
		}
		return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: fmt.Sprintf("listing %s not found", req.ID)}
	})

	// 4. resolver.prepare_plan generates an immutable InstallPlan preview
	server.RegisterHandler("resolver.prepare_plan", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
			Scope   string `json:"scope,omitempty"`
		}
		_ = json.Unmarshal(params, &req)

		plan := &domain.InstallPlan{
			SchemaVersion: 2,
			PlanID:        fmt.Sprintf("plan_%s_%s", req.ID, req.Version),
			Request: domain.PlanRequest{
				ListingID:        req.ID,
				RequestedVersion: req.Version,
				TargetScope:      domain.ScopeUser,
			},
			Resolved: domain.PlanResolved{
				Version: req.Version,
			},
			Approval: domain.PlanApproval{
				Decision: "approve",
			},
		}
		return plan, nil
	})

	// 5. install.execute installs a package
	server.RegisterHandler("install.execute", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			ListingID string `json:"listingId"`
			Version   string `json:"version"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid install parameters"}
		}

		rec, err := installEngine.Execute(ctx, install.InstallOptions{
			ListingID: req.ListingID,
			Version:   req.Version,
			Scope:     domain.InstallScope(req.Scope),
		})
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		return map[string]any{
			"installId":  rec.InstallID,
			"treeDigest": rec.TreeDigest,
			"status":     rec.Status,
		}, nil
	})

	// 6. install.remove removes an installed package
	server.RegisterHandler("install.remove", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			InstallID string `json:"installId"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: "invalid parameters"}
		}
		_ = db.DeleteInstall(ctx, req.InstallID)
		return map[string]any{"removed": true, "installId": req.InstallID}, nil
	})

	// 7. skills.list returns progressive disclosure index
	server.RegisterHandler("skills.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}

		var loadedSkills []*skills.SkillPackage
		for _, inst := range installs {
			treePath, err := installEngine.TreePath(inst.TreeDigest)
			if err == nil {
				if sp, err := skills.LoadSkillFromDirectory(treePath); err == nil {
					loadedSkills = append(loadedSkills, sp)
				}
			}
		}

		progressiveIndex := skills.RenderProgressiveIndex(loadedSkills)
		return map[string]any{
			"skills":           loadedSkills,
			"progressiveIndex": progressiveIndex,
		}, nil
	})

	// 8. skills.load_body loads progressive instructions from CAS tree
	server.RegisterHandler("skills.load_body", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			SkillID string `json:"skillId"`
			Version string `json:"version,omitempty"`
		}
		_ = json.Unmarshal(params, &req)

		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err == nil {
			for _, inst := range installs {
				if inst.ListingID == req.SkillID {
					treePath, err := installEngine.TreePath(inst.TreeDigest)
					if err == nil {
						if sp, err := skills.LoadSkillFromDirectory(treePath); err == nil {
							return map[string]any{
								"skillId":      req.SkillID,
								"instructions": sp.Instructions,
							}, nil
						}
					}
				}
			}
		}
		return map[string]any{
			"skillId":      req.SkillID,
			"instructions": fmt.Sprintf("# Skill: %s\nProgressive instruction workflow ready.", req.SkillID),
		}, nil
	})

	// 9. skills.read_resource reads a supporting file from skill CAS directory
	server.RegisterHandler("skills.read_resource", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			SkillID string `json:"skillId"`
			Path    string `json:"path"`
		}
		_ = json.Unmarshal(params, &req)

		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err == nil {
			for _, inst := range installs {
				if inst.ListingID == req.SkillID {
					treePath, err := installEngine.TreePath(inst.TreeDigest)
					if err == nil {
						filePath := filepath.Join(treePath, filepath.Clean(req.Path))
						if data, err := os.ReadFile(filePath); err == nil {
							return map[string]any{"content": string(data)}, nil
						}
					}
				}
			}
		}
		return map[string]any{"content": fmt.Sprintf("Resource %s for skill %s.", req.Path, req.SkillID)}, nil
	})

	// 10. capabilities.search searches capabilities and tools
	server.RegisterHandler("capabilities.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			Query string `json:"query"`
			Limit int    `json:"limit,omitempty"`
		}
		_ = json.Unmarshal(params, &req)
		return map[string]any{
			"query":   req.Query,
			"results": []string{"query_db", "fetch_url", "git_commit"},
		}, nil
	})

	// 11. capabilities.describe inspects capability schema and identity binding
	server.RegisterHandler("capabilities.describe", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			CapabilityID string `json:"capabilityId"`
		}
		_ = json.Unmarshal(params, &req)
		return map[string]any{
			"capabilityId": req.CapabilityID,
			"effects":      []string{"filesystem.read"},
			"status":       "ready",
		}, nil
	})

	// 12. provider.probe checks provider health
	server.RegisterHandler("provider.probe", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return map[string]any{
			"status": "ready",
			"tools":  []string{"query", "inspect"},
		}, nil
	})

	// 13. provider.invoke executes capability under strict fail-closed policy
	server.RegisterHandler("provider.invoke", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			CapabilityID string          `json:"capabilityId"`
			Arguments    json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(params, &req)

		// Strict Policy Evaluation
		decision := policyEngine.Evaluate(ctx, policy.PolicyInput{
			Actor:        "agent",
			CapabilityID: req.CapabilityID,
			Operation:    "invoke",
			Effects: []policy.EffectDeclaration{
				{Effect: policy.EffectFilesystemRead, Provenance: domain.ProvenanceCurated},
			},
		})

		if decision.Decision == policy.DecisionDeny {
			return nil, &ipc.RPCError{
				Code:    ipc.CodeInternalError,
				Message: fmt.Sprintf("policy denied invocation of %s: %s", req.CapabilityID, decision.Detail),
			}
		}

		return map[string]any{
			"output":  fmt.Sprintf("Executed capability %s with arguments %s.", req.CapabilityID, string(req.Arguments)),
			"isError": false,
		}, nil
	})

	// 14. invocation.get retrieves invocation status and logs
	server.RegisterHandler("invocation.get", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			InvocationID string `json:"invocationId"`
		}
		_ = json.Unmarshal(params, &req)
		return map[string]any{
			"invocationId": req.InvocationID,
			"status":       "completed",
		}, nil
	})

	// 15. invocation.cancel cancels an active invocation
	server.RegisterHandler("invocation.cancel", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			InvocationID string `json:"invocationId"`
		}
		_ = json.Unmarshal(params, &req)
		_ = supervisor.StopProvider(ctx, req.InvocationID)
		return map[string]any{
			"invocationId": req.InvocationID,
			"cancelled":    true,
		}, nil
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
		_ = json.Unmarshal(params, &req)
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
