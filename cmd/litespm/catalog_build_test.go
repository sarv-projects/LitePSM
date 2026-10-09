package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
)

const buildTestDataset = `[
	{
		"id": "mcp:example:demo-server",
		"kind": "mcp",
		"name": "demo-server",
		"slug": "demo-server",
		"summary": "A demo MCP server",
		"category": "Databases",
		"publisher": {"name": "example", "url": "https://github.com/example/demo"},
		"transport": "stdio",
		"version": "1.2.3",
		"command": "npx",
		"args": ["-y", "demo"],
		"installability": "metadata_verified"
	},
	{
		"id": "skill:example:demo-skill",
		"kind": "skill",
		"name": "demo-skill",
		"slug": "demo-skill",
		"summary": "A demo skill",
		"category": "Dev Skills",
		"publisher": {"name": "example", "url": "https://example.com"},
		"version": "0.1.0",
		"skillSource": "https://example.com/demo"
	}
]`

// writeBuildDataset stores the fixture dataset and returns its path.
func writeBuildDataset(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(buildTestDataset), 0644); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	return path
}

func TestBuildCatalogReleaseAdvancesAuthority(t *testing.T) {
	dataset := writeBuildDataset(t)
	outDir := t.TempDir()
	prevPath := filepath.Join(outDir, "v1", "current.json")
	createdAt := "2026-10-05T12:00:00Z"

	// First release: no previous pointer, sequence starts at 1 and the id is
	// derived from the build date.
	first, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath, createdAt: createdAt,
	})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if first.Manifest.ReleaseID != "rel-2026-10-05-01" {
		t.Errorf("first release id %q, want rel-2026-10-05-01", first.Manifest.ReleaseID)
	}
	if first.Current.Sequence != 1 {
		t.Errorf("first sequence %d, want 1", first.Current.Sequence)
	}
	for _, want := range []string{
		filepath.Join(outDir, "v1", "current.json"),
		filepath.Join(outDir, "v1", "releases", "rel-2026-10-05-01", "manifest.json"),
		filepath.Join(outDir, "v1", "releases", "rel-2026-10-05-01", "listings.json"),
		filepath.Join(outDir, "v1", "releases", "rel-2026-10-05-01", "versions.json"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected build output %s: %v", want, err)
		}
	}

	// Second release the same day: sequence advances and the -NN suffix is
	// bumped, because release ids are immutable in the CDN cache.
	second, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath, createdAt: createdAt,
	})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if second.Manifest.ReleaseID != "rel-2026-10-05-02" {
		t.Errorf("second release id %q, want rel-2026-10-05-02 (same-day id must not be reused)", second.Manifest.ReleaseID)
	}
	if second.Current.Sequence != 2 {
		t.Errorf("second sequence %d, want 2", second.Current.Sequence)
	}
}

func TestCatalogBuildPrintsActualManifestDigest(t *testing.T) {
	dataset := writeBuildDataset(t)
	outDir := t.TempDir()
	prevPath := filepath.Join(outDir, "v1", "current.json")

	output := captureStdout(t, func() {
		runCatalogBuild([]string{
			"--dataset", dataset,
			"--out", outDir,
			"--prev", prevPath,
			"--created-at", "2026-10-08T12:00:00Z",
		})
	})
	pointer, _, err := catalogbuild.VerifyMaterializedTree(outDir)
	if err != nil {
		t.Fatalf("verify CLI-built tree: %v", err)
	}
	if !strings.Contains(output, "Manifest: "+pointer.ManifestDigest) {
		t.Fatalf("catalog build did not print the pointer's actual manifest digest %s:\n%s",
			pointer.ManifestDigest, output)
	}
}

func TestBuildCatalogReleaseRejectsNonAdvancingSequence(t *testing.T) {
	dataset := writeBuildDataset(t)
	outDir := t.TempDir()
	prevPath := filepath.Join(outDir, "v1", "current.json")
	createdAt := "2026-10-05T12:00:00Z"

	if _, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath, createdAt: createdAt,
	}); err != nil {
		t.Fatalf("first build: %v", err)
	}

	_, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath,
		sequence: 1, releaseID: "rel-2026-10-05-09", createdAt: createdAt,
	})
	if err == nil {
		t.Fatal("expected a non-advancing explicit sequence to be rejected")
	}
	if !strings.Contains(err.Error(), "must increase") {
		t.Fatalf("error %q does not explain that sequences must increase", err)
	}
}

