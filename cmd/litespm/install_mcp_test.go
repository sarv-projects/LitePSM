package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/policy"
)

func mcpListing(id, name string) *domain.Listing {
	return &domain.Listing{
		ID:     id,
		Name:   name,
		Kind:   domain.KindMCP,
		Status: domain.ListingStatusActive,
		// Proven by a manifest in this fixture; discovery_only is refused.
		Installability: domain.InstallabilityMetadataVerified,
		Summary:        "an MCP server",
		Source:         domain.SourceReference{SourceID: "example", UpstreamID: name},
		Versions:       []domain.VersionSummary{{Version: "1.0.0"}},
	}
}

func stdioRuntime() *domain.RuntimeDescriptor {
	return &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", "demo-mcp"}}
}

// homeWithBridge writes a host config that already carries the LiteSPM bridge,
// which is the consent signal for a default-target install.
func homeWithBridge(t *testing.T) (home string, configPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	configPath = filepath.Join(home, ".claude.json")
	content := `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, configPath
}

func TestInstallMCPFromListingRegistersWithConfiguredHosts(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)

	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, []string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(outcome.Hosts) != 1 || outcome.Hosts[0].HostID != "claude-code" {
		t.Fatalf("expected the one configured host, got %+v", outcome.Hosts)
	}
	if outcome.Entry.Name != "demo-mcp" || outcome.Entry.Command != "npx" {
		t.Errorf("unexpected entry: %+v", outcome.Entry)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"demo-mcp": {`) || !strings.Contains(got, `"-y"`) {
		t.Errorf("entry not written:\n%s", got)
	}
	if !strings.Contains(got, `"mine"`) || !strings.Contains(got, `"litespm"`) {
		t.Errorf("existing entries were disturbed:\n%s", got)
	}

	// The install and the host registration must both be recorded, or the entry
	// in the config is an orphan nothing can ever find again.
	rec, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install record: %v", err)
	}
	if rec.ListingID != "mcp:example:demo-mcp" || rec.Status != domain.InstallActive {
		t.Errorf("unexpected install record: %+v", rec)
	}
}

