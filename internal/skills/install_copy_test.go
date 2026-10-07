package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCopySource builds a small source skill tree.
func writeCopySource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "body.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// A symlink in the source is refused — and the partial tree the walk had
// already created is removed, so a retry is possible and no half-copy is
// ever left looking like an installed skill.
func TestCopySkillDirRefusesSymlinkAndCleansUp(t *testing.T) {
	src := writeCopySource(t)
	link := filepath.Join(src, "evil-link")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "demo")

	err := CopySkillDir(src, dst)
	if err == nil {
		t.Fatal("expected a refusal for a symlinked source entry")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("refusal must name the symlink, got: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("failed copy left a partial destination at %s: %v", dst, err)
	}

	// The destination is usable again: the refusal did not poison it.
	os.Remove(link)
	if err := CopySkillDir(src, dst); err != nil {
		t.Fatalf("retry after a refused copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Errorf("retry did not copy the tree: %v", err)
	}
}

// The size bound is enforced during the walk, and the partial tree is
// removed. The oversized file is sparse, so the test costs no disk space.
func TestCopySkillDirEnforcesMaxSkillBytesAndCleansUp(t *testing.T) {
	src := writeCopySource(t)
	big := filepath.Join(src, "huge.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxSkillBytes + 1); err != nil {
		f.Close()
		t.Fatalf("truncate sparse file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "demo")

	err = CopySkillDir(src, dst)
	if err == nil {
		t.Fatal("expected the 32 MiB bound to refuse the tree")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("refusal must name the bound, got: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("refused copy left a partial destination at %s: %v", dst, err)
	}
}

// A destination that already exists is refused without touching it.
func TestCopySkillDirRefusesExistingDestinationUntouched(t *testing.T) {
	src := writeCopySource(t)
	dst := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dst, "USER-OWNED.md")
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopySkillDir(src, dst); err == nil {
		t.Fatal("expected a refusal for an existing destination")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "mine" {
		t.Errorf("existing destination was modified: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("copy wrote into an existing destination: %v", err)
	}
}
