package main

// plan_fields_test.go — the plan fields buildInstallPlan derives from data it
// already holds: effects, host changes and requested access. Nothing here may
// depend on the test machine's own agent configuration, so every test that
// reads host configs pins the environment first.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/resolver"
)

// pinHostScanHome points every environment variable a host adapter can read
// at a fresh temp directory, so RegisteredBridgeHosts scans an empty machine
// instead of whoever's configs the test is running on.
func pinHostScanHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for key, val := range map[string]string{
		"HOME":                    home,
		"USERPROFILE":             home,
		"XDG_CONFIG_HOME":         filepath.Join(home, ".config"),
		"APPDATA":                 filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":            filepath.Join(home, "AppData", "Local"),
		"CODEX_HOME":              filepath.Join(home, ".codex"),
		"GROK_HOME":               filepath.Join(home, ".grok"),
		"OPENCODE_CONFIG_DIR":     filepath.Join(home, ".config", "opencode"),
		"PI_CODING_AGENT_DIR":     filepath.Join(home, ".pi", "agent"),
		"CLINE_MCP_SETTINGS_PATH": filepath.Join(home, "cline", "mcp_settings.json"),
		"CLINE_DATA_DIR":          filepath.Join(home, "cline"),
	} {
		t.Setenv(key, val)
	}
	return home
}

// writeClaudeBridge registers the LiteSPM bridge in ~/.claude.json so the
// user scope has exactly one target host.
func writeClaudeBridge(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".claude.json")
	bridge := `{"mcpServers":{"litespm":{"command":"/opt/litespm/litespm","args":["bridge","stdio","--host","claude-code"]}}}`
	if err := os.WriteFile(path, []byte(bridge), 0o600); err != nil {
		t.Fatalf("write bridge config: %v", err)
	}
	return path
}

