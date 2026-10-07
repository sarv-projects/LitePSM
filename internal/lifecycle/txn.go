package lifecycle

// txn.go — the install-side transaction (ARCH/33 §5).
//
// An install spans two durability domains: SQLite (state rows) and the
// filesystem (host configs, skill directories). SQLite can commit atomically;
// files cannot. Until this type existed, install_mcp.go wrote every host
// config first and then saved install/component/registration/ledger rows — so
// a failure at any state write left the configs mutated with no state row
// describing them and no way to find them again.
//
// InstallTxn closes that window. Every owned write registers its compensation
// as it happens; the state rows go into one SQLite transaction (state.DB
// WithTx). Either the whole unit commits, or every compensation runs in
// reverse order (LIFO) and the machine is byte-identical to its pre-install
// state: files restored from their pre-write backups (or removed when the
// install created them), state rows never persisted.
//
// Scope and honest limits:
//
//   - It rolls back on every returned error, which is the failure mode tests
//     inject and the only one a caller observes.
//   - It does not survive a process kill between a file write and the commit:
//     crash recovery needs a write-ahead journal and is not implemented here.
//     The pre-write backup file does remain on disk, so an operator (or a
//     future recovery pass) can still restore by hand.

import (
	"context"
	"errors"
	"fmt"
)

// InstallTxn collects the compensations for one install operation. It is not
// safe for concurrent use by multiple goroutines; installs are sequential.
type InstallTxn struct {
	ctx context.Context
	// undos run in reverse registration order (LIFO), so each compensation
	// restores the machine to the state immediately before its write.
	undos []func(context.Context) error
	// committed is set once the state rows are durable; after that, rollback
	// must not run (it would undo a committed install).
	committed bool
	// finished makes Rollback idempotent: a deferred rollback after an
	// explicit one is a no-op, not a double-restore.
	finished bool
}

// BeginInstall starts a transaction bound to ctx.
func BeginInstall(ctx context.Context) *InstallTxn {
	if ctx == nil {
		ctx = context.Background()
	}
	return &InstallTxn{ctx: ctx}
}

// Ctx is the context the transaction was bound to, for compensations that
// need one.
func (t *InstallTxn) Ctx() context.Context { return t.ctx }

// Undo registers a compensation for a write that has already happened. It
// must be called immediately after the write succeeds, before the next write
// begins, so rollback order is exactly reverse write order.
//
// A compensation must restore the pre-write state of one resource and must
// tolerate being written over by a later compensation of the same resource
// (LIFO guarantees it is not, but a defensive compensation should still be
// idempotent).
func (t *InstallTxn) Undo(fn func(context.Context) error) {
	if t == nil || fn == nil || t.committed || t.finished {
		return
	}
	t.undos = append(t.undos, fn)
}

// Commit marks the unit durable. It must be called only after the state rows
// have been committed; after Commit, Rollback is a no-op.
func (t *InstallTxn) Commit() error {
	if t == nil {
		return fmt.Errorf("install transaction is nil")
	}
	if t.finished {
		return fmt.Errorf("install transaction already finished; cannot commit")
	}
	t.committed = true
	t.finished = true
	return nil
}

// Rollback runs every registered compensation in reverse order and returns
// the joined failures. It is a no-op after Commit and idempotent on repeat.
//
// A compensation failure is never swallowed: it means the machine may hold a
// half-written state, and the caller must surface it.
func (t *InstallTxn) Rollback() error {
	if t == nil || t.committed || t.finished {
		return nil
	}
	t.finished = true
	var errs []error
	for i := len(t.undos) - 1; i >= 0; i-- {
		if err := t.undos[i](t.ctx); err != nil {
			errs = append(errs, fmt.Errorf("rollback step %d of %d: %w", len(t.undos)-i, len(t.undos), err))
		}
	}
	t.undos = nil
	return errors.Join(errs...)
}

// RollbackUnlessCommitted is the deferred form: `defer txn.RollbackUnlessCommitted()`.
// Its error cannot be returned from a defer, so callers that must observe a
// rollback failure call Rollback explicitly; the deferred form still runs the
// compensations and discards nothing silently in the success path (there is
// nothing to discard: committed transactions have no undos left to run).
func (t *InstallTxn) RollbackUnlessCommitted() {
	if t == nil || t.committed || t.finished {
		return
	}
	_ = t.Rollback()
}

// Committed reports whether the transaction has committed (for tests).
func (t *InstallTxn) Committed() bool { return t != nil && t.committed }

// Pending reports how many compensations are still registered (for tests).
func (t *InstallTxn) Pending() int {
	if t == nil {
		return 0
	}
	return len(t.undos)
}
