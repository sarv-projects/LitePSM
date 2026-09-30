package state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// CurrentSchemaVersion is the maximum schema version supported by this binary.
const CurrentSchemaVersion = 1

// Migration defines a versioned database schema migration.
type Migration struct {
	Version     int
	Description string
	UpSQL       string
}

// Migrations contains the canonical ordered sequence of all database schema migrations.
var Migrations = []Migration{
	{
		Version:     1,
		Description: "001_initial_schema",
		UpSQL:       initialSchemaSQL,
	},
}

// ApplyMigrations executes pending migrations inside transactions with pre-migration backups.
func (db *DB) ApplyMigrations(ctx context.Context, backupDir string) error {
	// 1. Ensure schema_migrations table exists
	_, err := db.raw.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			description TEXT NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to initialize schema_migrations table: %w", err)
	}

	// 2. Query highest applied version
	var currentAppliedVersion int
	err = db.raw.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations;`).Scan(&currentAppliedVersion)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to query current schema version: %w", err)
	}

	// 3. Strict Downgrade Prevention
	if currentAppliedVersion > CurrentSchemaVersion {
		return domain.ErrStateConflict(fmt.Sprintf("database schema version v%d is newer than binary supported schema v%d (LPSM-STATE-VERSION-INCOMPATIBLE)", currentAppliedVersion, CurrentSchemaVersion))
	}

	// 4. Find unapplied migrations
	var pending []Migration
	for _, m := range Migrations {
		if m.Version > currentAppliedVersion {
			pending = append(pending, m)
		}
	}

	if len(pending) == 0 {
		return nil
	}

	// 5. Take pre-migration snapshot backup
	if backupDir != "" && db.dbPath != "" {
		if err := db.backupDatabaseFile(backupDir, currentAppliedVersion); err != nil {
			return fmt.Errorf("pre-migration backup failed: %w", err)
		}
	}

	// 6. Apply each pending migration inside an isolated transaction
	for _, m := range pending {
		tx, err := db.raw.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return fmt.Errorf("failed to begin migration v%d transaction: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, m.UpSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration v%d failed: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, description) VALUES (?, ?);`, m.Version, m.Description); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
		}
	}

	return nil
}

func (db *DB) backupDatabaseFile(backupDir string, currentVer int) error {
	_ = os.MkdirAll(backupDir, 0700)
	timestamp := time.Now().UTC().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("state-pre-migration-v%d-%s.db", currentVer, timestamp))

	in, err := os.Open(db.dbPath)
	if err != nil {
		return nil
	}
	defer in.Close()

	out, err := os.OpenFile(backupPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