func TestInstallMCPFromListingRefusesCollisions(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "mine"), "1.0.0", domain.ScopeUser, []string{"claude-code"}, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("install overwrote an entry the user already had")
	}
	if code := domain.ErrorCode(err); code != "LPSM-NAME-CONFLICT" {
		t.Errorf("expected LPSM-NAME-CONFLICT, got %q (%v)", code, err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(original) {
		t.Errorf("a refused install modified the config:\n%s", after)
	}
}

func TestInstallMCPFromListingRefusesWithoutAnyConfiguredHost(t *testing.T) {
	db := openTestState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	_, err := installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
	if err == nil {
		t.Fatal("install proceeded with no host configured")
	}
	if code := domain.ErrorCode(err); code != "LPSM-INSTALL-TARGET-UNAVAILABLE" {
		t.Errorf("expected LPSM-INSTALL-TARGET-UNAVAILABLE, got %q (%v)", code, err)
	}
}

func TestInstallMCPPolicyDenyPreventsHostConfigWrite(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := json.Marshal([]policy.DenyRule{{
		RuleID: "deny-demo-install", TargetRef: "mcp:example:demo-mcp", Effect: policy.EffectPackageInstall,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataRoot, policy.DenyRulesFile), rules, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, stdioRuntime(), nil)
	if err == nil || domain.ErrorCode(err) != "LPSM-POLICY-UNAUTHORIZED" {
		t.Fatalf("explicit package.install deny was not enforced: %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("denied install modified host config:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestInstallMCPFromListingRefusesWhenNoCommandIsPublished(t *testing.T) {
	db := openTestState(t)
	homeWithBridge(t)

	_, err := installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser, nil, false, nil, nil)
	if err == nil {
		t.Fatal("install proceeded with no runtime descriptor")
	}
	if code := domain.ErrorCode(err); code != "LPSM-ARTIFACT-UNAVAILABLE" {
		t.Errorf("expected LPSM-ARTIFACT-UNAVAILABLE, got %q (%v)", code, err)
	}
}

func TestServerEntryNameForNormalizesHostUnsafeNames(t *testing.T) {
	cases := map[string]string{
		"brave-search-mcp-server": "brave-search-mcp-server",
		"Brave Search":            "brave-search",
		"context7":                "context7",
		"My_Server.2":             "my_server-2",
		"--weird--":               "weird",
	}
	for in, want := range cases {
		got, err := serverEntryNameFor(mcpListing("mcp:x:y", in))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// A name that would collide with the bridge must be refused, not installed.
	if _, err := serverEntryNameFor(mcpListing("mcp:x:y", "litespm")); err == nil {
		t.Error("a listing named litespm was accepted")
	}
}

// TestInstallMCPFromListingRefusesDiscoveryOnly pins Phase 0.1/T3: heuristic
// rows (and unset installability) are searchable metadata, never installable.
func TestInstallMCPFromListingRefusesDiscoveryOnly(t *testing.T) {
	for _, class := range []domain.Installability{"", domain.InstallabilityDiscoveryOnly} {
		listing := mcpListing("mcp:example:demo", "demo")
		listing.Installability = class
		_, err := installMCPFromListing(context.Background(), nil, t.TempDir(), listing, "1.0.0", domain.ScopeUser, nil, false, stdioRuntime(), nil)
		if domain.ErrorCode(err) != "LPSM-NOT-INSTALLABLE" {
			t.Fatalf("class %q: err = %v, want LPSM-NOT-INSTALLABLE", class, err)
		}
	}
}

// --- B1 Phase 3: remote (URL) MCP execution --------------------------------

// claudeCodeGoldenEntry is EXACTLY the entry internal/host/remote_entry_test.go
// pins for a claude-code streamable-http registration (same endpoint, same
// discriminator, byte for byte). A remote entry is ONLY url + type: any
// command, args or env key in it means the writer leaked a stdio field.
const claudeCodeGoldenEntry = `"demo-mcp": {"type":"http","url":"` + remoteEndpoint + `"}`

// The execute half: an endpoint runtime registers a URL-only entry in the
// host's own spelling, never a launch line, and records the ledger rows a
// later remove/restore needs.
func TestInstallMCPFromListingRegistersRemoteEndpoint(t *testing.T) {
	db := openTestState(t)
	dataRoot := t.TempDir()
	_, configPath := homeWithBridge(t)

	outcome, err := installMCPFromListing(authorizedTestContext(), db, dataRoot,
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, remoteRuntime(), nil)
	if err != nil {
		t.Fatalf("remote install: %v", err)
	}
	if outcome.Entry.Endpoint != remoteEndpoint || outcome.Entry.Command != "" {
		t.Errorf("unexpected entry: %+v", outcome.Entry)
	}
	if outcome.Entry.Transport != host.TransportStreamableHTTP {
		t.Errorf("entry transport = %q, want %q", outcome.Entry.Transport, host.TransportStreamableHTTP)
	}
	if entryTransportLabel(outcome.Entry) != host.TransportStreamableHTTP {
		t.Errorf("entryTransportLabel = %q", entryTransportLabel(outcome.Entry))
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, claudeCodeGoldenEntry) {
		t.Errorf("config does not contain the golden entry %s:\n%s", claudeCodeGoldenEntry, got)
	}
	// Parsed: exactly {type, url} — no stdio field may ride along.
	doc := map[string]any{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("written config is not JSON: %v\n%s", err, got)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entryVal, ok := servers["demo-mcp"].(map[string]any)
	if !ok {
		t.Fatalf("installed entry missing:\n%s", got)
	}
	if want := map[string]any{"type": "http", "url": remoteEndpoint}; !reflect.DeepEqual(entryVal, want) {
		t.Errorf("written entry = %#v, want exactly %#v", entryVal, want)
	}
	for _, sibling := range []string{"litespm", "mine"} {
		if _, ok := servers[sibling]; !ok {
			t.Errorf("sibling entry %q was lost:\n%s", sibling, got)
		}
	}

	// The receipt rows a later remove/restore reads back.
	rec, err := db.GetInstall(context.Background(), outcome.InstallID)
	if err != nil {
		t.Fatalf("install record: %v", err)
	}
	if rec.Status != domain.InstallActive || rec.Kind != domain.KindMCP {
		t.Errorf("unexpected install record: %+v", rec)
	}
	if want := mcpEntryTreeDigest(remoteEndpoint, "", nil); rec.TreeDigest != want {
		t.Errorf("TreeDigest = %q, want the endpoint-bound digest %q", rec.TreeDigest, want)
	}
	regs, err := db.ListHostRegistrationsForEntry(context.Background(), domain.ScopeUser, "demo-mcp")
	if err != nil {
		t.Fatalf("host registrations: %v", err)
	}
	if len(regs) != 1 || regs[0].HostID != "claude-code" {
		t.Errorf("host registrations = %+v, want exactly claude-code", regs)
	}
	if muts := mutationsOf(t, db, outcome.InstallID); len(muts) != 1 {
		t.Errorf("deployment ledger rows = %d, want 1", len(muts))
	}
}

// Execute is the third and last run of the static egress guard (plan, sealed
// replay, execute): an unsafe endpoint never reaches the config writer.
func TestInstallMCPFromListingRefusesUnsafeRemoteEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://public.example/mcp",
		"https://169.254.169.254/latest/meta-data",
		"https://user:pass@mcp.example.com/mcp",
	} {
		db := openTestState(t)
		_, configPath := homeWithBridge(t)
		before, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
			mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
			[]string{"claude-code"}, false,
			&domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: endpoint}, nil)
		if domain.ErrorCode(err) != "LPSM-EGRESS-BLOCKED" {
			t.Errorf("%q: err = %v, want LPSM-EGRESS-BLOCKED", endpoint, err)
		}
		after, rerr := os.ReadFile(configPath)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(before) != string(after) {
			t.Errorf("%q: a refused install modified the config:\nbefore: %s\nafter: %s", endpoint, before, after)
		}
		if recs, _ := db.ListInstalls(context.Background(), domain.ScopeUser, ""); len(recs) != 0 {
			t.Errorf("%q: a refused install recorded %d installs", endpoint, len(recs))
		}
	}
}

// One entry, one transport: a descriptor carrying both or neither is refused
// before anything is written.
func TestInstallMCPFromListingRefusesInvalidTransportShapes(t *testing.T) {
	cases := []struct {
		name    string
		runtime *domain.RuntimeDescriptor
	}{
		{"both command and endpoint", &domain.RuntimeDescriptor{
			Type: "stdio", Command: "npx", Args: []string{"-y", "demo"}, Endpoint: remoteEndpoint,
		}},
		{"neither command nor endpoint", &domain.RuntimeDescriptor{Type: "stdio"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestState(t)
			_, configPath := homeWithBridge(t)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
				mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
				[]string{"claude-code"}, false, tc.runtime, nil)
			if domain.ErrorCode(err) != "LPSM-ARTIFACT-UNAVAILABLE" {
				t.Fatalf("err = %v, want LPSM-ARTIFACT-UNAVAILABLE", err)
			}
			if tc.name == "both command and endpoint" && !strings.Contains(err.Error(), "one transport") {
				t.Errorf("refusal must state the rule: %v", err)
			}
			after, _ := os.ReadFile(configPath)
			if string(before) != string(after) {
				t.Errorf("a refused install modified the config:\n%s", after)
			}
		})
	}
}

// The target-set capability rule also holds at execute time: a set that never
// went through the plan gate (a hand-built one) is refused for an incapable
// host before ANY config is touched, by name and with the capability reason.
func TestInstallMCPFromListingRefusesIncapableRemoteHost(t *testing.T) {
	db := openTestState(t)
	_, configPath := homeWithBridge(t)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code", "gemini-cli"}, false, remoteRuntime(), nil)
	if domain.ErrorCode(err) != "LPSM-INSTALL-TARGET-UNAVAILABLE" {
		t.Fatalf("err = %v, want LPSM-INSTALL-TARGET-UNAVAILABLE", err)
	}
	for _, want := range []string{"gemini-cli", "LPSM-HOST-REMOTE-UNSUPPORTED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal is missing %q: %v", want, err)
		}
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Errorf("a refused install modified the first host's config:\n%s", after)
	}
	if recs, _ := db.ListInstalls(context.Background(), domain.ScopeUser, ""); len(recs) != 0 {
		t.Errorf("a refused install recorded %d installs", len(recs))
	}
}

