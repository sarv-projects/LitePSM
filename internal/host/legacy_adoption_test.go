package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// jsonServersSeed builds a JSON config whose object at keyPath holds the given
// raw server members.
func jsonServersSeed(keyPath []string, servers string) string {
	if len(keyPath) == 1 {
		return `{"` + keyPath[0] + `":{` + servers + `}}`
	}
	return `{"` + keyPath[0] + `":{"` + keyPath[1] + `":{` + servers + `}}}`
}

// jsonContainer parses content and walks to the object at keyPath.
func jsonContainer(t *testing.T, content string, keyPath []string) map[string]any {
	t.Helper()
	root := mustParse(t, content)
	cur := root
	for _, k := range keyPath {
		next, ok := cur[k].(map[string]any)
		if !ok {
			t.Fatalf("key %q missing or not an object in:\n%s", k, content)
		}
		cur = next
	}
	return cur
}

// assertSingleCurrentBridge checks that exactly one current bridge entry exists
// and no legacy one does, inside the container at keyPath.
func assertSingleCurrentBridge(t *testing.T, content string, keyPath []string) {
	t.Helper()
	container := jsonContainer(t, content, keyPath)
	if _, ok := container[litespmServerName]; !ok {
		t.Fatalf("current bridge entry %q missing:\n%s", litespmServerName, content)
	}
	if _, ok := container[legacyServerName]; ok {
		t.Fatalf("legacy bridge entry %q survived:\n%s", legacyServerName, content)
	}
	if n := strings.Count(content, `"litespm"`); n != 1 {
		t.Fatalf("expected exactly one current bridge entry, found %d:\n%s", n, content)
	}
	if n := strings.Count(content, `"litepsm"`); n != 0 {
		t.Fatalf("expected no legacy bridge entries, found %d:\n%s", n, content)
	}
}

// TestGenericAdapterAdoptsLegacyBridgeEntry runs the data-driven JSON path for a
// flat key (cursor), a nested key (amp) and a comment-tolerant host (zed).
func TestGenericAdapterAdoptsLegacyBridgeEntry(t *testing.T) {
	for _, id := range []string{"cursor", "amp", "zed"} {
		t.Run(id, func(t *testing.T) {
			home := useTempHome(t)
			adapter := adapterFor(t, id)
			ctx := context.Background()
			path := configPathOf(t, adapter)
			keyPath := adapter.Target.keyPathFor(false)

			writeConfig(t, path, jsonServersSeed(keyPath,
				`"mine":{"command":"npx"},"litepsm":{"command":"/old/legacy","args":["bridge"]}`))

			plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
			if err != nil {
				t.Fatalf("PlanSetup: %v", err)
			}
			if _, err := adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}

			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertSingleCurrentBridge(t, string(out), keyPath)
			if !strings.Contains(string(out), `"mine"`) {
				t.Fatalf("foreign server was destroyed:\n%s", out)
			}
		})
	}
}

// TestGenericAdapterAdoptsWhenLegacyAndCurrentBothPresent is the duplicate
// scenario: a host already carrying both names must end with exactly one.
func TestGenericAdapterAdoptsWhenLegacyAndCurrentBothPresent(t *testing.T) {
	home := useTempHome(t)
	adapter := adapterFor(t, "cursor")
	ctx := context.Background()
	path := configPathOf(t, adapter)
	keyPath := adapter.Target.keyPathFor(false)

	writeConfig(t, path, jsonServersSeed(keyPath,
		`"litespm":{"command":"/new/current"},"litepsm":{"command":"/old/legacy"},"mine":{"command":"npx"}`))

	plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("ApplySetup: %v", err)
	}
	out, _ := os.ReadFile(path)
	assertSingleCurrentBridge(t, string(out), keyPath)
	if !strings.Contains(string(out), `"mine"`) {
		t.Fatalf("foreign server was destroyed:\n%s", out)
	}
}

