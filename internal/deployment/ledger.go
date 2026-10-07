// Package deployment implements the ARCH/33 deployment mutation ledger and
// three-way reconciliation.
//
// The ledger records what LiteSPM wrote, where, and against what pre-state,
// so uninstall and update can be surgical: only the owned locator is removed,
// never a whole-file backup restore that would destroy sibling edits.
package deployment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Mutation is one ARCH/33 §2 deployment_mutations row: a single atomic owned
// write inside one install.
type Mutation struct {
	MutationID    string
	InstallID     string
	CapabilityID  string
	HostID        string
	Scope         string
	FilePath      string
	StructureType string
	Locator       string
	PreImageHash  string
	PostImageHash string
	Owner         string
	// PriorEntry is the canonical text of a user-authored node this write
	// replaced ("" when the locator did not exist). Uninstall restores it.
	PriorEntry string
	Detached   bool
	CreatedAt  time.Time
}

// ReconcileOutcome is the ARCH/33 §4 decision for one owned locator.
type ReconcileOutcome string

const (
	// ReconcileUnmodified: C == B (or A == C): safe to update/uninstall.
	ReconcileUnmodified ReconcileOutcome = "unmodified"
	// ReconcileUserEdited: C != A and C != B: keep-local by default, never overwrite.
	ReconcileUserEdited ReconcileOutcome = "user_edited"
	// ReconcileMissing: the node or file is gone: report orphan, do not recreate.
	ReconcileMissing ReconcileOutcome = "missing"
)

// ReconcileReport is the read-only result of reconciling one mutation.
type ReconcileReport struct {
	Mutation Mutation
	Outcome  ReconcileOutcome
	Current  string
	Detail   string
}

// NewMutationID mints mutation_<26> in Crockford base32 (ARCH/23 note).
func NewMutationID() (string, error) {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("mutation_")
	for i := 0; i < 26; i++ {
		sb.WriteByte(alphabet[int(b[i%len(b)])%32])
	}
	return sb.String(), nil
}

// HashStructure digests an owned structure's canonical bytes (ARCH/33 §2.2).
// Callers pass the JCS-canonical encoding for JSON; raw bytes otherwise.
func HashStructure(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Execer is the write surface shared by *sql.DB and *sql.Tx, so a mutation can
// be recorded inside the same transaction as the install row it belongs to.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Store is the ledger persistence seam. It is implemented over *sql.DB by
// Ledger; the interface keeps the reconciler testable without SQLite.
type Store interface {
	SaveMutation(ctx context.Context, m *Mutation) error
	MutationsForInstall(ctx context.Context, installID string) ([]Mutation, error)
	DetachMutation(ctx context.Context, mutationID, reason string) error
}

// Ledger persists mutations in the deployment_mutations table. The table is
// created by migration 002; SaveMutation is idempotent per mutation id.
type Ledger struct {
	db *sql.DB
}

// NewLedger wraps a raw SQLite handle.
func NewLedger(db *sql.DB) *Ledger {
	return &Ledger{db: db}
}

// SaveMutation records one owned write. It is idempotent per target
// (install, host, scope, file, locator): re-installing the same entry updates
// the post-image but KEEPS the original pre-image and prior entry, because the
// first install's pre-state is the user's, while a later re-install's
// "pre-state" is LiteSPM's own previous write and must never be restored.
func (l *Ledger) SaveMutation(ctx context.Context, m *Mutation) error {
	return SaveMutationExec(ctx, l.db, m)
}

// SaveMutationExec is SaveMutation over any Execer (e.g. a *sql.Tx).
func SaveMutationExec(ctx context.Context, ex Execer, m *Mutation) error {
	if m.MutationID == "" {
		id, err := NewMutationID()
		if err != nil {
			return err
		}
		m.MutationID = id
	}
	if m.Owner == "" {
		m.Owner = "litespm"
	}
	if m.InstallID == "" || m.HostID == "" || m.FilePath == "" || m.Locator == "" {
		return fmt.Errorf("deployment mutation needs install, host, file and locator (got %q/%q/%q/%q)",
			m.InstallID, m.HostID, m.FilePath, m.Locator)
	}
	if m.PostImageHash == "" {
		return fmt.Errorf("deployment mutation %s/%s has no post-image hash: record the hash of what was written", m.HostID, m.Locator)
	}
	_, err := ex.ExecContext(ctx, `
	INSERT INTO deployment_mutations
		(mutation_id, install_id, capability_id, host_id, scope, file_path,
		 structure_type, locator, pre_image_hash, post_image_hash, owner, prior_entry, detached, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)
	ON CONFLICT(install_id, host_id, scope, file_path, locator) DO UPDATE SET
		capability_id = excluded.capability_id,
		post_image_hash = excluded.post_image_hash,
		detached = 0;`,
		m.MutationID, m.InstallID, m.CapabilityID, m.HostID, m.Scope,
		m.FilePath, m.StructureType, m.Locator,
		m.PreImageHash, m.PostImageHash, m.Owner, m.PriorEntry,
	)
	if err != nil {
		return fmt.Errorf("save deployment mutation: %w", err)
	}
	return nil
}

func (l *Ledger) MutationsForInstall(ctx context.Context, installID string) ([]Mutation, error) {
	rows, err := l.db.QueryContext(ctx, `
	SELECT mutation_id, install_id, capability_id, host_id, scope, file_path,
	       structure_type, locator, pre_image_hash, post_image_hash, owner, prior_entry, detached, created_at
	FROM deployment_mutations WHERE install_id = ? ORDER BY created_at, mutation_id;`, installID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mutation
	for rows.Next() {
		var m Mutation
		var detached int
		if err := rows.Scan(&m.MutationID, &m.InstallID, &m.CapabilityID, &m.HostID,
			&m.Scope, &m.FilePath, &m.StructureType, &m.Locator,
			&m.PreImageHash, &m.PostImageHash, &m.Owner, &m.PriorEntry, &detached, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Detached = detached == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (l *Ledger) DetachMutation(ctx context.Context, mutationID, reason string) error {
	res, err := l.db.ExecContext(ctx,
		`UPDATE deployment_mutations SET detached = 1 WHERE mutation_id = ?;`, mutationID)
	if err != nil {
		return err
	}
	_ = reason // the reason is audited by the caller (lifecycle records an audit event)
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("mutation %s not found", mutationID)
	}
	return nil
}

// Reconcile applies the ARCH/33 §4 decision table to one mutation given the
// current image hash C ("" when the node or file is missing).
//
//	A = PreImageHash (before LiteSPM wrote)
//	B = PostImageHash (what LiteSPM wrote)
//	C = current hash (now)
func Reconcile(m Mutation, current string) ReconcileReport {
	if current == "" {
		return ReconcileReport{Mutation: m, Outcome: ReconcileMissing,
			Detail: "owned node or file is missing; reported as orphan, not recreated"}
	}
	if current == m.PostImageHash || current == m.PreImageHash {
		return ReconcileReport{Mutation: m, Outcome: ReconcileUnmodified, Current: current,
			Detail: "node matches the owned image (or its pre-state); safe to update/uninstall"}
	}
	return ReconcileReport{Mutation: m, Outcome: ReconcileUserEdited, Current: current,
		Detail: "user edited the owned node; keep-local by default, detach ownership, never overwrite"}
}
