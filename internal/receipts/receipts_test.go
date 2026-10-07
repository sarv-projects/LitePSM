package receipts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordGetCancel(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	inv, err := s.Record("inst_x/srv/query", "prov-1", OutcomeSucceeded, start, start.Add(time.Second), "sha256:in", "sha256:out")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if inv.Receipt == nil || inv.Receipt.ReceiptHash == "" {
		t.Fatal("receipt must be hashed")
	}
	got, err := s.Get(inv.InvocationID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Receipt.ReceiptHash != inv.Receipt.ReceiptHash {
		t.Error("Get must return the same receipt")
	}
	// Cancel on a terminal invocation is idempotent, not an error.
	c, err := s.Cancel(inv.InvocationID)
	if err != nil {
		t.Fatalf("Cancel terminal: %v", err)
	}
	if c.Cancelled || c.State != OutcomeSucceeded {
		t.Errorf("terminal cancel must be a no-op: %+v", c)
	}
	if _, err := s.Get("invocation_nonexistent"); err == nil {
		t.Error("unknown id must fail")
	}
}

func TestChainAndCancelPending(t *testing.T) {
	s, _ := Open("")
	start := time.Now().UTC()
	a, _ := s.Record("cap/a", "prov-9", OutcomeSucceeded, start, start, "", "")
	b, _ := s.Record("cap/b", "prov-9", OutcomeFailed, start, start, "", "")
	if b.Receipt.PrevReceiptHash != a.Receipt.ReceiptHash {
		t.Error("receipts must chain per provider")
	}
	if err := s.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	// Tamper is detected.
	b2, _ := s.Get(b.InvocationID)
	_ = b2
	s.mu.Lock()
	s.invocs[b.InvocationID].Receipt.Outcome = OutcomeSucceeded
	s.mu.Unlock()
	if err := s.VerifyChain(); err == nil {
		t.Error("expected tamper detection")
	}
}

func TestDurableReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.jsonl")
	s, _ := Open(path)
	start := time.Now().UTC()
	inv, err := s.Record("cap/a", "prov-1", OutcomeSucceeded, start, start, "", "")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	got, err := s2.Get(inv.InvocationID)
	if err != nil {
		t.Fatalf("Get after replay: %v", err)
	}
	if got.Receipt.ReceiptHash != inv.Receipt.ReceiptHash {
		t.Error("replayed receipt differs")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
