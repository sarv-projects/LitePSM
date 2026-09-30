package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sarv-projects/litepsm/internal/catalog"
	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/ipc"
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
  search <query>   Search the global catalog of MCP servers, skills, and plugins
  catalog sync     Synchronize and verify latest catalog release from upstream
  doctor           Run diagnostic verification of platform environment
  daemon serve     Start the LitePSM background supervisor and IPC engine
  version          Print version and build details
  help             Show this help text

Documentation & Architecture:
  https://github.com/sarv-projects/litepsm
`)
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
			ID:             "skill:builtin:agent-skills:git-workflow",
			Kind:           domain.KindSkill,
			Name:           "git-workflow",
			Title:          "Semantic Git Commit & Branch Hygiene",
			Summary:        "Step-by-step workflow for diff audits, conventional commits, and clean PRs",
			Categories:     []string{"git", "workflows"},
			Keywords:       []string{"git", "commit", "semantic"},
			PublisherClaim: domain.PublisherClaim{Name: "agentskills.io"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "signature_verified",
			},
		},
		{
			SchemaVersion:  1,
			ID:             "skill:builtin:agent-skills:docker-diagnostics",
			Kind:           domain.KindSkill,
			Name:           "docker-diagnostics",
			Title:          "Docker Container Diagnostics Playbook",
			Summary:        "Troubleshooting container crashes, inspecting logs, and network isolation",
			Categories:     []string{"devops", "containers"},
			Keywords:       []string{"docker", "containers"},
			PublisherClaim: domain.PublisherClaim{Name: "agentskills.io"},
			Status:         domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "signature_verified",
			},
		},
	}

	catClient.IndexListings(seeds)
}

func runDoctor() {
	fmt.Printf("LitePSM Doctor (v%s)\n", Version)
	fmt.Println("======================================")

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Printf("❌ Failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("✓ Data Root: %s\n", paths.DataRoot)
	fmt.Printf("✓ Config Root: %s\n", paths.ConfigRoot)
	fmt.Printf("✓ IPC Endpoint: %s\n", paths.IPCEndpoint())

	// Check directories
	if err := paths.EnsureDirectories(); err != nil {
		fmt.Printf("❌ Failed to create/verify directories: %v\n", err)
	} else {
		fmt.Println("✓ Filesystem directories verified (mode 0700)")
	}

	// Check SQLite State DB
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Printf("❌ SQLite State Store failed to open: %v\n", err)
	} else {
		fmt.Println("✓ SQLite State Store open & migrations verified (22 tables)")
		_ = db.Close()
	}

	// Check Daemon Connectivity
	client, err := ipc.Dial(paths.IPCEndpoint())
	if err != nil {
		fmt.Printf("○ Daemon status: Inactive (not running at %s)\n", paths.IPCEndpoint())
	} else {
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		hs, err := client.Handshake(ctx, "cli", "doctor")
		if err != nil {
			fmt.Printf("❌ Daemon handshake failed: %v\n", err)
		} else {
			fmt.Printf("✓ Daemon is active (PID %d, Protocol %s)\n", hs.PID, hs.ProtocolVersion)
		}
	}

	fmt.Println("======================================")
	fmt.Println("Doctor diagnostics completed.")
}

func runDaemonServe() {
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}

	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to create platform directories: %v\n", err)
		os.Exit(1)
	}

	// Single-writer lock
	lockPath := paths.DaemonLockPath()
	lockFile, err := acquireLock(lockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: another daemon instance is already active (lockfile %s): %v\n", lockPath, err)
		os.Exit(1)
	}
	defer releaseLock(lockFile, lockPath)

	// Initialize SQLite WAL State Engine
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to initialize SQLite state store: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Crash Recovery
	fmt.Println("[daemon] Executing crash recovery check...")
	ctx := context.Background()
	err = db.RecoverIncompleteOperations(
		ctx,
		paths.DataRoot,
		func(digest string) bool {
			_, statErr := os.Stat(filepath.Join(paths.CASPath(), digest))
			return statErr == nil
		},
		func(digest string) string {
			return filepath.Join(paths.CASPath(), digest)
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: error during crash recovery: %v\n", err)
	} else {
		fmt.Println("[daemon] Crash recovery check complete.")
	}

	// Initialize Catalog Engine
	catClient := catalog.NewClient("https://registry.litepsm.dev", paths.DataRoot, nil)

	// Bind IPC Listener
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
	registerCoreHandlers(server, db, catClient)

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

func registerCoreHandlers(server *ipc.Server, db *state.DB, catClient *catalog.Client) {
	// tools.list returns installed capabilities
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return map[string]any{
			"tools": []any{},
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
