package discover

// Remote (URL) provider tests: the CONNECT half of B1 (Phase 4, step 4.1).
//
// Every fixture here is a local httptest server — nothing leaves the machine.
// Two facts are asserted repeatedly, because they are the two promises the
// remote path makes:
//
//  1. The wire shape is exactly what internal/mcpclient speaks, so the
//     connector selection is observable: the modern (2026-07-28) client is
//     stateless and never sends `initialize`, the legacy (2025-11-25) client
//     handshakes eagerly. A fixture that counts initialize therefore proves
//     WHICH connector ran, not merely that some request succeeded.
//  2. A failure writes ZERO rows. Not a provider row, not a capability row —
//     an entry nothing ever connected to has no invented health to render
//     (Decision 8).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

// remoteFixture is one MCP server double speaking the JSON-RPC-over-HTTP
// shapes both mcpclient connectors expect:
//
//   - modern Streamable HTTP: POSTs with Content-Type application/json and
//     MCP-Protocol-Version, answered with plain JSON (no session id, no
//     handshake);
//   - legacy stateful HTTP: POST `initialize` (which may receive
//     Mcp-Session-Id back), `notifications/initialized` answered 202, then
//     POST `tools/list` / `tools/call` with the session header, and DELETE on
//     CloseSession.
//
// The wire shape was read from internal/mcpclient/client_2026.go (sendRequest:
// header mirroring, `_meta.protocolVersion` = MCP-Protocol-Version, plain-JSON
// or SSE response body) and client_legacy.go (performHandshake/postRPCRaw),
// and mirrors the fixtures in internal/mcpclient/mcpclient_test.go
// (TestModern2026_StreamableHTTP, TestLegacy2025_Handshake).
type remoteFixture struct {
	server      *httptest.Server
	initializes atomic.Int32
}

func (f *remoteFixture) endpoint() string { return f.server.URL + "/mcp" }

func (f *remoteFixture) initializeCount() int32 { return f.initializes.Load() }

// newRemoteFixture starts the shared handler and closes it with the test.
func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	fx := &remoteFixture{}
	fx.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// CloseSession releases a legacy server-side session.
			w.WriteHeader(http.StatusOK)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Method == "initialize" {
			fx.initializes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			// A legacy client adopts the version the server answers and may
			// be assigned a session; the modern client never asks.
			w.Header().Set("Mcp-Session-Id", "sess-fixture-1")
			writeFixtureResult(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeFixtureResult(w, req.ID, `{"tools":[
				{"name":"echo","description":"Echo a message",
				 "inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}
			]}`)
		case "tools/call":
			writeFixtureResult(w, req.ID, `{"content":[{"type":"text","text":"remote-ok"}]}`)
		default:
			if req.ID == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			})
		}
	}))
	t.Cleanup(fx.server.Close)
	return fx
}

func writeFixtureResult(w http.ResponseWriter, id json.RawMessage, result string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  json.RawMessage(result),
	})
}

// seedRemoteInstall writes the install + install_components rows Discover needs
// (providers.component_id is a foreign key onto install_components).
func seedRemoteInstall(t *testing.T, db *state.DB, installID, component string) {
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
		InstallID: installID, Kind: domain.ComponentMCPProvider, ComponentName: component, Path: "",
	}); err != nil {
		t.Fatalf("SaveInstallComponent: %v", err)
	}
}

// requireZeroRows is the Decision 8 assertion: a probe that failed wrote no
// provider row and no capability rows, so nothing downstream can render a
// fabricated health state for it.
func requireZeroRows(t *testing.T, db *state.DB) {
	t.Helper()
	ctx := context.Background()
	providers, err := db.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("a failed probe wrote %d provider row(s); want none: %+v", len(providers), providers)
	}
	caps, err := db.ListCapabilities(ctx, "")
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("a failed probe wrote %d capability row(s); want none", len(caps))
	}
}

// requireTypedCode asserts err is (or wraps) an *domain.LPSMError with code.
func requireTypedCode(t *testing.T, err error, code string) *domain.LPSMError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a typed %s error, got nil", code)
	}
	var lerr *domain.LPSMError
	if !errors.As(err, &lerr) {
		t.Fatalf("expected *domain.LPSMError with %s, got %T: %v", code, err, err)
	}
	if lerr.Code != code {
		t.Fatalf("expected code %s, got %s: %v", code, lerr.Code, err)
	}
	return lerr
}

