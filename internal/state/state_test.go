package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

func openTestDB(t *testing.T) (*DB, string) {
	tmpDir, err := os.MkdirTemp("", "litepsm-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "state.db")
	db, err := Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Open failed: %v", err)
	}

	return db, tmpDir
}

func TestDatabaseOpenAndMigrate(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()

	// Verify WAL mode
	var journalMode string
	if err := db.raw.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected journal_mode=wal, got %s", journalMode)
	}

	// Verify foreign keys ON
	var foreignKeys int
	if err := db.raw.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", foreignKeys)
	}

	// Verify all 22 core tables are created
	expectedTables := []string{
		"schema_migrations", "settings", "sources", "source_snapshots",
		"catalog_releases", "plans", "approvals", "operations",
		"operation_steps", "artifacts", "installs", "install_components",
		"providers", "provider_sessions", "capabilities", "capability_grants",
		"host_registrations", "host_backups", "audit_events", "auth_profiles",
		"oauth_sessions", "operation_trees",
	}

	for _, tbl := range expectedTables {
		var count int
		err := db.raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?", tbl).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s was not created by migration", tbl)
		}
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()

	// Attempt to insert an install_component referencing non-existent install_id
	comp := &domain.InstallComponentRecord{
		InstallID:     "inst_nonexistent",
		ComponentName: "server",
		Kind:          domain.ComponentMCPProvider,
	}

	err := db.SaveInstallComponent(ctx, comp)
	if err == nil {
		t.Fatal("expected foreign key violation when saving component for non-existent install, but succeeded")
	}
}

func TestApprovalAtomicReplayPrevention(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	approvalID := "app_test_replay_001"

	// Record initial approval
	err := db.RecordApproval(ctx, approvalID, "install-plan", "sha256:1234", "user", "cli-tty", "user", nil)
	if err != nil {
		t.Fatalf("RecordApproval failed: %v", err)
	}

	// First consumption must succeed
	if err := db.ConsumeApproval(ctx, approvalID); err != nil {
		t.Fatalf("first ConsumeApproval failed: %v", err)
	}

	// Second consumption on the same approval MUST fail closed with LPSM-POLICY-APPROVAL-CONSUMED
	err = db.ConsumeApproval(ctx, approvalID)
	if err == nil {
		t.Fatal("expected second ConsumeApproval to fail with replay error, but succeeded")
	}

	if lpsmErr, ok := err.(*domain.LPSMError); ok {
		if lpsmErr.Code != "LPSM-POLICY-APPROVAL-CONSUMED" {
			t.Fatalf("expected code LPSM-POLICY-APPROVAL-CONSUMED, got %s", lpsmErr.Code)
		}
	} else {
		t.Fatalf("expected domain.LPSMError, got %T: %v", err, err)
	}
}

func TestOperationJournalAndRollback(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	opID := "op_test_001"

	// Create operation
	if err := db.CreateOperation(ctx, opID, nil, "install", "idemp_001"); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}

	// Advance state to staging
	if err := db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}

	// Create simulated staging folder
	stagingDir := filepath.Join(tmpDir, "staging", opID)
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		t.Fatalf("failed to create staging dir: %v", err)
	}
	testFile := filepath.Join(stagingDir, "scratch.txt")
	_ = os.WriteFile(testFile, []byte("in flight data"), 0600)

	// Execute crash recovery
	err := db.RecoverIncompleteOperations(ctx, tmpDir, nil, nil)
	if err != nil {
		t.Fatalf("RecoverIncompleteOperations failed: %v", err)
	}

	// Verify operation state rolled_back
	nonTerminal, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		t.Fatalf("GetNonTerminalOperations failed: %v", err)
	}
	if len(nonTerminal) != 0 {
		t.Fatalf("expected 0 non-terminal operations after recovery, got %d", len(nonTerminal))
	}

	// Verify staging directory was deleted
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("expected staging dir to be removed by recovery, but still exists")
	}
}

func TestSafeCASRollbackPreservation(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	opID := "op_test_cas_safe"

	if err := db.CreateOperation(ctx, opID, nil, "install", "idemp_cas_001"); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}

	if err := db.AdvanceOperationState(ctx, opID, "commit_intent"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}

	// Case 1: Tree 1 was created by this operation (should be deleted upon rollback)
	treeDigestCreated := "sha256:aaaa_created_by_op"
	if err := db.RecordOperationTree(ctx, opID, treeDigestCreated, true); err != nil {
		t.Fatalf("RecordOperationTree created failed: %v", err)
	}

	// Case 2: Tree 2 was a shared pre-existing tree (MUST NOT be deleted)
	treeDigestShared := "sha256:bbbb_shared_tree"
	if err := db.RecordOperationTree(ctx, opID, treeDigestShared, false); err != nil {
		t.Fatalf("RecordOperationTree shared failed: %v", err)
	}

	// Simulate trees on disk
	casDir := filepath.Join(tmpDir, "cas")
	_ = os.MkdirAll(casDir, 0700)
	pathCreated := filepath.Join(casDir, "tree_created")
	pathShared := filepath.Join(casDir, "tree_shared")
	_ = os.WriteFile(pathCreated, []byte("created tree content"), 0600)
	_ = os.WriteFile(pathShared, []byte("shared tree content"), 0600)

	// Simulate verifyTreeComplete returning false (tree incomplete)
	treePathMap := map[string]string{
		treeDigestCreated: pathCreated,
		treeDigestShared:  pathShared,
	}

	err := db.RecoverIncompleteOperations(
		ctx,
		tmpDir,
		func(digest string) bool { return false }, // Incomplete!
		func(digest string) string { return treePathMap[digest] },
	)
	if err != nil {
		t.Fatalf("RecoverIncompleteOperations failed: %v", err)
	}

	// Tree created by op must have been deleted
	if _, err := os.Stat(pathCreated); !os.IsNotExist(err) {
		t.Fatal("expected newly created tree to be deleted on rollback")
	}

	// Shared pre-existing tree MUST be preserved!
	if _, err := os.Stat(pathShared); err != nil {
		t.Fatal("shared pre-existing tree was erroneously deleted on rollback! Bug 10 regression")
	}
}

func TestProjectScopedInstalls(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()

	now := time.Now()
	rec := &domain.InstallRecord{
		InstallID:   "inst_project_001",
		ListingID:   "mcp:builtin:mcp-registry:pg",
		Version:     "1.2.0",
		TreeDigest:  "sha256:tree123",
		Scope:       domain.ScopeProject,
		WorkspaceID: "ws_repo_abc",
		ProjectRoot: "/home/user/myproject",
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}

	if err := db.SaveInstall(ctx, rec); err != nil {
		t.Fatalf("SaveInstall failed: %v", err)
	}

	fetched, err := db.GetInstall(ctx, "inst_project_001")
	if err != nil {
		t.Fatalf("GetInstall failed: %v", err)
	}

	if fetched.Scope != domain.ScopeProject || fetched.WorkspaceID != "ws_repo_abc" || fetched.ProjectRoot != "/home/user/myproject" {
		t.Fatalf("project-scoped fields mismatch: %+v", fetched)
	}

	list, err := db.ListInstalls(ctx, domain.ScopeProject, "ws_repo_abc")
	if err != nil {
		t.Fatalf("ListInstalls failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 install in workspace, got %d", len(list))
	}
}
