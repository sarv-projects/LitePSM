package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureProvenanceDeterministic(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pa, err := CaptureProvenance(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := CaptureProvenance(b)
	if err != nil {
		t.Fatal(err)
	}
	if pa.Digest == "" || pa.Digest != pb.Digest {
		t.Fatalf("identical trees must have identical digests: %q vs %q", pa.Digest, pb.Digest)
	}
	if len(pa.Inventory) != 2 {
		t.Fatalf("expected 2 inventory rows, got %+v", pa.Inventory)
	}
	// Inventory is sorted by path.
	if pa.Inventory[0].Path != "SKILL.md" || pa.Inventory[1].Path != "notes.txt" {
		t.Fatalf("inventory not sorted: %+v", pa.Inventory)
	}
	if pa.Inventory[1].Size != int64(len("same")) {
		t.Fatalf("inventory size wrong: %+v", pa.Inventory[1])
	}

	// A content change must change the digest.
	if err := os.WriteFile(filepath.Join(a, "notes.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	pa2, err := CaptureProvenance(a)
	if err != nil {
		t.Fatal(err)
	}
	if pa2.Digest == pa.Digest {
		t.Fatal("changed content must change the digest")
	}
}

// TestCaptureProvenanceFileBoundaryDistinguishes guards against the collision
// where two files' contents concatenate identically but the file boundaries
// differ. The digest binds path and size per file, so these must differ.
func TestCaptureProvenanceFileBoundaryDistinguishes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	if err := os.WriteFile(filepath.Join(a, "one"), []byte("ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "two"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "one"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "two"), []byte("bc"), 0o644); err != nil {
		t.Fatal(err)
	}
	pa, _ := CaptureProvenance(a)
	pb, _ := CaptureProvenance(b)
	if pa.Digest == pb.Digest {
		t.Fatal("different file boundaries must not collide")
	}
}

func TestCaptureProvenanceRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if _, err := CaptureProvenance(dir); err == nil {
		t.Fatal("symlink in a skill tree must be refused")
	}
}
