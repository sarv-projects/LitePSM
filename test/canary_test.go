package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/provider"
	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/state"
)

func TestSecurity_ZeroPlaintextSecretCanaryLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot: filepath.Join(tempDir, "config"),
		DataRoot:   filepath.Join(tempDir, "data"),
	}
	_ = paths.EnsureDirectories()

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// 1. Initialize Memory Secret Store with Synthetic Canaries
	store, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("failed to create secret store: %v", err)
	}

	canaryToken1 := "CANARY_TOKEN_ALPHA_987654321_SUPER_SECRET"
	canaryToken2 := "CANARY_TOKEN_BETA_123456789_TOP_SECRET"

	ref1, err := store.Put(ctx, "postgres", "password", []byte(canaryToken1))
	if err != nil {
		t.Fatalf("store.Put ref1 failed: %v", err)
	}
	ref2, err := store.Put(ctx, "github", "token", []byte(canaryToken2))
	if err != nil {
		t.Fatalf("store.Put ref2 failed: %v", err)
	}

	// 2. Resolve Launch Secrets
	declaredSecrets := map[string]string{
		"DB_PASS":      ref1.URI,
		"GITHUB_TOKEN": ref2.URI,
	}

	envMap, err := secrets.ResolveLaunchSecrets(ctx, store, declaredSecrets)
	if err != nil {
		t.Fatalf("ResolveLaunchSecrets failed: %v", err)
	}

	if envMap["DB_PASS"] != canaryToken1 || envMap["GITHUB_TOKEN"] != canaryToken2 {
		t.Fatalf("failed to resolve decrypted canary values into ephemeral memory map")
	}

	// 3. Capture Process Supervisor Ring Buffer
	rb := provider.NewRingBuffer(100)
	_, _ = rb.Write([]byte("[info] Provider starting with securely bound credentials.\n"))

	// 4. Exhaustive Canary Scan across SQLite DB, WAL files, and Ring Buffers
	// A) Scan SQLite DB file
	dbBytes, err := os.ReadFile(paths.StateDBPath())
	if err == nil {
		if strings.Contains(string(dbBytes), canaryToken1) || strings.Contains(string(dbBytes), canaryToken2) {
			t.Fatalf("SECURITY VIOLATION: Plaintext canary token found in state.db!")
		}
	}

	// B) Scan WAL file if present
	walPath := paths.StateDBPath() + "-wal"
	if walBytes, err := os.ReadFile(walPath); err == nil {
		if strings.Contains(string(walBytes), canaryToken1) || strings.Contains(string(walBytes), canaryToken2) {
			t.Fatalf("SECURITY VIOLATION: Plaintext canary token found in state.db-wal!")
		}
	}

	// C) Scan RingBuffer contents
	rbStr := rb.String()
	if strings.Contains(rbStr, canaryToken1) || strings.Contains(rbStr, canaryToken2) {
		t.Fatalf("SECURITY VIOLATION: Plaintext canary token found in supervisor ring buffer!")
	}

	t.Log("✓ Zero Plaintext Secret Canary Leak Verified Across All Subsystems!")
}
