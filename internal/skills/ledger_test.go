package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerAddAndList(t *testing.T) {
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Missing file is not an error.
	all, err := l.All()
	if err != nil || len(all) != 0 {
		t.Fatalf("expected an empty ledger, got %v err=%v", all, err)
	}

	if err := l.Add([]LedgerEntry{
		{SkillName: "pdf", HostLabel: "claude-code", DestDir: "/x/pdf"},
		{SkillName: "docx", HostLabel: "cursor", DestDir: "/x/docx"},
	}); err != nil {
		t.Fatal(err)
	}
	// Re-adding the same destination must not double-count.
	if err := l.Add([]LedgerEntry{{SkillName: "pdf", HostLabel: "claude-code", DestDir: "/x/pdf"}}); err != nil {
		t.Fatal(err)
	}
	all, _ = l.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	names, _ := l.Skills()
	if len(names) != 2 || names[0] != "docx" || names[1] != "pdf" {
		t.Fatalf("unexpected skill list: %v", names)
	}
}

func TestLedgerRemoveDeletesRecordedDirs(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))

	keepDir := filepath.Join(root, "skills", "docx")
	dropDir := filepath.Join(root, "skills", "pdf")
	writeSkill(t, keepDir)
	writeSkill(t, dropDir)

	_ = l.Add([]LedgerEntry{
		{SkillName: "docx", DestDir: keepDir},
		{SkillName: "pdf", DestDir: dropDir},
	})

	outcomes, err := l.Remove([]string{"pdf"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Removed {
		t.Fatalf("expected one removal, got %+v", outcomes)
	}
	if _, err := os.Stat(dropDir); !os.IsNotExist(err) {
		t.Fatal("removed directory still exists")
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatal("unrelated skill directory was deleted")
	}
	names, _ := l.Skills()
	if len(names) != 1 || names[0] != "docx" {
		t.Fatalf("ledger not updated: %v", names)
	}
}

// TestLedgerRemoveRefusesRepurposedDirectory is the safety property that
// matters most: a path the user has since put their own content in must survive.
func TestLedgerRemoveRefusesRepurposedDirectory(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))

	dir := filepath.Join(root, "skills", "pdf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No SKILL.md: the user replaced our directory with their own.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir}})

	outcomes, err := l.Remove([]string{"pdf"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Removed {
		t.Fatalf("expected a refusal, got %+v", outcomes)
	}
	if outcomes[0].Reason == "" {
		t.Fatal("a refusal must explain itself")
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("user content was destroyed")
	}
	// Still tracked so the user can inspect it.
	names, _ := l.Skills()
	if len(names) != 1 {
		t.Fatalf("refused entry should stay tracked, got %v", names)
	}
}

func TestLedgerRemoveAll(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))
	for _, n := range []string{"a", "b"} {
		d := filepath.Join(root, n)
		writeSkill(t, d)
		_ = l.Add([]LedgerEntry{{SkillName: n, DestDir: d}})
	}
	outcomes, err := l.Remove(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, o := range outcomes {
		if o.Removed {
			removed++
		}
	}
	if removed != 2 {
		t.Fatalf("expected 2 removals, got %d", removed)
	}
	all, _ := l.All()
	if len(all) != 0 {
		t.Fatalf("ledger should be empty, got %v", all)
	}
}

func TestLedgerDryRunChangesNothing(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))
	dir := filepath.Join(root, "pdf")
	writeSkill(t, dir)
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir}})

	outcomes, err := l.Remove(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Removed {
		t.Fatalf("dry run should report what it would do: %+v", outcomes)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("dry run deleted a directory")
	}
	all, _ := l.All()
	if len(all) != 1 {
		t.Fatal("dry run modified the ledger")
	}
}

func TestLedgerCorruptFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, _ := OpenLedger(path)
	if _, err := l.All(); err == nil {
		t.Fatal("a corrupt ledger was silently accepted")
	}
}
