package state

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial_schema.sql
var initialSchemaSQL string

// DB wraps a SQLite 3 connection pool configured for WAL mode and strong durability.
type DB struct {
	raw *sql.DB
	mu  sync.RWMutex
}

// Open opens or creates the SQLite state database at the given path and applies pending migrations.
func Open(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
	}

	// SQLite connection string with busy timeout and WAL pragmas
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", dbPath)
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database at %s: %w", dbPath, err)
	}

	// Configure pool limits for single-writer safety and non-blocking reads
	rawDB.SetMaxOpenConns(25)
	rawDB.SetMaxIdleConns(5)

	db := &DB{raw: rawDB}

	// Run initial pragmas directly to ensure connection defaults
	if err := db.applyBaselinePragmas(); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("failed to apply sqlite pragmas: %w", err)
	}

	// Run migrations
	if err := db.migrate(); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	return db, nil
}

func (db *DB) applyBaselinePragmas() error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, pragma := range pragmas {
		if _, err := db.raw.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) migrate() error {
	ctx := context.Background()

	// Ensure schema_migrations table exists
	createMigTable := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		description TEXT NOT NULL
	);`
	if _, err := db.raw.ExecContext(ctx, createMigTable); err != nil {
		return fmt.Errorf("failed to ensure schema_migrations table: %w", err)
	}

	// Check if version 1 is applied
	var count int
	err := db.raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check migration version: %w", err)
	}

	if count == 0 {
		tx, err := db.raw.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to start migration transaction: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		if _, err := tx.ExecContext(ctx, initialSchemaSQL); err != nil {
			return fmt.Errorf("failed to execute 001_initial_schema.sql: %w", err)
		}

		recordMig := "INSERT INTO schema_migrations (version, description) VALUES (1, '001_initial_schema');"
		if _, err := tx.ExecContext(ctx, recordMig); err != nil {
			return fmt.Errorf("failed to record schema migration 1: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration transaction: %w", err)
		}
	}

	return nil
}

// Close closes the underlying SQLite database.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.raw.Close()
}

// Raw returns the underlying *sql.DB.
func (db *DB) Raw() *sql.DB {
	return db.raw
}
