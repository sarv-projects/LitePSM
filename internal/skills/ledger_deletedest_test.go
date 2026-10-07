package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// DeleteDest is the ledger half of a failed install's rollback: it drops
// exactly the row for one destination while the digest still matches what the
// install wrote, refuses destinations outside a skills tree, and refuses to
// remove another install's record.
func TestDeleteDestRemovesOnlyTheMatchingRow(t *testing.T) {
	dir := t.TempDir()
	ledger, err := OpenLedger(LedgerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "skills", "demo")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := LedgerEntry{SkillName: "demo", DestDir: dest, Scope: "global", ContentDigest: "sha256:aaa"}
	if err := ledger.Add([]LedgerEntry{entry}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Wrong digest: the row is no longer provably ours — refused.
	if err := ledger.DeleteDest(dest, "sha256:bbb"); err == nil {
		t.Fatal("DeleteDest with a mismatched digest must refuse")
	}
	if entries, err := ledger.All(); err != nil || len(entries) != 1 {
		t.Fatalf("row removed despite digest mismatch: entries=%v err=%v", entries, err)
	}

	// Right digest: removed.
	if err := ledger.DeleteDest(dest, "sha256:aaa"); err != nil {
		t.Fatalf("DeleteDest: %v", err)
	}
	if entries, err := ledger.All(); err != nil || len(entries) != 0 {
		t.Fatalf("row not removed: entries=%v err=%v", entries, err)
	}

	// Already gone: refused (it is not the caller's to erase twice).
	if err := ledger.DeleteDest(dest, "sha256:aaa"); err == nil {
		t.Fatal("DeleteDest for a missing row must refuse")
	}
}

// A destination outside any skills tree is refused before the ledger is
// touched — the same confinement Remove enforces.
func TestDeleteDestRefusesOutsideSkillsTrees(t *testing.T) {
	dir := t.TempDir()
	ledger, err := OpenLedger(LedgerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "not-a-skills-dir")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := LedgerEntry{SkillName: "demo", DestDir: dest, Scope: "global", ContentDigest: "sha256:aaa"}
	if err := ledger.Add([]LedgerEntry{entry}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := ledger.DeleteDest(dest, "sha256:aaa"); err == nil {
		t.Fatal("DeleteDest outside a skills tree must refuse")
	}
	if entries, err := ledger.All(); err != nil || len(entries) != 1 {
		t.Fatalf("row lost: entries=%v err=%v", entries, err)
	}
}