func TestPlanEffectsDerivation(t *testing.T) {
	mcp := &domain.Listing{ID: "mcp:test:demo", Kind: domain.KindMCP, Name: "demo"}
	skill := &domain.Listing{ID: "skill:test:demo", Kind: domain.KindSkill, Name: "demo"}
	plugin := &domain.Listing{ID: "plugin:test:demo", Kind: domain.KindPlugin, Name: "demo"}
	stdioRec := &domain.VersionRecord{Components: []domain.Component{{
		Runtime: &domain.RuntimeDescriptor{Type: "stdio", Command: "npx"},
	}}}
	remoteRec := &domain.VersionRecord{Components: []domain.Component{{
		Runtime: &domain.RuntimeDescriptor{Type: "sse", Endpoint: "https://example.invalid/mcp"},
	}}}
	streamableRec := &domain.VersionRecord{Components: []domain.Component{{
		Runtime: &domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "https://example.invalid/mcp"},
	}}}
	endpointOnlyRec := &domain.VersionRecord{Components: []domain.Component{{
		Runtime: &domain.RuntimeDescriptor{Endpoint: "https://example.invalid/mcp"},
	}}}

	cases := []struct {
		name    string
		listing *domain.Listing
		rec     *domain.VersionRecord
		hosts   []string
		want    []string
	}{
		{"nil listing", nil, nil, nil, []string{"package.install"}},
		{"mcp with no target host", mcp, stdioRec, nil, []string{"package.install"}},
		{"mcp with a target host", mcp, stdioRec, []string{"claude-code"},
			[]string{"package.install", string(policy.EffectHostConfig)}},
		{"mcp claiming sse transport", mcp, remoteRec, nil,
			[]string{"package.install", string(policy.EffectNetworkOutbound)}},
		{"mcp claiming streamable-http transport", mcp, streamableRec, nil,
			[]string{"package.install", string(policy.EffectNetworkOutbound)}},
		{"mcp publishing an endpoint with no type token", mcp, endpointOnlyRec, nil,
			[]string{"package.install", string(policy.EffectNetworkOutbound)}},
		{"mcp with host and remote transport", mcp, remoteRec, []string{"codex"},
			[]string{"package.install", string(policy.EffectHostConfig), string(policy.EffectNetworkOutbound)}},
		{"mcp with no published runtime", mcp, nil, []string{"codex"},
			[]string{"package.install", string(policy.EffectHostConfig)}},
		{"skill", skill, nil, nil, []string{"package.install", string(policy.EffectFilesystemWrite)}},
		{"plugin claims only the install", plugin, nil, []string{"codex"},
			[]string{"package.install"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planEffects(c.listing, c.rec, c.hosts)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("planEffects() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPreparePlanDeclaresTargetHosts drives the whole path: one registered
// bridge in the scope, so the plan names the host it will write and the
// effects that write implies.
func TestPreparePlanDeclaresTargetHosts(t *testing.T) {
	home := pinHostScanHome(t)
	configPath := writeClaudeBridge(t, home)

	h := newHarness(t)
	ctx := context.Background()

	var plan domain.InstallPlan
	if err := h.client.Call(ctx, "resolver.prepare_plan", map[string]any{
		"id":      testListingID,
		"version": "1.0.0",
	}, &plan); err != nil {
		t.Fatalf("resolver.prepare_plan failed: %v", err)
	}

	wantEffects := []string{"package.install", string(policy.EffectHostConfig)}
	if !reflect.DeepEqual(plan.Effects, wantEffects) {
		t.Errorf("effects = %v, want %v", plan.Effects, wantEffects)
	}
	if len(plan.HostChanges) != 1 {
		t.Fatalf("hostChanges = %+v, want exactly the one configured host", plan.HostChanges)
	}
	change := plan.HostChanges[0]
	if change.HostID != "claude-code" || change.Action != "register_command" {
		t.Errorf("unexpected host change: %+v", change)
	}
	if change.ConfigPath != configPath {
		t.Errorf("configPath = %q, want %q", change.ConfigPath, configPath)
	}
	if change.EntryKey != "demo-tool" {
		t.Errorf("entryKey = %q, want the listing's server name %q", change.EntryKey, "demo-tool")
	}
	var value mcpInstallPlanValue
	if err := json.Unmarshal([]byte(change.ValueJSON), &value); err != nil {
		t.Fatalf("host change must bind the planned MCP runtime: %v", err)
	}
	if value.Runtime.Command != "npx" || !reflect.DeepEqual(value.Runtime.Args, []string{"-y", "demo-tool"}) {
		t.Errorf("plan did not bind the expected runtime: %+v", value.Runtime)
	}

	// The hash still covers the new fields: recomputation over the returned
	// document must reproduce it.
	recomputed, err := domain.ComputePlanHash(&plan)
	if err != nil {
		t.Fatalf("ComputePlanHash: %v", err)
	}
	if recomputed != plan.PlanHash {
		t.Errorf("planHash %q does not match recomputation %q", plan.PlanHash, recomputed)
	}
	// And the plan the journal stored round-trips with the same fields.
	stored, err := h.db.GetPlan(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	storedHash, err := domain.ComputePlanHash(stored)
	if err != nil {
		t.Fatalf("ComputePlanHash(stored): %v", err)
	}
	if storedHash != stored.PlanHash || !reflect.DeepEqual(stored.HostChanges, plan.HostChanges) {
		t.Errorf("stored plan lost its host changes: %+v", stored)
	}

	// A catalog refresh after approval must not change the command executed by
	// this plan; execution reads the descriptor sealed into HostChanges.
	h.catClient.IndexVersionRecords([]*domain.VersionRecord{{
		ListingID: testListingID,
		Version:   "1.0.0",
		Components: []domain.Component{{Runtime: &domain.RuntimeDescriptor{
			Type: "stdio", Command: "changed-after-approval", Args: []string{"--different"},
		}}},
	}})
	listing, err := h.catClient.GetListing(testListingID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := mcpInstallBindingFromPlan(ctx, &plan, listing, domain.ScopeUser)
	if err != nil {
		t.Fatalf("read approved MCP binding: %v", err)
	}
	if binding.Runtime.Command != "npx" || !reflect.DeepEqual(binding.Runtime.Args, []string{"-y", "demo-tool"}) {
		t.Fatalf("catalog refresh changed the approved command: %+v", binding.Runtime)
	}
}

func TestMCPPlanRejectsChangedHostConfigTarget(t *testing.T) {
	oldHome := pinHostScanHome(t)
	oldPath := writeClaudeBridge(t, oldHome)
	h := newHarness(t)
	var plan domain.InstallPlan
	if err := h.client.Call(context.Background(), "resolver.prepare_plan", map[string]any{
		"id": testListingID, "version": "1.0.0",
	}, &plan); err != nil {
		t.Fatalf("prepare plan: %v", err)
	}
	if len(plan.HostChanges) != 1 || plan.HostChanges[0].ConfigPath != oldPath {
		t.Fatalf("fixture plan has unexpected targets: %+v", plan.HostChanges)
	}

	newHome := t.TempDir()
	t.Setenv("HOME", newHome)
	t.Setenv("USERPROFILE", newHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(newHome, ".config"))
	listing, err := h.catClient.GetListing(testListingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mcpInstallBindingFromPlan(context.Background(), &plan, listing, domain.ScopeUser); err == nil || domain.ErrorCode(err) != "LPSM-PLAN-STALE" {
		t.Fatalf("changed host config target was not rejected as stale: %v", err)
	}
}

func TestRequestedAccessFor(t *testing.T) {
	if got := requestedAccessFor(nil); got != nil {
		t.Errorf("nil record must yield no requested access, got %+v", got)
	}
	// Nothing declared (what every wired source produces today) yields nil.
	empty := &domain.VersionRecord{}
	if got := requestedAccessFor(empty); got != nil {
		t.Errorf("a record that declares nothing must yield no requested access, got %+v", got)
	}

	declared := &domain.VersionRecord{
		PermissionsDeclared: []domain.PermissionDeclaration{
			{Type: "fs", Target: "~/.ssh"},
			{Type: "network", Target: "api.example.invalid"},
			{Type: "tool", Target: "shell"},
			{Type: "opaque-kind"},  // names no target: falls back to its own type
			{Type: "", Target: ""}, // names nothing at all: dropped
		},
		Requirements: []domain.Requirement{
			{Type: "tool", Name: "jq"},
			{Type: "runtime", Name: "node"}, // a prerequisite, not access
		},
	}
	got := requestedAccessFor(declared)
	if got == nil {
		t.Fatal("declared permissions must be carried into the plan")
	}
	if want := []string{"~/.ssh"}; !reflect.DeepEqual(got.Paths, want) {
		t.Errorf("paths = %v, want %v", got.Paths, want)
	}
	if want := []string{"api.example.invalid"}; !reflect.DeepEqual(got.Hosts, want) {
		t.Errorf("hosts = %v, want %v", got.Hosts, want)
	}
	// The tool-typed permission, its own type as the fallback resource, and
	// the tool-typed requirement — the runtime requirement stays out.
	if want := []string{"shell", "opaque-kind", "jq"}; !reflect.DeepEqual(got.Tools, want) {
		t.Errorf("tools = %v, want %v", got.Tools, want)
	}
}

func TestVersionRecordForFailsSoft(t *testing.T) {
	// No sync, no listing: absence of evidence is nil, never an error or a
	// fabricated record.
	if rec := versionRecordFor(nil, "mcp:test:demo", "1.0.0"); rec != nil {
		t.Errorf("nil client must yield nil, got %+v", rec)
	}
	client := catalog.NewClient("https://registry.invalid", t.TempDir(), nil)
	if rec := versionRecordFor(client, "mcp:test:demo", "1.0.0"); rec != nil {
		t.Errorf("an unsynced index must yield nil, got %+v", rec)
	}
}

func TestHostChangesForSkipsNonMCPKinds(t *testing.T) {
	ctx := context.Background()
	skill := &domain.Listing{ID: "skill:test:demo", Kind: domain.KindSkill, Name: "demo"}
	if got := hostChangesFor(ctx, skill, domain.ScopeUser, []string{"claude-code"}, nil); got != nil {
		t.Errorf("a skill install writes no host config, got %+v", got)
	}
	if got := hostChangesFor(ctx, nil, domain.ScopeUser, []string{"claude-code"}, nil); got != nil {
		t.Errorf("a nil listing produces no host changes, got %+v", got)
	}
	mcp := &domain.Listing{ID: "mcp:test:demo", Kind: domain.KindMCP, Name: "demo"}
	if got := hostChangesFor(ctx, mcp, domain.ScopeUser, nil, nil); got != nil {
		t.Errorf("no target host means no host changes, got %+v", got)
	}
}

// versionLessSkillFixture is a faithful copy of the version-less skill row the
// release publishes — `skill:aaron-he-zhu:aaron-marketing-skills` in
// rel-2026-10-07-01, one of the 5,811 of 5,825 listings with `versions: []` —
// together with its versions.json record (version "", component id embedding
// the @discovery segment). It keeps this test running in a clean checkout,
// where the release tree (web/public/v1/releases/, built at release time and
// gitignored) is absent; versionLessSkillFromRelease prefers the real bytes
// when they exist.
func versionLessSkillFixture() (*domain.Listing, *domain.VersionRecord) {
	const id = "skill:aaron-he-zhu:aaron-marketing-skills"
	listing := &domain.Listing{
		SchemaVersion:  1,
		ID:             id,
		Kind:           domain.KindSkill,
		Name:           "Aaron Marketing Skills",
		Summary:        "Production-grade agent playbook for aaron-he-zhu/aaron-marketing-skills",
		Installability: domain.InstallabilityDiscoveryOnly,
		Status:         domain.ListingStatusActive,
		Versions:       []domain.VersionSummary{},
		ComponentsSummary: []domain.ComponentSummary{
			{Kind: domain.ComponentSkill, Name: "Aaron Marketing Skills"},
		},
		Source: domain.SourceReference{
			SourceID:   "aaron-he-zhu",
			UpstreamID: "aaron-marketing-skills",
			URL:        "https://github.com/aaron-he-zhu/aaron-marketing-skills",
		},
	}
	record := &domain.VersionRecord{
		ListingID: id,
		Version:   "", // the release's honest value for a version-less listing
		Components: []domain.Component{{
			ID:                 id + "@discovery#skill/aaron-marketing-skills", // release byte form
			Kind:               domain.ComponentSkill,
			Name:               "aaron-marketing-skills",
			SupportedByLiteSPM: domain.SupportYes,
		}},
	}
	return listing, record
}

// versionLessSkillFromRelease loads the first version-less skill row (and its
// version record) from the committed-on-disk rel-2026-10-07-01 release tree.
// The tree is a gitignored build artifact, so its absence skips rather than
// fails; this checkout has it and the test runs against the real bytes.
func versionLessSkillFromRelease(t *testing.T) (*domain.Listing, *domain.VersionRecord) {
	t.Helper()
	base := filepath.Join("..", "..", "web", "public", "v1", "releases", "rel-2026-10-07-01")

	listingsRaw, err := os.ReadFile(filepath.Join(base, "listings.json"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("release tree %s not present (built at release time; gitignored)", base)
		}
		t.Fatalf("read listings.json: %v", err)
	}
	var listings []domain.Listing
	if err := json.Unmarshal(listingsRaw, &listings); err != nil {
		t.Fatalf("decode listings.json: %v", err)
	}
	var row *domain.Listing
	for i := range listings {
		if listings[i].Kind == domain.KindSkill && len(listings[i].Versions) == 0 {
			row = &listings[i]
			break
		}
	}
	if row == nil {
		t.Fatal("release publishes no version-less skill row (listings.json changed shape?)")
	}

	recordsRaw, err := os.ReadFile(filepath.Join(base, "versions.json"))
	if err != nil {
		t.Fatalf("read versions.json: %v", err)
	}
	var records []domain.VersionRecord
	if err := json.Unmarshal(recordsRaw, &records); err != nil {
		t.Fatalf("decode versions.json: %v", err)
	}
	for i := range records {
		if records[i].ListingID == row.ID {
			return row, &records[i]
		}
	}
	t.Fatalf("release publishes no version record for %s", row.ID)
	return nil, nil
}

// assertVersionLessPlan builds a plan for a version-less skill row and pins
// recommendation A2 end to end: it resolves, plan.Resolved.Version carries the
// release's implicit pin (never a synthesized version), no field is filled
// with invented data, and the recorded hash recomputes.
func assertVersionLessPlan(t *testing.T, row *domain.Listing, rec *domain.VersionRecord) {
	t.Helper()
	ctx := context.Background()

	client := catalog.NewClient("https://registry.invalid", t.TempDir(), nil)
	client.IndexListings([]*domain.Listing{row})
	client.IndexVersionRecords([]*domain.VersionRecord{rec})

	plan, _, rpcErr := buildInstallPlan(ctx, client, row.ID, "", domain.ScopeUser, row, nil)
	if rpcErr != nil {
		t.Fatalf("version-less listing must resolve, got: %s", rpcErr.Message)
	}

	if plan.Resolved.Version != resolver.ImplicitVersion {
		t.Errorf("Resolved.Version = %q, want the release's implicit pin %q (no synthesized version)",
			plan.Resolved.Version, resolver.ImplicitVersion)
	}
	if plan.Resolved.Version == "0.0.0" {
		t.Error("0.0.0 must never reach the plan")
	}
	if plan.Request.RequestedVersion != "" {
		t.Errorf("RequestedVersion = %q, want empty: the caller asked for no version", plan.Request.RequestedVersion)
	}
	if len(plan.Resolved.ImmutableRefs) != 0 {
		t.Errorf("ImmutableRefs = %v, want none: a version-less listing publishes no immutable ref", plan.Resolved.ImmutableRefs)
	}
	if len(plan.Resolved.Dependencies) != 0 {
		t.Errorf("Dependencies = %v, want none", plan.Resolved.Dependencies)
	}
	if len(plan.Resolved.Artifacts) != 0 {
		t.Errorf("Artifacts = %v, want none", plan.Resolved.Artifacts)
	}
	if plan.RequestedAccess != nil {
		t.Errorf("RequestedAccess = %+v, want nil: the published record declares no permissions", plan.RequestedAccess)
	}
	if !reflect.DeepEqual(plan.Preconditions, domain.PlanPreconditions{}) {
		t.Errorf("Preconditions = %+v, want zero: nothing here probes the machine", plan.Preconditions)
	}
	wantEffects := []string{"package.install", string(policy.EffectFilesystemWrite)}
	if !reflect.DeepEqual(plan.Effects, wantEffects) {
		t.Errorf("Effects = %v, want %v", plan.Effects, wantEffects)
	}
	if len(plan.HostChanges) != 0 {
		t.Errorf("HostChanges = %+v, want none for a skill", plan.HostChanges)
	}

	// The pin must name the record the release actually published (under the
	// empty version) — a version-less plan reads real catalog data instead of
	// silently degrading to "not knowable".
	lookedUp := versionRecordFor(client, row.ID, plan.Resolved.Version)
	if lookedUp == nil {
		t.Fatal("the implicit pin did not map to the listing's published version record")
	}
	if lookedUp.Version != "" {
		t.Errorf("mapped record version = %q, want the release's empty version", lookedUp.Version)
	}

	// The hash covers Resolved; it must recompute exactly over what was built.
	recomputed, err := domain.ComputePlanHash(plan)
	if err != nil {
		t.Fatalf("ComputePlanHash: %v", err)
	}
	if recomputed != plan.PlanHash {
		t.Errorf("planHash %q does not match recomputation %q", plan.PlanHash, recomputed)
	}
}

// TestBuildInstallPlanResolvesVersionLessSkillRow drives buildInstallPlan for
// a listing whose Versions is [] — the shape that used to die in the resolver
// with LPSM-RESOLVE-CONFLICT "no versions available in catalog".
func TestBuildInstallPlanResolvesVersionLessSkillRow(t *testing.T) {
	t.Run("faithful fixture", func(t *testing.T) {
		row, rec := versionLessSkillFixture()
		assertVersionLessPlan(t, row, rec)
	})
	t.Run("rel-2026-10-07-01 row", func(t *testing.T) {
		row, rec := versionLessSkillFromRelease(t)
		t.Logf("loaded real release row %s (versions: %d)", row.ID, len(row.Versions))
		assertVersionLessPlan(t, row, rec)
	})
}

// --- B1 Phase 3: plan-time gating for remote (URL) MCP installs ------------

// remoteEndpoint is the endpoint every remote fixture registers. It is an
// https hostname: plan time runs NO DNS, so nothing here can accidentally
// depend on the network.
const remoteEndpoint = "https://mcp.example.com/mcp"

func remoteRuntime() *domain.RuntimeDescriptor {
	return &domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: remoteEndpoint}
}

// remotePlanClient indexes one installable MCP listing and its published
// version record — an endpoint-only runtime, the shape the live registry
// publishes. Fixture-driven: no network, no sync.
func remotePlanClient(t *testing.T) (*catalog.Client, *domain.Listing) {
	t.Helper()
	listing := mcpListing("mcp:example:remote-demo", "remote-demo")
	client := catalog.NewClient("https://registry.invalid", t.TempDir(), nil)
	client.IndexListings([]*domain.Listing{listing})
	client.IndexVersionRecords([]*domain.VersionRecord{{
		ListingID:  listing.ID,
		Version:    "1.0.0",
		Components: []domain.Component{{Runtime: remoteRuntime()}},
	}})
	return client, listing
}

func hostChangeIDs(plan *domain.InstallPlan) []string {
	ids := make([]string, 0, len(plan.HostChanges))
	for _, c := range plan.HostChanges {
		ids = append(ids, c.HostID)
	}
	return ids
}

// TestDeclaresRemoteTransportTable pins the consent document: every transport
// vocabulary a real remote row can carry — including streamable-http, the only
// token the Official MCP Registry actually publishes — makes the plan declare
// network.outbound, and a stdio runtime or an empty record declares nothing.
func TestDeclaresRemoteTransportTable(t *testing.T) {
	cases := []struct {
		name string
		rec  *domain.VersionRecord
		want bool
	}{
		{"nil record", nil, false},
		{"no components", &domain.VersionRecord{}, false},
		{"component with no runtime", &domain.VersionRecord{Components: []domain.Component{{}}}, false},
		{"stdio", recordWithTypeCommand("stdio", "npx"), false},
		{"sse", recordWithTypeCommand("sse", ""), true},
		{"http", recordWithTypeCommand("http", ""), true},
		{"streamable-http", recordWithTypeCommand("streamable-http", ""), true},
		{"remote (opencode's discriminator)", recordWithTypeCommand("remote", ""), true},
		{"STREAMABLE-HTTP (case-insensitive)", recordWithTypeCommand("Streamable-HTTP", ""), true},
		{"endpoint with no type token", &domain.VersionRecord{Components: []domain.Component{{
			Runtime: &domain.RuntimeDescriptor{Endpoint: "https://example.invalid/mcp"},
		}}}, true},
		{"stdio command wins over an endpoint", &domain.VersionRecord{Components: []domain.Component{{
			Runtime: &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Endpoint: "https://example.invalid/mcp"},
		}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := declaresRemoteTransport(tc.rec); got != tc.want {
				t.Errorf("declaresRemoteTransport() = %t, want %t", got, tc.want)
			}
		})
	}
}

func recordWithTypeCommand(typ, command string) *domain.VersionRecord {
	return &domain.VersionRecord{Components: []domain.Component{{
		Runtime: &domain.RuntimeDescriptor{Type: typ, Command: command},
	}}}
}

// TestBuildInstallPlanFiltersIncapableHostsForRemoteRuntime is §1.5's target
// set rule: when LiteSPM chose the default host set, hosts that cannot express
// the runtime's transport are dropped BEFORE the plan is sealed, exactly the
// capable hosts remain, and every drop is reported with its capability reason.
// The sealed ValueJSON must carry the endpoint (hostChangesFor serialises the
// whole RuntimeDescriptor) so execution reads the URL the approver saw.
func TestBuildInstallPlanFiltersIncapableHostsForRemoteRuntime(t *testing.T) {
	pinHostScanHome(t)
	client, listing := remotePlanClient(t)
	binding := &mcpInstallBinding{
		Hosts:   []string{"claude-code", "gemini-cli", "codex", "amp"},
		Runtime: remoteRuntime(),
	}
	plan, drops, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, binding)
	if rpcErr != nil {
		t.Fatalf("plan build failed: %s", rpcErr.Message)
	}
	if got, want := hostChangeIDs(plan), []string{"claude-code", "codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan targets = %v, want exactly the capable hosts %v", got, want)
	}
	if len(drops) != 2 || drops[0].HostID != "gemini-cli" || drops[1].HostID != "amp" {
		t.Fatalf("drops = %+v, want gemini-cli then amp", drops)
	}
	for _, d := range drops {
		if !strings.Contains(d.Reason, "LPSM-HOST-REMOTE-UNSUPPORTED") {
			t.Errorf("drop %q reason must carry the typed capability code: %s", d.HostID, d.Reason)
		}
		if strings.Contains(d.Reason, d.HostID) {
			t.Errorf("drop %q reason repeats the host id (the caller prints both): %s", d.HostID, d.Reason)
		}
	}

	// The approval document: host.config.write for the surviving targets and
	// network.outbound for a real remote row.
	wantEffects := []string{"package.install", string(policy.EffectHostConfig), string(policy.EffectNetworkOutbound)}
	if !reflect.DeepEqual(plan.Effects, wantEffects) {
		t.Errorf("effects = %v, want %v", plan.Effects, wantEffects)
	}

	// ValueJSON seals the endpoint, so `mcpInstallBindingFromPlan` replays the
	// URL and not an empty descriptor.
	var value mcpInstallPlanValue
	if err := json.Unmarshal([]byte(plan.HostChanges[0].ValueJSON), &value); err != nil {
		t.Fatalf("ValueJSON must decode: %v", err)
	}
	if value.Runtime.Endpoint != remoteEndpoint || value.Runtime.Command != "" {
		t.Errorf("ValueJSON did not seal the endpoint runtime: %+v", value.Runtime)
	}
	if value.Runtime.Type != "streamable-http" {
		t.Errorf("ValueJSON lost the transport: %+v", value.Runtime)
	}

	recomputed, err := domain.ComputePlanHash(plan)
	if err != nil {
		t.Fatalf("ComputePlanHash: %v", err)
	}
	if recomputed != plan.PlanHash {
		t.Errorf("planHash %q does not match recomputation %q", plan.PlanHash, recomputed)
	}
}

// Explicit --host sets are never filtered: the user asked for that host by
// name, so an incapable one fails by name with the typed target error instead
// of disappearing from the plan.
func TestBuildInstallPlanExplicitHostFailsByName(t *testing.T) {
	pinHostScanHome(t)
	client, listing := remotePlanClient(t)
	binding := &mcpInstallBinding{
		Hosts:         []string{"gemini-cli"},
		Runtime:       remoteRuntime(),
		ExplicitHosts: true,
	}
	plan, drops, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, binding)
	if rpcErr == nil {
		t.Fatalf("an explicit host that cannot express a remote entry was accepted: %+v", plan)
	}
	if rpcErr.Code != ipc.CodeInvalidParams {
		t.Errorf("code = %d, want %d", rpcErr.Code, ipc.CodeInvalidParams)
	}
	if !strings.Contains(rpcErr.Message, "LPSM-INSTALL-TARGET-UNAVAILABLE") {
		t.Errorf("message must carry the typed target error: %s", rpcErr.Message)
	}
	if !strings.Contains(rpcErr.Message, "gemini-cli") {
		t.Errorf("the refusal must name the host the user asked for: %s", rpcErr.Message)
	}
	if !strings.Contains(rpcErr.Message, "LPSM-HOST-REMOTE-UNSUPPORTED") {
		t.Errorf("the refusal must carry the capability reason: %s", rpcErr.Message)
	}
	if len(drops) != 0 {
		t.Errorf("an explicit set reports no drops, got %+v", drops)
	}
}

// A default set in which NOT ONE host is capable fails closed at plan time
// with the capability reason for every host — not the generic "no host set up"
// text, and never a plan with zero targets.
func TestBuildInstallPlanAllIncapableHostsFailWithCapabilityReason(t *testing.T) {
	pinHostScanHome(t)
	client, listing := remotePlanClient(t)
	binding := &mcpInstallBinding{
		Hosts:   []string{"gemini-cli", "amp"},
		Runtime: remoteRuntime(),
	}
	plan, _, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, binding)
	if rpcErr == nil {
		t.Fatalf("an all-incapable target set produced a plan: %+v", plan)
	}
	if rpcErr.Code != ipc.CodeInvalidParams {
		t.Errorf("code = %d, want %d", rpcErr.Code, ipc.CodeInvalidParams)
	}
	for _, want := range []string{
		"LPSM-INSTALL-TARGET-UNAVAILABLE",
		"none of the configured agent hosts",
		"streamable-http",
		"gemini-cli",
		"amp",
		"LPSM-HOST-REMOTE-UNSUPPORTED",
	} {
		if !strings.Contains(rpcErr.Message, want) {
			t.Errorf("refusal is missing %q:\n%s", want, rpcErr.Message)
		}
	}
}

// An sse row against hosts that only document streamable-http is the same
// capability failure, by transport rather than by total incapability: capable
// hosts for streamable-http are dropped from an sse plan.
func TestBuildInstallPlanFiltersHostsThatLackTheTransport(t *testing.T) {
	pinHostScanHome(t)
	client, listing := remotePlanClient(t)
	binding := &mcpInstallBinding{
		Hosts: []string{"claude-code", "codex"},
		Runtime: &domain.RuntimeDescriptor{
			Type: "sse", Endpoint: "https://mcp.example.com/sse",
		},
	}
	plan, drops, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, binding)
	if rpcErr != nil {
		t.Fatalf("plan build failed: %s", rpcErr.Message)
	}
	// claude-code documents type:"sse"; codex's URL is streamable-HTTP-only.
	if got, want := hostChangeIDs(plan), []string{"claude-code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan targets = %v, want %v", got, want)
	}
	if len(drops) != 1 || drops[0].HostID != "codex" || !strings.Contains(drops[0].Reason, "sse") {
		t.Fatalf("drops = %+v, want codex dropped for the sse transport", drops)
	}
}

