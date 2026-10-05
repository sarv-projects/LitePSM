package catalogbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

func sampleData() ([]*domain.Listing, []*domain.VersionRecord) {
	listings := []*domain.Listing{
		{
			SchemaVersion:  1,
			ID:             "mcp:builtin:mcp-registry:sqlite",
			Kind:           domain.KindMCP,
			Name:           "sqlite",
			Title:          "SQLite MCP Server",
			Summary:        "SQLite query runner",
			Categories:     []string{"database"},
			PublisherClaim: domain.PublisherClaim{Name: "Anthropic"},
			Status:         domain.ListingStatusActive,
		},
		{
			SchemaVersion:  1,
			ID:             "mcp:builtin:mcp-registry:postgres",
			Kind:           domain.KindMCP,
			Name:           "postgres",
			Title:          "Postgres MCP Server",
			Summary:        "PostgreSQL query runner",
			Categories:     []string{"database", "sql"},
			PublisherClaim: domain.PublisherClaim{Name: "Anthropic"},
			Status:         domain.ListingStatusActive,
		},
	}

	versions := []*domain.VersionRecord{
		{
			ListingID: "mcp:builtin:mcp-registry:sqlite",
			Version:   "1.0.0",
		},
		{
			ListingID: "mcp:builtin:mcp-registry:postgres",
			Version:   "1.4.0",
		},
	}

	return listings, versions
}

func TestDeterministicReleaseCompilation(t *testing.T) {
	listings, versions := sampleData()
	fixedTime := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	releaseID := "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2"

	// Run 1
	out1, err := CompileRelease(releaseID, 1, []string{"snap_01"}, listings, versions, fixedTime)
	if err != nil {
		t.Fatalf("CompileRelease run 1 failed: %v", err)
	}

	// Run 2 with reversed listing input order
	reversedListings := []*domain.Listing{listings[1], listings[0]}
	out2, err := CompileRelease(releaseID, 1, []string{"snap_01"}, reversedListings, versions, fixedTime)
	if err != nil {
		t.Fatalf("CompileRelease run 2 failed: %v", err)
	}

	// Assert byte-for-byte identical output regardless of input slice order
	if out1.ManifestDigest != out2.ManifestDigest {
		t.Fatalf("manifest digests must be identical!\nRun 1: %s\nRun 2: %s", out1.ManifestDigest, out2.ManifestDigest)
	}
	if out1.Manifest.ContentDigest != out2.Manifest.ContentDigest {
		t.Fatalf("content digests must be identical!\nRun 1: %s\nRun 2: %s", out1.Manifest.ContentDigest, out2.Manifest.ContentDigest)
	}

	for path, bytes1 := range out1.Files {
		bytes2, exists := out2.Files[path]
		if !exists {
			t.Fatalf("file %s missing from run 2", path)
		}
		if string(bytes1) != string(bytes2) {
			t.Fatalf("byte content divergence for file %s", path)
		}
	}
}

func TestManifestIntegrity(t *testing.T) {
	listings, versions := sampleData()
	releaseID := "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2"
	now := time.Now().UTC()

	out, err := CompileRelease(releaseID, 5, nil, listings, versions, now)
	if err != nil {
		t.Fatalf("CompileRelease failed: %v", err)
	}

	// Check that each file in the manifest matches its actual hash
	for filename, fileMeta := range out.Manifest.Files {
		relPath := filepath.ToSlash(filepath.Join("v1", "releases", releaseID, filename))
		fileBytes, exists := out.Files[relPath]
		if !exists {
			t.Fatalf("manifest declares file %s (rel: %s) but not generated", filename, relPath)
		}

		actualDigest := domain.ComputeBytesDigest(fileBytes)
		if actualDigest != fileMeta.Digest {
			t.Fatalf("digest mismatch for %s: manifest=%s, actual=%s", filename, fileMeta.Digest, actualDigest)
		}
		if int64(len(fileBytes)) != fileMeta.Size {
			t.Fatalf("size mismatch for %s: manifest=%d, actual=%d", filename, fileMeta.Size, len(fileBytes))
		}
	}

	// Verify current.json manifestDigest matches
	manifestPath := fmt.Sprintf("v1/releases/%s/manifest.json", releaseID)
	expectedManifestDigest := domain.ComputeBytesDigest(out.Files[manifestPath])
	if out.Current.ManifestDigest != expectedManifestDigest {
		t.Fatalf("current.json manifestDigest mismatch: got %s, expected %s", out.Current.ManifestDigest, expectedManifestDigest)
	}
}

func TestWriteToDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "litespm-catalogbuild-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	listings, versions := sampleData()
	out, err := CompileRelease("rel_test_write", 1, nil, listings, versions, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease failed: %v", err)
	}

	if err := out.WriteToDirectory(tmpDir); err != nil {
		t.Fatalf("WriteToDirectory failed: %v", err)
	}

	// Check files on disk
	checkFiles := []string{
		filepath.Join(tmpDir, "v1", "current.json"),
		filepath.Join(tmpDir, "v1", "releases", "rel_test_write", "manifest.json"),
		filepath.Join(tmpDir, "v1", "releases", "rel_test_write", "listings.json"),
		filepath.Join(tmpDir, "v1", "releases", "rel_test_write", "versions.json"),
	}

	for _, f := range checkFiles {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Fatalf("expected file %s to exist on disk", f)
		}
	}
}
