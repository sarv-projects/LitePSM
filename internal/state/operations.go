package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OperationRecord represents a durable row in operations.
type OperationRecord struct {
	OperationID    string
	PlanID         *string
	OperationType  string
	State          string
	IdempotencyKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateOperation inserts a new operation entry into the journal.
func (db *DB) CreateOperation(ctx context.Context, opID string, planID *string, opType, idempotencyKey string) error {
	query := `
	INSERT INTO operations (operation_id, plan_id, operation_type, state, idempotency_key, created_at, updated_at)
	VALUES (?, ?, ?, 'created', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`

	var planArg any
	if planID != nil && *planID != "" {
		planArg = *planID
	}
	var idempArg any
	if idempotencyKey != "" {
		idempArg = idempotencyKey
	}

	_, err := db.raw.ExecContext(ctx, query, opID, planArg, opType, idempArg)
	if err != nil {
		return fmt.Errorf("failed to create operation %s: %w", opID, err)
	}
	return nil
}

// AdvanceOperationState transitions the state of an operation and updates updated_at.
func (db *DB) AdvanceOperationState(ctx context.Context, opID, newState string) error {
	return db.advanceOperationStateExec(ctx, db.raw, opID, newState)
}

func (db *DB) advanceOperationStateExec(ctx context.Context, ex execer, opID, newState string) error {
	query := `
	UPDATE operations
	SET state = ?, updated_at = CURRENT_TIMESTAMP
	WHERE operation_id = ?;`

	res, err := ex.ExecContext(ctx, query, newState, opID)
	if err != nil {
		return fmt.Errorf("failed to update state of operation %s to %s: %w", opID, newState, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("operation %s not found", opID)
	}
	return nil
}

// RecordOperationStep logs a fine-grained step within an operation execution.
func (db *DB) RecordOperationStep(ctx context.Context, opID, stepName, status, detail string) error {
	query := `
	INSERT INTO operation_steps (operation_id, step_name, status, detail, created_at)
	VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP);`

	_, err := db.raw.ExecContext(ctx, query, opID, stepName, status, detail)
	if err != nil {
		return fmt.Errorf("failed to record step %s for operation %s: %w", stepName, opID, err)
	}
	return nil
}

// RecordOperationTree records whether a CAS tree was created by this operation or reused.
func (db *DB) RecordOperationTree(ctx context.Context, opID, treeDigest string, createdByOp bool) error {
	createdInt := 0
	if createdByOp {
		createdInt = 1
	}

	query := `
	INSERT INTO operation_trees (operation_id, tree_digest, created_by_op)
	VALUES (?, ?, ?)
	ON CONFLICT(operation_id, tree_digest) DO UPDATE SET created_by_op = excluded.created_by_op;`

	_, err := db.raw.ExecContext(ctx, query, opID, treeDigest, createdInt)
	if err != nil {
		return fmt.Errorf("failed to record operation tree %s for op %s: %w", treeDigest, opID, err)
	}
	return nil
}

// WasTreeCreatedByOperation checks if a tree was newly created by the specified operation.
func (db *DB) WasTreeCreatedByOperation(ctx context.Context, opID, treeDigest string) (bool, error) {
	query := `
	SELECT created_by_op
	FROM operation_trees
	WHERE operation_id = ? AND tree_digest = ?;`

	var createdByOp int
	err := db.raw.QueryRowContext(ctx, query, opID, treeDigest).Scan(&createdByOp)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return createdByOp == 1, nil
}

// OperationTree mirrors one operation_trees row: which CAS tree an operation
// touched and whether it created that tree itself (shared trees are never
// deleted by a single operation's rollback).
type OperationTree struct {
	TreeDigest  string
	CreatedByOp bool
}

// GetOperationTrees lists every CAS tree linked to an operation.
func (db *DB) GetOperationTrees(ctx context.Context, opID string) ([]OperationTree, error) {
	query := `SELECT tree_digest, created_by_op FROM operation_trees WHERE operation_id = ? ORDER BY tree_digest;`

	rows, err := db.raw.QueryContext(ctx, query, opID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trees for operation %s: %w", opID, err)
	}
	defer rows.Close()

	var trees []OperationTree
	for rows.Next() {
		var t OperationTree
		var createdBy int
		if err := rows.Scan(&t.TreeDigest, &createdBy); err != nil {
			return nil, err
		}
		t.CreatedByOp = createdBy == 1
		trees = append(trees, t)
	}
	return trees, rows.Err()
}

// HasInstallsForTrees reports whether any install record references one of the
// given CAS tree digests. Recovery uses it to decide whether a interrupted
// metadata commit actually completed (the install row exists) or never ran.
func (db *DB) HasInstallsForTrees(ctx context.Context, digests []string) (bool, error) {
	if len(digests) == 0 {
		return false, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(digests)), ",")
	query := `SELECT 1 FROM installs WHERE tree_digest IN (` + placeholders + `) LIMIT 1;`

	args := make([]any, len(digests))
	for i, d := range digests {
		args[i] = d
	}

	var one int
	err := db.raw.QueryRowContext(ctx, query, args...).Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// TreeInUse reports whether a CAS tree is depended on by something other than
// operation opID: an install row that references the digest, or another
// operation (not rolled back, failed or cancelled) that recorded it. Rollback
// uses it so a tree this operation created, but someone else has since reused
// or committed against, is never deleted out from under them.
func (db *DB) TreeInUse(ctx context.Context, opID, treeDigest string) (bool, error) {
	var one int
	err := db.raw.QueryRowContext(ctx, `
	SELECT 1 WHERE EXISTS (SELECT 1 FROM installs WHERE tree_digest = ?)
	   OR EXISTS (
	        SELECT 1 FROM operation_trees ot JOIN operations o ON o.operation_id = ot.operation_id
	        WHERE ot.tree_digest = ? AND ot.operation_id != ?
	          AND o.state NOT IN ('rolled_back', 'failed', 'cancelled'));`,
		treeDigest, treeDigest, opID).Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetNonTerminalOperations fetches all operations not in a final terminal state.
func (db *DB) GetNonTerminalOperations(ctx context.Context) ([]*OperationRecord, error) {
	query := `
	SELECT operation_id, plan_id, operation_type, state, idempotency_key, created_at, updated_at
	FROM operations
	WHERE state NOT IN ('committed', 'rolled_back', 'failed', 'cancelled')
	ORDER BY created_at ASC;`

	rows, err := db.raw.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query non-terminal operations: %w", err)
	}
	defer rows.Close()

	var results []*OperationRecord
	for rows.Next() {
		var op OperationRecord
		var planID, idemp sql.NullString
		if err := rows.Scan(&op.OperationID, &planID, &op.OperationType, &op.State, &idemp, &op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, err
		}
		if planID.Valid {
			op.PlanID = &planID.String
		}
		if idemp.Valid {
			op.IdempotencyKey = &idemp.String
		}
		results = append(results, &op)
	}
	return results, rows.Err()
}

// RecoverySummary reports exactly what RecoverIncompleteOperations did. Every
// examined operation is counted in at most one outcome bucket.
type RecoverySummary struct {
	Examined   int // non-terminal operations found in the journal
	RolledBack int // interrupted operations reverted (staging cleaned, own trees removed)
	Committed  int // interrupted operations whose install row already existed; journal finalized
	Failed     int // operations that could not be recovered; reason in operation_steps
}

// RecoverIncompleteOperations handles crash recovery, normally run once on
// daemon startup before it starts serving.
//
// stagingRoot must be the shared staging directory itself
// (config.PlatformPaths.StagingPath()): each operation's scratch space lives at
// <stagingRoot>/<operation_id>, the exact layout install.Engine uses. This
// function is the global startup sweep; per-operation rollback must go through
// install.Engine.Rollback so one operation can never clean another's staging.
//
// Each non-terminal operation reaches exactly one terminal state:
//   - pre-commit states (created/resolving/awaiting_approval/approved/fetching/staging)
//     lose only their staging directory;
//   - post-placement states (commit_intent/verified/committing/rolling_back) are
//     finalized as committed when an install row already references a tree this
//     operation created (the metadata transaction completed and only the journal
//     advance was lost), and are otherwise rolled back after deleting ONLY trees
//     with operation_trees.created_by_op = 1 — shared pre-existing trees are
//     never touched.
//
// treePath maps a tree digest to its CAS directory; when nil no tree is removed.
//
// It returns a non-nil error when any operation could not be recovered, so a
// caller such as the daemon's startup sweep can halt rather than proceed on a
// journal whose recovery did not fully succeed. The summary is always
// populated, including on error, so the caller can report per-bucket counts.
func (db *DB) RecoverIncompleteOperations(ctx context.Context, stagingRoot string, treePath func(string) string) (RecoverySummary, error) {
	var summary RecoverySummary

	ops, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		return summary, err
	}

	for _, op := range ops {
		summary.Examined++
		finalState, err := db.recoverOperation(ctx, op, stagingRoot, treePath)
		if err != nil {
			summary.Failed++
			_ = db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "failed", err.Error())
			continue
		}
		switch finalState {
		case "committed":
			summary.Committed++
		case "rolled_back":
			summary.RolledBack++
		}
	}

	if summary.Failed > 0 {
		return summary, fmt.Errorf(
			"recovery failed for %d of %d incomplete operation(s); see operation_steps for detail",
			summary.Failed, summary.Examined)
	}
	return summary, nil
}