// --env on a remote entry is refused with the typed code before any config is
// touched (the writer's own LPSM-REMOTE-ENV-REFUSED gate is the last line, not
// the first).
func TestInstallMCPFromListingRefusesEnvForRemoteEntry(t *testing.T) {
	db := openTestState(t)
	_, configPath := homeWithBridge(t)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installMCPFromListing(authorizedTestContext(), db, t.TempDir(),
		mcpListing("mcp:example:demo-mcp", "demo-mcp"), "1.0.0", domain.ScopeUser,
		[]string{"claude-code"}, false, remoteRuntime(), []string{"API_TOKEN"})
	if domain.ErrorCode(err) != "LPSM-REMOTE-ENV-REFUSED" {
		t.Fatalf("err = %v, want LPSM-REMOTE-ENV-REFUSED", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Errorf("a refused --env modified the config:\n%s", after)
	}
}

// The registered TreeDigest must differ between a stdio and a remote
// registration of the SAME listing and version: they are different facts about
// the machine, and a shared digest would make one look like a reinstall of the
// other. Two independent state DBs stand in for two machines so both installs
// can carry the same install id.
func TestInstallTreeDigestSeparatesStdioFromRemote(t *testing.T) {
	ctx := context.Background()
	listing := mcpListing("mcp:example:demo-mcp", "demo-mcp")

	stdioDB := openTestState(t)
	homeWithBridge(t)
	stdioOutcome, err := installMCPFromListing(authorizedTestContext(), stdioDB, t.TempDir(),
		listing, "1.0.0", domain.ScopeUser, []string{"claude-code"}, false, stdioRuntime(), nil)
	if err != nil {
		t.Fatalf("stdio install: %v", err)
	}
	stdioRec, err := stdioDB.GetInstall(ctx, stdioOutcome.InstallID)
	if err != nil {
		t.Fatalf("stdio install record: %v", err)
	}

	remoteDB := openTestState(t)
	homeWithBridge(t) // a fresh machine: same listing, other transport
	remoteOutcome, err := installMCPFromListing(authorizedTestContext(), remoteDB, t.TempDir(),
		listing, "1.0.0", domain.ScopeUser, []string{"claude-code"}, false, remoteRuntime(), nil)
	if err != nil {
		t.Fatalf("remote install: %v", err)
	}
	remoteRec, err := remoteDB.GetInstall(ctx, remoteOutcome.InstallID)
	if err != nil {
		t.Fatalf("remote install record: %v", err)
	}

	if remoteRec.InstallID != stdioRec.InstallID {
		t.Fatalf("fixture error: the two installs must share an install id to make the digest the only difference (got %q vs %q)",
			stdioRec.InstallID, remoteRec.InstallID)
	}
	if remoteRec.TreeDigest == stdioRec.TreeDigest {
		t.Fatalf("a stdio and a remote registration of the same listing share TreeDigest %q", stdioRec.TreeDigest)
	}
	if want := mcpEntryTreeDigest("", "npx", []string{"-y", "demo-mcp"}); stdioRec.TreeDigest != want {
		t.Errorf("stdio TreeDigest = %q, want %q", stdioRec.TreeDigest, want)
	}
	if want := mcpEntryTreeDigest(remoteEndpoint, "", nil); remoteRec.TreeDigest != want {
		t.Errorf("remote TreeDigest = %q, want %q", remoteRec.TreeDigest, want)
	}
	// And the helper itself is endpoint-sensitive, not just endpoint-present.
	if mcpEntryTreeDigest(remoteEndpoint, "", nil) == mcpEntryTreeDigest("https://other.example.com/mcp", "", nil) {
		t.Error("two different endpoints share a digest")
	}
}

// The sealed plan is re-validated before it executes: a hand-crafted or
// mutated ValueJSON cannot smuggle an unsafe endpoint, an --env the writer
// would refuse, or a two-transport descriptor past the plan gate.
func TestMCPInstallBindingFromPlanRevalidatesTheRuntime(t *testing.T) {
	_, configPath := homeWithBridge(t)
	listing := mcpListing("mcp:example:demo-mcp", "demo-mcp")

	planFor := func(value mcpInstallPlanValue) *domain.InstallPlan {
		return &domain.InstallPlan{
			PlanID: "plan_00000000000000000000000000",
			HostChanges: []domain.HostChange{{
				HostID:     "claude-code",
				ConfigPath: configPath,
				Action:     "register_command",
				EntryKey:   "demo-mcp",
				ValueJSON: mcpInstallPlanValueJSON(&mcpInstallBinding{
					Hosts:    []string{"claude-code"},
					Runtime:  &value.Runtime,
					EnvNames: value.EnvNames,
				}),
			}},
		}
	}

	cases := []struct {
		name     string
		value    mcpInstallPlanValue
		wantCode string
	}{
		{"unsafe endpoint", mcpInstallPlanValue{
			Runtime: domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "http://public.example/mcp"},
		}, "LPSM-EGRESS-BLOCKED"},
		{"metadata endpoint", mcpInstallPlanValue{
			Runtime: domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "https://169.254.169.254/latest/meta-data"},
		}, "LPSM-EGRESS-BLOCKED"},
		{"--env sealed into a remote plan", mcpInstallPlanValue{
			Runtime:  domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: remoteEndpoint},
			EnvNames: []string{"API_TOKEN"},
		}, "LPSM-REMOTE-ENV-REFUSED"},
		{"both transports sealed in", mcpInstallPlanValue{
			Runtime: domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Endpoint: remoteEndpoint},
		}, "LPSM-PLAN-STALE"},
		{"neither transport sealed in", mcpInstallPlanValue{
			Runtime: domain.RuntimeDescriptor{Type: "stdio"},
		}, "LPSM-PLAN-STALE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mcpInstallBindingFromPlan(context.Background(), planFor(tc.value), listing, domain.ScopeUser)
			if domain.ErrorCode(err) != tc.wantCode {
				t.Fatalf("err = %v, want %s", err, tc.wantCode)
			}
		})
	}

	// Positive control: a clean sealed endpoint replays as the endpoint.
	binding, err := mcpInstallBindingFromPlan(context.Background(), planFor(mcpInstallPlanValue{
		Runtime: domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: remoteEndpoint},
	}), listing, domain.ScopeUser)
	if err != nil {
		t.Fatalf("clean sealed plan must replay: %v", err)
	}
	if binding.Runtime.Endpoint != remoteEndpoint || binding.Runtime.Command != "" {
		t.Errorf("replayed runtime = %+v, want the sealed endpoint", binding.Runtime)
	}
}

