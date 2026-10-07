package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestLedgerLegacyJSONLoads proves the provenance fields are additive: a ledger
// written before they existed must still load, with zero values.
func TestLedgerLegacyJSONLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	legacy := `[
	  {
	    "skillName": "pdf",
	    "hostLabel": "claude-code",
	    "destDir": "/x/pdf",
	    "source": "owner/repo",
	    "scope": "global",
	    "installedAt": "2026-01-02T03:04:05Z"
	  }
	]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := testLedger(t, path)
	if err != nil {
		t.Fatal(err)
	}
	all, err := l.All()
	if err != nil {
		t.Fatalf("legacy ledger must load: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}
	e := all[0]
	if e.SkillName != "pdf" || e.DestDir != "/x/pdf" || e.Scope != "global" {
		t.Fatalf("legacy fields lost: %+v", e)
	}
	if !e.InstalledAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("installedAt not parsed: %v", e.InstalledAt)
	}
	if e.ContentDigest != "" || e.SourceRef != "" || e.Inventory != nil {
		t.Fatalf("legacy entry should have empty provenance, got %+v", e)
	}
}

func TestLedgerProvenanceRoundTrip(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	writeSkill(t, dir)
	prov, err := CaptureProvenance(dir)
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "ledger.json")
	l, _ := testLedger(t, ledgerPath)
	if err := l.Add([]LedgerEntry{{
		SkillName:     "pdf",
		DestDir:       dir,
		Scope:         "project",
		ContentDigest: prov.Digest,
		SourceRef:     "abc123",
		Inventory:     prov.Inventory,
	}}); err != nil {
		t.Fatal(err)
	}

	reopened, err := testLedger(t, ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	all, err := reopened.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}
	if all[0].ContentDigest != prov.Digest || all[0].SourceRef != "abc123" {
		t.Fatalf("provenance not round-tripped: %+v", all[0])
	}
	if !reflect.DeepEqual(all[0].Inventory, prov.Inventory) {
		t.Fatalf("inventory not round-tripped: got %+v want %+v", all[0].Inventory, prov.Inventory)
	}
}

func TestRemoveScopeMismatchRefused(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-pdf")
	globalDir := filepath.Join(root, "global-pdf")
	writeSkill(t, projectDir)
	writeSkill(t, globalDir)

	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	if err := l.Add([]LedgerEntry{
		{SkillName: "pdf", DestDir: projectDir, Scope: "project"},
		{SkillName: "pdf", DestDir: globalDir, Scope: "global"},
	}); err != nil {
		t.Fatal(err)
	}

	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %+v", outcomes)
	}
	var removed, refused int
	for _, o := range outcomes {
		if o.Removed {
			removed++
			if o.Entry.DestDir != projectDir {
				t.Errorf("wrong directory removed by scope filter: %s", o.Entry.DestDir)
			}
		} else {
			refused++
			if !strings.Contains(o.Reason, "scope mismatch") {
				t.Errorf("refusal must explain the scope mismatch: %q", o.Reason)
			}
		}
	}
	if removed != 1 || refused != 1 {
		t.Fatalf("expected 1 removed / 1 refused, got %d/%d", removed, refused)
	}
	if _, err := os.Stat(globalDir); err != nil {
		t.Fatal("out-of-scope directory was deleted")
	}
	// The refused entry stays tracked.
	all, _ := l.All()
	if len(all) != 1 || all[0].Scope != "global" {
		t.Fatalf("refused entry should stay tracked, got %+v", all)
	}
}

func TestRemoveScopeMismatchRefusesLegacyEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "legacy")
	writeSkill(t, dir)
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "legacy", DestDir: dir}}) // no scope recorded

	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Removed {
		t.Fatalf("an entry with unknown scope must be refused, got %+v", outcomes)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("legacy directory must survive a scope-mismatched removal")
	}
}

// TestRemoveRefusesUserAddedFileAndForceOverrides is the core safety property:
// files the user put inside an installed skill must never be silently deleted.
func TestRemoveRefusesUserAddedFileAndForceOverrides(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	writeSkill(t, dir)
	prov, err := CaptureProvenance(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir, Scope: "project", Inventory: prov.Inventory}})

	// User adds a file after install.
	userFile := filepath.Join(dir, "my-notes.txt")
	if err := os.WriteFile(userFile, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Removed {
		t.Fatalf("user-added file must cause a refusal, got %+v", outcomes)
	}
	if !strings.Contains(outcomes[0].Reason, "not recorded at install") {
		t.Fatalf("refusal must explain the inventory mismatch: %q", outcomes[0].Reason)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatal("user content was destroyed")
	}

	// Force overrides the inventory guard.
	outcomes, err = l.RemoveScoped(RemoveOptions{Scope: "project", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Removed {
		t.Fatalf("Force should allow removal, got %+v", outcomes)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("directory should be gone after forced removal")
	}
}

// TestRemoveMatchedInventorySucceeds proves the inventory guard does not block
// the normal case where the directory still matches what was recorded.
func TestRemoveMatchedInventorySucceeds(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	writeSkill(t, dir)
	prov, _ := CaptureProvenance(dir)
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir, Scope: "project", Inventory: prov.Inventory}})

	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Removed {
		t.Fatalf("matching inventory should be removable, got %+v", outcomes)
	}
}

func TestRemoveRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	writeSkill(t, target)
	link := filepath.Join(root, "linked-skill")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "linked", DestDir: link, Scope: "project"}})

	for _, force := range []bool{false, true} {
		outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project", Force: force})
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0].Removed {
			t.Fatalf("force=%v: top-level symlink must be refused, got %+v", force, outcomes)
		}
		if !strings.Contains(outcomes[0].Reason, "symlink") {
			t.Fatalf("refusal must mention symlink: %q", outcomes[0].Reason)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("symlink target was disturbed")
	}
}

func TestRemoveRefusesSymlinkInsideDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	writeSkill(t, dir)
	prov, _ := CaptureProvenance(dir)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir, Scope: "project", Inventory: prov.Inventory}})

	// Force must not bypass the symlink refusal.
	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Removed {
		t.Fatalf("nested symlink must be refused even with Force, got %+v", outcomes)
	}
}

// TestRemoveForceDoesNotBypassRepurposedDir keeps the Force override scoped to
// the inventory guard only: a directory with no SKILL.md is still refused.
func TestRemoveForceDoesNotBypassRepurposedDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, _ := testLedger(t, filepath.Join(root, "ledger.json"))
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir, Scope: "project", Inventory: []FileRecord{{Path: "SKILL.md", Size: 8}}}})

	outcomes, err := l.RemoveScoped(RemoveOptions{Scope: "project", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Removed {
		t.Fatalf("missing SKILL.md must still be refused with Force, got %+v", outcomes)
	}
}
