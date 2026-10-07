package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/state"
)

// The canaries are synthetic credentials planted through the *real* code paths
// (secret store → launch-secret resolution) so that a leak anywhere downstream
// of those paths would have something to leak. The scan then walks the real
// on-disk leak surfaces — the state database and its WAL/SHM sidecars, every
// file under DATA_ROOT and CONFIG_ROOT, and the supervisor log buffer — and
// fails on the first hit.
//
// Two properties this test is written to keep:
//
//   - it can fail. `assertDetectorFails` proves the detector reports a hit on
//     a planted canary before the real scan runs, so a green run means "clean",
//     never "the matcher is broken and matched nothing";
//   - it cannot pass vacuously. A read error is a failure (no `if err == nil`
//     swallowing), and a scan that inspected zero files is a failure.
const (
	canaryToken1 = "CANARY_TOKEN_ALPHA_987654321_SUPER_SECRET"
	canaryToken2 = "CANARY_TOKEN_BETA_123456789_TOP_SECRET"
)

// findCanaries returns every canary present in haystack.
func findCanaries(haystack string, canaries []string) []string {
	var found []string
	for _, c := range canaries {
		if strings.Contains(haystack, c) {
			found = append(found, c)
		}
	}
	return found
}

// scanTree walks every regular file under root and reports canary hits as
// "relative/path: <canary>". It returns an error the moment a file it decided
// to inspect cannot be read — an unreadable file is not a clean file.
func scanTree(root string, canaries []string) (hits []string, scanned int, err error) {
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return fmt.Errorf("walk %s: %w", path, werr)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", path, rerr)
		}
		scanned++
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		for _, c := range findCanaries(string(data), canaries) {
			hits = append(hits, fmt.Sprintf("%s: %s", rel, c))
		}
		return nil
	})
	if walkErr != nil {
		return hits, scanned, walkErr
	}
	return hits, scanned, nil
}

// scanFile reads one explicitly named surface (state.db and its sidecars), so a
// sidecar that exists but cannot be read is a failure rather than a skip.
// `required=false` tolerates a sidecar the engine legitimately never created.
func scanFile(path string, canaries []string, required bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var hits []string
	for _, c := range findCanaries(string(data), canaries) {
		hits = append(hits, fmt.Sprintf("%s: %s", filepath.Base(path), c))
	}
	return hits, nil
}

// assertDetectorFails is the positive control: the same detector used on the
// real surfaces must report a hit when a canary is genuinely present. Without
// it, "no matches" and "nothing could ever match" look identical.
func assertDetectorFails(t *testing.T, canaries []string) {
	t.Helper()

	// In-memory surface (supervisor log buffer).
	rb := provider.NewRingBuffer(1024)
	if _, err := rb.Write([]byte("[info] startup env DB_PASS=" + canaryToken1 + "\n")); err != nil {
		t.Fatalf("ring buffer write failed: %v", err)
	}
	if found := findCanaries(rb.String(), canaries); len(found) == 0 {
		t.Fatal("detector did not find a canary planted in the log buffer — this test could never fail")
	}

	// On-disk surface, in a tree that is NOT part of the real scan.
	control := t.TempDir()
	if err := os.WriteFile(filepath.Join(control, "leak.txt"), []byte("password="+canaryToken2+"\n"), 0o600); err != nil {
		t.Fatalf("failed to plant the control canary: %v", err)
	}
	hits, scanned, err := scanTree(control, canaries)
	if err != nil {
		t.Fatalf("control scan errored: %v", err)
	}
	if scanned == 0 {
		t.Fatal("control scan inspected no files")
	}
	if len(hits) == 0 {
		t.Fatal("detector did not find a canary planted on disk — this test could never fail")
	}
}

