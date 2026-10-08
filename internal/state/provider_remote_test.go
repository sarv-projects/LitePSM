package state

// Remote (URL) provider persistence (B1 Phase 4, step 4.2) — no migration:
// the endpoint rides in the existing providers.launch_spec_json blob as a new
// omitempty key, so a record written before the field existed still loads.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// seedProviderRows writes the install + install_components rows a providers row
// hangs off (component_id is a foreign key onto install_components).
func seedProviderRows(t *testing.T, db *DB, installID, component string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID: installID, ListingID: "mcp:demo:" + component, Version: "1.0.0",
		Scope: domain.ScopeUser, Status: domain.InstallActive, InstalledAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveInstall: %v", err)
	}
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID: installID, Kind: domain.ComponentMCPProvider, ComponentName: component,
	}); err != nil {
		t.Fatalf("SaveInstallComponent: %v", err)
	}
}

func openProviderDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestProviderEndpointRoundTrip is the contract internal/discover depends on:
// a remote provider saves with its endpoint, reads back with it (so Invoke can
// rebuild the spec), and lands in the providers.mode enum the schema's CHECK
// allows. The mode value asserted here is the real one the column stores.
func TestProviderEndpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openProviderDB(t)
	const installID = "inst_user_mcp_demo_remote_1_0_0"
	seedProviderRows(t, db, installID, "remote-demo")
	now := time.Now().UTC()

	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    "prov_remote_demo",
		InstallID:     installID,
		ComponentName: "remote-demo",
		Transport:     "streamable-http",
		Endpoint:      "https://mcp.example.com/mcp",
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// The raw column proves the enum and the blob, not just the reader.
	var mode, launchRaw string
	if err := db.raw.QueryRow(
		`SELECT mode, launch_spec_json FROM providers WHERE provider_id = ?;`, "prov_remote_demo",
	).Scan(&mode, &launchRaw); err != nil {
		t.Fatalf("read providers row: %v", err)
	}
	if mode != "remote-http" {
		t.Errorf("mode = %q, want %q", mode, "remote-http")
	}
	if !strings.Contains(launchRaw, `"endpoint":"https://mcp.example.com/mcp"`) {
		t.Errorf("launch_spec_json does not carry the endpoint: %s", launchRaw)
	}
	var blob struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(launchRaw), &blob); err != nil {
		t.Fatalf("launch spec is not JSON: %v", err)
	}

	providers, err := db.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Mode != "remote-http" {
		t.Fatalf("unexpected provider configs: %+v", providers)
	}

	rec, err := db.GetProvider(ctx, "prov_remote_demo")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if rec.Endpoint != "https://mcp.example.com/mcp" {
		t.Errorf("endpoint did not round-trip: %q", rec.Endpoint)
	}
	// GetProvider reconstructs Transport from the mode column, so a caller
	// dispatches on "remote-http" — the same value discover's connector map
	// accepts.
	if rec.Transport != "remote-http" {
		t.Errorf("read-back transport = %q, want %q", rec.Transport, "remote-http")
	}
	if rec.Command != "" {
		t.Errorf("a remote provider must record no command, got %q", rec.Command)
	}
}

// TestProviderModeRemoteVocabulary pins the transport → mode mapping the
// column's CHECK constraint allows, including the sse leg.
func TestProviderModeRemoteVocabulary(t *testing.T) {
	for _, tc := range []struct {
		transport string
		want      string
	}{
		{"streamable-http", "remote-http"},
		{"http", "remote-http"},
		{"https", "remote-http"},
		{"remote-http", "remote-http"},
		{"sse", "legacy-sse"},
		{"legacy-sse", "legacy-sse"},
		{"stdio", "local-stdio"},
		{"local-stdio", "local-stdio"},
		{"", "local-stdio"},
	} {
		got, err := ProviderMode(tc.transport)
		if err != nil {
			t.Errorf("ProviderMode(%q): %v", tc.transport, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ProviderMode(%q) = %q, want %q", tc.transport, got, tc.want)
		}
	}
	if _, err := ProviderMode("websockets"); err == nil {
		t.Error("an unknown transport must have no providers.mode value")
	}
}

// TestLegacyProviderRecordWithoutEndpointStillLoads is the compatibility proof
// for rows written before the endpoint key existed: the exact blob the old
// writer produced is stored directly, then read back unchanged.
func TestLegacyProviderRecordWithoutEndpointStillLoads(t *testing.T) {
	ctx := context.Background()
	db := openProviderDB(t)
	const installID = "inst_user_mcp_legacy_stdio_1_0_0"
	seedProviderRows(t, db, installID, "legacy-stdio")
	now := time.Now().UTC()

	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    "prov_legacy_stdio",
		InstallID:     installID,
		ComponentName: "legacy-stdio",
		Transport:     "stdio",
		Command:       "/bin/sh",
		ArgsJSON:      `["-c","true"]`,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// A current stdio save must not grow an endpoint key at all (omitempty):
	// the blob stays exactly the shape the previous writer produced.
	var fresh string
	if err := db.raw.QueryRow(
		`SELECT launch_spec_json FROM providers WHERE provider_id = ?;`, "prov_legacy_stdio",
	).Scan(&fresh); err != nil {
		t.Fatalf("read launch spec: %v", err)
	}
	if strings.Contains(fresh, "endpoint") {
		t.Errorf("a stdio launch spec must carry no endpoint key: %s", fresh)
	}

	// Simulate a row persisted by the pre-endpoint writer.
	const oldBlob = `{"command":"/bin/sh","args":["-c","true"],"env":{"A":"b"},"workingDir":"/work"}`
	if _, err := db.raw.ExecContext(ctx,
		`UPDATE providers SET launch_spec_json = ? WHERE provider_id = ?;`,
		oldBlob, "prov_legacy_stdio"); err != nil {
		t.Fatalf("rewrite launch spec: %v", err)
	}

	rec, err := db.GetProvider(ctx, "prov_legacy_stdio")
	if err != nil {
		t.Fatalf("a record without an endpoint must still load: %v", err)
	}
	if rec.Command != "/bin/sh" || rec.ArgsJSON != `["-c","true"]` ||
		rec.EnvJSON != `{"A":"b"}` || rec.WorkingDir != "/work" {
		t.Errorf("legacy launch line did not round-trip: %+v", rec)
	}
	if rec.Endpoint != "" {
		t.Errorf("a legacy record must read back with no endpoint, got %q", rec.Endpoint)
	}
	if rec.Transport != "local-stdio" {
		t.Errorf("read-back transport = %q, want %q", rec.Transport, "local-stdio")
	}
}