// §2.4 Decision 5: no plan, no approval prompt for an unsafe URL. These are
// the static refusals egress.CheckURL (+ the literal-IP destination rule)
// makes before anything is sealed.
func TestBuildInstallPlanRefusesUnsafeEndpoint(t *testing.T) {
	pinHostScanHome(t)
	cases := []struct {
		name     string
		endpoint string
	}{
		{"plain http on a public host", "http://public.example/mcp"},
		{"cloud metadata endpoint", "https://169.254.169.254/latest/meta-data"},
		{"credentials embedded in the URL", "https://user:pass@mcp.example.com/mcp"},
		{"private IP literal (no AllowPrivate opt-in)", "https://192.168.1.10/mcp"},
		{"non-http scheme", "ftp://mcp.example.com/mcp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, listing := remotePlanClient(t)
			binding := &mcpInstallBinding{Hosts: []string{"claude-code"}, Runtime: &domain.RuntimeDescriptor{
				Type: "streamable-http", Endpoint: tc.endpoint,
			}}
			plan, _, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
				domain.ScopeUser, listing, binding)
			if rpcErr == nil {
				t.Fatalf("unsafe endpoint %q produced a plan: %+v", tc.endpoint, plan)
			}
			if rpcErr.Code != ipc.CodeInvalidParams {
				t.Errorf("code = %d, want %d", rpcErr.Code, ipc.CodeInvalidParams)
			}
			if !strings.Contains(rpcErr.Message, "LPSM-EGRESS-BLOCKED") {
				t.Errorf("refusal must be the typed egress error:\n%s", rpcErr.Message)
			}
		})
	}

	// Positive controls: the guard refuses nothing an approved endpoint needs.
	for _, ok := range []string{
		"https://mcp.example.com/mcp",
		"http://127.0.0.1:8080/mcp",
		"http://localhost:8080/mcp",
	} {
		client, listing := remotePlanClient(t)
		binding := &mcpInstallBinding{Hosts: []string{"claude-code"}, Runtime: &domain.RuntimeDescriptor{
			Type: "streamable-http", Endpoint: ok,
		}}
		if _, _, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
			domain.ScopeUser, listing, binding); rpcErr != nil {
			t.Errorf("legitimate endpoint %q was refused at plan time: %s", ok, rpcErr.Message)
		}
	}
}