// TestDiscoverAndInvokeOverStreamableHTTP is the happy path over the modern
// stateless profile: host-side spec → discovery → rows → invoke, all against a
// loopback httptest endpoint (AllowLoopbackHTTP is the policy's deliberate
// exception; no raw http.Client is built here).
func TestDiscoverAndInvokeOverStreamableHTTP(t *testing.T) {
	fx := newRemoteFixture(t)
	db := openState(t)
	ctx := context.Background()
	const installID = "inst_user_mcp_demo_remote_1_0_0"
	seedRemoteInstall(t, db, installID, "remote-demo")

	found, err := Discover(ctx, db, ProviderSpec{
		InstallID: installID, ComponentName: "remote-demo",
		Endpoint: fx.endpoint(), Transport: "streamable-http",
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found.Capabilities) != 1 || found.Capabilities[0].Name != "echo" {
		t.Fatalf("unexpected capabilities: %+v", found.Capabilities)
	}
	if got := fx.initializeCount(); got != 0 {
		t.Fatalf("the modern connector must be stateless (no initialize), saw %d", got)
	}

	// The provider row must carry the endpoint back, in the providers.mode
	// vocabulary the CHECK constraint stores, so Invoke can rebuild the spec
	// without re-reading the host config.
	provider, err := db.GetProvider(ctx, found.ProviderID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if provider.Endpoint != fx.endpoint() {
		t.Errorf("endpoint did not round-trip: %q, want %q", provider.Endpoint, fx.endpoint())
	}
	if provider.Transport != "remote-http" {
		t.Errorf("provider transport = %q, want the mode value \"remote-http\"", provider.Transport)
	}
	if provider.Command != "" {
		t.Errorf("a remote provider must record no command, got %q", provider.Command)
	}

	result, err := Invoke(ctx, db, found.Capabilities[0].CapabilityID,
		json.RawMessage(`{"message":"hi"}`), readOnlyOpt())
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Output, "remote-ok") {
		t.Errorf("unexpected invoke result: %+v", result)
	}
}

// TestDiscoverAndInvokeOverLegacySSE covers the sse transport: ConnectLegacy
// performs the eager initialize handshake (the fixture counts it), keeps the
// assigned session, and Invoke reuses that session rather than re-handshaking.
func TestDiscoverAndInvokeOverLegacySSE(t *testing.T) {
	fx := newRemoteFixture(t)
	db := openState(t)
	ctx := context.Background()
	const installID = "inst_user_mcp_demo_sse_1_0_0"
	seedRemoteInstall(t, db, installID, "sse-demo")

	found, err := Discover(ctx, db, ProviderSpec{
		InstallID: installID, ComponentName: "sse-demo",
		Endpoint: fx.endpoint(), Transport: "sse",
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found.Capabilities) != 1 || found.Capabilities[0].Name != "echo" {
		t.Fatalf("unexpected capabilities: %+v", found.Capabilities)
	}
	if got := fx.initializeCount(); got != 1 {
		t.Fatalf("the legacy connector must handshake exactly once, saw %d", got)
	}

	provider, err := db.GetProvider(ctx, found.ProviderID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	// Read-back is the providers.mode value, not the spec's "sse": Invoke
	// must dispatch on THAT spelling.
	if provider.Transport != "legacy-sse" {
		t.Errorf("provider transport = %q, want the mode value \"legacy-sse\"", provider.Transport)
	}
	if provider.Endpoint != fx.endpoint() {
		t.Errorf("endpoint did not round-trip: %q", provider.Endpoint)
	}

	result, err := Invoke(ctx, db, found.Capabilities[0].CapabilityID,
		json.RawMessage(`{"message":"hi"}`), readOnlyOpt())
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Output, "remote-ok") {
		t.Errorf("unexpected invoke result: %+v", result)
	}
	// Invoke opens its own session (a stdio Invoke spawns its own process), so
	// exactly one handshake per dial — never a re-handshake inside one dial.
	if got := fx.initializeCount(); got != 2 {
		t.Errorf("expected one handshake per dial (Discover + Invoke = 2), saw %d", got)
	}
}

// TestRemoteTransportSelectsTheConnector pins the transport → connector
// dispatch through the public API. Each case dials a fixture and asserts the
// initialize counter: a wrong connector either handshakes a stateless
// endpoint (modern fixture would count initialize) or silently skips the
// handshake a legacy endpoint expects (legacy fixture would count none).
func TestRemoteTransportSelectsTheConnector(t *testing.T) {
	modern := newRemoteFixture(t)
	legacy := newRemoteFixture(t)

	for i, tc := range []struct {
		name       string
		transport  string
		fixture    *remoteFixture
		wantLegacy bool
		wantMode   string
	}{
		{"empty defaults to streamable-http", "", modern, false, "remote-http"},
		{"registry streamable-http", "streamable-http", modern, false, "remote-http"},
		{"providers.mode remote-http", "remote-http", modern, false, "remote-http"},
		{"host discriminator http", "http", modern, false, "remote-http"},
		{"host discriminator streamableHttp", "streamableHttp", modern, false, "remote-http"},
		{"host discriminator remote", "remote", modern, false, "remote-http"},
		{"registry sse", "sse", legacy, true, "legacy-sse"},
		{"providers.mode legacy-sse", "legacy-sse", legacy, true, "legacy-sse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := tc.fixture
			beforeModern, beforeLegacy := modern.initializeCount(), legacy.initializeCount()
			db := openState(t)
			installID := fmt.Sprintf("inst_user_mcp_dispatch_%d", i)
			component := fmt.Sprintf("dispatch-%d", i)
			seedRemoteInstall(t, db, installID, component)

			found, err := Discover(context.Background(), db, ProviderSpec{
				InstallID: installID, ComponentName: component,
				Endpoint: fx.endpoint(), Transport: tc.transport,
			})
			if err != nil {
				t.Fatalf("Discover over %q: %v", tc.transport, err)
			}

			gotModern, gotLegacy := modern.initializeCount()-beforeModern, legacy.initializeCount()-beforeLegacy
			if tc.wantLegacy {
				if gotLegacy != 1 || gotModern != 0 {
					t.Fatalf("transport %q selected the wrong connector: legacy %d, modern %d initializations",
						tc.transport, gotLegacy, gotModern)
				}
			} else if gotModern != 0 || gotLegacy != 0 {
				t.Fatalf("transport %q must be stateless: legacy %d, modern %d initializations",
					tc.transport, gotLegacy, gotModern)
			}

			// Whatever spelling arrived, the row stores LiteSPM's own
			// vocabulary: the providers.mode CHECK accepts only it, and
			// Invoke dispatches on the read-back value.
			provider, perr := db.GetProvider(context.Background(), found.ProviderID)
			if perr != nil {
				t.Fatalf("GetProvider: %v", perr)
			}
			if provider.Transport != tc.wantMode {
				t.Errorf("stored mode = %q, want %q (transport %q)", provider.Transport, tc.wantMode, tc.transport)
			}
			if provider.Endpoint != fx.endpoint() {
				t.Errorf("stored endpoint = %q, want %q", provider.Endpoint, fx.endpoint())
			}
		})
	}

	// An unknown transport on a remote spec is a refusal, never a guess.
	db := openState(t)
	seedRemoteInstall(t, db, "inst_user_mcp_dispatch_ws", "dispatch-ws")
	_, err := Discover(context.Background(), db, ProviderSpec{
		InstallID: "inst_user_mcp_dispatch_ws", ComponentName: "dispatch-ws",
		Endpoint: modern.endpoint(), Transport: "ws",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Fatalf("an unknown remote transport must be refused, got %v", err)
	}
	requireZeroRows(t, db)
}

// TestMetadataServiceEndpointRefusedBeforeAnyConnection is the SSRF test: the
// cloud metadata address must be refused by the URL check, before a socket is
// ever opened. The evidence is the refusal's own reason text — a connection
// attempt would fail with a dial/TLS error instead of the guard's static
// verdict — plus the typed code and the absence of any row.
func TestMetadataServiceEndpointRefusedBeforeAnyConnection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoint  string
		transport string
		wantText  string
	}{
		{"http metadata, streamable-http", "http://169.254.169.254/mcp", "streamable-http", "require https"},
		{"https metadata, streamable-http", "https://169.254.169.254/mcp", "streamable-http", "never permitted"},
		{"http metadata, sse", "http://169.254.169.254/mcp", "sse", "require https"},
		{"https metadata, sse", "https://169.254.169.254/mcp", "sse", "never permitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openState(t)
			ctx := context.Background()
			installID := "inst_user_mcp_metadata_guard"
			seedRemoteInstall(t, db, installID, "metadata-guard")

			_, err := Discover(ctx, db, ProviderSpec{
				InstallID: installID, ComponentName: "metadata-guard",
				Endpoint: tc.endpoint, Transport: tc.transport,
			})
			lerr := requireTypedCode(t, err, "LPSM-EGRESS-BLOCKED")
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("refusal reason %q does not show the pre-dial URL check (%q)", err, tc.wantText)
			}
			if got, _ := lerr.Details["endpoint"].(string); got != tc.endpoint {
				t.Errorf("typed error does not name the endpoint: %+v", lerr.Details)
			}
			requireZeroRows(t, db)
		})
	}
}