// recoverOperation resolves a single interrupted operation to a terminal state.
func (db *DB) recoverOperation(ctx context.Context, op *OperationRecord, stagingRoot string, treePath func(string) string) (string, error) {
	stagingPath := filepath.Join(stagingRoot, op.OperationID)

	switch op.State {
	case "created", "resolving", "awaiting_approval", "approved", "fetching", "staging":
		// The staging root must be a directory when it exists. On Windows,
		// os.RemoveAll of "<file>/<op>" reports path-not-found, which maps to
		// "nothing to clean" and silently rolls the operation back while
		// leaving the misconfigured root in place; on Unix it fails with
		// ENOTDIR. Checking the root itself makes the classification identical
		// everywhere and keeps a broken staging root from passing recovery.
		if fi, err := os.Lstat(stagingRoot); err == nil && !fi.IsDir() {
			return "", fmt.Errorf("staging root %s is not a directory; cannot roll back %s", stagingRoot, op.OperationID)
		}
		if err := os.RemoveAll(stagingPath); err != nil {
			return "", fmt.Errorf("failed to remove staging directory %s: %w", stagingPath, err)
		}
		if err := db.AdvanceOperationState(ctx, op.OperationID, "rolled_back"); err != nil {
			return "", err
		}
		if err := db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "cleaned staging and rolled back"); err != nil {
			return "", err
		}
		return "rolled_back", nil

	case "verified", "commit_intent", "committing", "rolling_back":
		trees, err := db.GetOperationTrees(ctx, op.OperationID)
		if err != nil {
			return "", err
		}

		// Only trees this operation created can carry its install row: a
		// reused (shared) tree may legitimately be referenced by an older
		// install and says nothing about this operation's metadata commit.
		var createdByOp []string
		for _, t := range trees {
			if t.CreatedByOp {
				createdByOp = append(createdByOp, t.TreeDigest)
			}
		}

		installExists, err := db.HasInstallsForTrees(ctx, createdByOp)
		if err != nil {
			return "", err
		}
		if installExists {
			if err := db.AdvanceOperationState(ctx, op.OperationID, "committed"); err != nil {
				return "", err
			}
			if err := db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "install row already present; finalized interrupted commit"); err != nil {
				return "", err
			}
			return "committed", nil
		}

		for _, t := range trees {
			if !t.CreatedByOp || treePath == nil {
				continue
			}
			p := treePath(t.TreeDigest)
			if p == "" {
				continue
			}
			// A tree another live operation reused (or an install now
			// references) is not ours to delete any more.
			inUse, uerr := db.TreeInUse(ctx, op.OperationID, t.TreeDigest)
			if uerr != nil {
				return "", uerr
			}
			if inUse {
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				return "", fmt.Errorf("failed to remove tree %s created by operation %s: %w", t.TreeDigest, op.OperationID, err)
			}
		}
		if err := os.RemoveAll(stagingPath); err != nil {
			return "", fmt.Errorf("failed to remove staging directory %s: %w", stagingPath, err)
		}
		if err := db.AdvanceOperationState(ctx, op.OperationID, "rolled_back"); err != nil {
			return "", err
		}
		if err := db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "rolled back interrupted operation; removed only trees created by this operation"); err != nil {
			return "", err
		}
		return "rolled_back", nil

	default:
		return "", fmt.Errorf("operation %s is in unhandled state %q; inspect state.db manually", op.OperationID, op.State)
	}
}
