package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
)

// syncClientWithRecords serves a release built from sampleListings() plus the
// given version records, then returns a client synced against it.
func syncClientWithRecords(t *testing.T, releaseID string, sequence int, records []*domain.VersionRecord) *Client {
	t.Helper()
	compiled, err := catalogbuild.CompileRelease(releaseID, sequence, nil, sampleListings(), records, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	server := serveFiles(t, files)
	t.Cleanup(server.Close)

	client := NewClient(server.URL, t.TempDir(), server.Client())
	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return client
}

// TestRuntimeForListingAcceptsEndpointOnlyComponent pins the B1 blocker fix:
// a version record whose only component is a remote (URL) launch line -- an
// endpoint and transport, no command -- must resolve, or every plan builder
// would fail closed with LPSM-ARTIFACT-UNAVAILABLE before an endpoint install
// could start.
func TestRuntimeForListingAcceptsEndpointOnlyComponent(t *testing.T) {
	listings := sampleListings()
	records := []*domain.VersionRecord{{
		ListingID: listings[0].ID,
		Version:   "2.1.0",
		Components: []domain.Component{{
			ID:   listings[0].ID + "@2.1.0#mcp-provider/server",
			Kind: domain.ComponentMCPProvider,
			Name: "server",
			Runtime: &domain.RuntimeDescriptor{
				Type:     "streamable-http",
				Endpoint: "https://mcp.example.com/mcp",
			},
		}},
	}}
	client := syncClientWithRecords(t, "rel-endpoint-01", 9, records)

	runtime, err := client.RuntimeForListing(listings[0].ID, "")
	if err != nil {
		t.Fatalf("RuntimeForListing refused an endpoint-only component: %v", err)
	}
	if runtime.Endpoint != "https://mcp.example.com/mcp" {
		t.Errorf("endpoint = %q, want the published URL", runtime.Endpoint)
	}
	if runtime.Type != "streamable-http" {
		t.Errorf("type = %q, want the published transport", runtime.Type)
	}
	if runtime.Command != "" {
		t.Errorf("command = %q: an endpoint row must not carry a command", runtime.Command)
	}
}

// TestRuntimeForListingFailsClosedWithoutALaunchLine keeps the rejection
// honest for a component that declares a runtime but neither a command nor an
// endpoint: the error must say what is missing, not blame a command.
func TestRuntimeForListingFailsClosedWithoutALaunchLine(t *testing.T) {
	listings := sampleListings()
	records := []*domain.VersionRecord{{
		ListingID: listings[0].ID,
		Version:   "1.0.0",
		Components: []domain.Component{{
			ID:      listings[0].ID + "@1.0.0#mcp-provider/server",
			Kind:    domain.ComponentMCPProvider,
			Name:    "server",
			Runtime: &domain.RuntimeDescriptor{Type: "stdio"},
		}},
	}}
	client := syncClientWithRecords(t, "rel-endpoint-02", 10, records)

	_, err := client.RuntimeForListing(listings[0].ID, "")
	if err == nil {
		t.Fatal("RuntimeForListing accepted a component with neither a command nor an endpoint")
	}
	if !strings.Contains(err.Error(), "publishes no launch line (neither a command nor an endpoint)") {
		t.Errorf("error %q does not name both missing launch-line forms", err.Error())
	}
}
