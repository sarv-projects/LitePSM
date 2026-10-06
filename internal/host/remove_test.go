package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripJSONEntryRemovesOnlyTheBridgeEntry(t *testing.T) {
	content := `{
  // my own servers
  "mcpServers": {
    "mine": { "command": "npx", "args": ["a"] },
    "litespm": { "command": "/bin/litespm", "args": ["bridge", "stdio", "--host", "cursor"] },
    "theirs": { "command": "uvx" }
  },
  "theme": "dark"
}
`
	out, removed, err := stripJSONEntry(content, []string{"mcpServers"})
	if err != nil {
		t.Fatalf("strip failed: %v", err)
	}
	if !removed {
		t.Fatal("entry was not removed")
	}
	if strings.Contains(out, "litespm") {
		t.Fatalf("bridge entry survived:\n%s", out)
	}
	if !strings.Contains(out, "// my own servers") {
		t.Fatalf("comment lost:\n%s", out)
	}
	if !strings.Contains(out, `"mine"`) || !strings.Contains(out, `"theirs"`) {
		t.Fatalf("sibling server lost:\n%s", out)
	}
	if !strings.Contains(out, `"theme": "dark"`) {
		t.Fatalf("unrelated key lost:\n%s", out)
	}
	// Must still be valid JSON(C).
	if _, err := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(out)); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}
}

func TestStripJSONEntryWhenLastMember(t *testing.T) {
	content := `{
  "mcpServers": {
    "mine": { "command": "npx" },
    "litespm": { "command": "/bin/litespm" }
  }
}
`
	out, removed, err := stripJSONEntry(content, []string{"mcpServers"})
	if err != nil || !removed {
		t.Fatalf("strip failed: removed=%v err=%v", removed, err)
	}
	parsed, err := parseConfigJSON(BridgeTarget{}, []byte(out))
	if err != nil {
		t.Fatalf("invalid JSON after removing the last member: %v\n%s", err, out)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Fatalf("expected exactly one remaining server, got %v", servers)
	}
	if _, ok := servers["mine"]; !ok {
		t.Fatalf("the wrong member was removed: %v", servers)
	}
}

func TestStripJSONEntryWhenOnlyMember(t *testing.T) {
	content := `{"mcpServers": {"litespm": {"command": "/bin/litespm"}}}`
	out, removed, err := stripJSONEntry(content, []string{"mcpServers"})
	if err != nil || !removed {
		t.Fatalf("strip failed: removed=%v err=%v", removed, err)
	}
	parsed, err := parseConfigJSON(BridgeTarget{}, []byte(out))
	if err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if len(servers) != 0 {
		t.Fatalf("expected an empty object, got %v", servers)
	}
}

func TestStripJSONEntryAbsentIsNotAnError(t *testing.T) {
	content := `{"mcpServers": {"mine": {"command": "npx"}}}`
	out, removed, err := stripJSONEntry(content, []string{"mcpServers"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Fatal("reported removal when nothing was present")
	}
	if out != content {
		t.Fatalf("file was modified despite no entry:\n got %s\nwant %s", out, content)
	}
}

func TestStripJSONEntryNestedKeyPath(t *testing.T) {
	content := `{
  "mcp": {
    "servers": {
      "litespm": { "type": "local", "command": ["litespm", "bridge"] },
      "keep": { "type": "local", "command": ["x"] }
    }
  }
}
`
	out, removed, err := stripJSONEntry(content, []string{"mcp", "servers"})
	if err != nil || !removed {
		t.Fatalf("strip failed: removed=%v err=%v", removed, err)
	}
	if strings.Contains(out, "litespm") {
		t.Fatalf("entry survived:\n%s", out)
	}
	parsed, err := parseConfigJSON(BridgeTarget{}, []byte(out))
	if err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	servers := parsed["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("sibling lost: %v", servers)
	}
}

func TestStripJSONEntryIsIdempotent(t *testing.T) {
	content := `{"mcpServers": {"a": {}, "litespm": {}, "b": {}}}`
	once, _, err := stripJSONEntry(content, []string{"mcpServers"})
	if err != nil {
		t.Fatal(err)
	}
	twice, removed, err := stripJSONEntry(once, []string{"mcpServers"})
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("second removal reported a change")
	}
	if twice != once {
		t.Fatalf("not idempotent:\n%q\n%q", once, twice)
	}
}

