package catalogbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

const validMCPRow = `{
	"id": "mcp:brave:brave-search-mcp-server",
	"kind": "mcp",
	"name": "brave-search-mcp-server",
	"slug": "brave-search-mcp-server",
	"summary": "Web search using Brave's Search API.",
	"category": "Search & Retrieval",
	"publisher": {"name": "brave", "url": "https://github.com/brave"},
	"transport": "stdio",
	"version": "1.0.0",
	"command": "npx",
	"args": ["-y", "brave-search-mcp-server-mcp"]
}`

const validSkillRow = `{
	"id": "skill:example:demo-skill",
	"kind": "skill",
	"name": "demo-skill",
	"slug": "demo-skill",
	"summary": "A demo skill",
	"category": "Dev Skills",
	"publisher": {"name": "example", "url": "https://example.com"},
	"version": "0.1.0",
	"skillSource": "https://example.com/demo"
}`

// TestParseDatasetRejectsMalformedRows pins the fail-closed contract: the
// generator normalizes ids to the domain grammar (canonical_id in
// scripts/build_full_catalog.py), so any violation here means producer and
// domain have drifted and the release must not build.
func TestParseDatasetRejectsMalformedRows(t *testing.T) {
	cases := []struct {
		name    string
		dataset string
		want    string
	}{
		{"empty dataset", `[]`, "contains no rows"},
		{"not json", `{"nope": true}`, "decode dataset"},
		{
			"id outside domain grammar (spaces)",
			`[{"id":"skill:community:agent trust hub","kind":"skill","name":"n","summary":"s","category":"c","publisher":{"name":"community"},"version":"1.0.0"}]`,
			"does not match the domain grammar",
		},
		{
			"id outside domain grammar (query junk)",
			`[{"id":"mcp:planetscale:cli?tab=readme-ov-file","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"planetscale"},"transport":"stdio","version":"1.0.0","command":"x","args":[]}]`,
			"does not match the domain grammar",
		},
		{
			"id kind disagrees with row kind",
			`[{"id":"mcp:example:demo","kind":"skill","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"version":"1.0.0"}]`,
			"disagrees with row kind",
		},
		{"duplicate id", `[` + validMCPRow + `,` + validMCPRow + `]`, "duplicate id"},
		{
			"missing name",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"1.0.0","command":"x","args":[]}]`,
			"missing name",
		},
		{
			"missing version",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"","command":"x","args":[]}]`,
			"missing version",
		},
		{
			"mcp row without launch command",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"1.0.0","command":"","args":[]}]`,
			"no launch command",
		},
		{
			"mcp row without transport",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"","version":"1.0.0","command":"x","args":[]}]`,
			"no transport",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDataset([]byte(tc.dataset))
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseDatasetAcceptsValidRows(t *testing.T) {
	rows, err := ParseDataset([]byte(`[` + validMCPRow + `,` + validSkillRow + `]`))
	if err != nil {
		t.Fatalf("valid dataset rejected: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

// TestConvertDatasetClaimsAreHonest pins every claim the converter is allowed
// to write: schema version, active status, "unverified" verification (the
// dataset carries no audit evidence), provenance pointing at the snapshot and
// release, and component support levels that do not overclaim the install path.
func TestConvertDatasetClaimsAreHonest(t *testing.T) {
	rows, err := ParseDataset([]byte(`[` + validMCPRow + `,` + validSkillRow + `]`))
	if err != nil {
		t.Fatalf("ParseDataset: %v", err)
	}

	ingestedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	listings, versions, err := ConvertDataset(rows, "rel-2026-10-05-01", "snap-deadbeef", ingestedAt)
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	if len(listings) != 2 || len(versions) != 2 {
		t.Fatalf("expected 2 listings and 2 versions, got %d and %d", len(listings), len(versions))
	}

	for i, listing := range listings {
		row := rows[i]
		if listing.ID != row.ID {
			t.Errorf("listing %d: id %q != row id %q", i, listing.ID, row.ID)
		}
		if listing.SchemaVersion != 1 {
			t.Errorf("listing %s: schemaVersion %d, want 1", listing.ID, listing.SchemaVersion)
		}
		if listing.Status != domain.ListingStatusActive {
			t.Errorf("listing %s: status %q, want active", listing.ID, listing.Status)
		}
		if listing.VerificationSummary.Level != "unverified" {
			t.Errorf("listing %s: verification %q, want unverified (dataset carries no audit evidence)",
				listing.ID, listing.VerificationSummary.Level)
		}
		if listing.Provenance.SourceSnapshotID != "snap-deadbeef" ||
			listing.Provenance.CatalogReleaseID != "rel-2026-10-05-01" ||
			!listing.Provenance.IngestedAt.Equal(ingestedAt) {
			t.Errorf("listing %s: provenance not stamped: %+v", listing.ID, listing.Provenance)
		}
		if len(listing.Versions) != 1 || listing.Versions[0].Version != row.Version {
			t.Errorf("listing %s: versions %+v do not carry dataset version %q", listing.ID, listing.Versions, row.Version)
		}
		// The dataset's popularity figure is deliberately null; nothing may
		// invent one downstream. The domain model must not grow a field for it.
		if _, err := json.Marshal(listing); err != nil {
			t.Errorf("listing %s: marshal: %v", listing.ID, err)
		}
	}

	// MCP launch line preserved, but support is NOT claimed: installing a
	// catalog-sourced MCP server is not wired yet (STATUS.md install row).
	mcpListing, mcpVersion := listings[0], versions[0]
	if len(mcpListing.ComponentsSummary) != 1 || mcpListing.ComponentsSummary[0].Kind != domain.ComponentMCPProvider {
		t.Errorf("mcp components summary: %+v", mcpListing.ComponentsSummary)
	}
	if len(mcpVersion.Components) != 1 {
		t.Fatalf("mcp version components: %+v", mcpVersion.Components)
	}
	mcpComponent := mcpVersion.Components[0]
	if mcpComponent.Runtime == nil {
		t.Fatal("mcp component has no runtime descriptor: launch line dropped")
	}
	if mcpComponent.Runtime.Command != "npx" || mcpComponent.Runtime.Type != "stdio" {
		t.Errorf("mcp runtime: %+v, want command npx / type stdio", mcpComponent.Runtime)
	}
	if mcpComponent.SupportedByLiteSPM != domain.SupportUnknown {
		t.Errorf("mcp support %q: catalog install path is not wired; must be unknown, not a claim",
			mcpComponent.SupportedByLiteSPM)
	}

	// Skill components are shipped today (skills lifecycle), so SupportYes is
	// honest for them.
	skillListing, skillVersion := listings[1], versions[1]
	if len(skillVersion.Components) != 1 || skillVersion.Components[0].SupportedByLiteSPM != domain.SupportYes {
		t.Errorf("skill component: %+v, want SupportYes", skillVersion.Components)
	}
	if len(skillListing.ComponentsSummary) != 1 || skillListing.ComponentsSummary[0].Kind != domain.ComponentSkill {
		t.Errorf("skill components summary: %+v", skillListing.ComponentsSummary)
	}

	// Version records must be complete arrays (never null) so the emitted
	// JSON satisfies schemas/version.schema.json.
	for _, v := range versions {
		if v.Artifacts == nil || v.Components == nil || v.Dependencies == nil ||
			v.Requirements == nil || v.PermissionsDeclared == nil ||
			v.CompatibilityClaims == nil || v.TestEvidence == nil {
			t.Errorf("version %s/%s has a null array field: %+v", v.ListingID, v.Version, v)
		}
		if v.SourceSnapshotID != "snap-deadbeef" || !v.FetchedAt.Equal(ingestedAt) {
			t.Errorf("version %s/%s provenance not stamped: %+v", v.ListingID, v.Version, v)
		}
	}
}

// TestDatasetPipelineAgainstRealCatalog runs the committed 5,814-row dataset
// through the exact validation and conversion a release build performs. If the
// generator ever regresses on id grammar (spaces, query junk, duplicates),
// this is the test that fails before any tree is published.
func TestDatasetPipelineAgainstRealCatalog(t *testing.T) {
	datasetPath := filepath.Join("..", "..", "web", "data", "catalog.json")
	raw, err := os.ReadFile(datasetPath)
	if err != nil {
		t.Fatalf("read committed dataset %s: %v", datasetPath, err)
	}

	var control []json.RawMessage
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("decode dataset for independent row count: %v", err)
	}

	rows, err := ParseDataset(raw)
	if err != nil {
		t.Fatalf("committed dataset failed validation: %v", err)
	}
	if len(rows) != len(control) {
		t.Fatalf("validated %d rows but dataset has %d", len(rows), len(control))
	}
	if len(rows) < 5000 {
		t.Fatalf("dataset has only %d rows: expected thousands (truncated dataset?)", len(rows))
	}

	ingestedAt := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	snapshotID := DatasetSnapshotID(raw)
	listings, versions, err := ConvertDataset(rows, "rel-2026-10-05-01", snapshotID, ingestedAt)
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	if len(listings) != len(rows) || len(versions) != len(rows) {
		t.Fatalf("converted %d listings / %d versions for %d rows", len(listings), len(versions), len(rows))
	}

	seen := make(map[string]bool, len(listings))
	for i, listing := range listings {
		row := rows[i]
		if seen[listing.ID] {
			t.Fatalf("duplicate listing id %q", listing.ID)
		}
		seen[listing.ID] = true
		if listing.ID != row.ID || string(listing.Kind) != row.Kind {
			t.Fatalf("row %d: listing id/kind %q/%q != dataset %q/%q", i, listing.ID, listing.Kind, row.ID, row.Kind)
		}
		if listing.Status != domain.ListingStatusActive ||
			listing.VerificationSummary.Level != "unverified" ||
			listing.Provenance.SourceSnapshotID != snapshotID {
			t.Fatalf("listing %s: dishonest or unstamped claim: status=%q verification=%q provenance=%+v",
				listing.ID, listing.Status, listing.VerificationSummary.Level, listing.Provenance)
		}
		if versions[i].ListingID != row.ID {
			t.Fatalf("version record %d names %q, want %q", i, versions[i].ListingID, row.ID)
		}
	}

	// The whole corpus must compile into a release tree — the real build's
	// first stage — and the compiled listings must round-trip at full count.
	output, err := CompileRelease("rel-2026-10-05-01", 1, []string{snapshotID}, listings, versions, ingestedAt)
	if err != nil {
		t.Fatalf("CompileRelease over real dataset: %v", err)
	}
	listingsBytes, ok := output.Files["v1/releases/rel-2026-10-05-01/listings.json"]
	if !ok {
		t.Fatalf("builder did not emit listings.json (keys: %v)", sortedOutputKeys(output))
	}
	var roundTrip []*domain.Listing
	if err := json.Unmarshal(listingsBytes, &roundTrip); err != nil {
		t.Fatalf("compiled listings do not decode: %v", err)
	}
	if len(roundTrip) != len(rows) {
		t.Fatalf("compiled listings round-tripped %d of %d", len(roundTrip), len(rows))
	}
	if output.Manifest.ItemCount != len(rows) {
		t.Fatalf("manifest itemCount %d != %d", output.Manifest.ItemCount, len(rows))
	}

	// The compiled manifest must decode into its own Go types with sizes at
	// real-catalog magnitude (millions of bytes): RFC 8785 number formatting
	// once emitted scientific notation for exactly this case, which
	// encoding/json refuses to read into an int64 size field.
	manifestBytes, ok := output.Files["v1/releases/rel-2026-10-05-01/manifest.json"]
	if !ok {
		t.Fatalf("builder did not emit manifest.json (keys: %v)", sortedOutputKeys(output))
	}
	var manifest ReleaseManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("compiled manifest does not decode into ReleaseManifest: %v", err)
	}
	entry, ok := manifest.Files["listings.json"]
	if !ok {
		t.Fatal("compiled manifest has no listings.json entry")
	}
	if entry.Size != int64(len(listingsBytes)) {
		t.Errorf("manifest listings size %d != actual %d", entry.Size, len(listingsBytes))
	}
	if entry.Size < 1_000_000 {
		t.Errorf("listings.json is %d bytes: expected real-catalog magnitude to exercise large-size formatting", entry.Size)
	}
	if entry.Digest != domain.ComputeBytesDigest(listingsBytes) {
		t.Errorf("manifest listings digest %s != %s", entry.Digest, domain.ComputeBytesDigest(listingsBytes))
	}
	if manifestDigest := domain.ComputeBytesDigest(manifestBytes); output.ManifestDigest != manifestDigest {
		t.Errorf("BuildOutput.ManifestDigest %s != digest of emitted manifest %s", output.ManifestDigest, manifestDigest)
	}
}

func TestDatasetSnapshotIDContentAddressed(t *testing.T) {
	a := DatasetSnapshotID([]byte("one"))
	b := DatasetSnapshotID([]byte("one"))
	c := DatasetSnapshotID([]byte("two"))
	if a != b {
		t.Fatalf("identical bytes produced different snapshot ids: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different bytes produced the same snapshot id: %q", a)
	}
	if !strings.HasPrefix(a, "snap-") {
		t.Fatalf("snapshot id %q lacks snap- prefix", a)
	}
}

func sortedOutputKeys(b *BuildOutput) []string {
	keys := make([]string, 0, len(b.Files))
	for k := range b.Files {
		keys = append(keys, k)
	}
	return keys
}
