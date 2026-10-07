package source

// mcp_registry_remote_test.go — a registry `remotes` entry publishes an
// endpoint, never a version.
//
// The adapter used to stamp the sentinel version "remote" on every remote
// entry: a version string the upstream registry never published, and one that
// mcpInstallability counted as proven, so a row holding nothing but an endpoint
// could claim metadata_verified and look installable. The honest shape of such
// a row is zero version summaries and discovery_only. The endpoint itself is
// real metadata and still has to survive ingestion — it lands on a version-less
// record, which is why these tests check both halves at once.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// TestMCPRegistryRemoteOnlyListingClaimsNoVersion covers an entry whose package
// registry publishes no version at all: only a hosted endpoint.
func TestMCPRegistryRemoteOnlyListingClaimsNoVersion(t *testing.T) {
	feed := []byte(`[
		{
			"name": "hosted-query",
			"description": "a hosted endpoint published without any version",
			"remotes": [
				{ "url": "https://mcp.example.com/v1", "transport": "http", "authType": "oauth2" }
			]
		}
	]`)

	res := ingestFeed(t, feed)
	if len(res.Listings) != 1 {
		t.Fatalf("expected 1 listing, got %d", len(res.Listings))
	}

	listing := res.Listings[0]
	if len(listing.Versions) != 0 {
		t.Errorf("a remote entry must contribute no version summary, got %+v", listing.Versions)
	}
	if listing.Installability != domain.InstallabilityDiscoveryOnly {
		t.Errorf("installability = %q, want %q", listing.Installability, domain.InstallabilityDiscoveryOnly)
	}
	if listing.IsInstallable() {
		t.Error("a listing with no published version must not be installable")
	}

	// The endpoint is still carried, on a record that claims no version and no
	// artifact: there is no package coordinate to fetch and no version to name
	// a locator after.
	if len(res.Versions) != 1 {
		t.Fatalf("expected exactly 1 record carrying the endpoint, got %d", len(res.Versions))
	}
	rec := res.Versions[0]
	if rec.Version != "" {
		t.Errorf("endpoint record claims version %q, want none", rec.Version)
	}
	if len(rec.Artifacts) != 0 {
		t.Errorf("endpoint record carries artifacts with no published version: %+v", rec.Artifacts)
	}
	if len(rec.Components) != 1 {
		t.Fatalf("expected 1 component carrying the endpoint, got %d", len(rec.Components))
	}
	rt := rec.Components[0].Runtime
	if rt == nil || rt.Type != "http" || rt.Endpoint != "https://mcp.example.com/v1" {
		t.Errorf("endpoint not preserved: %+v", rt)
	}

	assertNoRemoteSentinel(t, res)
}

// TestMCPRegistryMixedListingKeepsOnlyRealVersions covers an entry that
// publishes one real package version alongside an unversioned package and a
// remote endpoint: only the published version may reach the summaries.
func TestMCPRegistryMixedListingKeepsOnlyRealVersions(t *testing.T) {
	feed := []byte(`[
		{
			"name": "mixed-server",
			"description": "one published version, one package without one, one endpoint",
			"packages": [
				{
					"registryType": "npm",
					"name": "mixed-server",
					"command": "npx",
					"transport": "stdio"
				},
				{
					"registryType": "npm",
					"name": "mixed-server",
					"version": "2.1.0",
					"digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					"command": "npx",
					"transport": "stdio"
				}
			],
			"remotes": [
				{ "url": "https://mcp.example.com/v1", "transport": "http" }
			]
		}
	]`)

	res := ingestFeed(t, feed)
	if len(res.Listings) != 1 {
		t.Fatalf("expected 1 listing, got %d", len(res.Listings))
	}

	listing := res.Listings[0]
	if len(listing.Versions) != 1 || listing.Versions[0].Version != "2.1.0" {
		t.Fatalf("versions = %+v, want exactly the published 2.1.0", listing.Versions)
	}
	if listing.Installability != domain.InstallabilityMetadataVerified {
		t.Errorf("installability = %q, want %q", listing.Installability, domain.InstallabilityMetadataVerified)
	}
	if !listing.IsInstallable() {
		t.Error("a listing that published a real version stays installable")
	}

	// Two records: the published version (with its artifact) and the endpoint
	// (without one). Nothing else.
	if len(res.Versions) != 2 {
		t.Fatalf("expected 2 records (published version + endpoint), got %d", len(res.Versions))
	}
	withArtifact, endpoint := 0, 0
	for _, rec := range res.Versions {
		if rec.Version == "2.1.0" {
			withArtifact++
			if len(rec.Artifacts) != 1 {
				t.Errorf("published version must keep its artifact, got %+v", rec.Artifacts)
			}
			continue
		}
		if rec.Version == "" {
			endpoint++
			if len(rec.Artifacts) != 0 {
				t.Errorf("version-less record carries an artifact: %+v", rec.Artifacts)
			}
			continue
		}
		t.Errorf("invented version %q on %s", rec.Version, rec.ListingID)
	}
	if withArtifact != 1 || endpoint != 1 {
		t.Errorf("records: %d published, %d endpoint, want 1 and 1", withArtifact, endpoint)
	}

	assertNoRemoteSentinel(t, res)
}

func ingestFeed(t *testing.T, feed []byte) *IngestResult {
	t.Helper()
	adapter, err := NewMCPRegistryAdapter("builtin:mcp-registry", feed)
	if err != nil {
		t.Fatalf("NewMCPRegistryAdapter: %v", err)
	}
	res, err := adapter.Ingest(context.Background(), "snap_042")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return res
}

// assertNoRemoteSentinel fails if the sentinel string reaches any part of the
// ingest output — not just the version fields, because the old sentinel also
// lived in a component name and therefore in every component ID.
func assertNoRemoteSentinel(t *testing.T, res *IngestResult) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal ingest result: %v", err)
	}
	if strings.Contains(string(raw), "remote") {
		t.Errorf("ingest output contains \"remote\":\n%s", raw)
	}
}
