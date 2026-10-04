package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	summary, err := db.RecoverIncompleteOperations(ctx, filepath.Join(tmpDir, "staging"), func(string) string {
		return filepath.Join(tmpDir, "cas", "missing")
	})
	if err != nil {
		t.Fatalf("RecoverIncompleteOperations failed: %v", err)
	}
	if summary.Examined != 1 || summary.RolledBack != 1 || summary.Committed != 0 || summary.Failed != 0 {
		t.Fatalf("unexpected recovery summary: %+v", summary)
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

	// Resolve tree digests to on-disk paths for recovery cleanup
	treePathMap := map[string]string{
		treeDigestCreated: pathCreated,
		treeDigestShared:  pathShared,
	}

	summary, err := db.RecoverIncompleteOperations(
		ctx,
		filepath.Join(tmpDir, "staging"),
		func(digest string) string { return treePathMap[digest] },
	)
	if err != nil {
		t.Fatalf("RecoverIncompleteOperations failed: %v", err)
	}
	if summary.Examined != 1 || summary.RolledBack != 1 || summary.Committed != 0 || summary.Failed != 0 {
		t.Fatalf("unexpected recovery summary: %+v", summary)
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

func TestDeleteInstall_NotFoundIsAnError(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	rec := &domain.InstallRecord{
		InstallID:   "inst_user_del_0001",
		ListingID:   "mcp:builtin:mcp-registry:del",
		Version:     "1.0.0",
		TreeDigest:  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Scope:       domain.ScopeUser,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := db.SaveInstall(ctx, rec); err != nil {
		t.Fatalf("SaveInstall failed: %v", err)
	}

	if err := db.DeleteInstall(ctx, rec.InstallID); err != nil {
		t.Fatalf("DeleteInstall failed: %v", err)
	}
	if _, err := db.GetInstall(ctx, rec.InstallID); err == nil {
		t.Fatal("install still readable after delete")
	}

	// Deleting the same record again must be an explicit not-found error.
	err := db.DeleteInstall(ctx, rec.InstallID)
	if err == nil {
		t.Fatal("second delete must fail, not report success")
	}
	if code := domain.ErrorCode(err); code != "LPSM-STATE-NOT-FOUND" {
		t.Fatalf("expected LPSM-STATE-NOT-FOUND, got %s (%v)", code, err)
	}
}

func TestCommitInstallOperation_Transactional(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	treeDigest := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	// Success path: install + component + journal state land together.
	if err := db.CreateOperation(ctx, "op_tx_commit", nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, "op_tx_commit", "committing"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	rec := &domain.InstallRecord{
		InstallID: "inst_user_tx_ok", ListingID: "mcp:builtin:mcp-registry:tx", Version: "1.0.0",
		TreeDigest: treeDigest, Scope: domain.ScopeUser, Status: domain.InstallActive,
		InstalledAt: now, UpdatedAt: now,
	}
	comp := &domain.InstallComponentRecord{
		InstallID: rec.InstallID, Kind: domain.ComponentMCPProvider,
		ComponentName: "default", Path: "/cas/tree",
	}
	if err := db.CommitInstallOperation(ctx, rec, comp, "op_tx_commit"); err != nil {
		t.Fatalf("CommitInstallOperation failed: %v", err)
	}
	if _, err := db.GetInstall(ctx, rec.InstallID); err != nil {
		t.Fatalf("install missing after commit: %v", err)
	}
	nonTerminal, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		t.Fatalf("GetNonTerminalOperations failed: %v", err)
	}
	if len(nonTerminal) != 0 {
		t.Errorf("operation must be terminal after commit, still open: %+v", nonTerminal)
	}

	// Failure path: a component row that violates its FK must roll back the
	// install row written earlier in the same transaction.
	if err := db.CreateOperation(ctx, "op_tx_fail", nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, "op_tx_fail", "committing"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	badRec := &domain.InstallRecord{
		InstallID: "inst_user_tx_bad", ListingID: "mcp:builtin:mcp-registry:tx", Version: "1.0.0",
		TreeDigest: treeDigest, Scope: domain.ScopeUser, Status: domain.InstallActive,
		InstalledAt: now, UpdatedAt: now,
	}
	badComp := &domain.InstallComponentRecord{
		InstallID: "inst_does_not_exist", Kind: domain.ComponentMCPProvider,
		ComponentName: "default", Path: "/cas/tree",
	}
	if err := db.CommitInstallOperation(ctx, badRec, badComp, "op_tx_fail"); err == nil {
		t.Fatal("expected the transactional commit to fail")
	}
	if _, err := db.GetInstall(ctx, badRec.InstallID); err == nil {
		t.Fatal("install row must not survive a failed commit transaction")
	}
	var stateName string
	if err := db.Raw().QueryRow("SELECT state FROM operations WHERE operation_id = ?", "op_tx_fail").Scan(&stateName); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if stateName != "committing" {
		t.Errorf("journal must not advance on a failed commit, got %s", stateName)
	}
}

func TestListProviders_DecodesConfiguration(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	if list, err := db.ListProviders(ctx); err != nil || len(list) != 0 {
		t.Fatalf("expected an empty provider list on a fresh database, got %d (err=%v)", len(list), err)
	}

	now := time.Now().UTC()
	installID := "inst_user_prov_0001"
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   "mcp:builtin:mcp-registry:prov",
		Version:     "1.0.0",
		TreeDigest:  "sha256:1212121212121212121212121212121212121212121212121212121212121212",
		Scope:       domain.ScopeUser,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveInstall failed: %v", err)
	}
	// SaveInstallComponent keys component_id by install id (existing behaviour),
	// which is also the value SaveProvider stores in component_id.
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID: installID, Kind: domain.ComponentMCPProvider,
		ComponentName: "default", Path: "/cas/tree",
	}); err != nil {
		t.Fatalf("SaveInstallComponent failed: %v", err)
	}

	launchSpec := `{"executable":"/bin/sh","args":["-c","sleep 1"]}`
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    "prov_test_0001",
		InstallID:     installID,
		ComponentName: installID,
		Transport:     "local-stdio",
		ArgsJSON:      launchSpec,
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveProvider failed: %v", err)
	}

	list, err := db.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(list))
	}
	p := list[0]
	if p.ProviderID != "prov_test_0001" || p.Mode != "local-stdio" || p.LaunchSpecJSON != launchSpec {
		t.Errorf("unexpected provider config: %+v", p)
	}
	if !p.Enabled {
		t.Error("provider row defaults to enabled")
	}
	if p.Autostart {
		t.Error("provider row must not claim autostart it was not configured with")
	}
}

