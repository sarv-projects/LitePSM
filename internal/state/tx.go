package state

// tx.go — one SQLite transaction for the whole state side of an install.
//
// install_mcp.go used to save the install row, the component row, one host
// registration per host and one deployment-ledger row per write as separate
// statements: a failure at any of them left earlier rows behind describing a
// config write that had already happened (or, worse, orphaned nothing at all
// because the config write was already done). WithTx puts every one of those
// statements in a single transaction, so the state side of an install is all
// or nothing — the durability anchor ARCH/33 §5 asks for.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sarv-projects/litespm/internal/domain"
)

// Execer is the write surface shared by *sql.DB and *sql.Tx, so repository
// writes can run standalone or inside a larger transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execer is the historical unexported spelling; the alias keeps the existing
// helpers unchanged while letting exported wrappers name the type.
type execer = Execer

// WithTx runs fn inside one SQLite transaction: the transaction commits when
// fn returns nil and rolls back (leaving no rows) otherwise. fn must not
// commit or roll back the transaction itself.
//
// Panics inside fn roll the transaction back via the deferred rollback.
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if db == nil {
		return fmt.Errorf("state.WithTx: nil database")
	}
	if fn == nil {
		return fmt.Errorf("state.WithTx: nil callback")
	}
	tx, err := db.raw.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin state transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state transaction: %w", err)
	}
	return nil
}

// SaveInstallExec writes an install record through ex (a *sql.Tx or *sql.DB).
func (db *DB) SaveInstallExec(ctx context.Context, ex Execer, rec *domain.InstallRecord) error {
	return db.saveInstallExec(ctx, ex, rec)
}

// SaveInstallComponentExec writes a component row through ex.
func (db *DB) SaveInstallComponentExec(ctx context.Context, ex Execer, comp *domain.InstallComponentRecord) error {
	return db.saveInstallComponentExec(ctx, ex, comp)
}

// SaveHostRegistrationExec writes a managed host-config registration through
// ex. The fingerprint requirement is identical to SaveHostRegistration: an
// entry LiteSPM cannot prove it wrote is never recorded.
func (db *DB) SaveHostRegistrationExec(ctx context.Context, ex Execer, reg *domain.HostRegistrationRecord) error {
	key := reg.ManagedEntryKey
	if key == "" {
		key = "litespm"
	}
	if reg.EntryFingerprint == "" {
		return fmt.Errorf("host registration %s/%s: entry fingerprint is required (hash of the written entry)", reg.HostID, key)
	}
	query := `
	INSERT INTO host_registrations (host_id, scope, workspace_id, config_path, managed_entry_key, entry_fingerprint, registered_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(host_id, scope, workspace_id, managed_entry_key) DO UPDATE SET
		config_path = excluded.config_path,
		entry_fingerprint = excluded.entry_fingerprint,
		registered_at = excluded.registered_at;`

	_, err := ex.ExecContext(ctx, query,
		reg.HostID,
		string(reg.Scope),
		reg.WorkspaceID,
		reg.ConfigPath,
		key,
		reg.EntryFingerprint,
		reg.RegisteredAt,
	)
	return err
}
