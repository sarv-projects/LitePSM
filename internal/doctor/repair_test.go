package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/state"
)

func repairPaths(t *testing.T) *config.PlatformPaths {
	t.Helper()
	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}
	return paths
}

func openRepairDB(t *testing.T, paths *config.PlatformPaths) *state.DB {
	t.Helper()
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestApplyRepairPlan_CleanStagingRefusesWithoutJournal(t *testing.T) {
	ctx := context.Background()
	paths := repairPaths(t)
	db := openRepairDB(t, paths)

	orphan := filepath.Join(paths.StagingPath(), "op_orphan_1")
	if err := os.MkdirAll(orphan, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	plan := &RepairPlan{Actions: []RepairAction{{
		ID: "clean_staging", Description: "purge", ActionKind: "filesystem_clean",
	}}}

	err := ApplyRepairPlan(ctx, plan, paths, nil) // nil journal
	if err == nil {
		t.Fatal("expected an error when the journal is unavailable")
	}
	if plan.Actions[0].Applied {
		t.Error("Applied must not be true when nothing was removed")
	}
	if !strings.Contains(plan.Actions[0].Error, "journal") {
		t.Errorf("action must explain the refusal, got %q", plan.Actions[0].Error)
	}
	if _, statErr := os.Stat(orphan); statErr != nil {
		t.Error("staging entry must survive a refused clean")
	}

	// Same plan with a journal: the orphan goes, nothing is claimed otherwise.
	err = ApplyRepairPlan(ctx, plan, paths, db)
	if err != nil {
		t.Fatalf("ApplyRepairPlan with journal failed: %v", err)
	}
	if !plan.Actions[0].Applied {
		t.Errorf("expected orphan removal to be applied, error=%q", plan.Actions[0].Error)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Error("orphan staging entry was not removed")
	}
}

func TestApplyRepairPlan_CleanStagingSkipsActiveOperations(t *testing.T) {
	ctx := context.Background()
	paths := repairPaths(t)
	db := openRepairDB(t, paths)

	const activeOp = "op_active_0001"
	if err := db.CreateOperation(ctx, activeOp, nil, "install", "idem_1"); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, activeOp, "staging"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}

	activeDir := filepath.Join(paths.StagingPath(), activeOp)
	orphanDir := filepath.Join(paths.StagingPath(), "op_orphan_gone")
	for _, d := range []string{activeDir, orphanDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
	}

	plan := &RepairPlan{Actions: []RepairAction{{
		ID: "clean_staging", Description: "purge", ActionKind: "filesystem_clean",
	}}}
	if err := ApplyRepairPlan(ctx, plan, paths, db); err != nil {
		t.Fatalf("ApplyRepairPlan failed: %v", err)
	}

	if _, err := os.Stat(activeDir); err != nil {
		t.Error("staging of an in-flight operation was deleted by clean_staging")
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Error("orphan staging entry was not removed")
	}
	if !plan.Actions[0].Applied {
		t.Errorf("orphan removal must be reported applied, error=%q", plan.Actions[0].Error)
	}
}

func TestApplyRepairPlan_ReadDirFailureIsReported(t *testing.T) {
	ctx := context.Background()
	paths := repairPaths(t)
	db := openRepairDB(t, paths)

	// Replace the staging directory with a regular file so ReadDir fails with
	// a non-ENOENT error.
	staging := paths.StagingPath()
	if err := os.RemoveAll(staging); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if err := os.WriteFile(staging, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	plan := &RepairPlan{Actions: []RepairAction{{
		ID: "clean_staging", Description: "purge", ActionKind: "filesystem_clean",
	}}}
	err := ApplyRepairPlan(ctx, plan, paths, db)
	if err == nil {
		t.Fatal("expected the ReadDir failure to be returned")
	}
	if plan.Actions[0].Applied {
		t.Error("Applied must stay false when the listing failed")
	}
	if plan.Actions[0].Error == "" {
		t.Error("the failure must be recorded on the action")
	}
}

func TestApplyRepairPlan_CollectsFailuresAndKeepsGoing(t *testing.T) {
	ctx := context.Background()
	paths := repairPaths(t)
	db := openRepairDB(t, paths)

	plan := &RepairPlan{Actions: []RepairAction{
		{ID: "repair_dirs", Description: "dirs", ActionKind: "directory_repair"}, // paths==nil below
		{ID: "clean_staging", Description: "purge", ActionKind: "filesystem_clean"},
		{ID: "unknown_action", Description: "??", ActionKind: "other"},
	}}

	err := ApplyRepairPlan(ctx, plan, nil, db)
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	if plan.Actions[0].Applied || plan.Actions[0].Error == "" {
		t.Errorf("repair_dirs with nil paths must fail explicitly: %+v", plan.Actions[0])
	}
	if plan.Actions[2].Error == "" {
		t.Errorf("unknown actions must be reported, not silently ignored: %+v", plan.Actions[2])
	}
	if !strings.Contains(err.Error(), "repair_dirs") || !strings.Contains(err.Error(), "unknown repair action") {
		t.Errorf("joined error must carry both failures: %v", err)
	}
}

func TestBuildRepairPlan_OnlyProposesKnownActions(t *testing.T) {
	report := &DoctorReport{
		Checks: []CheckResult{
			{ID: "check_staging", Status: StatusWarn},
			{ID: "check_dirs", Status: StatusFail},
			{ID: "check_cas", Status: StatusFail},
		},
	}
	plan := BuildRepairPlan(report, repairPaths(t))
	if len(plan.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(plan.Actions))
	}
	if plan.Actions[0].ID != "clean_staging" || plan.Actions[1].ID != "repair_dirs" {
		t.Errorf("unexpected actions: %+v", plan.Actions)
	}
}
