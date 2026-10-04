package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/state"
)

const testPlanID = "plan_ABCDEFGHIJKMNPQRSTVWXYZ01"

// saveTestPlan seals a plan with a real planHash and persists it.
func saveTestPlan(t *testing.T, db *state.DB, plan *domain.InstallPlan) *domain.InstallPlan {
	t.Helper()
	hash, err := domain.ComputePlanHash(plan)
	if err != nil {
		t.Fatalf("ComputePlanHash failed: %v", err)
	}
	plan.PlanHash = hash
	if err := db.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}
	return plan
}

func baseTestPlan(listingID, version string) *domain.InstallPlan {
	now := time.Now().UTC()
	return &domain.InstallPlan{
		SchemaVersion: 2,
		PlanID:        testPlanID,
		CreatedAt:     now,
		ExpiresAt:     now.Add(15 * time.Minute),
		Request: domain.PlanRequest{
			ListingID:        listingID,
			RequestedVersion: version,
			TargetScope:      domain.ScopeUser,
		},
		Resolved: domain.PlanResolved{
			Version:   version,
			Artifacts: []domain.PlanArtifact{},
		},
		Effects: []string{"package.install"},
	}
}

func testZipSource(files map[string][]byte) ArchiveSourceFunc {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write(content)
	}
	_ = zw.Close()
	zipData := buf.Bytes()
	return func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(zipData)), "zip", nil
	}
}

// opState reads the journal state and plan binding of the most recent operation.
func opState(t *testing.T, db *state.DB) (stateName string, planID string) {
	t.Helper()
	err := db.Raw().QueryRow(
		"SELECT state, COALESCE(plan_id, '') FROM operations ORDER BY created_at DESC, operation_id DESC LIMIT 1",
	).Scan(&stateName, &planID)
	if err != nil {
		t.Fatalf("failed to read operation row: %v", err)
	}
	return stateName, planID
}

func TestInstall_PlanBoundExecution(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	saveTestPlan(t, db, baseTestPlan("tool-plan", "1.2.3"))

	// Only the plan id is supplied: listing, version and scope must be derived
	// from the verified plan, not guessed.
	rec, err := engine.Execute(ctx, InstallOptions{
		PlanID:        testPlanID,
		ArchiveSource: testZipSource(map[string][]byte{"index.js": []byte("ok\n")}),
	})
	if err != nil {
		t.Fatalf("plan-bound install failed: %v", err)
	}
	if rec.ListingID != "tool-plan" || rec.Version != "1.2.3" || rec.Scope != domain.ScopeUser {
		t.Fatalf("record not derived from plan: %+v", rec)
	}

	stateName, planID := opState(t, db)
	if stateName != "committed" {
		t.Errorf("expected final journal state committed, got %s", stateName)
	}
	if planID != testPlanID {
		t.Errorf("expected operation bound to plan %s, got %q", testPlanID, planID)
	}
	if nonTerminal, err := db.GetNonTerminalOperations(ctx); err != nil || len(nonTerminal) != 0 {
		t.Errorf("expected no non-terminal operations, got %d (err=%v)", len(nonTerminal), err)
	}
}

func TestInstall_PlanHashMismatchRejected(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	plan := baseTestPlan("tool-tampered", "1.0.0")
	// Persist a hash that does not match the stored document (tampering).
	plan.PlanHash = "sha256:" + strings.Repeat("0", 64)
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}

	_, err := engine.Execute(ctx, InstallOptions{
		PlanID:        testPlanID,
		ArchiveSource: testZipSource(map[string][]byte{"a": []byte("a")}),
	})
	if err == nil {
		t.Fatal("expected tampered plan to be rejected")
	}
	if code := domain.ErrorCode(err); code != "LPSM-PLAN-STALE" {
		t.Fatalf("expected LPSM-PLAN-STALE, got %s (%v)", code, err)
	}
	if installs, err := db.ListInstalls(ctx, domain.ScopeUser, ""); err != nil || len(installs) != 0 {
		t.Errorf("no install may exist after plan rejection, got %d (err=%v)", len(installs), err)
	}
	if nonTerminal, _ := db.GetNonTerminalOperations(ctx); len(nonTerminal) != 0 {
		t.Errorf("rejection must happen before journaling, found %d open operations", len(nonTerminal))
	}
}

func TestInstall_PlanExpiredRejected(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	plan := baseTestPlan("tool-expired", "1.0.0")
	plan.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	saveTestPlan(t, db, plan)

	_, err := engine.Execute(ctx, InstallOptions{
		PlanID:        testPlanID,
		ArchiveSource: testZipSource(map[string][]byte{"a": []byte("a")}),
	})
	if err == nil {
		t.Fatal("expected expired plan to be rejected")
	}
	if code := domain.ErrorCode(err); code != "LPSM-PLAN-EXPIRED" {
		t.Fatalf("expected LPSM-PLAN-EXPIRED, got %s (%v)", code, err)
	}
}

func TestInstall_PlanUnknownIDRejected(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	_, err := engine.Execute(ctx, InstallOptions{
		PlanID:        "plan_MISSING0000000000000000",
		ArchiveSource: testZipSource(map[string][]byte{"a": []byte("a")}),
	})
	if err == nil {
		t.Fatal("expected unknown plan id to be rejected")
	}
	if code := domain.ErrorCode(err); code != "LPSM-STATE-NOT-FOUND" {
		t.Fatalf("expected LPSM-STATE-NOT-FOUND, got %s (%v)", code, err)
	}
}

