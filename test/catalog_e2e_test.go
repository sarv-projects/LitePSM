package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/catalogbuild"
)

// TestCatalogRelease_EndToEnd is the catalog gate as an automated test: the
// committed 5,814-row dataset goes through validation, conversion, release
// compilation, and on-disk publication, then a client synchronizes it over
// HTTP exactly as it would against the live origin, searches it, and reloads
// it from cache with no network. It exists because the two halves of that
// path had never run against each other on real-sized data: unit tests used
// small fixtures (which hid a number-formatting bug in the manifest), and the
// manual origin-replica run is not a test anyone re-runs.
func TestCatalogRelease_EndToEnd(t *testing.T) {
	datasetPath := filepath.Join("..", "web", "data", "catalog.json")
	dataset, err := os.ReadFile(datasetPath)
	if err != nil {
		t.Fatalf("read committed dataset: %v", err)
	}

	rows, err := catalogbuild.ParseDataset(dataset)
	if err != nil {
		t.Fatalf("dataset failed validation: %v", err)
	}

	createdAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	snapshotID := catalogbuild.DatasetSnapshotID(dataset)
	listings, versions, err := catalogbuild.ConvertDataset(rows, "rel-e2e-001", snapshotID, createdAt)
	if err != nil {
		t.Fatalf("convert dataset: %v", err)
	}

	// Publish the release tree to a directory, like `catalog build --out`.
	outDir := t.TempDir()
	compiled, err := catalogbuild.CompileRelease("rel-e2e-001", 1, []string{snapshotID}, listings, versions, createdAt)
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	if err := compiled.WriteToDirectory(outDir); err != nil {
		t.Fatalf("WriteToDirectory: %v", err)
	}

	// The build must be reproducible: identical inputs, identical bytes, or
	// the pointer digests a tree the next build will not produce.
	again, err := catalogbuild.CompileRelease("rel-e2e-001", 1, []string{snapshotID}, listings, versions, createdAt)
	if err != nil {
		t.Fatalf("second CompileRelease: %v", err)
	}
	for path, want := range compiled.Files {
		if got := again.Files[path]; string(got) != string(want) {
			t.Fatalf("release build is not reproducible: %s differs between runs", path)
		}
	}

	// Serve the published tree like the CDN origin.
	server := httptest.NewServer(http.FileServer(http.Dir(outDir)))
	defer server.Close()

	cacheDir := t.TempDir()
	client := catalog.NewClient(server.URL, cacheDir, server.Client())
	ctx := context.Background()

	result, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("catalog sync against the published tree failed: %v", err)
	}
	if !result.Updated {
		t.Fatal("first sync reported no update")
	}
	if result.ItemCount != len(rows) {
		t.Fatalf("sync indexed %d items, dataset has %d", result.ItemCount, len(rows))
	}

	// The digest chain held end to end; search must now resolve real queries
	// from the synced index across kinds.
	hits := client.Search("postgres", catalog.SearchOptions{})
	if len(hits) == 0 {
		t.Fatal("search for postgres returned no results after sync")
	}
	kinds := map[string]bool{}
	for _, hit := range hits {
		kinds[string(hit.Listing.Kind)] = true
	}
	if len(kinds) < 2 {
		t.Errorf("expected results across multiple kinds, got %v", kinds)
	}

	// A repeat sync with an unchanged pointer is a no-op.
	repeat, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("repeat sync: %v", err)
	}
	if repeat.Updated {
		t.Fatal("repeat sync re-downloaded an unchanged release")
	}

	// Offline: a cold client with only the cache must serve the same query.
	offline := catalog.NewClient("http://127.0.0.1:1", cacheDir, nil)
	if err := offline.LoadFromCache(); err != nil {
		t.Fatalf("LoadFromCache: %v", err)
	}
	if offlineHits := offline.Search("postgres", catalog.SearchOptions{}); len(offlineHits) == 0 {
		t.Fatal("offline search returned no results from a verified cache")
	}
}
