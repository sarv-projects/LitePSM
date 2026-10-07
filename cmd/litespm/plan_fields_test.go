package main

// plan_fields_test.go — the plan fields buildInstallPlan derives from data it
// already holds: effects, host changes and requested access. Nothing here may
// depend on the test machine's own agent configuration, so every test that
// reads host configs pins the environment first.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/policy"
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
	if change.ValueJSON != "" {
		t.Errorf("valueJson must stay empty until install writes it, got %q", change.ValueJSON)
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
	if got := hostChangesFor(ctx, skill, domain.ScopeUser, []string{"claude-code"}); got != nil {
		t.Errorf("a skill install writes no host config, got %+v", got)
	}
	if got := hostChangesFor(ctx, nil, domain.ScopeUser, []string{"claude-code"}); got != nil {
		t.Errorf("a nil listing produces no host changes, got %+v", got)
	}
	mcp := &domain.Listing{ID: "mcp:test:demo", Kind: domain.KindMCP, Name: "demo"}
	if got := hostChangesFor(ctx, mcp, domain.ScopeUser, nil); got != nil {
		t.Errorf("no target host means no host changes, got %+v", got)
	}
}
