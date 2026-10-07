package source

// mcp_registry_version_test.go — an unpublished version is never invented.
//
// The adapter used to default a package with no `version` field to "1.0.0",
// which produced a resolvable, installable listing whose artifact URL and
// digest key were built from a version the upstream registry never published.
// The honest outcome for such a row is discovery_only: searchable metadata
// with no proven version.

import (
	"context"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestMCPRegistryUnversionedPackageIsDiscoveryOnly(t *testing.T) {
	feed := []byte(`[
		{
			"name": "never-versioned",
			"description": "the registry published no version for this package",
			"packages": [
				{
					"registryType": "npm",
					"name": "never-versioned",
					"command": "npx",
					"transport": "stdio"
				}
			]
		},
		{
			"name": "partially-versioned",
			"description": "one package without a version, one with",
			"packages": [
				{
					"registryType": "npm",
					"name": "partially-versioned",
					"command": "npx",
					"transport": "stdio"
				},
				{
					"registryType": "npm",
					"name": "partially-versioned",
					"version": "2.1.0",
					"command": "npx",
					"transport": "stdio"
				}
			]
		}
	]`)

	adapter, err := NewMCPRegistryAdapter("builtin:mcp-registry", feed)
	if err != nil {
		t.Fatalf("NewMCPRegistryAdapter: %v", err)
	}
	res, err := adapter.Ingest(context.Background(), "snap_test")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Listings) != 2 {
		t.Fatalf("expected 2 listings, got %d", len(res.Listings))
	}

	// Nothing anywhere may carry the old guess.
	for _, v := range res.Versions {
		if v.Version == "1.0.0" {
			t.Fatalf("ingest invented version 1.0.0 for %s", v.ListingID)
		}
		if strings.TrimSpace(v.Version) == "" {
			t.Fatalf("ingest emitted a version record with an empty version for %s", v.ListingID)
		}
	}

	unversioned := res.Listings[0]
	if len(unversioned.Versions) != 0 {
		t.Errorf("a package with no published version must yield no versions, got %+v", unversioned.Versions)
	}
	if unversioned.Installability != domain.InstallabilityDiscoveryOnly {
		t.Errorf("installability = %q, want %q", unversioned.Installability, domain.InstallabilityDiscoveryOnly)
	}
	if unversioned.IsInstallable() {
		t.Error("a listing with no proven version must not be installable")
	}

	// The proven version of the second listing still counts: only the
	// unversioned package is dropped.
	partial := res.Listings[1]
	if len(partial.Versions) != 1 || partial.Versions[0].Version != "2.1.0" {
		t.Errorf("versions = %+v, want exactly the proven 2.1.0", partial.Versions)
	}
	if !partial.IsInstallable() {
		t.Error("a listing that did publish a version stays installable")
	}
	for _, v := range res.Versions {
		if strings.HasPrefix(v.ListingID, partial.ID) && v.Version == "1.0.0" {
			t.Errorf("guessed version attached to %s", v.ListingID)
		}
	}
}

func TestMCPInstallabilityRejectsEmptyVersionSummaries(t *testing.T) {
	if got := mcpInstallability(nil); got != domain.InstallabilityDiscoveryOnly {
		t.Errorf("no versions = %q, want discovery_only", got)
	}
	if got := mcpInstallability([]domain.VersionSummary{{Version: ""}}); got != domain.InstallabilityDiscoveryOnly {
		t.Errorf("an empty version string is not a version: got %q, want discovery_only", got)
	}
	if got := mcpInstallability([]domain.VersionSummary{{Version: "  "}}); got != domain.InstallabilityDiscoveryOnly {
		t.Errorf("a blank version string is not a version: got %q, want discovery_only", got)
	}
	if got := mcpInstallability([]domain.VersionSummary{{Version: ""}, {Version: "0.3.1"}}); got != domain.InstallabilityMetadataVerified {
		t.Errorf("one proven version = %q, want metadata_verified", got)
	}
}