// --env on a remote entry is refused BEFORE the plan exists (typed code), so
// a user never approves a plan the writer would later reject mid-install.
func TestBuildInstallPlanRefusesEnvForRemoteRuntime(t *testing.T) {
	pinHostScanHome(t)
	client, listing := remotePlanClient(t)
	binding := &mcpInstallBinding{
		Hosts:    []string{"claude-code"},
		Runtime:  remoteRuntime(),
		EnvNames: []string{"API_TOKEN"},
	}
	plan, _, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, binding)
	if rpcErr == nil {
		t.Fatalf("--env on a remote runtime produced a plan: %+v", plan)
	}
	if !strings.Contains(rpcErr.Message, "LPSM-REMOTE-ENV-REFUSED") {
		t.Errorf("want the typed env refusal, got: %s", rpcErr.Message)
	}

	// The same binding for a stdio runtime is accepted: --env is a stdio-only
	// refusal, and the plan gate must not become a blanket one.
	stdio := &mcpInstallBinding{
		Hosts:    []string{"claude-code"},
		Runtime:  &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", "demo"}},
		EnvNames: []string{"API_TOKEN"},
	}
	if _, _, rpcErr := buildInstallPlan(context.Background(), client, listing.ID, "1.0.0",
		domain.ScopeUser, listing, stdio); rpcErr != nil {
		t.Fatalf("--env on a stdio runtime must still plan: %s", rpcErr.Message)
	}
}
