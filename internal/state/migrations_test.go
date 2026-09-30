package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrations_InitialAndIdempotent(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")
	backupDir := filepath.Join(tempDir, "backups", "db")

	// 1. First open: applies initial migrations
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database and apply migrations: %v", err)
	}

	// 2. Second apply call: idempotent no-op
	err = db.ApplyMigrations(ctx, backupDir)
	if err != nil {
		t.Fatalf("idempotent migration check failed: %v", err)
	}

	_ = db.Close()
}

func TestMigrations_DowngradeRejection(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state_future.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Simulate a database with future migration version (e.g. v99)
	_, err = db.raw.ExecContext(ctx, `INSERT INTO schema_migrations (version, description) VALUES (99, 'future_version');`)
	if err != nil {
		t.Fatalf("failed to insert mock future version: %v", err)
	}

	// Attempting to apply migrations on older binary must halt with conflict error
	err = db.ApplyMigrations(ctx, "")
	if err == nil {
		t.Fatalf("expected downgrade rejection error on newer DB schema version, got nil")
	}
}
