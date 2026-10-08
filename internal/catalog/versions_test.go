package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/resolver"
)

// compileReleaseWithRuntime builds a release whose single listing publishes a
// runnable stdio component — the shape an MCP install depends on.
func compileReleaseWithRuntime(t *testing.T, releaseID string, sequence int) *catalogbuild.BuildOutput {
	t.Helper()
	listings := sampleListings()
	versions := make([]*domain.VersionRecord, 0, len(listings))
	for _, listing := range listings {
		rec := &domain.VersionRecord{ListingID: listing.ID, Version: "1.0.0"}
		if listing.Kind == domain.KindMCP {
			rec.Components = []domain.Component{{
				ID:      listing.ID + "@1.0.0#mcp-provider/server",
				Kind:    domain.ComponentMCPProvider,
				Name:    "server",
				Runtime: &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", "demo-mcp"}},
			}}
		}
		versions = append(versions, rec)
	}
	compiled, err := catalogbuild.CompileRelease(releaseID, sequence, nil, listings, versions, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	return compiled
}

// TestSyncFetchesAndCachesVersions pins the gap that made MCP installs
// impossible: the release publishes versions.json (the only place a launch line
// exists) and the client used to ignore it entirely.
func TestSyncFetchesAndCachesVersions(t *testing.T) {
	compiled := compileReleaseWithRuntime(t, "rel-test-01", 5)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	server := serveFiles(t, files)
	defer server.Close()

	cache := t.TempDir()
	client := NewClient(server.URL, cache, server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	cached := filepath.Join(cache, "v1", "releases", compiled.ReleaseID, "versions.json")
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("versions.json was not cached: %v", err)
	}

	listingID := sampleListings()[0].ID
	runtime, err := client.RuntimeForListing(listingID, "")
	if err != nil {
		t.Fatalf("RuntimeForListing: %v", err)
	}
	if runtime.Command != "npx" || len(runtime.Args) != 2 || runtime.Args[1] != "demo-mcp" {
		t.Errorf("unexpected runtime: %+v", runtime)
	}
}

// TestSyncRejectsTamperedVersions is the reason versions.json is verified rather
// than trusted: it is the file an installer reads a command line out of.
func TestSyncRejectsTamperedVersions(t *testing.T) {
	compiled := compileReleaseWithRuntime(t, "rel-test-02", 6)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}

	// Same length, different bytes: only the digest can notice.
	tampered := []byte(compiled.Files["v1/releases/"+compiled.ReleaseID+"/versions.json"])
	tampered[len(tampered)-3] = 'x'
	files["v1/releases/"+compiled.ReleaseID+"/versions.json"] = tampered

	server := serveFiles(t, files)
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	if _, err := client.Sync(context.Background()); err == nil {
		t.Fatal("Sync accepted a versions.json that does not match the manifest digest")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("expected a checksum failure, got %v", err)
	}
}

