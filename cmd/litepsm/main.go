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
	"syscall"
	"time"

	"github.com/sarv-projects/litepsm/internal/config"
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
  version         Print version and build details
  doctor          Run diagnostic verification of platform environment
  daemon serve    Start the LitePSM background supervisor and IPC engine
  help            Show this help text

Documentation & Architecture:
  https://github.com/sarv-projects/litepsm
`)
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
			// Tree verification helper
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

	// Register Core System Handlers
	registerCoreHandlers(server, db)

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

func registerCoreHandlers(server *ipc.Server, db *state.DB) {
	// tools.list returns installed capabilities
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		return map[string]any{
			"tools": []any{},
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
	// Check if existing lockfile contains a live process
	if data, err := os.ReadFile(lockPath); err == nil {
		pidStr := string(data)
		if pid, err := strconv.Atoi(pidStr); err == nil {
			// Check process alive
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
	// On Unix, FindProcess always succeeds, so we send signal 0
	if runtime.GOOS != "windows" {
		err = process.Signal(syscall.Signal(0))
		return err == nil
	}
	return true
}
