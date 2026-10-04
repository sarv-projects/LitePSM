package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litepsm/internal/artifact"
	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/state"
)

func newTestEngine(t *testing.T) (*Engine, *state.DB, *config.PlatformPaths) {
	t.Helper()

	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		DataRoot:    filepath.Join(tempDir, "data"),
		ConfigRoot:  filepath.Join(tempDir, "config"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return NewEngine(paths, db, store), db, paths
}

// The journal check must derive its verdict from the operations table, not a
// hard-coded pass.
func TestDoctor_JournalCheckQueriesOperations(t *testing.T) {
	engine, db, _ := newTestEngine(t)
	ctx := context.Background()

	if got := engine.checkOperationJournal(ctx); got.Status != StatusPass {
		t.Fatalf("empty journal: expected pass, got %s (%s)", got.Status, got.Message)
	}

	if err := db.CreateOperation(ctx, "op_doctor_test", nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}

	got := engine.checkOperationJournal(ctx)
	if got.Status != StatusWarn {
		t.Fatalf("non-terminal operation: expected warn, got %s (%s)", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "1 operation") {
		t.Errorf("expected message to count the pending operation, got %q", got.Message)
	}
	if ids, ok := got.Details["operationIds"].([]string); !ok || len(ids) != 1 || ids[0] != "op_doctor_test" {
		t.Errorf("expected operationIds detail to list op_doctor_test, got %#v", got.Details["operationIds"])
	}

	if err := db.AdvanceOperationState(ctx, "op_doctor_test", "committed"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	if got := engine.checkOperationJournal(ctx); got.Status != StatusPass {
		t.Fatalf("terminal operation: expected pass, got %s (%s)", got.Status, got.Message)
	}
}

// The CAS check must re-hash tree contents; a bare directory existence check is
// not verification.
func TestDoctor_CASCheckVerifiesTreeDigests(t *testing.T) {
	engine, _, paths := newTestEngine(t)

	if got := engine.checkCASStore(); got.Status != StatusWarn {
		t.Fatalf("missing CAS store: expected warn, got %s (%s)", got.Status, got.Message)
	}

	treeRoot := filepath.Join(paths.CASPath(), "trees", "sha256")
	treeDir := filepath.Join(treeRoot, "placeholder")
	if err := os.MkdirAll(treeDir, 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(treeDir, "SKILL.md"), []byte("# skill"), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	digest, err := artifact.ComputeCanonicalTreeDigest(treeDir)
	if err != nil {
		t.Fatalf("ComputeCanonicalTreeDigest failed: %v", err)
	}
	goodDir := filepath.Join(treeRoot, strings.TrimPrefix(digest, "sha256:"))
	if err := os.Rename(treeDir, goodDir); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	got := engine.checkCASStore()
	if got.Status != StatusPass {
		t.Fatalf("digest-matching tree: expected pass, got %s (%s)", got.Status, got.Message)
	}
	if verified, ok := got.Details["verifiedCount"].(int); !ok || verified != 1 {
		t.Errorf("expected verifiedCount 1, got %#v", got.Details["verifiedCount"])
	}

	if err := os.WriteFile(filepath.Join(goodDir, "tampered.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	tampered := engine.checkCASStore()
	if tampered.Status != StatusFail {
		t.Fatalf("tampered tree: expected fail, got %s (%s)", tampered.Status, tampered.Message)
	}
}

// A staging ReadDir error must never be reported as an empty, clean directory.
func TestDoctor_StagingCheckNeverPassesOnReadError(t *testing.T) {
	engine, _, paths := newTestEngine(t)

	if err := os.RemoveAll(paths.StagingPath()); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}

	got := engine.checkStagingHygiene()
	if got.Status == StatusPass {
		t.Fatalf("ReadDir error reported as pass: %q", got.Message)
	}
	if got.Status != StatusWarn {
		t.Fatalf("missing staging directory: expected warn, got %s (%s)", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "staging directory not present") {
		t.Errorf("expected honest message, got %q", got.Message)
	}
}

type deleteFailingStore struct {
	secrets.SecretStore
}

func (s *deleteFailingStore) Delete(ctx context.Context, ref secrets.SecretRef) error {
	return errors.New("keyring locked")
}

// The secret-store canary must surface a Delete failure instead of ignoring it.
func TestDoctor_SecretCanarySurfacesDeleteError(t *testing.T) {
	base, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	defer base.Close()

	engine := &Engine{secretStore: &deleteFailingStore{SecretStore: base}}
	got := engine.checkSecretVault(context.Background())
	if got.Status != StatusWarn {
		t.Fatalf("delete failure: expected warn, got %s (%s)", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "keyring locked") {
		t.Errorf("expected the delete error to be surfaced, got %q", got.Message)
	}
}

// The disk check must report a measured value, not a canned pass message.
func TestDoctor_DiskCheckReportsMeasuredFreeSpace(t *testing.T) {
	engine, _, _ := newTestEngine(t)

	got := engine.checkDiskSpace()
	freeBytes, ok := got.Details["freeBytes"].(uint64)
	if !ok {
		t.Fatalf("expected measured freeBytes detail, got %#v (status %s, message %q)", got.Details["freeBytes"], got.Status, got.Message)
	}
	if freeBytes == 0 {
		t.Fatalf("expected non-zero measured free space, message %q", got.Message)
	}
	if strings.Contains(got.Message, "adequate storage available") {
		t.Errorf("canned disk message returned: %q", got.Message)
	}
}