// TestGenericAdapterPreservesCommentsWhileAdopting proves legacy adoption does
// not reformat a JSONC host: comments and unrelated keys survive byte-for-byte.
func TestGenericAdapterPreservesCommentsWhileAdopting(t *testing.T) {
	home := useTempHome(t)
	adapter := adapterFor(t, "zed")
	ctx := context.Background()
	path := configPathOf(t, adapter)

	original := "{\n  // keep my theme\n  \"theme\": \"One Dark\",\n  \"context_servers\": {\n    // keep my server\n    \"mine\": { \"command\": \"uvx\" },\n    \"litepsm\": { \"command\": \"/old/legacy\" }\n  }\n}\n"
	writeConfig(t, path, original)

	plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("ApplySetup: %v", err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{"// keep my theme", "// keep my server", `"theme": "One Dark"`, `"mine"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("adoption destroyed %q:\n%s", want, out)
		}
	}
	assertSingleCurrentBridge(t, string(out), []string{"context_servers"})
}

// TestGenericAdapterLegacyTOMLAdoption exercises the data-driven TOML writer
// (no shipped target uses it yet) through a synthetic target.
func TestGenericAdapterLegacyTOMLAdoption(t *testing.T) {
	home := useTempHome(t)
	tgt := BridgeTarget{
		ID:       "legacy-toml-host",
		Name:     "Legacy TOML Host",
		Format:   FormatTOML,
		Shape:    ShapeObject,
		UserKey:  []string{"mcp_servers"},
		UserPath: func(h string) string { return filepath.Join(h, "legacy-toml.config.toml") },
	}
	adapter := NewGenericAdapter(tgt)
	ctx := context.Background()
	path, err := adapter.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, "# keep me\n[mcp_servers.mine]\ncommand = \"npx\"\n\n[mcp_servers.litepsm]\ncommand = \"/old/legacy\"\nargs = [\"bridge\"]\n")

	plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("ApplySetup: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, "[mcp_servers.litepsm]") {
		t.Fatalf("legacy TOML table survived:\n%s", s)
	}
	if strings.Count(s, "[mcp_servers.litespm]") != 1 {
		t.Fatalf("expected exactly one bridge table:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.mine]") || !strings.Contains(s, "# keep me") {
		t.Fatalf("foreign content or comment lost:\n%s", s)
	}
}

// TestBespokeJSONAdaptersAdoptLegacyEntry covers the hand-written JSON adapters.
func TestBespokeJSONAdaptersAdoptLegacyEntry(t *testing.T) {
	ctx := context.Background()
	for _, id := range []string{"claude-code", "cline", "pi-agent"} {
		t.Run(id, func(t *testing.T) {
			home := useTempHome(t)
			adapter, err := GetAdapter(id)
			if err != nil {
				t.Fatal(err)
			}
			path := configPathOfAny(t, adapter)
			writeConfig(t, path, `{"mcpServers":{"mine":{"command":"npx"},"litepsm":{"command":"/old/legacy","args":["bridge"]}}}`)

			plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
			if err != nil {
				t.Fatalf("PlanSetup: %v", err)
			}
			if _, err := adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}
			out, _ := os.ReadFile(path)
			assertSingleCurrentBridge(t, string(out), []string{"mcpServers"})
		})
	}
}

// TestPiAgentAdoptsLegacyEntryInNestedLayout covers pi-agent's documented
// mcp.servers layout.
// TestPiAgentIgnoresForeignContainersAndAdoptsLegacyEntry pins that the bridge
// always lands in `mcpServers`, the only container Pi reads. Earlier revisions
// wrote into `mcp` / `mcp.servers` whenever the file happened to contain an
// unrelated top-level `mcp` object, which put the bridge where Pi never looks.
// Pi ignores unknown top-level keys, so that foreign object is left untouched.
func TestPiAgentIgnoresForeignContainersAndAdoptsLegacyEntry(t *testing.T) {
	home := useTempHome(t)
	adapter := &PiAgentAdapter{}
	ctx := context.Background()
	path := configPathOfAny(t, adapter)
	writeConfig(t, path, `{"mcp":{"servers":{"mine":{"command":"x"},"litepsm":{"command":"/old/legacy"}}}}`)

	plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("ApplySetup: %v", err)
	}
	out, _ := os.ReadFile(path)
	root, err := parseHostJSON(out)
	if err != nil {
		t.Fatalf("config does not parse: %v\n%s", err, out)
	}
	container, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers container missing:\n%s", out)
	}
	if _, ok := container[litespmServerName]; !ok {
		t.Fatalf("bridge entry missing from mcpServers:\n%s", out)
	}
	if n := strings.Count(string(out), `"litespm"`); n != 1 {
		t.Errorf("expected exactly one bridge entry, found %d:\n%s", n, out)
	}
}

// TestOpenCodeAdoptsLegacyEntryInBothLayouts covers the flat layout and the
// nested `mcp.servers` layout that earlier LiteSPM versions wrote. Upstream
// OpenCode has no nested layout — the published schema types `mcp` as a map of
// server entries and the runtime rejects a member without `type` — so the
// legacy entry must be adopted from wherever it sits and the current entry
// written flat, or the bridge is invisible to the host.
func TestOpenCodeAdoptsLegacyEntryInBothLayouts(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		seed    string
		keyPath []string
	}{
		{
			name:    "flat",
			seed:    `{"mcp":{"mine":{"command":"x"},"litepsm":{"command":"/old/legacy"}}}`,
			keyPath: []string{"mcp"},
		},
		{
			name:    "legacy nested layout",
			seed:    `{"mcp":{"servers":{"mine":{"type":"local","command":["x"]},"litepsm":{"type":"local","command":["/old/legacy"]}}}}`,
			keyPath: []string{"mcp"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := useTempHome(t)
			adapter := &OpenCodeAdapter{}
			path := configPathOfAny(t, adapter)
			writeConfig(t, path, tc.seed)

			plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
			if err != nil {
				t.Fatalf("PlanSetup: %v", err)
			}
			if _, err := adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}
			out, _ := os.ReadFile(path)
			assertSingleCurrentBridge(t, string(out), tc.keyPath)
		})
	}
}

// TestTOMLBespokeAdaptersAdoptLegacyEntry covers the hand-written TOML adapters.
func TestTOMLBespokeAdaptersAdoptLegacyEntry(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		id      string
		adapter HostAdapter
	}{
		{"codex", &CodexAdapter{}},
		{"grok-build", &GrokBuildAdapter{}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			home := useTempHome(t)
			path := configPathOfAny(t, tc.adapter)
			writeConfig(t, path, "# keep me\n[mcp_servers.mine]\ncommand = \"npx\"\n\n[mcp_servers.litepsm]\ncommand = \"/old/legacy\"\nargs = [\"bridge\"]\n")

			plan, err := tc.adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
			if err != nil {
				t.Fatalf("PlanSetup: %v", err)
			}
			if _, err := tc.adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}
			out, _ := os.ReadFile(path)
			s := string(out)
			if strings.Contains(s, "[mcp_servers.litepsm]") {
				t.Fatalf("legacy table survived:\n%s", s)
			}
			if strings.Count(s, "[mcp_servers.litespm]") != 1 {
				t.Fatalf("expected exactly one bridge table:\n%s", s)
			}
			if !strings.Contains(s, "[mcp_servers.mine]") || !strings.Contains(s, "# keep me") {
				t.Fatalf("foreign content or comment lost:\n%s", s)
			}
		})
	}
}

// TestStripBridgeEntryRemovesLegacyAndCurrent is the unit-level guarantee that
// removal is not name-specific.
func TestStripBridgeEntryRemovesLegacyAndCurrent(t *testing.T) {
	jsonContent := `{"mcpServers":{"mine":{"command":"x"},"litespm":{"command":"y"},"litepsm":{"command":"z"}}}`
	out, removed, err := stripBridgeEntry(jsonContent, removalSpec{Format: FormatJSON, KeyPath: []string{"mcpServers"}})
	if err != nil || !removed {
		t.Fatalf("json strip failed: removed=%v err=%v", removed, err)
	}
	if strings.Contains(out, "litespm") || strings.Contains(out, "litepsm") {
		t.Fatalf("a bridge entry survived JSON removal:\n%s", out)
	}
	if !strings.Contains(out, `"mine"`) {
		t.Fatalf("sibling lost from JSON:\n%s", out)
	}
	if _, err := parseConfigJSON(BridgeTarget{}, []byte(out)); err != nil {
		t.Fatalf("JSON removal produced invalid JSON: %v\n%s", err, out)
	}

	tomlContent := "[mcp_servers.mine]\ncommand = \"x\"\n\n[mcp_servers.litespm]\ncommand = \"y\"\n\n[mcp_servers.litepsm]\ncommand = \"z\"\n"
	out, removed, err = stripBridgeEntry(tomlContent, removalSpec{Format: FormatTOML, KeyPath: []string{"mcp_servers"}})
	if err != nil || !removed {
		t.Fatalf("toml strip failed: removed=%v err=%v", removed, err)
	}
	if strings.Contains(out, "[mcp_servers.litespm]") || strings.Contains(out, "[mcp_servers.litepsm]") {
		t.Fatalf("a bridge table survived TOML removal:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.mine]") {
		t.Fatalf("sibling lost from TOML:\n%s", out)
	}
}

// TestRemoveSetupCleansLegacyOnlyEntry proves an upgraded install that still
// carries only the old entry can be uninstalled cleanly.
func TestRemoveSetupCleansLegacyOnlyEntry(t *testing.T) {
	ctx := context.Background()
	for _, id := range []string{"cursor", "codex", "claude-code"} {
		t.Run(id, func(t *testing.T) {
			home := useTempHome(t)
			adapter, err := GetAdapter(id)
			if err != nil {
				t.Fatal(err)
			}
			path := configPathOfAny(t, adapter)
			if adapter.Descriptor().ConfigFormat == "toml" {
				writeConfig(t, path, "[mcp_servers.mine]\ncommand = \"npx\"\n\n[mcp_servers.litepsm]\ncommand = \"/old/legacy\"\n")
			} else {
				writeConfig(t, path, `{"mcpServers":{"mine":{"command":"npx"},"litepsm":{"command":"/old/legacy"}}}`)
			}

			result, err := RemoveSetup(ctx, adapter, filepath.Join(home, "backups"))
			if err != nil {
				t.Fatalf("RemoveSetup: %v", err)
			}
			if !result.Removed {
				t.Fatalf("legacy-only entry was not reported removed: %+v", result)
			}
			out, _ := os.ReadFile(path)
			if strings.Contains(string(out), "litepsm") || strings.Contains(string(out), "litespm") {
				t.Fatalf("a bridge entry survived removal:\n%s", out)
			}
			if !strings.Contains(string(out), "mine") {
				t.Fatalf("sibling was destroyed:\n%s", out)
			}
		})
	}
}

// TestDetectPreExistingComponentsIgnoresLegacyBridge proves our own legacy
// bridge is never surfaced as a foreign, read-only component.
func TestDetectPreExistingComponentsIgnoresLegacyBridge(t *testing.T) {
	useTempHome(t)
	ctx := context.Background()
	for _, id := range []string{"cursor", "amp", "claude-code", "codex"} {
		t.Run(id, func(t *testing.T) {
			adapter, err := GetAdapter(id)
			if err != nil {
				t.Fatal(err)
			}
			path := configPathOfAny(t, adapter)
			if adapter.Descriptor().ConfigFormat == "toml" {
				writeConfig(t, path, "[mcp_servers.mine]\ncommand = \"npx\"\n\n[mcp_servers.litepsm]\ncommand = \"/old/legacy\"\n")
			} else {
				keyPath := []string{"mcpServers"}
				if g, ok := adapter.(*GenericAdapter); ok {
					keyPath = g.Target.keyPathFor(false)
				}
				writeConfig(t, path, jsonServersSeed(keyPath,
					`"mine":{"command":"npx"},"litepsm":{"command":"/old/legacy"}`))
			}

			comps, err := adapter.DetectPreExistingComponents(ctx)
			if err != nil {
				t.Fatalf("DetectPreExistingComponents: %v", err)
			}
			names := map[string]bool{}
			for _, c := range comps {
				names[c.Name] = true
			}
			if names[legacyServerName] {
				t.Fatalf("legacy bridge %q was reported as a foreign component", legacyServerName)
			}
			if !names["mine"] {
				t.Fatalf("foreign component %q missing: %+v", "mine", comps)
			}
		})
	}
}
