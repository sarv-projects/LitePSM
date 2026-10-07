package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The transaction's contract: LIFO compensation order, commit makes rollback a
// no-op, rollback is idempotent, and a compensation failure is surfaced.
func TestInstallTxnRollsBackInReverseOrder(t *testing.T) {
	txn := BeginInstall(t.Context())
	var order []string
	txn.Undo(func(context.Context) error { order = append(order, "first"); return nil })
	txn.Undo(func(context.Context) error { order = append(order, "second"); return nil })
	txn.Undo(func(context.Context) error { order = append(order, "third"); return nil })

	if err := txn.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if strings.Join(order, ",") != "third,second,first" {
		t.Errorf("compensation order = %v, want reverse registration order", order)
	}
	// Idempotent: a deferred rollback after an explicit one does nothing.
	if err := txn.Rollback(); err != nil {
		t.Errorf("second rollback: %v", err)
	}
	if strings.Join(order, ",") != "third,second,first" {
		t.Errorf("second rollback re-ran compensations: %v", order)
	}
}

func TestInstallTxnCommitDisablesRollback(t *testing.T) {
	txn := BeginInstall(t.Context())
	ran := false
	txn.Undo(func(context.Context) error { ran = true; return nil })
	if err := txn.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := txn.Rollback(); err != nil {
		t.Fatalf("rollback after commit: %v", err)
	}
	if ran {
		t.Error("rollback after commit ran a compensation")
	}
	if !txn.Committed() {
		t.Error("Committed() = false after Commit")
	}
	// Commit after finish is an error, not a silent second state.
	if err := txn.Commit(); err == nil {
		t.Error("second Commit must fail")
	}
}

func TestInstallTxnSurfacesCompensationFailures(t *testing.T) {
	txn := BeginInstall(t.Context())
	txn.Undo(func(context.Context) error { return errors.New("restore of config B failed") })
	txn.Undo(func(context.Context) error { return nil })
	err := txn.Rollback()
	if err == nil {
		t.Fatal("a failing compensation must not be swallowed")
	}
	if !strings.Contains(err.Error(), "restore of config B failed") {
		t.Errorf("error must name the failed step, got: %v", err)
	}
	// Both steps ran despite the first failure (LIFO: the failing one is the
	// most recent write, but earlier ones must still be compensated).
	var ran []string
	txn2 := BeginInstall(t.Context())
	txn2.Undo(func(context.Context) error { ran = append(ran, "older"); return nil })
	txn2.Undo(func(context.Context) error { ran = append(ran, "newer"); return errors.New("boom") })
	_ = txn2.Rollback()
	if len(ran) != 2 {
		t.Errorf("rollback stopped early: %v", ran)
	}
}

// Undo after commit is inert: a stray registration must not resurrect a
// committed transaction's undo list.
func TestInstallTxnUndoAfterCommitIsInert(t *testing.T) {
	txn := BeginInstall(t.Context())
	if err := txn.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	txn.Undo(func(context.Context) error { return errors.New("must never run") })
	if n := txn.Pending(); n != 0 {
		t.Errorf("Pending() = %d after commit, want 0", n)
	}
}