// A rollback interrupted half-way leaves the journal in "rolling_back"; the
// next recovery sweep must still resolve it to a terminal state and clean only
// what that operation created.
func TestRecovery_ResolvesInterruptedRollback(t *testing.T) {
	db, tmpDir := openTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	opID := "op_rolling_back_001"
	treeDigest := "sha256:9999999999999999999999999999999999999999999999999999999999999999"

	if err := db.CreateOperation(ctx, opID, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, opID, "rolling_back"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	if err := db.RecordOperationTree(ctx, opID, treeDigest, true); err != nil {
		t.Fatalf("RecordOperationTree failed: %v", err)
	}

	treeDir := filepath.Join(tmpDir, "cas", "trees", "sha256", strings.Repeat("9", 64))
	if err := os.MkdirAll(treeDir, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	stagingDir := filepath.Join(tmpDir, "staging", opID)
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	summary, err := db.RecoverIncompleteOperations(ctx, filepath.Join(tmpDir, "staging"), func(string) string {
		return treeDir
	})
	if err != nil {
		t.Fatalf("RecoverIncompleteOperations failed: %v", err)
	}
	if summary.RolledBack != 1 || summary.Failed != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}
	if _, err := os.Stat(treeDir); !os.IsNotExist(err) {
		t.Error("tree created by the interrupted rollback must be removed")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Error("staging of the interrupted rollback must be removed")
	}
}
