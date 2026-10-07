package install

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func sameZipSource(t *testing.T, files map[string][]byte) ArchiveSourceFunc {
	zipData := createTestZip(t, files)
	return func(ctx context.Context, listingID, version string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(zipData)), "zip", nil
	}
}

// Concurrent installs of identical content must produce exactly one creator of
// the CAS tree; every other operation records created_by_op=0 and the tree
// stays intact (single-flight per digest).
func TestE6_ConcurrentSameDigestHasExactlyOneCreator(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()
	src := sameZipSource(t, map[string][]byte{"a.txt": []byte("alpha\n"), "b/c.txt": []byte("gamma\n")})

	const n = 8
	var wg sync.WaitGroup
	recs := make([]*domain.InstallRecord, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i], errs[i] = engine.Execute(ctx, InstallOptions{
				ListingID: fmt.Sprintf("tool-%d", i), Version: "1.0.0", ArchiveSource: src,
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("install %d failed: %v", i, err)
		}
	}
	digest := recs[0].TreeDigest
	var creators int
	rows, err := db.Raw().Query(`SELECT created_by_op FROM operation_trees WHERE tree_digest = ?`, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var c int
		_ = rows.Scan(&c)
		total++
		creators += c
	}
	if total != n || creators != 1 {
		t.Fatalf("operation_trees rows=%d creators=%d, want %d rows and exactly 1 creator", total, creators, n)
	}
	p, _ := engine.TreePath(digest)
	if _, err := os.Stat(filepath.Join(p, "a.txt")); err != nil {
		t.Fatalf("shared tree damaged: %v", err)
	}
}

// A loser that fails after reusing the tree must not delete the winner's tree,
// even if a stale created_by_op=1 row exists for it.
func TestE6_RollbackNeverDeletesTreeInUse(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()
	src := sameZipSource(t, map[string][]byte{"x.txt": []byte("x\n")})
	rec, err := engine.Execute(ctx, InstallOptions{ListingID: "winner", Version: "1", ArchiveSource: src})
	if err != nil {
		t.Fatal(err)
	}
	// A second op wrongly claims creation of the same digest.
	if err := db.CreateOperation(ctx, "op_stale_claim", nil, "install", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordOperationTree(ctx, "op_stale_claim", rec.TreeDigest, true); err != nil {
		t.Fatal(err)
	}
	if err := engine.Rollback(ctx, "op_stale_claim"); err != nil {
		t.Fatal(err)
	}
	p, _ := engine.TreePath(rec.TreeDigest)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("rollback deleted a tree an install depends on: %v", err)
	}
}

// A reused CAS tree whose bytes no longer match its digest is quarantined and
// replaced, never silently reused and never left corrupt forever.
func TestE6_CorruptReusedTreeIsQuarantinedAndReplaced(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()
	files := map[string][]byte{"main.js": []byte("good\n"), "lib/util.js": []byte("util\n")}
	src := sameZipSource(t, files)

	first, err := engine.Execute(ctx, InstallOptions{ListingID: "first", Version: "1", ArchiveSource: src})
	if err != nil {
		t.Fatal(err)
	}
	treePath, _ := engine.TreePath(first.TreeDigest)
	if err := os.WriteFile(filepath.Join(treePath, "main.js"), []byte("TAMPERED\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := engine.Execute(ctx, InstallOptions{ListingID: "second", Version: "1", ArchiveSource: src})
	if err != nil {
		t.Fatalf("install over a corrupt tree must self-heal, got: %v", err)
	}
	if second.TreeDigest != first.TreeDigest {
		t.Fatal("digests differ for identical content")
	}
	data, _ := os.ReadFile(filepath.Join(treePath, "main.js"))
	if string(data) != "good\n" {
		t.Fatalf("CAS tree still corrupt after reinstall: %q", data)
	}
	q, _ := filepath.Glob(filepath.Join(engine.casRoot, "quarantine", "*"))
	if len(q) != 1 {
		t.Fatalf("expected exactly one quarantined tree, got %v", q)
	}
	tampered, _ := os.ReadFile(filepath.Join(q[0], "main.js"))
	if string(tampered) != "TAMPERED\n" {
		t.Errorf("quarantine must preserve the corrupt bytes as evidence, got %q", tampered)
	}
	// The healing operation is the creator of the replacement tree.
	var created int
	_ = db.Raw().QueryRow(`SELECT COUNT(*) FROM operation_trees WHERE tree_digest = ? AND created_by_op = 1`, first.TreeDigest).Scan(&created)
	if created != 2 {
		t.Errorf("both the original and the replacement creator should be recorded, got %d", created)
	}
}

func TestE6_PromoteTreeDoesNotMergeIntoExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	_ = os.MkdirAll(src, 0o700)
	_ = os.MkdirAll(dst, 0o700)
	_ = os.WriteFile(filepath.Join(src, "new.txt"), []byte("n"), 0o600)
	_ = os.WriteFile(filepath.Join(dst, "keep.txt"), []byte("k"), 0o600)
	created, err := promoteTree(src, dst)
	if err != nil || created {
		t.Fatalf("created=%v err=%v; losing a race must report created=false", created, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "new.txt")); err == nil {
		t.Error("promote merged files into an existing tree")
	}
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Error("existing tree was damaged")
	}
}

func TestInstallRecordsRealKindAndPath(t *testing.T) {
	ctx := context.Background()
	engine, db, _ := setupTestEngine(t)
	defer db.Close()
	src := sameZipSource(t, map[string][]byte{"SKILL.md": []byte("# s\n")})
	rec, err := engine.Execute(ctx, InstallOptions{ListingID: "skill-x", Version: "1", Kind: domain.KindSkill, ArchiveSource: src})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetInstall(ctx, rec.InstallID)
	if err != nil || got.Kind != domain.KindSkill {
		t.Fatalf("kind not persisted: %+v %v", got, err)
	}
	want, _ := engine.TreePath(rec.TreeDigest)
	if got.InstallPath != want {
		t.Errorf("install_path = %q, want CAS tree %q", got.InstallPath, want)
	}
	comps, _ := db.ListInstallComponents(ctx, rec.InstallID)
	if len(comps) != 1 || comps[0].ComponentID == rec.InstallID || comps[0].Kind != domain.ComponentSkill {
		t.Errorf("component row wrong: %+v", comps)
	}
}
