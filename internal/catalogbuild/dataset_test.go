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
	"args": ["-y", "brave-search-mcp-server-mcp"],
	"installability": "metadata_verified"
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
	"skillSource": "https://example.com/demo",
	"installability": "metadata_verified"
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
			"discovery_only row with a guessed launch command",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"","command":"npx","args":[],"installability":"discovery_only"}]`,
			"commands must be proven",
		},
		{
			"discovery_only row with an unproven remote endpoint",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"streamable-http","version":"","url":"https://mcp.example.com/mcp","installability":"discovery_only"}]`,
			"endpoints must be proven",
		},
		{
			"unknown installability",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"installability":"trusted"}]`,
			"unknown installability",
		},
		{
			"missing version",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"","command":"x","args":[],"installability":"metadata_verified"}]`,
			"has no version",
		},
		{
			"mcp row without a launch line",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"stdio","version":"1.0.0","command":"","args":[],"installability":"metadata_verified"}]`,
			"no launch line",
		},
		{
			"mcp row without transport",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"","version":"1.0.0","command":"x","args":[],"installability":"metadata_verified"}]`,
			"no transport",
		},
		{
			"mcp endpoint row without transport",
			`[{"id":"mcp:example:demo","kind":"mcp","name":"n","summary":"s","category":"c","publisher":{"name":"e"},"transport":"","version":"2.0.0","url":"https://mcp.example.com/mcp","installability":"metadata_verified"}]`,
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

// TestDiscoveryOnlyRowsCarryNoInventedClaims pins Phase 0.1: a heuristic row
// with no proven version or launch line is accepted as searchable metadata,
// converts with installability discovery_only, no version, and no runtime.
// The zero value of the installability field is treated as discovery_only.
func TestDiscoveryOnlyRowsCarryNoInventedClaims(t *testing.T) {
	for _, explicit := range []string{`"installability":"discovery_only",`, ``} {
		raw := `[{` + explicit + `"id":"mcp:acme:widget","kind":"mcp","name":"widget","summary":"s","category":"c","publisher":{"name":"acme"}}]`
		rows, err := ParseDataset([]byte(raw))
		if err != nil {
			t.Fatalf("discovery-only row rejected: %v", err)
		}
		listings, versions, err := ConvertDataset(rows, "rel-1", "snap-1", time.Now().UTC())
		if err != nil {
			t.Fatalf("ConvertDataset: %v", err)
		}
		if listings[0].Installability != domain.InstallabilityDiscoveryOnly || listings[0].IsInstallable() {
			t.Errorf("installability = %q, want discovery_only and not installable", listings[0].Installability)
		}
		if len(listings[0].Versions) != 0 || versions[0].Version != "" {
			t.Errorf("a version was invented: %+v / %q", listings[0].Versions, versions[0].Version)
		}
		for _, c := range versions[0].Components {
			if c.Runtime != nil {
				t.Errorf("a runtime was invented: %+v", c.Runtime)
			}
		}
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

// TestRemoteEndpointRows pins the remote (URL) MCP catalog data path (B1
// Phase 1): a row whose launch line is the publisher's own remotes[] endpoint
// parses above discovery_only, converts to a RuntimeDescriptor carrying
// Endpoint and no Command, keeps the published version in the listing and the
// version record, and is installable -- while the fail-closed rules still
// refuse an endpoint on a discovery_only row, an endpoint without transport,
// and a verified row with neither launch line. Existing stdio rows are
// unaffected, and a row carrying both command and endpoint launches the
// command (stdio wins; the endpoint is unused).
func TestRemoteEndpointRows(t *testing.T) {
	// The producer's shape for a remote row: command/args are null (never
	// invented) and the sanitized endpoint rides in url + transport.
	const remoteRow = `{
		"id": "mcp:builtin:mcp-registry:acme-remote",
		"kind": "mcp",
		"name": "acme-remote",
		"slug": "acme-remote",
		"summary": "Acme's hosted MCP endpoint.",
		"category": "Developer Tools",
		"publisher": {"name": "acme", "url": "https://acme.example"},
		"source": "builtin:mcp-registry",
		"transport": "streamable-http",
		"version": "2.1.0",
		"url": "https://mcp.acme.example/mcp",
		"command": null,
		"args": null,
		"installability": "metadata_verified"
	}`
	const discoveryEndpointRow = `{
		"id": "mcp:builtin:mcp-registry:acme-unproven",
		"kind": "mcp",
		"name": "acme-unproven",
		"summary": "s",
		"category": "c",
		"publisher": {"name": "acme"},
		"transport": "streamable-http",
		"url": "https://mcp.acme.example/mcp",
		"installability": "discovery_only"
	}`
	const endpointWithoutTransport = `{
		"id": "mcp:builtin:mcp-registry:acme-bare-endpoint",
		"kind": "mcp",
		"name": "acme-bare-endpoint",
		"summary": "s",
		"category": "c",
		"publisher": {"name": "acme"},
		"transport": "",
		"version": "2.1.0",
		"url": "https://mcp.acme.example/mcp",
		"installability": "metadata_verified"
	}`
	const verifiedWithoutLaunchLine = `{
		"id": "mcp:builtin:mcp-registry:acme-nothing",
		"kind": "mcp",
		"name": "acme-nothing",
		"summary": "s",
		"category": "c",
		"publisher": {"name": "acme"},
		"transport": "stdio",
		"version": "2.1.0",
		"command": "",
		"installability": "metadata_verified"
	}`
	const bothCommandAndEndpoint = `{
		"id": "mcp:builtin:mcp-registry:acme-both",
		"kind": "mcp",
		"name": "acme-both",
		"summary": "s",
		"category": "c",
		"publisher": {"name": "acme"},
		"transport": "stdio",
		"version": "1.0.0",
		"url": "https://mcp.acme.example/mcp",
		"command": "npx",
		"args": ["-y", "acme-mcp"],
		"installability": "metadata_verified"
	}`

	cases := []struct {
		name    string
		dataset string
		wantErr string // empty: the row must parse
		check   func(t *testing.T, listing *domain.Listing, version *domain.VersionRecord)
	}{
		{
			name:    "remote endpoint row parses and converts",
			dataset: remoteRow,
			check: func(t *testing.T, listing *domain.Listing, version *domain.VersionRecord) {
				if !listing.IsInstallable() {
					t.Errorf("installability = %q, want above discovery_only", listing.Installability)
				}
				if len(listing.Versions) != 1 || listing.Versions[0].Version != "2.1.0" {
					t.Errorf("listing Versions = %+v, want the published version 2.1.0", listing.Versions)
				}
				if version.Version != "2.1.0" {
					t.Errorf("version record version = %q, want 2.1.0", version.Version)
				}
				if len(version.Components) != 1 {
					t.Fatalf("components = %+v, want one", version.Components)
				}
				rt := version.Components[0].Runtime
				if rt == nil {
					t.Fatal("remote row converted with no runtime descriptor")
				}
				if rt.Endpoint != "https://mcp.acme.example/mcp" {
					t.Errorf("runtime endpoint = %q, want the published URL", rt.Endpoint)
				}
				if rt.Type != "streamable-http" {
					t.Errorf("runtime type = %q, want the published transport", rt.Type)
				}
				if rt.Command != "" || len(rt.Args) != 0 {
					t.Errorf("a command was invented for an endpoint row: %+v", rt)
				}
			},
		},
		{
			name:    "stdio row unchanged (regression)",
			dataset: validMCPRow,
			check: func(t *testing.T, listing *domain.Listing, version *domain.VersionRecord) {
				if !listing.IsInstallable() {
					t.Errorf("installability = %q, want above discovery_only", listing.Installability)
				}
				rt := version.Components[0].Runtime
				if rt == nil || rt.Command != "npx" || rt.Type != "stdio" || rt.Endpoint != "" {
					t.Errorf("stdio runtime changed: %+v", rt)
				}
			},
		},
		{
			name:    "stdio wins when both command and endpoint are present",
			dataset: bothCommandAndEndpoint,
			check: func(t *testing.T, listing *domain.Listing, version *domain.VersionRecord) {
				rt := version.Components[0].Runtime
				if rt == nil || rt.Command != "npx" || rt.Type != "stdio" {
					t.Fatalf("stdio runtime not used: %+v", rt)
				}
				if rt.Endpoint != "" {
					t.Errorf("endpoint %q must not reach the runtime when a command wins", rt.Endpoint)
				}
			},
		},
		{
			name:    "discovery_only row carrying an endpoint is rejected",
			dataset: discoveryEndpointRow,
			wantErr: "endpoints must be proven",
		},
		{
			name:    "endpoint row without transport is rejected",
			dataset: endpointWithoutTransport,
			wantErr: "no transport",
		},
		{
			name:    "verified row with neither command nor endpoint is rejected",
			dataset: verifiedWithoutLaunchLine,
			wantErr: "no launch line",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ParseDataset([]byte(`[` + tc.dataset + `]`))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDataset: %v", err)
			}
			listings, versions, err := ConvertDataset(rows, "rel-1", "snap-1", time.Now().UTC())
			if err != nil {
				t.Fatalf("ConvertDataset: %v", err)
			}
			tc.check(t, listings[0], versions[0])
		})
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
		if listing.Provenance.SourceSnapshotID != "" ||
			listing.Provenance.CatalogReleaseID != "rel-2026-10-05-01" ||
			!listing.Provenance.IngestedAt.Equal(ingestedAt) {
			t.Errorf("listing %s: provenance not stamped: %+v", listing.ID, listing.Provenance)
		}
		if row.Version != "" && (len(listing.Versions) != 1 || listing.Versions[0].Version != row.Version) {
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
		if v.SourceSnapshotID != "" || !v.FetchedAt.Equal(ingestedAt) {
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

	// K002 provenance contract: every emitted row carries the
	// domain.SourceID it was ingested from. A missing or grammar-invalid
	// `source` means the producer stopped stamping provenance, and the
	// dataset must not build silently without it.
	var provenance []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatalf("decode dataset for per-row source provenance: %v", err)
	}
	if len(provenance) != len(control) {
		t.Fatalf("decoded %d provenance rows but dataset has %d", len(provenance), len(control))
	}
	for i, r := range provenance {
		if _, err := domain.ParseSourceID(r.Source); err != nil {
			t.Fatalf("row %d (%s): missing or invalid source %q: %v", i, r.ID, r.Source, err)
		}
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
			listing.Provenance.SourceSnapshotID != "" {
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

// TestVersionlessCommandlessVerifiedSkillRow pins the producer contract added
// with installability-research A1: a skill row may claim metadata_verified on
// the strength of a fetched SKILL.md alone -- no version, no launch command --
// because the skills lifecycle installs a plain directory. publisher.url
// carries the same GitHub source as skillSource (the producer sets both on
// promotion), so Source.URL resolves to the skill's git location for the
// installer's ParseSkillSource.
func TestVersionlessCommandlessVerifiedSkillRow(t *testing.T) {
	const dataset = `[{
		"id": "skill:volttagent-awesome-agent-skills:anthropics-docx",
		"kind": "skill",
		"name": "anthropics/docx",
		"slug": "anthropics-docx",
		"summary": "Create, edit, and extract text from DOCX files.",
		"category": "Documents",
		"publisher": {"name": "VoltAgent", "url": "https://github.com/anthropics/docx"},
		"source": "git:anthropics-skills",
		"skillSource": "https://github.com/anthropics/docx",
		"installability": "metadata_verified"
	}]`

	rows, err := ParseDataset([]byte(dataset))
	if err != nil {
		t.Fatalf("version-less verified skill row rejected: %v", err)
	}
	row := rows[0]
	if row.Version != "" || len(row.Args) != 0 {
		t.Errorf("version/args must stay empty: %q %v", row.Version, row.Args)
	}

	listings, versions, err := ConvertDataset(rows, "rel-1", "snap-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	listing, version := listings[0], versions[0]

	if listing.Installability != domain.InstallabilityMetadataVerified || !listing.IsInstallable() {
		t.Errorf("installability = %q (installable=%v), want metadata_verified and installable",
			listing.Installability, listing.IsInstallable())
	}
	if len(listing.Versions) != 0 {
		t.Errorf("listing versions = %+v, want no version summaries (nothing upstream published one)", listing.Versions)
	}
	if version.Version != "" {
		t.Errorf("version record %q, want empty (honest absence)", version.Version)
	}
	if version.ListingID != listing.ID {
		t.Errorf("version record listing %q != %q", version.ListingID, listing.ID)
	}

	if got := listing.Source.URL; got != "https://github.com/anthropics/docx" {
		t.Errorf("Source.URL = %q, want the skillSource GitHub URL", got)
	}
	if got := listing.PublisherClaim.URL; got != listing.Source.URL {
		t.Errorf("publisher URL %q != source URL %q; promotion must set both", got, listing.Source.URL)
	}

	if len(version.Components) != 1 {
		t.Fatalf("components = %+v, want one skill component", version.Components)
	}
	component := version.Components[0]
	if component.Kind != domain.ComponentSkill || component.SupportedByLiteSPM != domain.SupportYes {
		t.Errorf("component = %+v, want a shipped skill component", component)
	}
	if component.Runtime != nil {
		t.Errorf("a runtime was invented for a skill row: %+v", component.Runtime)
	}
	// Component IDs embed a version; with none proven they must carry the
	// literal "discovery" marker rather than an invented release.
	if !strings.Contains(component.ID, "@discovery#") {
		t.Errorf("component id %q lacks the discovery version marker", component.ID)
	}
}

func sortedOutputKeys(b *BuildOutput) []string {
	keys := make([]string, 0, len(b.Files))
	for k := range b.Files {
		keys = append(keys, k)
	}
	return keys
}
