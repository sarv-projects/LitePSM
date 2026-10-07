// Package lifecycle coordinates the install/remove path as one audited unit
// over filesystem, host config, deployment ledger, and SQLite (ARCH/33 §5).
//
// Installs record one deployment_mutations row per owned file write inside
// the same logical operation as the SQLite commit; removals are ledger-driven
// and symmetric: reconcile each owned locator three-way, remove only nodes
// still owned, detach (never delete) user-edited nodes, then delete state.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/state"
)

// Orchestrator owns the cross-cutting install/remove coordination. The state
// DB remains the durability anchor; the ledger observes the same operation.
type Orchestrator struct {
	db     *state.DB
	ledger *deployment.Ledger
}

// New builds an orchestrator over an open state database.
func New(db *state.DB) *Orchestrator {
	return &Orchestrator{db: db, ledger: deployment.NewLedger(db.Raw())}
}

// Ledger exposes the deployment ledger for install paths that write it.
func (o *Orchestrator) Ledger() *deployment.Ledger {
	return o.ledger
}

// HashFile digests a file's current bytes for pre/post images. A missing file
// hashes to "" so reconcile reports it as missing rather than as a hash.
func HashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// OwnedWrite builds the deployment-ledger row for one owned write with
// explicit images (ARCH/33 §2):
//
//   - preImage is the hash of the OWNED STRUCTURE before this install wrote
//     it ("" when it did not exist). For a host config entry that is the
//     EntryInstallResult.PriorFingerprint; for a skill directory it is ""
//     because Install refuses a destination that already exists. Without it
//     the Reconcile pre-image arm is dead and every three-way decision
//     degrades to a two-way one.
//   - postImage is the hash of what was actually written: the entry
//     fingerprint for a config node, the content digest for a skill
//     directory. SaveMutation refuses an empty one.
//   - priorEntry is the canonical text of a user-authored node this write
//     replaced ("" when none existed), so uninstall restores it instead of
//     deleting it.
//   - backupPath is the pre-edit backup file the write was taken against ( ""
//     when the write created the target). `litespm restore` replays it for a
//     byte-exact rollback; SaveMutation records it on first insert only.
func OwnedWrite(installID, capabilityID, hostID, scope, filePath, structureType, locator, preImage, postImage, priorEntry, backupPath string) *deployment.Mutation {
	return &deployment.Mutation{
		InstallID:     installID,
		CapabilityID:  capabilityID,
		HostID:        hostID,
		Scope:         scope,
		FilePath:      filePath,
		StructureType: structureType,
		Locator:       locator,
		PreImageHash:  preImage,
		PostImageHash: postImage,
		PriorEntry:    priorEntry,
		BackupPath:    backupPath,
		Owner:         "litespm",
	}
}

// RecordOwnedWrite stores one ledger row outside a transaction. Install paths
// that already hold a *sql.Tx call deployment.SaveMutationExec directly with
// OwnedWrite instead, so the row lands in the same transaction as the install
// row it belongs to.
func (o *Orchestrator) RecordOwnedWrite(ctx context.Context, m *deployment.Mutation) error {
	return o.ledger.SaveMutation(ctx, m)
}

// RemovePlan is the ledger-driven uninstall plan: which owned nodes are safe
// to remove and which must be detached because the user edited them.
type RemovePlan struct {
	InstallID string
	Remove    []deployment.Mutation
	Detach    []deployment.ReconcileReport
	Missing   []deployment.ReconcileReport
}

// PlanRemove reconciles every owned mutation for an install without writing
// anything (ARCH/33 §4: a reconcile pass is read-only until a resolution is
// chosen). currentHash maps a mutation to the locator's present image ("" =
// missing).
func (o *Orchestrator) PlanRemove(ctx context.Context, installID string, currentHash func(deployment.Mutation) string) (*RemovePlan, error) {
	muts, err := o.ledger.MutationsForInstall(ctx, installID)
	if err != nil {
		return nil, err
	}
	plan := &RemovePlan{InstallID: installID}
	for _, m := range muts {
		if m.Detached {
			continue
		}
		rep := deployment.Reconcile(m, currentHash(m))
		switch rep.Outcome {
		case deployment.ReconcileUnmodified:
			plan.Remove = append(plan.Remove, m)
		case deployment.ReconcileUserEdited:
			plan.Detach = append(plan.Detach, rep)
		default:
			plan.Missing = append(plan.Missing, rep)
		}
	}
	return plan, nil
}

// RemoveInstall executes a symmetric remove: owned-and-unmodified locators
// are stripped via stripOwned, user-edited nodes are detached (left in
// place), and only then are the SQLite rows deleted in one transaction.
// stripOwned may be nil when there is nothing file-backed to strip (pure
// state removal); detach handling always runs.
func (o *Orchestrator) RemoveInstall(ctx context.Context, installID string, currentHash func(deployment.Mutation) string, stripOwned func(deployment.Mutation) error) (*RemovePlan, error) {
	plan, err := o.PlanRemove(ctx, installID, currentHash)
	if err != nil {
		return nil, err
	}
	for _, m := range plan.Remove {
		if stripOwned != nil {
			if err := stripOwned(m); err != nil {
				return plan, fmt.Errorf("remove owned %s (%s): %w", m.FilePath, m.Locator, err)
			}
		}
	}
	for _, rep := range plan.Detach {
		_ = o.ledger.DetachMutation(ctx, rep.Mutation.MutationID, "user-edited node kept local")
	}
	for _, rep := range plan.Missing {
		_ = o.ledger.DetachMutation(ctx, rep.Mutation.MutationID, "owned node missing; orphan detached")
	}
	if err := deleteInstallRows(ctx, o.db.Raw(), installID); err != nil {
		return plan, err
	}
	return plan, nil
}

// deleteInstallRows removes providers/capabilities state that cascades from
// the install row, then the install row itself, in one transaction. A missing
// install is an explicit not-found error, never a silent success.
func deleteInstallRows(ctx context.Context, raw *sql.DB, installID string) error {
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM installs WHERE install_id = ?;`, installID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("install %s not found", installID)
	}
	return tx.Commit()
}