func TestInstall_PlanRequestMismatchRejected(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	saveTestPlan(t, db, baseTestPlan("tool-plan", "1.2.3"))

	_, err := engine.Execute(ctx, InstallOptions{
		PlanID:        testPlanID,
		ListingID:     "tool-other",
		ArchiveSource: testZipSource(map[string][]byte{"a": []byte("a")}),
	})
	if err == nil {
		t.Fatal("expected listing/plan mismatch to be rejected")
	}
	if code := domain.ErrorCode(err); code != "LPSM-STATE-CONFLICT" {
		t.Fatalf("expected LPSM-STATE-CONFLICT, got %s (%v)", code, err)
	}
}

func TestInstall_PlanArtifactDigestVerification(t *testing.T) {
	ctx := context.Background()
	zipSource := testZipSource(map[string][]byte{"index.js": []byte("console.log(1);\n")})

	// Real digest of the archive the source will serve.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("index.js")
	_, _ = w.Write([]byte("console.log(1);\n"))
	_ = zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	realDigest := "sha256:" + hex.EncodeToString(sum[:])

	t.Run("matching digest installs", func(t *testing.T) {
		engine, db, _ := setupTestEngine(t)
		defer db.Close()

		plan := baseTestPlan("tool-digest", "1.0.0")
		plan.Resolved.Artifacts = []domain.PlanArtifact{{
			ArtifactID: "art_1", Type: "zip", Locator: "mem://art_1", Digest: realDigest,
		}}
		saveTestPlan(t, db, plan)

		if _, err := engine.Execute(ctx, InstallOptions{PlanID: testPlanID, ArchiveSource: zipSource}); err != nil {
			t.Fatalf("install with correct digest failed: %v", err)
		}
	})

	t.Run("mismatching digest fails closed", func(t *testing.T) {
		engine, db, _ := setupTestEngine(t)
		defer db.Close()

		plan := baseTestPlan("tool-digest-bad", "1.0.0")
		plan.Resolved.Artifacts = []domain.PlanArtifact{{
			ArtifactID: "art_1", Type: "zip", Locator: "mem://art_1",
			Digest: "sha256:" + strings.Repeat("ab", 32),
		}}
		saveTestPlan(t, db, plan)

		_, err := engine.Execute(ctx, InstallOptions{PlanID: testPlanID, ArchiveSource: zipSource})
		if err == nil {
			t.Fatal("expected digest mismatch to fail")
		}
		if code := domain.ErrorCode(err); code != "LPSM-VERIFY-CHECKSUM-MISMATCH" {
			t.Fatalf("expected LPSM-VERIFY-CHECKSUM-MISMATCH, got %s (%v)", code, err)
		}
		if installs, _ := db.ListInstalls(ctx, domain.ScopeUser, ""); len(installs) != 0 {
			t.Errorf("failed digest check must leave no install rows, got %d", len(installs))
		}
		if nonTerminal, _ := db.GetNonTerminalOperations(ctx); len(nonTerminal) != 0 {
			t.Errorf("failed operation must be rolled back, %d still open", len(nonTerminal))
		}
	})
}

// Rollback must touch only its own operation: another operation's staging and
// shared CAS trees survive.
func TestInstall_RollbackIsPerOperation(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()

	const (
		opMine     = "op_mine_0001"
		opOther    = "op_other_0001"
		treeOwn    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		treeShared = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	if err := db.CreateOperation(ctx, opMine, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.CreateOperation(ctx, opOther, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.RecordOperationTree(ctx, opMine, treeOwn, true); err != nil {
		t.Fatalf("RecordOperationTree failed: %v", err)
	}
	if err := db.RecordOperationTree(ctx, opMine, treeShared, false); err != nil {
		t.Fatalf("RecordOperationTree failed: %v", err)
	}

	mineStaging := filepath.Join(engine.stagingRoot, opMine)
	otherStaging := filepath.Join(engine.stagingRoot, opOther)
	ownTreePath, _ := engine.TreePath(treeOwn)
	sharedTreePath, _ := engine.TreePath(treeShared)
	for _, dir := range []string{mineStaging, otherStaging, filepath.Dir(ownTreePath), filepath.Dir(sharedTreePath)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
	}
	if err := os.WriteFile(ownTreePath, []byte("own"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := os.WriteFile(sharedTreePath, []byte("shared"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if err := engine.Rollback(ctx, opMine); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	if _, err := os.Stat(mineStaging); !os.IsNotExist(err) {
		t.Error("own staging directory must be removed")
	}
	if _, err := os.Stat(otherStaging); err != nil {
		t.Error("another operation's staging must never be touched by a per-operation rollback")
	}
	if _, err := os.Stat(ownTreePath); !os.IsNotExist(err) {
		t.Error("tree created by this operation must be removed")
	}
	if _, err := os.Stat(sharedTreePath); err != nil {
		t.Error("shared pre-existing tree must be preserved")
	}

	// opMine must be terminal while opOther is still open.
	nonTerminal, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		t.Fatalf("GetNonTerminalOperations failed: %v", err)
	}
	if len(nonTerminal) != 1 || nonTerminal[0].OperationID != opOther {
		t.Errorf("expected only %s to remain open, got %+v", opOther, nonTerminal)
	}
}