// TestRemoteUnauthorizedIsATypedAuthErrorWithZeroRows is Decision 8's most
// common real outcome: a protected endpoint answers 401 to an unauthenticated
// probe. That is correct configuration with missing credentials — typed as an
// auth error (not a broken URL), and with zero rows, because nothing was
// discovered.
func TestRemoteUnauthorizedIsATypedAuthErrorWithZeroRows(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(auth.Close)

	for _, transport := range []string{"streamable-http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			db := openState(t)
			ctx := context.Background()
			installID := "inst_user_mcp_auth_guard"
			seedRemoteInstall(t, db, installID, "auth-guard")

			_, err := Discover(ctx, db, ProviderSpec{
				InstallID: installID, ComponentName: "auth-guard",
				Endpoint: auth.URL + "/mcp", Transport: transport,
			})
			lerr := requireTypedCode(t, err, CodeRemoteAuth)
			if !strings.Contains(err.Error(), auth.URL) || !strings.Contains(err.Error(), "401") {
				t.Errorf("auth error must name the endpoint and the status: %v", err)
			}
			if status, _ := lerr.Details["status"].(int); status != http.StatusUnauthorized {
				t.Errorf("details.status = %v, want 401", lerr.Details["status"])
			}
			requireZeroRows(t, db)
		})
	}
}

