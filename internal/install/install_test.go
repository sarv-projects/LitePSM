package install

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/state"
)

func createTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("failed to write zip content %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

func setupTestEngine(t *testing.T) (*Engine, *state.DB, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	casRoot := filepath.Join(tempDir, "cas")
	stagingRoot := filepath.Join(tempDir, "staging")

	engine, err := NewEngine(db, casRoot, stagingRoot)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, db, tempDir
}

func TestInstall_SuccessAndDeduplication(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	zipData := createTestZip(t, map[string][]byte{
		"index.js":     []byte("console.log('hello');\n"),
		"package.json": []byte("{\"name\": \"tool-a\"}\n"),
	})

	archiveSource := func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(zipData)), "zip", nil
	}

	// First installation
	opts1 := InstallOptions{
		ListingID:     "tool-a",
		Version:       "1.0.0",
		Scope:         domain.ScopeUser,
		ArchiveSource: archiveSource,
	}

	rec1, err := engine.Execute(ctx, opts1)
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	if rec1.ListingID != "tool-a" || rec1.Version != "1.0.0" {
		t.Errorf("unexpected record: %+v", rec1)
	}

	treePath1, err := engine.TreePath(rec1.TreeDigest)
	if err != nil {
		t.Fatalf("failed to get tree path: %v", err)
	}
	if _, err := os.Stat(treePath1); err != nil {
		t.Fatalf("tree path does not exist on disk: %v", err)
	}

	// Verify install in SQLite
	stored1, err := db.GetInstall(ctx, rec1.InstallID)
	if err != nil {
		t.Fatalf("failed to retrieve stored install: %v", err)
	}
	if stored1.TreeDigest != rec1.TreeDigest {
		t.Errorf("digest mismatch in db: %s vs %s", stored1.TreeDigest, rec1.TreeDigest)
	}

	// Second installation (same content -> should deduplicate in CAS)
	opts2 := InstallOptions{
		ListingID:     "tool-b",
		Version:       "2.0.0",
		Scope:         domain.ScopeUser,
		ArchiveSource: archiveSource,
	}

	rec2, err := engine.Execute(ctx, opts2)
	if err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	if rec2.TreeDigest != rec1.TreeDigest {
		t.Errorf("expected identical Merkle tree digest, got %s vs %s", rec1.TreeDigest, rec2.TreeDigest)
	}
}

func TestInstall_ApprovalReplayPrevention(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	zipData := createTestZip(t, map[string][]byte{
		"server.py": []byte("print('ok')\n"),
	})
	archiveSource := func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(zipData)), "zip", nil
	}

	approvalID := "appr_test_123"
	expires := time.Now().Add(1 * time.Hour)
	err := db.RecordApproval(ctx, approvalID, "install-plan", "hash123", "user", "cli-tty", "user", &expires)
	if err != nil {
		t.Fatalf("failed to record approval: %v", err)
	}

	// 1st consumption should succeed
	opts := InstallOptions{
		ListingID:     "tool-c",
		Version:       "1.0.0",
		ApprovalID:    approvalID,
		ArchiveSource: archiveSource,
	}

	_, err = engine.Execute(ctx, opts)
	if err != nil {
		t.Fatalf("first execution with approval failed: %v", err)
	}

	// 2nd consumption with same approval must fail (replay attack prevented)
	_, err = engine.Execute(ctx, opts)
	if err == nil {
		t.Fatal("expected approval replay error, got nil")
	}

	if !strings.Contains(err.Error(), "LPSM-POLICY-APPROVAL-CONSUMED") && !strings.Contains(err.Error(), "already been consumed") {
		t.Errorf("expected approval consumed error, got: %v", err)
	}
}

func TestInstall_SafeRollbackPreservesSharedTrees(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	zipData := createTestZip(t, map[string][]byte{
		"shared.js": []byte("export const version = 1;\n"),
	})

	archiveSource := func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(zipData)), "zip", nil
	}

	// 1. Install tool-shared successfully
	rec1, err := engine.Execute(ctx, InstallOptions{
		ListingID:     "tool-shared",
		Version:       "1.0.0",
		ArchiveSource: archiveSource,
	})
	if err != nil {
		t.Fatalf("tool-shared install failed: %v", err)
	}

	sharedTreePath, _ := engine.TreePath(rec1.TreeDigest)
	if _, err := os.Stat(sharedTreePath); err != nil {
		t.Fatalf("shared tree path does not exist: %v", err)
	}

	// 2. Attempt install of tool-failing which has same tree content, but an invalid approval ID to force failure after tree staging
	invalidApprovalID := "appr_non_existent"
	_, err = engine.Execute(ctx, InstallOptions{
		ListingID:     "tool-failing",
		Version:       "1.0.0",
		ApprovalID:    invalidApprovalID,
		ArchiveSource: archiveSource,
	})
	if err == nil {
		t.Fatal("expected failure on non-existent approval")
	}

	// 3. Verify that the pre-existing shared tree from tool-shared is STILL INTACT on disk!
	if _, err := os.Stat(sharedTreePath); err != nil {
		t.Fatalf("CRITICAL BUG: pre-existing shared tree was deleted during rollback of tool-failing: %v", err)
	}
}
