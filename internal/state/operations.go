package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
	query := `
	UPDATE operations
	SET state = ?, updated_at = CURRENT_TIMESTAMP
	WHERE operation_id = ?;`

	res, err := db.raw.ExecContext(ctx, query, newState, opID)
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
	INSERT OR REPLACE INTO operation_trees (operation_id, tree_digest, created_by_op)
	VALUES (?, ?, ?);`

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

// RecoverIncompleteOperations handles crash recovery on daemon startup.
// It rolls back staging directories and incomplete state transitions safely.
func (db *DB) RecoverIncompleteOperations(ctx context.Context, dataRoot string, verifyTreeComplete func(string) bool, treePath func(string) string) error {
	ops, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		return err
	}

	for _, op := range ops {
		switch op.State {
		case "created", "resolving", "awaiting_approval", "approved", "fetching", "verified", "staging":
			stagingPath := filepath.Join(dataRoot, "staging", op.OperationID)
			_ = os.RemoveAll(stagingPath)
			_ = db.AdvanceOperationState(ctx, op.OperationID, "rolled_back")
			_ = db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "cleaned staging and rolled back")

		case "commit_intent":
			// Query all trees linked to this operation
			treeRows, err := db.raw.QueryContext(ctx, "SELECT tree_digest, created_by_op FROM operation_trees WHERE operation_id = ?", op.OperationID)
			if err == nil {
				allComplete := true
				type treeEntry struct {
					digest      string
					createdByOp bool
				}
				var trees []treeEntry

				for treeRows.Next() {
					var digest string
					var createdInt int
					if err := treeRows.Scan(&digest, &createdInt); err == nil {
						complete := verifyTreeComplete != nil && verifyTreeComplete(digest)
						if !complete {
							allComplete = false
						}
						trees = append(trees, treeEntry{digest: digest, createdByOp: createdInt == 1})
					}
				}
				treeRows.Close()

				if allComplete && len(trees) > 0 {
					_ = db.AdvanceOperationState(ctx, op.OperationID, "committed")
					_ = db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "finalized commit for complete trees")
				} else {
					// Incomplete: only remove trees created by this operation
					for _, t := range trees {
						if t.createdByOp && treePath != nil {
							_ = os.RemoveAll(treePath(t.digest))
						}
					}
					stagingPath := filepath.Join(dataRoot, "staging", op.OperationID)
					_ = os.RemoveAll(stagingPath)
					_ = db.AdvanceOperationState(ctx, op.OperationID, "rolled_back")
					_ = db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "rolled back incomplete commit_intent trees")
				}
			}

		case "committing":
			stagingPath := filepath.Join(dataRoot, "staging", op.OperationID)
			_ = os.RemoveAll(stagingPath)
			_ = db.AdvanceOperationState(ctx, op.OperationID, "rolled_back")
			_ = db.RecordOperationStep(ctx, op.OperationID, "crash_recovery", "done", "rolled back interrupted transaction")
		}
	}
	return nil
}