func TestStripTOMLEntryRemovesOnlyOurTable(t *testing.T) {
	content := "# my config\n[other]\nkey = 1\n\n[mcp_servers.litespm]\ncommand = \"/bin/litespm\"\nargs = [\"bridge\"]\n\n[mcp_servers.mine]\ncommand = \"npx\"\n"
	out, removed, err := stripTOMLEntry(content, []string{"mcp_servers"})
	if err != nil || !removed {
		t.Fatalf("strip failed: removed=%v err=%v", removed, err)
	}
	if strings.Contains(out, "[mcp_servers.litespm]") || strings.Contains(out, "/bin/litespm") {
		t.Fatalf("our table survived:\n%s", out)
	}
	for _, want := range []string{"# my config", "[other]", "key = 1", "[mcp_servers.mine]", `command = "npx"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("lost %q:\n%s", want, out)
		}
	}
}

func TestStripTOMLEntryAbsent(t *testing.T) {
	content := "[mcp_servers.mine]\ncommand = \"npx\"\n"
	out, removed, err := stripTOMLEntry(content, []string{"mcp_servers"})
	if err != nil || removed {
		t.Fatalf("expected no removal: removed=%v err=%v", removed, err)
	}
	if out != content {
		t.Fatal("content changed despite nothing to remove")
	}
}

// TestRemoveSetupRoundTrip proves an install can be fully undone: apply the
// bridge entry, remove it, and the file must be byte-identical to the original.
func TestRemoveSetupRoundTrip(t *testing.T) {
	home := useTempHome(t)
	ctx := context.Background()
	backups := filepath.Join(home, "backups")

	for _, id := range []string{"cursor", "zed", "kilo", "codex", "opencode"} {
		t.Run(id, func(t *testing.T) {
			adapter, err := GetAdapter(id)
			if err != nil {
				t.Fatalf("adapter %s missing: %v", id, err)
			}
			isTOML := adapter.Descriptor().ConfigFormat == "toml"

			// Seed a realistic config with foreign content. The path comes from
			// the adapter: on Windows several targets resolve under %APPDATA%,
			// so a hardcoded Unix path would write outside the sandbox.
			path := configPathOfAny(t, adapter)
			original := "{\n  \"mine\": {\"command\": \"npx\"}\n}\n"
			if isTOML {
				original = "[mine]\ncommand = \"npx\"\n"
			}
			writeConfig(t, path, original)

			plan, err := adapter.PlanSetup(ctx, "/bin/litespm", backups)
			if err != nil {
				t.Fatalf("plan install: %v", err)
			}
			if _, err := adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("apply install: %v", err)
			}
			verify, _ := adapter.VerifySetup(ctx)
			if verify.Status != "ready" {
				t.Fatalf("install did not verify: %+v", verify)
			}

			result, err := RemoveSetup(ctx, adapter, backups)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if !result.Removed {
				t.Fatalf("removal reported nothing removed: %+v", result)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(after), "litespm") {
				t.Fatalf("bridge entry survived removal:\n%s", after)
			}
			if !strings.Contains(string(after), "mine") {
				t.Fatalf("foreign content destroyed:\n%s", after)
			}
			verify, _ = adapter.VerifySetup(ctx)
			if verify.Registered {
				t.Fatalf("still registered after removal: %+v", verify)
			}
		})
	}
}

func TestRemoveSetupOnUninstalledHostIsSafe(t *testing.T) {
	home := useTempHome(t)
	ctx := context.Background()

	adapter := adapterFor(t, "cursor")

	// No file at all.
	result, err := RemoveSetup(ctx, adapter, filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("removal of a non-existent config errored: %v", err)
	}
	if result == nil || result.Removed {
		t.Fatalf("expected a not-removed result, got %+v", result)
	}
	if result.Reason == "" {
		t.Fatal("a non-removal must explain itself")
	}

	// File present, no bridge entry.
	path := configPathOf(t, adapter)
	original := `{"mcpServers": {"mine": {"command": "npx"}}}`
	writeConfig(t, path, original)
	result, err = RemoveSetup(ctx, adapter, filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Removed {
		t.Fatal("reported removal when no entry existed")
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatalf("file changed despite no entry:\n%s", after)
	}
}

func TestRemoveFromAllReportsPerHostOutcomes(t *testing.T) {
	home := useTempHome(t)
	ctx := context.Background()
	backups := filepath.Join(home, "backups")

	// Install into two hosts only.
	installed := map[string]bool{}
	for _, id := range []string{"cursor", "zed"} {
		tgt, _ := LookupBridgeTarget(id)
		a := NewGenericAdapter(tgt)
		plan, err := a.PlanSetup(ctx, "/bin/litespm", backups)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.ApplySetup(ctx, plan); err != nil {
			t.Fatal(err)
		}
		installed[id] = true
	}

	results := RemoveFromAll(ctx, backups)
	removed := map[string]bool{}
	for _, r := range results {
		if r.Removed {
			removed[r.HostID] = true
		}
	}
	for id := range installed {
		if !removed[id] {
			t.Errorf("host %s was installed but not removed", id)
		}
	}
	if len(results) < len(installed) {
		t.Fatalf("expected a result per host, got %d", len(results))
	}
}

func TestRemovalSpecForBespokeAdapters(t *testing.T) {
	cases := map[string]removalSpec{
		"claude-code": {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
		"cline":       {Format: FormatJSON, KeyPath: []string{"mcpServers"}},
		"codex":       {Format: FormatTOML, KeyPath: []string{"mcp_servers"}},
		"grok-build":  {Format: FormatTOML, KeyPath: []string{"mcp_servers"}},
	}
	for id, want := range cases {
		adapter, err := GetAdapter(id)
		if err != nil {
			t.Fatalf("adapter %s not registered: %v", id, err)
		}
		got, err := removalSpecFor(adapter)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got.Format != want.Format || strings.Join(got.KeyPath, ".") != strings.Join(want.KeyPath, ".") {
			t.Errorf("%s: got %v/%v want %v/%v", id, got.Format, got.KeyPath, want.Format, want.KeyPath)
		}
	}
}

func TestEveryRegisteredAdapterHasARemovalPath(t *testing.T) {
	for _, adapter := range ListAdapters() {
		id := adapter.Descriptor().HostID
		if _, err := removalSpecFor(adapter); err != nil {
			t.Errorf("host %q cannot be uninstalled: %v", id, err)
		}
	}
}
