package source

// mcp_registry_live_test.go — the adapter pinned against VERBATIM live
// registry documents (see mcp_registry_live_fixture_test.go for provenance).
//
// The demanded property: whatever the 2025-12-11 schema publishes —
// `registryType`, `identifier`, `runtimeHint`, `packageArguments` — the
// adapter must never turn any of it into a launch line. Every component
// below carries an empty Runtime.Command with nil Args and nil Env, and the
// whole ingest result contains no `"command":` at all.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// TestMCPRegistryLiveSubsetIngestsWithoutLaunchLines ingests the captured
// live documents and proves no launch line is emitted anywhere.
func TestMCPRegistryLiveSubsetIngestsWithoutLaunchLines(t *testing.T) {
	res := ingestFeed(t, []byte(liveRegistrySubset))

	if len(res.Listings) != 9 {
		t.Fatalf("got %d listings, want 9 (the captured subset)", len(res.Listings))
	}
	byName := map[string]*domain.Listing{}
	for _, l := range res.Listings {
		byName[l.Name] = l
	}

	// Repository object (2025-12-11 spelling) lands as the listing source.
	bev, ok := byName["ai.agent-bev/bev-door"]
	if !ok {
		t.Fatalf("missing ai.agent-bev/bev-door, got %v", keysOfListings(res))
	}
	if bev.Source.URL != "https://github.com/agent-bev/bev-door" {
		t.Errorf("repository object not mapped to Source.URL: %q", bev.Source.URL)
	}
	// A package coordinate carries no version-pinning surprise.
	if len(bev.Versions) != 1 || bev.Versions[0].Version != "0.2.5" {
		t.Errorf("bev-door versions = %+v, want 0.2.5", bev.Versions)
	}

	// Remote-only entries publish no version and stay discovery_only.
	remoteOnly, ok := byName["ac.inference.sh/mcp"]
	if !ok {
		t.Fatalf("missing ac.inference.sh/mcp, got %v", keysOfListings(res))
	}
	if remoteOnly.Installability != domain.InstallabilityDiscoveryOnly {
		t.Errorf("remote-only installability = %q", remoteOnly.Installability)
	}

	// A versionless package (the OCI entry publishes none) contributes no
	// version summary and no fabricated one in its place.
	apithreshold, ok := byName["ai.apithreshold/apithreshold"]
	if !ok {
		t.Fatalf("missing ai.apithreshold/apithreshold, got %v", keysOfListings(res))
	}
	if len(apithreshold.Versions) != 0 {
		t.Errorf("versionless package must contribute no version summary, got %+v", apithreshold.Versions)
	}
	if apithreshold.Installability != domain.InstallabilityDiscoveryOnly {
		t.Errorf("versionless-only installability = %q", apithreshold.Installability)
	}

	// The hard negative: no component may carry a command, args or env,
	// because nothing in these documents declares one.
	for _, rec := range res.Versions {
		for _, comp := range rec.Components {
			rt := comp.Runtime
			if rt == nil {
				t.Errorf("%s/%s: component %s has no runtime descriptor", rec.ListingID, rec.Version, comp.ID)
				continue
			}
			if rt.Command != "" {
				t.Errorf("%s/%s: fabricated command %q", rec.ListingID, rec.Version, rt.Command)
			}
			if len(rt.Args) != 0 {
				t.Errorf("%s/%s: fabricated args %v", rec.ListingID, rec.Version, rt.Args)
			}
			if len(rt.Env) != 0 {
				t.Errorf("%s/%s: fabricated env %v", rec.ListingID, rec.Version, rt.Env)
			}
		}
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal ingest result: %v", err)
	}
	for _, banned := range []string{`"command"`, `"args"`, `"env"`, "npx", "runtimeHint", "packageArguments"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("ingest output contains %s (a launch-line ingredient):\n%s", banned, raw)
		}
	}
}

// TestMCPRegistryLiveRemotesCarryOnlyEndpoints pins the endpoint shape:
// `type` (not the legacy `transport`) with the URL, on a record that claims
// no version and no artifact.
func TestMCPRegistryLiveRemotesCarryOnlyEndpoints(t *testing.T) {
	res := ingestFeed(t, []byte(liveRegistrySubset))

	var endpoints int
	for _, rec := range res.Versions {
		for _, comp := range rec.Components {
			rt := comp.Runtime
			if rt == nil || rt.Endpoint == "" {
				continue
			}
			endpoints++
			if rt.Type != "streamable-http" && rt.Type != "sse" {
				t.Errorf("endpoint transport = %q, want the declared type verbatim", rt.Type)
			}
			if rt.Command != "" {
				t.Errorf("endpoint %q carries a fabricated command %q", rt.Endpoint, rt.Command)
			}
			if len(rec.Artifacts) != 0 {
				t.Errorf("endpoint record %q carries artifacts: %+v", rec.ListingID, rec.Artifacts)
			}
		}
	}
	if endpoints != 6 {
		t.Errorf("got %d endpoints, want 6 (ac.inference, 1325, adramp, agent-bev, agentdm, affiliate)", endpoints)
	}
}

