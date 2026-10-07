package source

// snapshot_test.go — the fetch snapshot layer.
//
// The testdata snapshot under testdata/snapshots/ was WRITTEN BY
// scripts/snapshot_store.py (the Python producer's store), so these tests are
// the cross-language contract: whatever Python records must load, verify and
// normalize here.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

const testSnapshotURL = "https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json"

func testSnapshotRoot() string {
	return filepath.Join("testdata", "snapshots")
}

// copySnapshotFixture clones the Python-written snapshot into a temp root so
// tamper tests never touch the committed testdata.
func copySnapshotFixture(t *testing.T) string {
	t.Helper()
	srcDir, err := SnapshotDir(testSnapshotRoot(), testSnapshotURL)
	if err != nil {
		t.Fatalf("SnapshotDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, snapshotMetaFile)); err != nil {
		t.Fatalf("python-written snapshot missing: %v", err)
	}
	dst := t.TempDir()
	dstDir, err := SnapshotDir(dst, testSnapshotURL)
	if err != nil {
		t.Fatalf("SnapshotDir: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{snapshotMetaFile, snapshotBodyFile} {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, name), data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dst
}

// TestSnapshotDirMatchesPythonStore pins the key derivation across languages:
// the Python store wrote exactly one directory under testdata/snapshots, and
// its name must be what SnapshotKey computes for the same URL.
func TestSnapshotDirMatchesPythonStore(t *testing.T) {
	key, err := SnapshotKey(testSnapshotURL)
	if err != nil {
		t.Fatalf("SnapshotKey: %v", err)
	}
	entries, err := os.ReadDir(testSnapshotRoot())
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 recorded snapshot dir, got %d", len(entries))
	}
	if entries[0].Name() != key {
		t.Fatalf("key mismatch: python wrote %q, go computes %q", entries[0].Name(), key)
	}
	if _, err := SnapshotKey("http://example.com/x.json"); err == nil {
		t.Error("non-https URL must be refused")
	}
}

// TestLoadSnapshotRecordedByPythonStore verifies the recorded bytes load and
// verify, and that the record claims only what it can support.
func TestLoadSnapshotRecordedByPythonStore(t *testing.T) {
	body, rec, err := LoadSnapshot(testSnapshotRoot(), testSnapshotURL)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "source", "claude", "marketplace.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(body, fixture) {
		t.Fatalf("loaded %d bytes, fixture has %d", len(body), len(fixture))
	}

	if rec.Format != SnapshotFormat {
		t.Errorf("format = %q", rec.Format)
	}
	if rec.Snapshot.Status != SnapshotStatusHealthy {
		t.Errorf("status = %q, want healthy", rec.Snapshot.Status)
	}
	if rec.Snapshot.ItemCount == nil || *rec.Snapshot.ItemCount != 3 {
		t.Errorf("itemCount = %v, want 3", rec.Snapshot.ItemCount)
	}
	if rec.Snapshot.ContentDigest != SnapshotContentDigest(body) {
		t.Errorf("contentDigest = %q does not match body", rec.Snapshot.ContentDigest)
	}
	if !strings.HasPrefix(rec.Snapshot.SnapshotID, "snap_") || len(rec.Snapshot.SnapshotID) != 5+26 {
		t.Errorf("snapshotId %q fails the snap_<26> grammar", rec.Snapshot.SnapshotID)
	}
	if rec.Snapshot.AdapterVersion == "" {
		t.Error("adapterVersion must name the writer")
	}
	if rec.Snapshot.UpstreamRevision != nil {
		t.Errorf("content-shaped etag must not claim a git revision, got %q", *rec.Snapshot.UpstreamRevision)
	}
	if rec.Fetch.ETag == nil || rec.Fetch.HTTPStatus != 200 || rec.Fetch.ByteSize != len(body) {
		t.Errorf("fetch block incomplete: %+v", rec.Fetch)
	}
	if _, err := domain.ParseSourceID(rec.Snapshot.SourceID); err != nil {
		t.Errorf("sourceId %q rejected by the domain grammar: %v", rec.Snapshot.SourceID, err)
	}
}

// TestIngestFromSnapshotBytes is the pipeline seam: an adapter normalizes
// from the SNAPSHOT (recorded bytes), never from a live response.
func TestIngestFromSnapshotBytes(t *testing.T) {
	body, rec, err := LoadSnapshot(testSnapshotRoot(), testSnapshotURL)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	adapter := NewClaudeMarketplaceAdapter(domain.SourceID(rec.Snapshot.SourceID))
	res, err := adapter.Ingest(t.Context(), rec.Snapshot.SnapshotID, body)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Listings) != 3 {
		t.Fatalf("expected 3 listings from the recorded manifest (command source rejected), got %d", len(res.Listings))
	}
	if res.SnapshotID != rec.Snapshot.SnapshotID {
		t.Errorf("ingest result provenance %q != snapshot id %q", res.SnapshotID, rec.Snapshot.SnapshotID)
	}
	want := "plugin:git:claude-plugins-official:document-skills"
	found := false
	for _, l := range res.Listings {
		if l.ID == want {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %s among %d listings", want, len(res.Listings))
	}
	if rec.Snapshot.ItemCount != nil && len(res.Listings) != *rec.Snapshot.ItemCount {
		t.Errorf("recorded itemCount %d != ingested %d", *rec.Snapshot.ItemCount, len(res.Listings))
	}
}

// TestLoadSnapshotRefusesTamperedRecords: a snapshot whose digest does not
// match its bytes is refused, not used; a missing recording is missing, not
// invented.
func TestLoadSnapshotRefusesTamperedRecords(t *testing.T) {
	t.Run("body digest mismatch", func(t *testing.T) {
		root := copySnapshotFixture(t)
		dir, _ := SnapshotDir(root, testSnapshotURL)
		bodyPath := filepath.Join(dir, snapshotBodyFile)
		raw, err := os.ReadFile(bodyPath)
		if err != nil {
			t.Fatal(err)
		}
		// Flip one byte in place: same size, different content.
		raw[len(raw)/2] ^= 0x20
		if err := os.WriteFile(bodyPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSnapshot(root, testSnapshotURL); !errors.Is(err, ErrSnapshotDigestMismatch) {
			t.Fatalf("want ErrSnapshotDigestMismatch, got %v", err)
		}
	})

	t.Run("byte size mismatch", func(t *testing.T) {
		root := copySnapshotFixture(t)
		dir, _ := SnapshotDir(root, testSnapshotURL)
		if err := os.WriteFile(filepath.Join(dir, snapshotBodyFile), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSnapshot(root, testSnapshotURL); !errors.Is(err, ErrSnapshotInvalid) {
			t.Fatalf("want ErrSnapshotInvalid, got %v", err)
		}
	})

	t.Run("missing record", func(t *testing.T) {
		if _, _, err := LoadSnapshot(t.TempDir(), testSnapshotURL); !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("want ErrSnapshotMissing, got %v", err)
		}
	})

	t.Run("corrupt record json", func(t *testing.T) {
		root := copySnapshotFixture(t)
		dir, _ := SnapshotDir(root, testSnapshotURL)
		if err := os.WriteFile(filepath.Join(dir, snapshotMetaFile), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSnapshot(root, testSnapshotURL); !errors.Is(err, ErrSnapshotInvalid) {
			t.Fatalf("want ErrSnapshotInvalid, got %v", err)
		}
	})

	t.Run("failed status is not ingestible", func(t *testing.T) {
		root := copySnapshotFixture(t)
		dir, _ := SnapshotDir(root, testSnapshotURL)
		metaPath := filepath.Join(dir, snapshotMetaFile)
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		doc["snapshot"].(map[string]any)["status"] = "failed"
		updated, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(metaPath, updated, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSnapshot(root, testSnapshotURL); !errors.Is(err, ErrSnapshotInvalid) {
			t.Fatalf("want ErrSnapshotInvalid, got %v", err)
		}
	})

	t.Run("foreign url binding", func(t *testing.T) {
		root := copySnapshotFixture(t)
		// A different URL maps to a different directory: nothing recorded.
		if _, _, err := LoadSnapshot(root, "https://raw.githubusercontent.com/other/repo/main/x.json"); !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("want ErrSnapshotMissing, got %v", err)
		}
	})
}

// TestWriteLoadMarkRoundTrip: write -> partial -> load verifies -> mark ->
// healthy, and re-marking is a no-op so re-runs stay byte-stable.
func TestWriteLoadMarkRoundTrip(t *testing.T) {
	root := t.TempDir()
	url := "https://example.com/vendor/marketplace.json"
	body := []byte(`{"plugins": [{"name": "demo"}]}`)
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	sha40 := "0123456789abcdef0123456789abcdef01234567"

	rec, err := WriteSnapshot(root, "git:claude-plugins-official", url, `"`+sha40+`"`, 200, body, at)
	if err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if rec.Snapshot.Status != SnapshotStatusPartial {
		t.Errorf("fresh snapshot status = %q, want partial", rec.Snapshot.Status)
	}
	if rec.Snapshot.CompletedAt != "" {
		t.Errorf("partial snapshot must not claim completedAt: %q", rec.Snapshot.CompletedAt)
	}
	if rec.Snapshot.UpstreamRevision == nil || *rec.Snapshot.UpstreamRevision != "git:"+sha40 {
		t.Errorf("40-hex etag must map to a git revision, got %v", rec.Snapshot.UpstreamRevision)
	}

	got, loaded, err := LoadSnapshot(root, url)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("round-trip bytes differ")
	}
	if loaded.Snapshot.SnapshotID != rec.Snapshot.SnapshotID {
		t.Errorf("recorded id %q != loaded %q", rec.Snapshot.SnapshotID, loaded.Snapshot.SnapshotID)
	}

	changed, err := MarkIngested(root, url, 1, at.Add(time.Second))
	if err != nil {
		t.Fatalf("MarkIngested: %v", err)
	}
	if !changed {
		t.Fatal("first MarkIngested must change the record")
	}
	_, healthy, err := LoadSnapshot(root, url)
	if err != nil {
		t.Fatalf("LoadSnapshot after mark: %v", err)
	}
	if healthy.Snapshot.Status != SnapshotStatusHealthy ||
		healthy.Snapshot.ItemCount == nil || *healthy.Snapshot.ItemCount != 1 ||
		healthy.Snapshot.CompletedAt == "" {
		t.Fatalf("mark incomplete: %+v", healthy.Snapshot)
	}

	metaPath := filepath.Join(mustSnapshotDir(t, root, url), snapshotMetaFile)
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = MarkIngested(root, url, 1, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("second MarkIngested: %v", err)
	}
	if changed {
		t.Error("identical re-mark must be a no-op")
	}
	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("healthy record must be byte-stable across re-runs")
	}

	// A changed upstream body replaces the recording: new id, new digest.
	rec2, err := WriteSnapshot(root, "git:claude-plugins-official", url, "", 200, []byte(`{"plugins": []}`), at.Add(time.Hour))
	if err != nil {
		t.Fatalf("second WriteSnapshot: %v", err)
	}
	if rec2.Snapshot.SnapshotID == rec.Snapshot.SnapshotID {
		t.Error("snapshot ids must be unique per capture")
	}
	if rec2.Snapshot.ContentDigest == rec.Snapshot.ContentDigest {
		t.Error("different bytes must yield different digests")
	}
	if rec2.Fetch.ETag != nil || rec2.Snapshot.UpstreamRevision != nil {
		t.Error("ETag-less capture must record null, not a guess")
	}
}

// TestMarkIngestedMissingSnapshot: nothing recorded, nothing fabricated.
func TestMarkIngestedMissingSnapshot(t *testing.T) {
	root := t.TempDir()
	changed, err := MarkIngested(root, "https://example.com/never/fetched.json", 5, time.Now())
	if err != nil {
		t.Fatalf("MarkIngested on missing snapshot: %v", err)
	}
	if changed {
		t.Error("MarkIngested must not create or claim a snapshot that was never fetched")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("root should stay empty, found %d entries", len(entries))
	}
}

// TestWriteSnapshotRefusesInvalidInputs: only a real, well-formed fetch can be
// recorded.
func TestWriteSnapshotRefusesInvalidInputs(t *testing.T) {
	root := t.TempDir()
	at := time.Now()
	if _, err := WriteSnapshot(root, "git:claude-plugins-official", "http://example.com/x.json", "", 200, []byte("{}"), at); err == nil {
		t.Error("non-https URL must be refused")
	}
	if _, err := WriteSnapshot(root, "not-a-source-id", "https://example.com/x.json", "", 200, []byte("{}"), at); err == nil {
		t.Error("invalid sourceId must be refused")
	}
	if _, err := WriteSnapshot(root, "git:claude-plugins-official", "https://example.com/x.json", "", 304, nil, at); err == nil {
		t.Error("a 304 carries no body and must not be recorded as a snapshot")
	}
}

func mustSnapshotDir(t *testing.T, root, url string) string {
	t.Helper()
	dir, err := SnapshotDir(root, url)
	if err != nil {
		t.Fatalf("SnapshotDir: %v", err)
	}
	return dir
}