// The outcome text must describe what was actually registered: an endpoint and
// its transport for a remote entry (never a warning about a "local command"
// that does not exist), and the unchanged command line for a stdio one.
func TestPrintMCPInstallOutcomeText(t *testing.T) {
	t.Run("remote", func(t *testing.T) {
		out := captureStdout(t, func() {
			printMCPInstall(&mcpInstallOutcome{
				InstallID: "inst_user_mcp_example_remote-demo_1_0_0",
				Entry: host.ServerEntry{
					Name: "remote-demo", Endpoint: remoteEndpoint, Transport: host.TransportStreamableHTTP,
				},
				Hosts: []*host.EntryInstallResult{
					{HostID: "codex", ConfigPath: "/home/u/.codex/config.toml"},
					{HostID: "claude-code", ConfigPath: "/home/u/.claude.json"},
				},
				ClaimedTransport: host.TransportStreamableHTTP,
			})
		})
		want := "Endpoint:    " + remoteEndpoint + " (streamable-http)"
		if !strings.Contains(out, want) {
			t.Errorf("outcome must print %q:\n%s", want, out)
		}
		if strings.Contains(out, "Command:") {
			t.Errorf("a remote entry printed a command line:\n%s", out)
		}
		if strings.Contains(out, "publishes no endpoint URL") || strings.Contains(out, "local command above") {
			t.Errorf("a remote entry printed the stdio-only stale-transport caveat:\n%s", out)
		}
		if !strings.Contains(out, "LPSM-REMOTE-ENV-REFUSED") {
			t.Errorf("the --env refusal for remote entries must be stated in the outcome:\n%s", out)
		}
		if !strings.Contains(out, "connects to this endpoint") {
			t.Errorf("a multi-host remote install must say the agent connects, not spawns:\n%s", out)
		}
	})

	t.Run("stdio keeps its own text", func(t *testing.T) {
		out := captureStdout(t, func() {
			printMCPInstall(&mcpInstallOutcome{
				InstallID: "inst_user_mcp_example_demo-mcp_1_0_0",
				Entry: host.ServerEntry{
					Name: "demo-mcp", Command: "npx", Args: []string{"-y", "demo-mcp"},
				},
				Hosts:            []*host.EntryInstallResult{{HostID: "claude-code", ConfigPath: "/home/u/.claude.json"}},
				ClaimedTransport: "sse",
			})
		})
		if !strings.Contains(out, "Command:     npx -y demo-mcp") {
			t.Errorf("stdio outcome lost its command line:\n%s", out)
		}
		if !strings.Contains(out, "publishes no endpoint URL") {
			t.Errorf("a stdio entry claiming sse must keep the stale-transport caveat:\n%s", out)
		}
		if strings.Contains(out, "Endpoint:") {
			t.Errorf("a stdio entry printed an endpoint line:\n%s", out)
		}
		if !strings.Contains(out, "--env <VARIABLE_NAME>") {
			t.Errorf("stdio outcome lost its --env advice:\n%s", out)
		}
	})
}