func TestBuildCatalogReleaseRejectsReleaseIDReuse(t *testing.T) {
	dataset := writeBuildDataset(t)
	outDir := t.TempDir()
	prevPath := filepath.Join(outDir, "v1", "current.json")
	createdAt := "2026-10-05T12:00:00Z"

	if _, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath, createdAt: createdAt,
	}); err != nil {
		t.Fatalf("first build: %v", err)
	}

	_, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath,
		releaseID: "rel-2026-10-05-01", sequence: 2, createdAt: createdAt,
	})
	if err == nil {
		t.Fatal("expected republishing under an already-used release id to be rejected")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("error %q does not explain CDN immutability", err)
	}
}

// TestBuildCatalogReleaseSubSecondReleaseTimeStillReproduces pins the fix for
// the nanosecond drift: RFC3339 accepts fractional seconds (and a live
// build's time.Now() carries them), but the pointer stores seconds. A release
// cut with sub-second precision must therefore still materialize byte for
// byte, and the emitted identity must be second-precision.
func TestBuildCatalogReleaseSubSecondReleaseTimeStillReproduces(t *testing.T) {
	dataset := writeBuildDataset(t)
	releaseDir := t.TempDir()
	prevPath := filepath.Join(releaseDir, "v1", "current.json")

	released, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: releaseDir, prevPath: prevPath,
		createdAt: "2026-10-05T12:00:00.987654321Z",
	})
	if err != nil {
		t.Fatalf("release build with sub-second time: %v", err)
	}
	if strings.Contains(released.Manifest.CreatedAt, ".") {
		t.Fatalf("release createdAt %q is not second-precision: pointer and provenance would diverge",
			released.Manifest.CreatedAt)
	}

	materializeDir := t.TempDir()
	materialized, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: materializeDir,
		prevPath: prevPath, materialize: true,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	for path, releasedBytes := range released.Files {
		if string(materialized.Files[path]) != string(releasedBytes) {
			t.Fatalf("materialized %s differs from the sub-second-precision release", path)
		}
	}
}

// TestBuildCatalogReleaseMaterializeReproducesBytes is the deploy contract:
// materializing a released pointer must reproduce its tree byte for byte (the
// CDN caches those exact bytes), and must refuse when there is no release to
// reproduce. The release time is pinned to second precision precisely so the
// nanosecond-precision "now" of a live release step cannot leak into listing
// provenance and make reproduction impossible.
func TestBuildCatalogReleaseMaterializeReproducesBytes(t *testing.T) {
	dataset := writeBuildDataset(t)
	releaseDir := t.TempDir()
	prevPath := filepath.Join(releaseDir, "v1", "current.json")
	createdAt := "2026-10-05T12:00:00Z"

	released, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: releaseDir, prevPath: prevPath, createdAt: createdAt,
	})
	if err != nil {
		t.Fatalf("release build: %v", err)
	}

	materializeDir := t.TempDir()
	materialized, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: materializeDir,
		prevPath: prevPath, materialize: true,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if materialized.Manifest.ReleaseID != released.Manifest.ReleaseID ||
		materialized.Current.Sequence != released.Current.Sequence ||
		materialized.Manifest.CreatedAt != released.Manifest.CreatedAt {
		t.Fatalf("materialized identity %+v != released %+v",
			materialized.Current, released.Current)
	}
	for path, releasedBytes := range released.Files {
		materializedBytes, ok := materialized.Files[path]
		if !ok {
			t.Errorf("materialize missing %s", path)
			continue
		}
		if string(materializedBytes) != string(releasedBytes) {
			t.Errorf("materialized %s differs from the released bytes", path)
		}
	}

	// No released pointer: nothing to reproduce.
	_, _, err = buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: t.TempDir(),
		prevPath:    filepath.Join(t.TempDir(), "missing", "current.json"),
		materialize: true,
	})
	if err == nil {
		t.Fatal("expected materialize without a released pointer to fail")
	}
}

func TestBuildCatalogReleaseMaterializeRejectsChangedDataset(t *testing.T) {
	dataset := writeBuildDataset(t)
	releaseDir := t.TempDir()
	prevPath := filepath.Join(releaseDir, "v1", "current.json")
	if _, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: releaseDir, prevPath: prevPath,
		createdAt: "2026-10-05T12:00:00Z",
	}); err != nil {
		t.Fatalf("release build: %v", err)
	}

	changedDataset := strings.Replace(buildTestDataset, "A demo MCP server", "Changed MCP server", 1)
	if err := os.WriteFile(dataset, []byte(changedDataset), 0o644); err != nil {
		t.Fatalf("change dataset: %v", err)
	}

	outDir := t.TempDir()
	if _, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset, outDir: outDir, prevPath: prevPath, materialize: true,
	}); err == nil || !strings.Contains(err.Error(), "does not match released pointer digest") {
		t.Fatalf("materializing changed inputs should fail on digest mismatch, got %v", err)
	}
	if entries, err := os.ReadDir(outDir); err != nil {
		t.Fatalf("read output dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("failed materialization wrote output: %v", entries)
	}
}