// TestMCPRegistryEnvelopeFeedRefused names the registry API list response
// instead of failing on an incidental field (or ingesting 100 entries as if
// they were 100 distinct servers).
func TestMCPRegistryEnvelopeFeedRefused(t *testing.T) {
	// The verbatim ?limit=100 response captured by the interop lane.
	data, err := os.ReadFile("../interop/testdata/registry-live-list-100.json")
	if err != nil {
		t.Fatalf("read live list fixture: %v", err)
	}
	adapter, _ := NewMCPRegistryAdapter("builtin:mcp-registry", data)
	_, err = adapter.Ingest(context.Background(), "snap_042")
	if err == nil {
		t.Fatal("a registry list response must be refused, not ingested")
	}
	for _, want := range []string{"list response", "servers", "not a server.json feed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must say %q, got: %v", want, err)
		}
	}
}

// TestMCPRegistryNamelessObjectRefused: a single object with no name used to
// ingest as zero rows — a silent no-op that read like success.
func TestMCPRegistryNamelessObjectRefused(t *testing.T) {
	adapter, _ := NewMCPRegistryAdapter("builtin:mcp-registry",
		[]byte(`{"description":"no name at all"}`))
	_, err := adapter.Ingest(context.Background(), "snap_042")
	if err == nil {
		t.Fatal("a nameless object must be refused")
	}
	if !strings.Contains(err.Error(), "declares no server name") {
		t.Errorf("error must name the problem: %v", err)
	}
}

// TestMCPRegistryPackageWithoutCoordinateRefused: the live schema requires
// `identifier` (legacy `name`); a package with neither is unparseable, and
// the refusal must say which field is missing.
func TestMCPRegistryPackageWithoutCoordinateRefused(t *testing.T) {
	adapter, _ := NewMCPRegistryAdapter("builtin:mcp-registry", []byte(`[
		{"name":"x/y","packages":[{"registryType":"npm","version":"1.0.0"}]}
	]`))
	_, err := adapter.Ingest(context.Background(), "snap_042")
	if err == nil {
		t.Fatal("a coordinateless package must be refused")
	}
	if !strings.Contains(err.Error(), "packages[0].identifier is required") {
		t.Errorf("error must name the field: %v", err)
	}
}

// TestMCPRegistryFileSha256LandsInDigestAndIsValidated: the live schema's
// bare 64-hex package hash becomes the artifact digest in the one vocabulary
// domain.ArtifactRef documents, and a malformed one is refused rather than
// recorded as if it were valid.
func TestMCPRegistryFileSha256LandsInDigestAndIsValidated(t *testing.T) {
	const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	feed := []byte(`[
		{"name":"x/y","packages":[
			{"registryType":"npm","identifier":"x-y","version":"1.0.0","fileSha256":"` + hex64 + `","transport":{"type":"stdio"}}
		]}
	]`)
	res := ingestFeed(t, feed)
	if len(res.Versions) != 1 {
		t.Fatalf("got %d records, want 1", len(res.Versions))
	}
	art := res.Versions[0].Artifacts[0]
	if art.Digest != "sha256:"+hex64 {
		t.Errorf("digest = %q, want sha256:%s", art.Digest, hex64)
	}
	if art.ArtifactID == "" || art.Locator == "" {
		t.Errorf("artifact not fully formed: %+v", art)
	}

	bad := []byte(`[
		{"name":"x/y","packages":[
			{"registryType":"npm","identifier":"x-y","version":"1.0.0","fileSha256":"ABC","transport":{"type":"stdio"}}
		]}
	]`)
	adapter, _ := NewMCPRegistryAdapter("builtin:mcp-registry", bad)
	if _, err := adapter.Ingest(context.Background(), "snap_042"); err == nil ||
		!strings.Contains(err.Error(), "fileSha256") {
		t.Fatalf("malformed fileSha256 must be refused naming the field, got: %v", err)
	}
}

// keysOfListings lists listing names for failure messages.
func keysOfListings(res *IngestResult) []string {
	names := make([]string, 0, len(res.Listings))
	for _, l := range res.Listings {
		names = append(names, l.Name)
	}
	return names
}
