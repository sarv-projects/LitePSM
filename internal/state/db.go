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
	raw    *sql.DB
	dbPath string
	mu     sync.RWMutex
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

	db := &DB{raw: rawDB, dbPath: dbPath}

	// Run initial pragmas directly to ensure connection defaults
	if err := db.applyBaselinePragmas(); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("failed to apply sqlite pragmas: %w", err)
	}

	// Run migrations
	backupDir := filepath.Join(dir, "backups", "db")
	if err := db.ApplyMigrations(context.Background(), backupDir); err != nil {
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
