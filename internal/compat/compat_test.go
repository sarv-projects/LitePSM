package compat

import (
	"testing"
	"time"
)

func TestDeclaredNeverVerifies(t *testing.T) {
	s := New()
	if err := s.Declare("claude-code", "mcp:x:y:z", "publisher lists support"); err != nil {
		t.Fatal(err)
	}
	if got := s.VerifiedHosts("mcp:x:y:z"); len(got) != 0 {
		t.Errorf("declared support must not verify: %v", got)
	}
}

func TestTestedVerifies(t *testing.T) {
	s := New()
	err := s.Add(Record{
		HostID: "codex", OS: "linux", PackageID: "mcp:x:y:z",
		Outcome: OutcomePass, Evidence: EvidenceTested,
		TestRef:    "test/e2e_host_test.go#TestCodexProbe",
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := s.VerifiedHosts("mcp:x:y:z")
	if len(got) != 1 || got[0] != "codex" {
		t.Errorf("VerifiedHosts = %v", got)
	}
}

func TestEvidenceRequired(t *testing.T) {
	s := New()
	if err := s.Add(Record{HostID: "h", PackageID: "p", Outcome: OutcomePass}); err == nil {
		t.Error("record without evidence must fail")
	}
	if err := s.Add(Record{HostID: "h", PackageID: "p", Outcome: OutcomePass, Evidence: EvidenceTested}); err == nil {
		t.Error("tested record without testRef must fail")
	}
	if err := s.Add(Record{HostID: "h", PackageID: "p", Outcome: "maybe", Evidence: EvidenceDeclared}); err == nil {
		t.Error("bad outcome must fail")
	}
}
