package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/discover"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/state"
)

func TestParseCapabilityFlags(t *testing.T) {
	hosts, limit, capID, query, limitSet, err := parseCapabilityFlags([]string{"--host", "codex", "--host", "cursor", "--limit", "5", "--capability", "x/y/z"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "codex" || hosts[1] != "cursor" {
		t.Errorf("hosts = %v", hosts)
	}
	if limit != 5 {
		t.Errorf("limit = %d", limit)
	}
	if !limitSet {
		t.Error("explicit --limit was not recorded")
	}
	if capID != "x/y/z" {
		t.Errorf("capability = %q", capID)
	}
	if query != "" {
		t.Errorf("query = %q, want empty", query)
	}

	// Flags are parsed on either side of the search term.
	hosts, limit, capID, query, limitSet, err = parseCapabilityFlags([]string{"database", "--limit", "5", "--host", "codex"})
	if err != nil || len(hosts) != 1 || limit != 5 || capID != "" || query != "database" || !limitSet {
		t.Errorf("query/flags parsed incorrectly (hosts=%v limit=%d capability=%q query=%q limitSet=%t err=%v)", hosts, limit, capID, query, limitSet, err)
	}

	for _, bad := range [][]string{
		{"--host"}, {"--limit"}, {"--capability"}, {"--limit", "0"}, {"--limit", "abc"},
	} {
		if _, _, _, _, _, err := parseCapabilityFlags(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestValidateCapabilityCommandHostScope(t *testing.T) {
	if err := validateCapabilityCommand("refresh", []string{"codex"}, "", "", false); err != nil {
		t.Fatalf("refresh should accept --host: %v", err)
	}
	for _, command := range []string{"list", "search", "describe"} {
		if err := validateCapabilityCommand(command, []string{"codex"}, "capability", "query", false); err == nil {
			t.Errorf("%s accepted unsupported --host targeting", command)
		}
	}
	if err := validateCapabilityCommand("refresh", nil, "", "", true); err == nil {
		t.Error("refresh accepted and ignored --limit")
	}
	if err := validateCapabilityCommand("describe", nil, "capability", "", true); err == nil {
		t.Error("describe accepted and ignored --limit")
	}
}

// TestServerEntryNameForMatchesTheInstalledName is the contract that makes
// `capabilities refresh` able to find what `install` wrote: the refresh path
// recomputes the entry name from the listing and matches on it, so the two must
// agree exactly.
func TestServerEntryNameForMatchesTheInstalledName(t *testing.T) {
	listing := mcpListing("mcp:upstash:context7", "Context7")
	name, err := serverEntryNameFor(listing)
	if err != nil {
		t.Fatal(err)
	}
	if name != "context7" {
		t.Fatalf("entry name %q, want context7", name)
	}
	// A name that needs normalizing must still be host-legal, because the entry
	// is written into a config whose schema restricts these keys.
	odd, err := serverEntryNameFor(mcpListing("mcp:x:y", "Weird Name (v2)!"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHostEntryName(odd); err != nil {
		t.Errorf("normalized name %q is not host-legal: %v", odd, err)
	}
	if strings.Contains(odd, " ") {
		t.Errorf("normalized name still has a space: %q", odd)
	}
}

// validateHostEntryName mirrors the restriction hosts document, without pulling
// the host package into this test's import graph.
func validateHostEntryName(name string) error {
	if name == "" {
		return errString("empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return errString("illegal character")
		}
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestCapabilitiesUsageMentionsEveryCommand(t *testing.T) {
	for _, want := range []string{"list", "search", "describe", "refresh", "--capability", "--host"} {
		if !strings.Contains(capabilitiesUsage, want) {
			t.Errorf("usage does not document %q", want)
		}
	}
	if strings.Contains(capabilitiesUsage, "Only this agent host") {
		t.Error("usage must not imply --host filters list/search")
	}
}

// TestMCPInstallWritesTheComponentRowFksRequire pins why the install writes an
// install_components row: providers.component_id is a foreign key onto it
// (ARCH/12 §13), so a discovery pass after an install fails with a constraint
// error if the row is missing.
func TestMCPInstallWritesTheComponentRowFksRequire(t *testing.T) {
	db := openTestState(t)
	homeWithBridge(t)

	outcome, err := installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
		mcpListing("mcp:example:needs-component", "needs-component"), "1.0.0", domain.ScopeUser, []string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// The component id is deliberately distinct from the install id (several
	// components can share one install), so the row is keyed by its derived id.
	componentID := state.ComponentIDFor(outcome.InstallID, domain.ComponentMCPProvider, "needs-component")
	row, err := db.GetInstallComponent(t.Context(), componentID)
	if err != nil {
		t.Fatalf("install component row missing: %v", err)
	}
	if row.ComponentName != "needs-component" || row.Kind != domain.ComponentMCPProvider {
		t.Errorf("unexpected component row: %+v", row)
	}
}

// --- Remote (URL) MCP: refresh + invoke (B1 Phase 4, step 4.3) ---

// remoteMCPFixture starts a hermetic streamable-HTTP MCP server and returns
// its endpoint. The wire shape is the modern stateless profile
// internal/mcpclient/client_2026.go speaks: every request is a JSON-RPC POST
// (Content-Type application/json, MCP-Protocol-Version mirroring
// _meta.protocolVersion) and the answer is plain JSON — no initialize
// handshake, no session id. See internal/discover/discover_remote_test.go for
// the fuller double that also covers the legacy handshake.
func remoteMCPFixture(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reply := func(result string) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": json.RawMessage(result),
			})
		}
		switch req.Method {
		case "tools/list":
			reply(`{"tools":[{"name":"echo","description":"Echo a message",
				"inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}]}`)
		case "tools/call":
			reply(`{"content":[{"type":"text","text":"remote-ok"}]}`)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(req.ID),
				"error": map[string]any{"code": -32601, "message": "Method not found"},
			})
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/mcp"
}

// capabilityListing is mcpListing with the fields the release compiler's known
// fixture carries (source URL, publisher, component summary).
func capabilityListing(id, name string) *domain.Listing {
	l := mcpListing(id, name)
	l.Source.URL = "https://example.com/" + name
	l.PublisherClaim = domain.PublisherClaim{Name: "Example Publisher"}
	l.ComponentsSummary = []domain.ComponentSummary{{Kind: domain.ComponentMCPProvider, Name: name}}
	return l
}

// seedCapabilityCatalog compiles a release into a temp cache dir and returns a
// client whose index was loaded from that cache — the offline path a synced
// machine takes (mirrors seedLockCatalog in lock_test.go). No network.
func seedCapabilityCatalog(t *testing.T, listings []*domain.Listing) *catalog.Client {
	t.Helper()
	versions := make([]*domain.VersionRecord, 0, len(listings))
	for _, l := range listings {
		versions = append(versions, &domain.VersionRecord{
			ListingID: l.ID,
			Version:   "1.0.0",
			Components: []domain.Component{{
				ID:      string(domain.NewComponentID(domain.ListingID(l.ID), "1.0.0", domain.ComponentMCPProvider, l.Name)),
				Kind:    domain.ComponentMCPProvider,
				Name:    l.Name,
				Runtime: &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", l.Name}},
			}},
		})
	}
	out, err := catalogbuild.CompileRelease("rel-capability-001", 1, nil, listings, versions, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompileRelease: %v", err)
	}
	cacheDir := t.TempDir()
	if err := out.WriteToDirectory(cacheDir); err != nil {
		t.Fatalf("WriteToDirectory: %v", err)
	}
	client := catalog.NewClient("https://registry.invalid", cacheDir, nil)
	if client.Count() != len(listings) {
		t.Fatalf("seeded catalog has %d listings, want %d", client.Count(), len(listings))
	}
	return client
}

// seedCapabilityInstall writes the install + install_components rows discovery
// needs (providers.component_id is a foreign key onto install_components).
func seedCapabilityInstall(t *testing.T, db *state.DB, installID, listingID, component string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID: installID, ListingID: listingID, Version: "1.0.0",
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

// TestCapabilitiesRefreshOverAMixedRemoteAndStdioSet is the step-4.3 proof,
// host-layer-first: a remote entry is written through host.InstallServerEntry
// (claude-code's own `type:"http"` spelling), a stdio entry beside it, then
// the refresh path builds specs from both, probes both, and names what it
// actually dials on each "Probing" line — the endpoint for the remote server,
// the launch line for the stdio one. The stdio server deliberately does not
// exist, which also proves per-server isolation: its failure stays on its own
// line while the remote probe still records its capabilities.
func TestCapabilitiesRefreshOverAMixedRemoteAndStdioSet(t *testing.T) {
	db := openTestState(t)
	_, configPath := homeWithBridge(t)
	ctx := context.Background()
	endpoint := remoteMCPFixture(t)

	// 1. Host layer first: the remote entry is written in the exact shape
	//    this host documents (url + type discriminator), never as a command.
	if _, err := host.InstallServerEntry(ctx, "claude-code", host.ServerEntry{
		Name: "remote-demo", Endpoint: endpoint, Transport: host.TransportStreamableHTTP,
	}, host.EntryInstallOptions{Scope: domain.ScopeUser, BackupDir: t.TempDir()}); err != nil {
		t.Fatalf("write the remote entry: %v", err)
	}
	if _, err := host.InstallServerEntry(ctx, "claude-code", host.ServerEntry{
		Name: "stdio-demo", Command: "/nonexistent/litespm-stdio-fixture", Args: []string{"--stdio"},
	}, host.EntryInstallOptions{Scope: domain.ScopeUser, BackupDir: t.TempDir()}); err != nil {
		t.Fatalf("write the stdio entry: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// The entry must be url + type discriminator — claude-code's own remote
	// shape — never a launch line.
	hasEndpoint := strings.Contains(string(data), quotedJSON(endpoint))
	hasDiscriminator := strings.Contains(string(data), `"type":"http"`) ||
		strings.Contains(string(data), `"type": "http"`)
	if !hasEndpoint || !hasDiscriminator {
		t.Errorf("remote entry not written in claude-code's own shape (endpoint=%t type=%t):\n%s",
			hasEndpoint, hasDiscriminator, data)
	}
	entries, err := host.ListServerEntriesWithValues(ctx, "claude-code", domain.ScopeUser)
	if err != nil {
		t.Fatalf("read back the entries: %v", err)
	}
	var remoteEntry, stdioEntry *host.HostServerEntry
	for i := range entries {
		switch entries[i].Name {
		case "remote-demo":
			remoteEntry = &entries[i]
		case "stdio-demo":
			stdioEntry = &entries[i]
		}
	}
	if remoteEntry == nil || remoteEntry.Endpoint != endpoint || remoteEntry.Transport != "http" {
		t.Fatalf("remote entry read back wrong: %+v", remoteEntry)
	}
	if stdioEntry == nil || stdioEntry.Command != "/nonexistent/litespm-stdio-fixture" || stdioEntry.Endpoint != "" {
		t.Fatalf("stdio entry read back wrong: %+v", stdioEntry)
	}

	// 2. The installs the refresh path matches entries against.
	seedCapabilityInstall(t, db, "inst_user_mcp_example_remote_demo_1_0_0", "mcp:example:remote-demo", "remote-demo")
	seedCapabilityInstall(t, db, "inst_user_mcp_example_stdio_demo_1_0_0", "mcp:example:stdio-demo", "stdio-demo")
	client := seedCapabilityCatalog(t, []*domain.Listing{
		capabilityListing("mcp:example:remote-demo", "remote-demo"),
		capabilityListing("mcp:example:stdio-demo", "stdio-demo"),
	})

	specs, err := installedMCPServers(ctx, db, client, []string{"claude-code"})
	if err != nil {
		t.Fatalf("installedMCPServers: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d: %+v", len(specs), specs)
	}
	// Sorted by component name: remote-demo, stdio-demo.
	if specs[0].ComponentName != "remote-demo" || specs[0].Endpoint != endpoint ||
		specs[0].Transport != host.TransportStreamableHTTP || specs[0].Command != "" {
		t.Errorf("unexpected remote spec: %+v", specs[0])
	}
	if specs[1].ComponentName != "stdio-demo" || specs[1].Command != "/nonexistent/litespm-stdio-fixture" ||
		specs[1].Endpoint != "" {
		t.Errorf("unexpected stdio spec: %+v", specs[1])
	}

	// 3. Probe both: each line names what it dials, and one failure does not
	//    abandon the other.
	var out bytes.Buffer
	total, failed := probeAll(ctx, db, specs, &out)
	if failed != 1 || total != 1 {
		t.Errorf("probeAll total=%d failed=%d, want 1/1; output:\n%s", total, failed, out.String())
	}
	got := out.String()
	wantRemote := "Probing remote-demo (" + endpoint + " " + host.TransportStreamableHTTP + ")..."
	wantStdio := "Probing stdio-demo (/nonexistent/litespm-stdio-fixture --stdio)..."
	if !strings.Contains(got, wantRemote) {
		t.Errorf("remote probe line does not name the endpoint:\n%s", got)
	}
	if !strings.Contains(got, wantStdio) {
		t.Errorf("stdio probe line does not name the command:\n%s", got)
	}
	if strings.Contains(got, "Probing remote-demo ()") {
		t.Errorf("remote probe line printed an empty target:\n%s", got)
	}
	if !strings.Contains(got, "  ✓ 1 tools: echo") {
		t.Errorf("remote probe did not report its tool:\n%s", got)
	}

	// 4. The refresh left exactly the remote server's capability — a failed
	//    probe writes nothing (Decision 8).
	caps, err := db.ListCapabilities(ctx, "")
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(caps) != 1 || caps[0].Name != "echo" {
		t.Fatalf("unexpected capability rows: %+v", caps)
	}

	// 5. Invoke the discovered tool over the same guarded remote path.
	result, err := discover.Invoke(ctx, db, caps[0].CapabilityID, json.RawMessage(`{"message":"hi"}`),
		discover.WithEffects(policy.EffectDeclaration{
			Effect: policy.EffectFilesystemRead, Provenance: domain.ProvenanceUserClassified,
		}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Output, "remote-ok") {
		t.Errorf("unexpected invoke result: %+v", result)
	}
}

// TestProbeTargetNamesWhatIsDialed pins the exact target string, including
// the stdio spelling the pre-remote "Probing %s (%s %s)" line produced — the
// command, one space, then the joined args (so an arg-less command still
// ends in the same space it used to).
func TestProbeTargetNamesWhatIsDialed(t *testing.T) {
	if got := probeTarget(discover.ProviderSpec{Command: "/bin/x", Args: []string{"a", "b"}}); got != "/bin/x a b" {
		t.Errorf("stdio target = %q", got)
	}
	if got := probeTarget(discover.ProviderSpec{Command: "/bin/x"}); got != "/bin/x " {
		t.Errorf("arg-less stdio target = %q, want the historical %q", got, "/bin/x ")
	}
	if got := probeTarget(discover.ProviderSpec{Endpoint: "https://mcp.example.com/mcp", Transport: "streamable-http"}); got != "https://mcp.example.com/mcp streamable-http" {
		t.Errorf("remote target = %q", got)
	}
	if got := probeTarget(discover.ProviderSpec{Endpoint: "https://mcp.example.com/mcp"}); got != "https://mcp.example.com/mcp" {
		t.Errorf("transport-less remote target = %q", got)
	}
}

// quotedJSON renders s as it appears inside a JSON document.
func quotedJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
