package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/secrets"
	"github.com/sarv-projects/litepsm/internal/state"
)

func TestDoctor_RunChecksAndRepair(t *testing.T) {
	tempDir := t.TempDir()

	paths := &config.PlatformPaths{
		DataRoot:    filepath.Join(tempDir, "data"),
		ConfigRoot:  filepath.Join(tempDir, "config"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
		SocketPath:  filepath.Join(tempDir, "run", "test.sock"),
		PipeName:    `\\.\pipe\test`,
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	defer db.Close()

	store, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	defer store.Close()

	engine := NewEngine(paths, db, store)
	ctx := context.Background()

	// 1. Initial healthy run
	report := engine.RunChecks(ctx)
	if report.OverallStatus == StatusFail {
		t.Fatalf("expected healthy or warning status, got fail: %+v", report)
	}
	if len(report.Checks) < 10 {
		t.Fatalf("expected 10 diagnostic checks, got %d", len(report.Checks))
	}

	// 2. Inject anomaly (dangling staging scratch file)
	danglingFile := filepath.Join(paths.StagingPath(), "dangling_download.tmp")
	if err := os.WriteFile(danglingFile, []byte("garbage data"), 0600); err != nil {
		t.Fatalf("failed to write dangling file: %v", err)
	}

	reportWithAnomaly := engine.RunChecks(ctx)
	if reportWithAnomaly.OverallStatus != StatusWarn {
		t.Errorf("expected warning status for dangling staging file, got %s", reportWithAnomaly.OverallStatus)
	}

	// 3. Generate repair plan
	plan := BuildRepairPlan(reportWithAnomaly, paths)
	if len(plan.Actions) == 0 {
		t.Fatalf("expected at least 1 repair action for staging hygiene")
	}

	// 4. Apply repair plan
	if err := ApplyRepairPlan(ctx, plan, paths, db); err != nil {
		t.Fatalf("ApplyRepairPlan failed: %v", err)
	}

	// 5. Verify clean after repair
	reportClean := engine.RunChecks(ctx)
	if reportClean.OverallStatus == StatusFail {
		t.Fatalf("expected healthy status after repair, got %+v", reportClean)
	}
	if _, err := os.Stat(danglingFile); !os.IsNotExist(err) {
		t.Errorf("dangling file was not purged by repair plan")
	}
}