// TestRuntimeForListingFailsClosedWithoutARunnableComponent keeps an installer
// from writing a config entry that cannot start.
func TestRuntimeForListingFailsClosedWithoutARunnableComponent(t *testing.T) {
	listings := sampleListings()
	versions := []*domain.VersionRecord{{ListingID: listings[0].ID, Version: "1.0.0"}}
	compiled, err := catalogbuild.CompileRelease("rel-test-03", 7, nil, listings, versions, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	server := serveFiles(t, files)
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := client.RuntimeForListing(listings[0].ID, ""); err == nil {
		t.Fatal("RuntimeForListing invented a command for a version with no runtime component")
	}
}

// TestVersionRecordsLatestUsesSemver pins that "latest" is chosen by semver
// precedence: a lexical sort would rank 1.9.0 above 1.10.0.
func TestVersionRecordsLatestUsesSemver(t *testing.T) {
	idx := NewSearchIndex()
	idx.IndexVersions([]*domain.VersionRecord{
		{ListingID: "mcp:demo:semver", Version: "1.9.0"},
		{ListingID: "mcp:demo:semver", Version: "1.10.0"},
		{ListingID: "mcp:demo:semver", Version: "1.2.0"},
		{ListingID: "mcp:demo:semver", Version: "1.10.0-rc.1"},
		{ListingID: "mcp:demo:semver", Version: "remote"},
	})
	records := idx.VersionRecords("mcp:demo:semver")
	var got []string
	for _, r := range records {
		got = append(got, r.Version)
	}
	want := []string{"1.10.0", "1.10.0-rc.1", "1.9.0", "1.2.0", "remote"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if records[0].Version != "1.10.0" {
		t.Fatalf("latest = %q, want 1.10.0", records[0].Version)
	}
}

// componentVersionSegment returns the version segment of a component id —
// `<listing-id>@<version>#<kind>/<name>` — the way the release publishes it.
func componentVersionSegment(t *testing.T, id string) string {
	t.Helper()
	at, hash := strings.Index(id, "@"), strings.Index(id, "#")
	if at < 0 || hash <= at {
		t.Fatalf("component id %q is not of the form <listing>@<version>#<kind>/<name>", id)
	}
	return id[at+1 : hash]
}

// TestVersionLessListingRoundTripsThroughRelease pins the release convention
// that version-less resolution (recommendation A2) depends on, end to end:
// a dataset row with no version converts to a listing with `Versions: []` and
// a version record whose `version` is "", every component id of that record
// embeds the "discovery" literal, and both facts survive CompileRelease →
// sync → index unchanged. The segment is compared against
// resolver.ImplicitVersion — written from the other side of the boundary —
// so either convention drifting fails this test first.
func TestVersionLessListingRoundTripsThroughRelease(t *testing.T) {
	const listingID = "skill:aaron-he-zhu:aaron-marketing-skills"
	dataset := `[{
		"id": "skill:aaron-he-zhu:aaron-marketing-skills",
		"kind": "skill",
		"name": "Aaron Marketing Skills",
		"slug": "aaron-marketing-skills",
		"summary": "Production-grade agent playbook for aaron-he-zhu/aaron-marketing-skills",
		"category": "Community Skills",
		"publisher": {"name": "aaron-he-zhu", "url": "https://github.com/aaron-he-zhu/aaron-marketing-skills"},
		"version": "",
		"installability": "discovery_only"
	}]`
	rows, err := catalogbuild.ParseDataset([]byte(dataset))
	if err != nil {
		t.Fatalf("ParseDataset: %v", err)
	}
	listings, versions, err := catalogbuild.ConvertDataset(rows, "rel-test-vless", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	if len(listings[0].Versions) != 0 {
		t.Fatalf("Versions = %+v, want [] for a row that publishes no version", listings[0].Versions)
	}
	if versions[0].Version != "" {
		t.Fatalf("record version = %q, want the honest empty value", versions[0].Version)
	}
	if got := componentVersionSegment(t, versions[0].Components[0].ID); got != resolver.ImplicitVersion {
		t.Fatalf("component id embeds %q, but the resolver pins %q — the two conventions have drifted",
			got, resolver.ImplicitVersion)
	}

	// Round-trip the shape through a compiled release and the client's real
	// sync path: both facts must survive publication and re-loading byte-stable.
	compiled, err := catalogbuild.CompileRelease("rel-test-vless", 8, nil, listings, versions, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	server := serveFiles(t, files)
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := client.GetListing(listingID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if len(got.Versions) != 0 {
		t.Fatalf("synced listing Versions = %+v, want []", got.Versions)
	}
	rec, err := client.VersionRecordFor(listingID, "")
	if err != nil {
		t.Fatalf("VersionRecordFor: %v", err)
	}
	if rec.Version != "" {
		t.Errorf("synced record version = %q, want %q", rec.Version, "")
	}
	if len(rec.Components) == 0 {
		t.Fatal("synced record lost its components")
	}
	if seg := componentVersionSegment(t, rec.Components[0].ID); seg != resolver.ImplicitVersion {
		t.Errorf("synced component id embeds %q, want %q", seg, resolver.ImplicitVersion)
	}
}
