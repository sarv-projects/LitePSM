package state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// CurrentSchemaVersion is the maximum schema version supported by this binary.
// It must equal the highest Version in Migrations (asserted by a test).
const CurrentSchemaVersion = 2

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
	{
		Version:     2,
		Description: "002_phase1_state_lifecycle",
		UpSQL:       phase1LifecycleSQL,
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

	// 5. Take pre-migration snapshot backup. A database with nothing applied
	// yet holds no user data, so there is nothing to protect; any other
	// database that cannot be backed up blocks the migration.
	if backupDir != "" && db.dbPath != "" && currentAppliedVersion > 0 {
		if err := db.backupDatabaseFile(backupDir, currentAppliedVersion); err != nil {
			return fmt.Errorf("pre-migration backup failed: %w", err)
		}
	}

	// 6. Apply each pending migration inside an isolated transaction
	for _, m := range pending {
		if err := db.applyOne(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

// applyOne runs one migration on a dedicated connection with foreign-key
// enforcement OFF (a PRAGMA that is a no-op inside a transaction, hence set
// beforehand). Table-rebuild migrations drop parent tables; with enforcement on,
// that implicit DELETE would cascade away child rows. Integrity is re-checked
// with PRAGMA foreign_key_check before COMMIT, and enforcement is restored on
// the connection before it returns to the pool.
func (db *DB) applyOne(ctx context.Context, m Migration) (err error) {
	conn, err := db.raw.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection for migration v%d: %w", m.Version, err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`); err != nil {
		return fmt.Errorf("failed to disable foreign keys for migration v%d: %w", m.Version, err)
	}
	defer func() {
		if _, ferr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON;`); ferr != nil && err == nil {
			err = fmt.Errorf("failed to re-enable foreign keys after migration v%d: %w", m.Version, ferr)
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin migration v%d transaction: %w", m.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, m.UpSQL); err != nil {
		return fmt.Errorf("migration v%d failed: %w", m.Version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, description) VALUES (?, ?);`, m.Version, m.Description); err != nil {
		return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
	}

	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check;`)
	if err != nil {
		return fmt.Errorf("migration v%d foreign key check failed to run: %w", m.Version, err)
	}
	violations := 0
	var first string
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if scanErr := rows.Scan(&table, &rowid, &parent, &fkid); scanErr == nil && violations == 0 {
			first = fmt.Sprintf("%s -> %s", table, parent)
		}
		violations++
	}
	_ = rows.Close()
	if violations > 0 {
		return fmt.Errorf("migration v%d would leave %d foreign key violation(s) (first: %s); rolled back", m.Version, violations, first)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
	}
	committed = true
	return nil
}

// backupDatabaseFile takes the pre-migration snapshot. It never silently skips:
// the WAL is checkpointed first (so the main file holds every committed page),
// and an unreadable database file is an error — a migration must not proceed
// without the recovery copy it promised. A brand-new database (nothing applied
// yet, so no user data) is the one explicit exemption, decided by the caller.
func (db *DB) backupDatabaseFile(backupDir string, currentVer int) error {
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return fmt.Errorf("create backup dir %s: %w", backupDir, err)
	}
	if _, err := db.raw.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		return fmt.Errorf("checkpoint WAL before backup: %w", err)
	}
	timestamp := time.Now().UTC().Format("20060102-150405.000000000")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("state-pre-migration-v%d-%s.db", currentVer, timestamp))

	in, err := os.Open(db.dbPath)
	if err != nil {
		return fmt.Errorf("open database %s for backup: %w", db.dbPath, err)
	}
	defer in.Close()

	out, err := os.OpenFile(backupPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create backup %s: %w", backupPath, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(backupPath)
		return fmt.Errorf("copy database to %s: %w", backupPath, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(backupPath)
		return fmt.Errorf("sync backup %s: %w", backupPath, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("close backup %s: %w", backupPath, err)
	}
	return nil
}