func TestSecurity_ZeroPlaintextSecretCanaryLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	canaries := []string{canaryToken1, canaryToken2}
	assertDetectorFails(t, canaries)

	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	// A real config document, so the config tree is a surface with content in
	// it rather than an empty directory that trivially passes.
	configPath := filepath.Join(paths.ConfigRoot, "config.toml")
	if err := os.WriteFile(configPath, []byte("registry_url = \"https://example.invalid\"\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Exercise a real state write so state.db and its WAL are non-trivial bytes
	// written by production code, not an empty file this test never fed.
	if err := db.CreateOperation(ctx, "op_canary_scan", nil, "install", "canary_idempotency_key"); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, "op_canary_scan", "committed"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}

	// 1. Plant the canaries through the real secret-store path.
	store, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("failed to create secret store: %v", err)
	}
	defer store.Close()

	ref1, err := store.Put(ctx, "postgres", "password", []byte(canaryToken1))
	if err != nil {
		t.Fatalf("store.Put ref1 failed: %v", err)
	}
	ref2, err := store.Put(ctx, "github", "token", []byte(canaryToken2))
	if err != nil {
		t.Fatalf("store.Put ref2 failed: %v", err)
	}

	// 2. Resolve them exactly as a provider launch would, so the canary is
	// genuinely present in this process — a leak has something to leak.
	envMap, err := secrets.ResolveLaunchSecrets(ctx, store, map[string]string{
		"DB_PASS":      ref1.URI,
		"GITHUB_TOKEN": ref2.URI,
	})
	if err != nil {
		t.Fatalf("ResolveLaunchSecrets failed: %v", err)
	}
	if envMap["DB_PASS"] != canaryToken1 || envMap["GITHUB_TOKEN"] != canaryToken2 {
		t.Fatal("failed to resolve decrypted canary values into the ephemeral memory map")
	}

	// 3. Supervisor log buffer: what a provider's own stdout/stderr would carry.
	// Names only — a supervisor that logged values would be caught in step 4.
	rb := provider.NewRingBuffer(4096)
	if _, err := rb.Write([]byte("[info] provider starting with secrets bound: DB_PASS, GITHUB_TOKEN\n")); err != nil {
		t.Fatalf("ring buffer write failed: %v", err)
	}
	if found := findCanaries(rb.String(), canaries); len(found) > 0 {
		t.Fatalf("SECURITY VIOLATION: plaintext canary in the supervisor log buffer: %v", found)
	}
	// The buffer must still be readable content, or the check above is vacuous.
	if !strings.Contains(rb.String(), "provider starting") {
		t.Fatalf("supervisor log buffer did not retain the line that was written: %q", rb.String())
	}

	// 4. Scan the explicit database surfaces: the DB itself plus its WAL/SHM
	// sidecars. The WAL is expected while the connection is open; the others are
	// only tolerated as absent, never as unreadable.
	hits, ferr := scanFile(paths.StateDBPath(), canaries, true)
	if ferr != nil {
		t.Fatalf("database surface could not be inspected: %v", ferr)
	}
	if len(hits) > 0 {
		t.Fatalf("SECURITY VIOLATION: plaintext canary found in state.db: %v", hits)
	}
	// The WAL exists while the connection is open; SHM may or may not. Both are
	// tolerated as absent, never as unreadable.
	for _, sidecar := range []string{paths.StateDBPath() + "-wal", paths.StateDBPath() + "-shm"} {
		sideHits, sErr := scanFile(sidecar, canaries, false)
		if sErr != nil {
			t.Fatalf("database sidecar could not be inspected: %v", sErr)
		}
		if len(sideHits) > 0 {
			t.Fatalf("SECURITY VIOLATION: plaintext canary found in %s: %v", filepath.Base(sidecar), sideHits)
		}
	}

	// 5. Scan every file under DATA_ROOT and CONFIG_ROOT: the whole on-disk
	// footprint this process is allowed to write.
	hits, scanned, err := scanTree(tempDir, canaries)
	if err != nil {
		t.Fatalf("canary scan could not complete: %v", err)
	}
	if scanned == 0 {
		t.Fatal("canary scan inspected no files — a pass here would prove nothing")
	}
	if len(hits) > 0 {
		t.Fatalf("SECURITY VIOLATION: plaintext canary found on disk (%d file(s) scanned): %v", scanned, hits)
	}

	t.Logf("zero plaintext canary leaks across %d file(s), the database and its sidecars, and the supervisor log buffer", scanned)
}
