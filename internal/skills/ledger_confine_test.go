package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A hand-edited or corrupted ledger must never aim RemoveAll outside a skills tree.
func TestLedgerRemoveRefusesDestinationsOutsideSkillsTrees(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json")) // default confinement, no explicit roots

	victim := filepath.Join(root, "precious")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	// Looks like a skill (has SKILL.md) but is not inside a skills directory.
	if err := os.WriteFile(filepath.Join(victim, "SKILL.md"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	bad := []LedgerEntry{
		{SkillName: "root", DestDir: string(filepath.Separator)},
		{SkillName: "home", DestDir: home},
		{SkillName: "rel", DestDir: "relative/skills/x"},
		{SkillName: "dots", DestDir: root + string(filepath.Separator) + "skills" + string(filepath.Separator) + ".." + string(filepath.Separator) + "precious"},
		{SkillName: "notree", DestDir: victim},
	}
	if err := l.Add(bad); err != nil {
		t.Fatal(err)
	}
	outcomes, err := l.RemoveScoped(RemoveOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != len(bad) {
		t.Fatalf("expected %d outcomes, got %d", len(bad), len(outcomes))
	}
	for _, o := range outcomes {
		if o.Removed || !strings.Contains(o.Reason, "refusing to delete") {
			t.Errorf("%s must be refused by confinement, got %+v", o.Entry.SkillName, o)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "SKILL.md")); err != nil {
		t.Fatalf("a confined-out directory was deleted: %v", err)
	}
	if all, _ := l.All(); len(all) != len(bad) {
		t.Errorf("refused entries must stay tracked, got %d", len(all))
	}
}

func TestLedgerRemoveAcceptsDefaultSkillsTree(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))
	dir := filepath.Join(root, ".claude", "skills", "pdf")
	writeSkill(t, dir)
	_ = l.Add([]LedgerEntry{{SkillName: "pdf", DestDir: dir, Scope: "project"}})
	out, err := l.Remove([]string{"pdf"}, false)
	if err != nil || len(out) != 1 || !out[0].Removed {
		t.Fatalf("a normal skills-tree entry must be removable: %+v %v", out, err)
	}
}

func TestLedgerExplicitRootsConfine(t *testing.T) {
	root := t.TempDir()
	l, _ := OpenLedger(filepath.Join(root, "ledger.json"))
	allowed := filepath.Join(root, "allowed")
	l.SetRoots(allowed)
	inside := filepath.Join(allowed, "skills", "a")
	outside := filepath.Join(root, "elsewhere", "skills", "b")
	writeSkill(t, inside)
	writeSkill(t, outside)
	_ = l.Add([]LedgerEntry{{SkillName: "a", DestDir: inside}, {SkillName: "b", DestDir: outside}})
	out, _ := l.Remove(nil, false)
	removed := map[string]bool{}
	for _, o := range out {
		removed[o.Entry.SkillName] = o.Removed
	}
	if !removed["a"] || removed["b"] {
		t.Fatalf("roots must confine removal: %+v", removed)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("outside root was deleted")
	}
}

// Independent Ledger values share no in-process mutex, so only the lock file can
// keep their read-modify-write cycles from losing each other's entries.
func TestLedgerCrossInstanceConcurrentAddsLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, _ := OpenLedger(path)
			if err := l.Add([]LedgerEntry{{SkillName: fmt.Sprintf("s%02d", i), DestDir: fmt.Sprintf("/x/skills/s%02d", i)}}); err != nil {
				t.Errorf("add %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	l, _ := OpenLedger(path)
	all, err := l.All()
	if err != nil || len(all) != n {
		t.Fatalf("lost updates: have %d of %d entries (err %v)", len(all), n, err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Error("lock file leaked after all operations finished")
	}
}
