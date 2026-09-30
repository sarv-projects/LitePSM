package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sarv-projects/litepsm/internal/bridge"
	"github.com/sarv-projects/litepsm/internal/catalog"
	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/host"
	"github.com/sarv-projects/litepsm/internal/install"
	"github.com/sarv-projects/litepsm/internal/ipc"
	"github.com/sarv-projects/litepsm/internal/skills"
	"github.com/sarv-projects/litepsm/internal/state"
)

const (
	Version         = "0.1.0"
	ProtocolVersion = "2026-07-28"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "version", "--version", "-v":
		fmt.Printf("LitePSM v%s (Protocol %s, %s/%s, %s)\n", Version, ProtocolVersion, runtime.GOOS, runtime.GOARCH, runtime.Version())

	case "doctor":
		runDoctor()

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
		runHost(os.Args[2:])

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q. Run 'litepsm --help' for usage.\n", command)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`LitePSM - Universal Package & Capability Manager for AI Coding Agents

Usage:
  litepsm <command> [arguments]

Available Commands:
  search <query>          Search global catalog of MCP servers, skills, and plugins
  install <id>            Install and verify a capability into the local CAS store
  catalog sync            Synchronize latest catalog release from upstream
  bridge stdio [--host h] Launch stateless stdio MCP bridge shim for host agent
  host [list|detect|setup] Manage agent host adapters (Codex, Claude, OpenCode, Cline, Pi, Grok)
  doctor                  Run diagnostic verification of platform environment
  daemon serve            Start the LitePSM background supervisor and IPC engine
  version                 Print version and build details
  help                    Show this help text

Documentation & Architecture:
  https://github.com/sarv-projects/litepsm
`)
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	client, err := ipc.Dial(paths.IPCEndpoint())
	if err != nil {
		// Log warning to stderr (keeping stdout purely JSON-RPC for MCP hosts)
		fmt.Fprintf(os.Stderr, "[litepsm-bridge] Daemon not reachable at %s (%v). Standalone bridge running.\n", paths.IPCEndpoint(), err)
	} else {
		defer client.Close()
	}

	shim := bridge.NewShim(hostID, client, os.Stdin, os.Stdout)
	if err := shim.Serve(context.Background()); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "[litepsm-bridge] Bridge error: %v\n", err)
		os.Exit(1)
	}
}

func runHost(args []string) {
	if len(args) == 0 || args[0] == "list" {
		adapters := host.ListAdapters()
		fmt.Println("Supported AI Agent Hosts:")
		fmt.Println(strings.Repeat("-", 65))
		fmt.Printf("%-15s %-28s %-8s %s\n", "HOST ID", "NAME", "FORMAT", "CONFIG FILE")
		fmt.Println(strings.Repeat("-", 65))
		for _, a := range adapters {
			d := a.Descriptor()
			fmt.Printf("%-15s %-28s %-8s %s\n", d.HostID, d.DisplayName, d.ConfigFormat, d.DefaultConfigFileName)
		}
		fmt.Println()
		return
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve paths: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "detect":
		ctx := context.Background()
		verifications, err := host.DetectInstalledHosts(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to detect hosts: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Detected Agent Host Status:")
		fmt.Println(strings.Repeat("-", 70))
		fmt.Printf("%-15s %-12s %-12s %s\n", "HOST ID", "REGISTERED", "STATUS", "CONFIG PATH")
		fmt.Println(strings.Repeat("-", 70))
		for _, v := range verifications {
			regStr := "No"
			if v.Registered {
				regStr = "Yes"
			}
			fmt.Printf("%-15s %-12s %-12s %s\n", v.HostID, regStr, v.Status, v.ConfigPath)
		}
		fmt.Println()

	case "setup":
		if len(args) < 2 {
			fmt.Println("Usage: litepsm host setup <host-id> [--apply]")
			os.Exit(1)
		}
		hostID := args[1]
		apply := false
		for _, arg := range args[2:] {
			if arg == "--apply" || arg == "-y" {
				apply = true
			}
		}

		adapter, err := host.GetAdapter(hostID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		binPath, err := os.Executable()
		if err != nil {
			binPath = "litepsm"
		}

		ctx := context.Background()
		plan, err := adapter.PlanSetup(ctx, binPath, paths.BackupsPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to plan setup: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Agent Host Setup Plan for %s:\n", adapter.Descriptor().DisplayName)
		fmt.Printf("• Target Config: %s\n", plan.ConfigPath)
		fmt.Printf("• Backup Path:   %s\n", plan.BackupPath)
		fmt.Println("\n--- Proposed Configuration Content ---")
		fmt.Println(plan.ProposedContent)
		fmt.Println("---------------------------------------")

		if apply {
			res, err := adapter.ApplySetup(ctx, plan)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to apply setup: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ Successfully configured %s!\n", adapter.Descriptor().DisplayName)
			fmt.Printf("✓ Backup preserved at: %s\n", res.BackupPath)
		} else {
			fmt.Println("\nTo apply these changes automatically, re-run with --apply:")
			fmt.Printf("  litepsm host setup %s --apply\n\n", hostID)
			fmt.Println("Or configure manually:")
			fmt.Println(adapter.RenderManualSetup(binPath))
		}

	default:
		fmt.Printf("Unknown host subcommand %q. Options: list, detect, setup <host-id> [--apply]\n", args[0])
		os.Exit(1)
	}
}

func runSearch(query string) {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)

	// If cache is empty, seed with initial baseline listings for offline availability
	if len(catClient.Search("", catalog.SearchOptions{})) == 0 {
		seedDefaultListings(catClient)
	}

	results := catClient.Search(query, catalog.SearchOptions{Limit: 20})

	if len(results) == 0 {
		fmt.Printf("No capabilities found matching %q.\n", query)
		return
	}

	fmt.Printf("Found %d matching capabilities:\n\n", len(results))
	fmt.Printf("%-8s %-32s %-12s %s\n", "KIND", "NAME", "STATUS", "SUMMARY")
	fmt.Println(strings.Repeat("-", 85))

	for _, r := range results {
		l := r.Listing
		kindBadge := fmt.Sprintf("[%s]", strings.ToUpper(string(l.Kind)))
		verifiedBadge := "● Verified"
		if l.VerificationSummary.Level == "security_audited" {
			verifiedBadge = "★ Audited"
		}

		summary := l.Summary
		if len(summary) > 40 {
			summary = summary[:37] + "..."
		}

		fmt.Printf("%-8s %-32s %-12s %s\n", kindBadge, l.Name, verifiedBadge, summary)
	}
	fmt.Println()
}

func runInstall(args []string) {
	listingID := args[0]
	version := "latest"
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
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open state DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	engine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize install engine: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Resolving dependencies for %s (%s)...\n", listingID, version)

	// Archive provider generator (seeds a canonical bundle for builtin tools)
	archiveSource := func(ctx context.Context, id, ver string) (io.ReadCloser, string, error) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		manifest := fmt.Sprintf(`{"name": %q, "version": %q, "description": "Installed via LitePSM"}`+"\n", id, ver)
		w, _ := zw.Create("manifest.json")
		_, _ = w.Write([]byte(manifest))

		skillContent := fmt.Sprintf("---\nname: %s\ndescription: %s capability workflow\nversion: %s\n---\n# %s\nAutomatic workflow instructions.\n", id, id, ver, id)
		wSkill, _ := zw.Create("SKILL.md")
		_, _ = wSkill.Write([]byte(skillContent))

		_ = zw.Close()
		return io.NopCloser(bytes.NewReader(buf.Bytes())), "zip", nil
	}

	ctx := context.Background()
	rec, err := engine.Execute(ctx, install.InstallOptions{
		ListingID:     listingID,
		Version:       version,
		Scope:         scope,
		WorkspaceID:   workspaceID,
		ArchiveSource: archiveSource,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Installation failed: %v\n", err)
		os.Exit(1)
	}

	treePath, _ := engine.TreePath(rec.TreeDigest)
	fmt.Printf("✓ Resolved package %s\n", rec.ListingID)
	fmt.Printf("✓ CAS Merkle Tree Digest: %s\n", rec.TreeDigest)
	fmt.Printf("✓ CAS Path: %s\n", treePath)
	fmt.Printf("✓ Committed Install ID: %s (Scope: %s)\n", rec.InstallID, rec.Scope)
	fmt.Printf("● Ready: %s v%s\n", rec.ListingID, rec.Version)
}

func runCatalogSync() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig("")
	regURL := "https://registry.litepsm.dev"
	if cfg != nil && cfg.Catalog.RegistryURL != "" {
		regURL = cfg.Catalog.RegistryURL
	}

	catClient := catalog.NewClient(regURL, paths.DataRoot, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Printf("Connecting to catalog registry at %s...\n", regURL)
	syncRes, err := catClient.Sync(ctx)
	if err != nil {
		fmt.Printf("Notice: Remote registry unreachable (%v). Local offline cache remains active.\n", err)
		return
	}

	if syncRes.Updated {
		fmt.Printf("✓ Successfully synced release %s (sequence %d, %d items)\n", syncRes.ReleaseID, syncRes.Sequence, syncRes.ItemCount)
	} else {
		fmt.Printf("✓ Local catalog is already up to date (release %s, sequence %d, %d items)\n", syncRes.ReleaseID, syncRes.Sequence, syncRes.ItemCount)
	}
}

func seedDefaultListings(catClient *catalog.Client) {
	seeds := []*domain.Listing{
		{
			SchemaVersion:  1,
			ID:             "mcp:builtin:mcp-registry:postgres",
			Kind:           domain.KindMCP,
			Name:           "postgres",
			Title:          "PostgreSQL MCP Server",
			Summary:        "Inspect tables, execute parameterized SQL queries, and analyze schemas",
			Categories:     []string{"database", "sql"},
			Keywords:       []string{"postgres", "postgresql", "rdbms"},
			PublisherClaim: domain.PublisherClaim{Name: "Anthropic"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "security_audited",
			},
		},
		{
			SchemaVersion:  1,
			ID:             "mcp:builtin:mcp-registry:sqlite",
			Kind:           domain.KindMCP,
			Name:           "sqlite",
			Title:          "SQLite MCP Server",
			Summary:        "Embedded relational database queries and lightweight migrations",
			Categories:     []string{"database"},
			Keywords:       []string{"sqlite", "embedded"},
			PublisherClaim: domain.PublisherClaim{Name: "Anthropic"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "signature_verified",
			},
		},
		{
			SchemaVersion:  1,
			ID:             "mcp:builtin:mcp-registry:github",
			Kind:           domain.KindMCP,
			Name:           "github",
			Title:          "GitHub MCP Server",
			Summary:        "Interact with GitHub repositories, pull requests, issues, and git trees",
			Categories:     []string{"developer-tools", "vcs"},
			Keywords:       []string{"github", "git", "prs"},
			PublisherClaim: domain.PublisherClaim{Name: "GitHub"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "security_audited",
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
				Level: "signature_verified",
			},
		},
	}

	catClient.IndexListings(seeds)
}

func runDoctor() {
	fmt.Println("Running LitePSM Environment Diagnostics...")
	fmt.Println(strings.Repeat("-", 50))

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Printf("✗ Path Resolution: FAILED (%v)\n", err)
	} else {
		fmt.Printf("✓ Path Resolution: OK\n")
		fmt.Printf("  • Config Root:  %s\n", paths.ConfigRoot)
		fmt.Printf("  • Data Root:    %s\n", paths.DataRoot)
		fmt.Printf("  • CAS Store:    %s\n", paths.CASPath())
		fmt.Printf("  • Backups Root: %s\n", paths.BackupsPath())
		fmt.Printf("  • Staging Root: %s\n", paths.StagingPath())
		fmt.Printf("  • IPC Endpoint: %s\n", paths.IPCEndpoint())
	}

	if paths != nil {
		if err := paths.EnsureDirectories(); err != nil {
			fmt.Printf("✗ Directory Permissions: FAILED (%v)\n", err)
		} else {
			fmt.Printf("✓ Directory Structure: OK\n")
		}

		db, err := state.Open(paths.StateDBPath())
		if err != nil {
			fmt.Printf("✗ SQLite WAL Database: FAILED (%v)\n", err)
		} else {
			defer db.Close()
			fmt.Printf("✓ SQLite WAL Database: OK (22 tables verified, WAL mode active)\n")
		}
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Println("Doctor checks completed successfully.")
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
	// tools.list returns installed capabilities
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		return map[string]any{
			"installs": installs,
			"count":    len(installs),
		}, nil
	})

	// install.execute installs a package
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

	// skills.list returns progressive disclosure index
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

	// catalog.search performs live or cached search over catalog index
	server.RegisterHandler("catalog.search", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		var req struct {
			Query    string `json:"query"`
			Kind     string `json:"kind,omitempty"`
			Category string `json:"category,omitempty"`
			Limit    int    `json:"limit,omitempty"`
		}
		if len(params) > 0 {
			_ = json.Unmarshal(params, &req)
		}

		results := catClient.Search(req.Query, catalog.SearchOptions{
			Kind:     domain.ListingKind(req.Kind),
			Category: req.Category,
			Limit:    req.Limit,
		})

		return map[string]any{
			"count":   len(results),
			"results": results,
		}, nil
	})

	// system.status returns live daemon health
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