func TestParseBuildCreatedAt(t *testing.T) {
	explicit, err := parseBuildCreatedAt("2026-10-05T12:00:00Z")
	if err != nil {
		t.Fatalf("explicit timestamp: %v", err)
	}
	if explicit.Format(time.RFC3339) != "2026-10-05T12:00:00Z" {
		t.Errorf("explicit timestamp parsed as %s", explicit.Format(time.RFC3339))
	}

	if _, err := parseBuildCreatedAt("yesterday"); err == nil {
		t.Fatal("expected a non-RFC3339 timestamp to be rejected")
	}

	t.Setenv("SOURCE_DATE_EPOCH", "1759665600") // 2025-10-05T12:00:00Z
	fromEpoch, err := parseBuildCreatedAt("")
	if err != nil {
		t.Fatalf("SOURCE_DATE_EPOCH: %v", err)
	}
	if fromEpoch.Format(time.RFC3339) != "2025-10-05T12:00:00Z" {
		t.Errorf("SOURCE_DATE_EPOCH parsed as %s", fromEpoch.Format(time.RFC3339))
	}
}

// TestSyncReleaseStatsMerge pins the single-writer-per-key split of
// web/data/release.json: the builder stamps release identity, the dataset
// stats from the generator are preserved, and builds that are not the
// authority update leave the bundle untouched.
func TestSyncReleaseStatsMerge(t *testing.T) {
	base := t.TempDir()
	outDir := filepath.Join(base, "web", "public")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	statsPath := filepath.Join(base, "web", "data", "release.json")
	if err := os.MkdirAll(filepath.Dir(statsPath), 0755); err != nil {
		t.Fatal(err)
	}
	datasetStats := `{
  "itemCount": 999,
  "mcpServersCount": 111,
  "agentSkillsCount": 222,
  "pluginsCount": 333,
  "datasetDigest": "sha256:deadbeef",
  "releaseId": "rel-legacy",
  "sequence": 42,
  "manifestDigest": "sha256:legacy",
  "createdAt": "2020-01-01T00:00:00Z"
}`
	if err := os.WriteFile(statsPath, []byte(datasetStats), 0644); err != nil {
		t.Fatal(err)
	}

	compiled := compileSampleBuildOutput(t)
	if err := syncReleaseStats(filepath.Join(outDir, "v1", "current.json"), outDir, compiled); err != nil {
		t.Fatalf("syncReleaseStats: %v", err)
	}

	data, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"mcpServersCount": 111`,                              // dataset stats preserved
		`"datasetDigest": "sha256:deadbeef"`,                  // dataset stats preserved
		`"releaseId": "` + compiled.Manifest.ReleaseID + `"`,  // identity stamped
		`"manifestDigest": "` + compiled.ManifestDigest + `"`, // identity stamped
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stats bundle missing %s after merge:\n%s", want, text)
		}
	}
	if strings.Contains(text, "rel-legacy") {
		t.Error("stats bundle kept the stale release identity")
	}

	// A build whose output does not host the authority pointer (deploy
	// materialization, test builds) must not touch the bundle.
	otherDir := t.TempDir()
	before, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncReleaseStats(filepath.Join(outDir, "v1", "current.json"), otherDir, compiled); err != nil {
		t.Fatalf("syncReleaseStats (non-authority): %v", err)
	}
	after, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("non-authority build modified the stats bundle")
	}
}

// compileSampleBuildOutput builds a release over the fixture dataset for the
// stats-merge test.
func compileSampleBuildOutput(t *testing.T) *catalogbuild.BuildOutput {
	t.Helper()
	dataset := writeBuildDataset(t)
	outDir := t.TempDir()
	output, _, err := buildCatalogRelease(catalogBuildOptions{
		datasetPath: dataset,
		outDir:      outDir,
		prevPath:    filepath.Join(outDir, "v1", "current.json"),
		createdAt:   "2026-10-05T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("build sample release: %v", err)
	}
	return output
}
