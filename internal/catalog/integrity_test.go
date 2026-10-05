package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
)

// compileSampleRelease builds a real release from the sample listings so the
// pointer, manifest, and listings always agree — mutations of the served
// pointer are then the only way to make them disagree.
func compileSampleRelease(t *testing.T, releaseID string, sequence int) *catalogbuild.BuildOutput {
	t.Helper()
	listings := sampleListings()
	versions := make([]*domain.VersionRecord, 0, len(listings))
	for _, listing := range listings {
		versions = append(versions, &domain.VersionRecord{ListingID: listing.ID, Version: "1.0.0"})
	}
	compiled, err := catalogbuild.CompileRelease(releaseID, sequence, nil, listings, versions, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	return compiled
}

// serveFiles answers only what it is given, like the live origin.
func serveFiles(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

// rewritePointer decodes the built pointer, applies the mutation, and returns
// the mutated bytes to serve in place of the original.
func rewritePointer(t *testing.T, compiled *catalogbuild.BuildOutput, mutate func(map[string]any)) []byte {
	t.Helper()
	var pointer map[string]any
	if err := json.Unmarshal(compiled.Files["v1/current.json"], &pointer); err != nil {
		t.Fatalf("decode built pointer: %v", err)
	}
	mutate(pointer)
	mutated, err := json.Marshal(pointer)
	if err != nil {
		t.Fatalf("re-encode pointer: %v", err)
	}
	return mutated
}

func cacheFiles(t *testing.T, cacheDir string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(cacheDir, path)
			if err != nil {
				return err
			}
			found = append(found, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk cache dir: %v", err)
	}
	return found
}

// TestSyncRejectsPointerManifestDigestMismatch closes the trust-anchor gap:
// the pointer's manifestDigest must be checked against the served manifest
// bytes, otherwise a swapped manifest that is internally self-consistent is
// accepted as authentic.
func TestSyncRejectsPointerManifestDigestMismatch(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	files["v1/current.json"] = rewritePointer(t, compiled, func(pointer map[string]any) {
		// Well-formed digest of the wrong bytes: passes field validation,
		// fails the anchor comparison.
		pointer["manifestDigest"] = domain.ComputeBytesDigest([]byte("attacker-controlled manifest"))
	})

	server := serveFiles(t, files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	_, err := client.Sync(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject a pointer whose manifestDigest does not match the served manifest")
	}
	lpsmErr, ok := err.(*domain.LPSMError)
	if !ok || lpsmErr.Code != "LPSM-VERIFY-CHECKSUM-MISMATCH" {
		t.Fatalf("expected LPSM-VERIFY-CHECKSUM-MISMATCH, got %#v", err)
	}
	if files := cacheFiles(t, cacheDir); len(files) != 0 {
		t.Fatalf("rejected sync wrote cache files: %v", files)
	}
}

// TestSyncRejectsUnsafeReleaseID pins the path-safety gate: the release id
// comes from the network and becomes both a URL segment and a cache directory,
// so "../evil" must be refused before anything is fetched or written.
func TestSyncRejectsUnsafeReleaseID(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	files["v1/current.json"] = rewritePointer(t, compiled, func(pointer map[string]any) {
		pointer["releaseId"] = "../../evil"
	})

	server := serveFiles(t, files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	_, err := client.Sync(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject an unsafe release id")
	}
	if !strings.Contains(err.Error(), "unsafe release id") {
		t.Fatalf("error %q does not explain the unsafe release id", err)
	}
	if files := cacheFiles(t, cacheDir); len(files) != 0 {
		t.Fatalf("rejected release id caused cache writes: %v", files)
	}
}

// TestSyncRejectsUnsupportedPointerSchema is the legacy-pointer guard: the
// pre-builder origin published a pointer without a schemaVersion; the client
// must reject it instead of treating its zero value as v1.
func TestSyncRejectsUnsupportedPointerSchema(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	files["v1/current.json"] = rewritePointer(t, compiled, func(pointer map[string]any) {
		delete(pointer, "schemaVersion") // zero value on decode
	})

	server := serveFiles(t, files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	_, err := client.Sync(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject a pointer without the supported schemaVersion")
	}
	if !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("error %q does not mention schemaVersion", err)
	}
}

// TestSyncPersistsServedBytesVerbatim verifies the cache mirrors the origin
// byte for byte: a re-marshal would silently change key order and break every
// later digest comparison against the same manifest.
func TestSyncPersistsServedBytesVerbatim(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	server := serveFiles(t, compiled.Files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Sync persists exactly these files; every one must be the served bytes
	// verbatim (versions.json is not fetched: search needs listings only).
	for _, path := range []string{
		"v1/current.json",
		"v1/releases/rel-2026-10-05-01/manifest.json",
		"v1/releases/rel-2026-10-05-01/listings.json",
	} {
		cached, err := os.ReadFile(filepath.Join(cacheDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read cached %s: %v", path, err)
		}
		if string(cached) != string(compiled.Files[path]) {
			t.Fatalf("cached %s differs from served bytes (re-marshal drift)", path)
		}
	}
}

// TestLoadFromCacheRejectsTamperedManifest ensures offline loads re-verify the
// cache: a corrupted manifest must produce an error and an empty index, never
// poisoned search results.
func TestLoadFromCacheRejectsTamperedManifest(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	server := serveFiles(t, compiled.Files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	manifestPath := filepath.Join(cacheDir, "v1", "releases", "rel-2026-10-05-01", "manifest.json")
	tampered, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read cached manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(tampered, ' '), 0644); err != nil {
		t.Fatalf("tamper cached manifest: %v", err)
	}

	offline := NewClient("http://127.0.0.1:1", cacheDir, nil)
	if err := offline.LoadFromCache(); err == nil {
		t.Fatal("expected LoadFromCache to reject a tampered manifest")
	}
	if results := offline.Search("postgres", SearchOptions{}); len(results) != 0 {
		t.Fatalf("tampered cache served %d search results", len(results))
	}
}

// TestLoadFromCacheRejectsTamperedListings is the other half: listings that no
// longer match the manifest's digest must not reach the index.
func TestLoadFromCacheRejectsTamperedListings(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	server := serveFiles(t, compiled.Files)
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewClient(server.URL, cacheDir, server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	listingsPath := filepath.Join(cacheDir, "v1", "releases", "rel-2026-10-05-01", "listings.json")
	var listings []*domain.Listing
	data, err := os.ReadFile(listingsPath)
	if err != nil {
		t.Fatalf("read cached listings: %v", err)
	}
	if err := json.Unmarshal(data, &listings); err != nil {
		t.Fatalf("decode cached listings: %v", err)
	}
	listings[0].Summary = "tampered summary"
	rewritten, err := json.Marshal(listings)
	if err != nil {
		t.Fatalf("encode tampered listings: %v", err)
	}
	if err := os.WriteFile(listingsPath, rewritten, 0644); err != nil {
		t.Fatalf("write tampered listings: %v", err)
	}

	offline := NewClient("http://127.0.0.1:1", cacheDir, nil)
	if err := offline.LoadFromCache(); err == nil {
		t.Fatal("expected LoadFromCache to reject listings that do not match the manifest digest")
	}
	if results := offline.Search("postgres", SearchOptions{}); len(results) != 0 {
		t.Fatalf("tampered cache served %d search results", len(results))
	}
}