// TestRemoteUnreachableEndpointIsAConnectionError covers the other half of the
// classification: an endpoint nothing is listening on is LPSM-PROVIDER-REMOTE-CONNECT.
func TestRemoteUnreachableEndpointIsAConnectionError(t *testing.T) {
	// Bind and immediately release a port so the URL is syntactically valid
	// and the address is loopback-allowed, but nothing answers.
	leaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := leaky.URL
	leaky.Close()

	db := openState(t)
	seedRemoteInstall(t, db, "inst_user_mcp_dead", "dead-endpoint")
	_, err := Discover(context.Background(), db, ProviderSpec{
		InstallID: "inst_user_mcp_dead", ComponentName: "dead-endpoint",
		Endpoint: dead + "/mcp", Transport: "streamable-http",
	})
	requireTypedCode(t, err, CodeRemoteConnect)
	if !strings.Contains(err.Error(), dead) {
		t.Errorf("connection error must name the endpoint: %v", err)
	}
	requireZeroRows(t, db)
}

// TestDiscoverRefusesSpecWithNeitherTargetAndWithBoth: the one-transport gate.
// Neither target means there is nothing to dial or spawn; both means the host
// would pick which one it honours, by its own rule rather than ours.
func TestDiscoverRefusesSpecWithNeitherTargetAndWithBoth(t *testing.T) {
	db := openState(t)
	seedRemoteInstall(t, db, "inst_user_mcp_guard_shapes", "guard-shapes")

	_, err := Discover(context.Background(), db, ProviderSpec{
		InstallID: "inst_user_mcp_guard_shapes", ComponentName: "guard-shapes",
		Transport: "stdio",
	})
	if err == nil || !strings.Contains(err.Error(), "no command to run and no endpoint") {
		t.Errorf("a spec with neither target must be refused, got %v", err)
	}

	_, err = Discover(context.Background(), db, ProviderSpec{
		InstallID: "inst_user_mcp_guard_shapes", ComponentName: "guard-shapes",
		Command: "/bin/true", Endpoint: "https://example.com/mcp", Transport: "streamable-http",
	})
	if err == nil || !strings.Contains(err.Error(), "an entry is one transport") {
		t.Errorf("a spec with both targets must be refused, got %v", err)
	}
	requireZeroRows(t, db)
}

// TestInvokeRefusesARemoteProviderWithNoEndpointRecorded: a providers row that
// claims a remote mode but carries no endpoint in launch_spec_json is corrupt
// state; Invoke must say which fact is missing instead of demanding a command
// a remote row will never have.
func TestInvokeRefusesARemoteProviderWithNoEndpointRecorded(t *testing.T) {
	db := openState(t)
	ctx := context.Background()
	const installID = "inst_user_mcp_dangling_1_0_0"
	seedRemoteInstall(t, db, installID, "dangling")

	now := time.Now().UTC()
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID: ProviderIDFor(installID, "dangling"), InstallID: installID,
		ComponentName: "dangling", Transport: "remote-http", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	capabilityID := string(domain.NewCapabilityID(domain.InstallID(installID), "dangling", "echo"))
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{
		CapabilityID: capabilityID, ProviderID: ProviderIDFor(installID, "dangling"),
		Name: "echo", InputSchemaJSON: `{"type":"object"}`, SchemaFingerprint: "sha256:x",
		DiscoveredAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCapability: %v", err)
	}

	_, err := Invoke(ctx, db, capabilityID, nil, readOnlyOpt())
	if err == nil || !strings.Contains(err.Error(), "no endpoint recorded") {
		t.Errorf("a remote row with no endpoint must be refused by name, got %v", err)
	}
}
