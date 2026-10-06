package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// A host config is somebody else's file. These tests pin the guarantee that a
// bridge setup touches exactly one member and nothing else: comments, key
// order, indentation, unknown keys and the trailing newline all survive, and
// removal restores the original bytes.

// spliceHome isolates a test from the real user configuration: these adapters
// read and write whatever HOME points at.
func spliceHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

func TestBespokeJSONAdaptersSpliceWithoutRewriting(t *testing.T) {
	ctx := context.Background()

	t.Run("cline_jsonc_comments_preserved", func(t *testing.T) {
		home := spliceHome(t)
		dir := filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "cline_mcp_settings.json")
		original := "{\n  // keep my cline settings\n  \"clineSetting\": true,\n" +
			"  \"mcpServers\": {\n    // an existing server of mine\n" +
			"    \"mine\": { \"command\": \"npx\" }\n  }\n}\n"
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}

		adapter := &ClineAdapter{}
		plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
		if err != nil {
			t.Fatalf("PlanSetup on a JSONC config must succeed, got: %v", err)
		}
		if _, err := adapter.ApplySetup(ctx, plan); err != nil {
			t.Fatalf("ApplySetup: %v", err)
		}
		verify, _ := adapter.VerifySetup(ctx)
		if !verify.Registered {
			t.Fatalf("setup did not verify: %+v", verify)
		}

		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := string(after)
		for _, want := range []string{
			"// keep my cline settings",
			"// an existing server of mine",
			`"clineSetting": true`,
			`"mine": { "command": "npx" }`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("setup destroyed %q:\n%s", want, got)
			}
		}
		if !strings.HasSuffix(got, "}\n") {
			t.Errorf("trailing newline lost:\n%q", got)
		}

		// Removal restores the original file byte for byte.
		if res, err := RemoveSetup(ctx, adapter, filepath.Dir(plan.BackupPath)); err != nil || !res.Removed {
			t.Fatalf("remove failed: %+v %v", res, err)
		}
		restored, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(restored) != original {
			t.Errorf("config not restored byte-for-byte:\n--- want ---\n%s\n--- got ---\n%s", original, restored)
		}
	})

	t.Run("claude_code_only_the_entry_changes", func(t *testing.T) {
		home := spliceHome(t)
		path := filepath.Join(home, ".claude.json")
		// Compact, oddly ordered, with unknown keys: everything but the
		// mcpServers subtree must come back unchanged.
		original := `{"zzz":{"deep":[1,2]},"mcpServers":{"mine":{"command":"npx","args":["-y","mine"]}},"theme":"One Dark"}`
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}

		adapter := &ClaudeCodeAdapter{}
		plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
		if err != nil {
			t.Fatalf("PlanSetup: %v", err)
		}
		got := plan.ProposedContent
		for _, want := range []string{
			`"zzz":{"deep":[1,2]}`,
			`"mine":{"command":"npx","args":["-y","mine"]}`,
			`"theme":"One Dark"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("reformatting or key loss: %q missing from:\n%s", want, got)
			}
		}
		if !strings.HasPrefix(got, `{"zzz":`) {
			t.Errorf("key order was not preserved:\n%s", got)
		}
		if strings.Count(got, `"mcpServers"`) != 1 {
			t.Errorf("expected exactly one mcpServers key:\n%s", got)
		}
	})

	t.Run("opencode_v2_nested_path_has_no_extra_level", func(t *testing.T) {
		home := spliceHome(t)
		dir := filepath.Join(home, ".config", "opencode")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "opencode.json")
		if err := os.WriteFile(path, []byte(`{"mine":{"command":"npx"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		adapter := &OpenCodeAdapter{}
		plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
		if err != nil {
			t.Fatalf("PlanSetup: %v", err)
		}
		// A config with no `mcp` key gets the v2 nested layout, exactly once.
		if strings.Count(plan.ProposedContent, `"mcp"`) != 1 {
			t.Errorf("expected one mcp object, got:\n%s", plan.ProposedContent)
		}
		if strings.Contains(plan.ProposedContent, `"mcp":{"mcp"`) {
			t.Errorf("spurious extra mcp level:\n%s", plan.ProposedContent)
		}
		merged, err := parseHostJSON([]byte(plan.ProposedContent))
		if err != nil {
			t.Fatalf("merged config does not parse: %v", err)
		}
		servers, ok := merged["mcp"].(map[string]any)["servers"].(map[string]any)
		if !ok {
			t.Fatalf("mcp.servers missing:\n%s", plan.ProposedContent)
		}
		if _, ok := servers[litespmServerName]; !ok {
			t.Errorf("litespm entry missing under mcp.servers:\n%s", plan.ProposedContent)
		}
	})
}

// TestBespokeJSONAdaptersCreateMissingConfigFile covers the first-run path: no
// config file at all. The container and the entry are created, and the document
// parses afterwards — the splice must work on an empty starting point, not only
// on a file that already had the container.
func TestBespokeJSONAdaptersCreateMissingConfigFile(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		id      string
		keyPath []string
	}{
		{"claude-code", []string{"mcpServers"}},
		{"cline", []string{"mcpServers"}},
		{"pi-agent", []string{"mcpServers"}},
		{"opencode", []string{"mcp", "servers"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			spliceHome(t)
			adapter, err := GetAdapter(tc.id)
			if err != nil {
				t.Fatalf("adapter %s not registered: %v", tc.id, err)
			}
			plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
			if err != nil {
				t.Fatalf("PlanSetup on a missing config: %v", err)
			}
			if _, err := adapter.ApplySetup(ctx, plan); err != nil {
				t.Fatalf("ApplySetup: %v", err)
			}
			data, err := os.ReadFile(plan.ConfigPath)
			if err != nil {
				t.Fatalf("config was not created: %v", err)
			}
			root, err := parseHostJSON(data)
			if err != nil {
				t.Fatalf("created config does not parse: %v\n%s", err, data)
			}
			if !inspectEntry(root, tc.keyPath) {
				t.Errorf("bridge entry missing at %v:\n%s", tc.keyPath, data)
			}
			verify, err := adapter.VerifySetup(ctx)
			if err != nil {
				t.Fatalf("VerifySetup: %v", err)
			}
			if !verify.Registered {
				t.Errorf("setup did not verify: %+v", verify)
			}
		})
	}
}

// TestMergeJSONEntrySurgicalTwoLevelKeyPath pins the container-chain builder:
// a two-level path must create exactly those two levels and put `litespm` in
// the innermost one. The previous helper dropped the intermediate name, which
// produced mcp.mcp.litespm for opencode's mcp.servers path.
func TestMergeJSONEntrySurgicalTwoLevelKeyPath(t *testing.T) {
	got, err := mergeJSONEntrySurgical(`{"mine":{"command":"npx"}}`, []string{"mcp", "servers"},
		map[string]any{"type": "local", "command": []string{"/bin/litespm"}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	root, err := parseHostJSON([]byte(got))
	if err != nil {
		t.Fatalf("merged text does not parse: %v", err)
	}
	mcpMap, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp missing: %s", got)
	}
	if inner, dup := mcpMap["mcp"]; dup {
		t.Errorf("spurious extra mcp level: %v\n%s", inner, got)
	}
	servers, ok := mcpMap["servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.servers missing: %s", got)
	}
	if _, ok := servers[litespmServerName]; !ok {
		t.Errorf("litespm missing under mcp.servers: %s", got)
	}
	if _, ok := root["mine"]; !ok {
		t.Errorf("pre-existing member destroyed: %s", got)
	}
}

// TestOpenCodeRemovalFindsEntryInEveryLayout pins that removal looks where the
// entry actually is. OpenCode has three documented container shapes, and the
// adapter writes one of them; a user (or an earlier version) may have a
// different one, and `host remove` must not silently leave a bridge
// registration behind.
func TestOpenCodeRemovalFindsEntryInEveryLayout(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		config string
	}{
		{"v1 flat under mcp", `{"mcp":{"mine":{"command":"npx"},"litespm":{"type":"local","command":["/bin/litespm"]}}}`},
		{"v2 nested", `{"mcp":{"servers":{"mine":{"command":"npx"},"litespm":{"type":"local","command":["/bin/litespm"]}}}}`},
		{"mcpServers spelling", `{"mcpServers":{"mine":{"command":"npx"},"litespm":{"command":"/bin/litespm"}}}`},
		// Realistic JSONC: the entry sits on its own line, so removal is clean.
		{"jsonc with comments", "{\n  // mine\n  \"mcp\": {\n    \"mine\": {\"command\": \"npx\"},\n    // bridge\n    \"litespm\": {\"command\": \"/bin/litespm\"}\n  }\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := spliceHome(t)
			dir := filepath.Join(home, ".config", "opencode")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.json")
			if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			adapter := &OpenCodeAdapter{}
			result, err := RemoveSetup(ctx, adapter, t.TempDir())
			if err != nil {
				t.Fatalf("RemoveSetup: %v", err)
			}
			if !result.Removed {
				t.Fatalf("removal reported nothing removed: %+v", result)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(after), litespmServerName) {
				t.Errorf("bridge entry survived removal:\n%s", after)
			}
			if !strings.Contains(string(after), "mine") {
				t.Errorf("foreign content destroyed:\n%s", after)
			}
			if _, err := parseHostJSON(after); err != nil {
				t.Errorf("file no longer parses: %v\n%s", err, after)
			}
		})
	}
}

// TestRemovalRefusesRatherThanCorruptsUnrepairableJSON pins the fail-closed
// contract for a shape the surgical remover cannot repair: a `//` comment
// immediately above the entry, with the object's closing brace on the same
// line, so the brace ends up inside the comment. Rather than guess, the
// remover must refuse and leave the file untouched — a failed removal is
// recoverable, a corrupted config is not.
func TestRemovalRefusesRatherThanCorruptsUnrepairableJSON(t *testing.T) {
	home := spliceHome(t)
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.json")
	original := "{\n  // mine\n  \"mcp\": {\"mine\":{\"command\":\"npx\"},\n  // bridge\n  \"litespm\":{\"command\":\"/bin/litespm\"}}\n}"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &OpenCodeAdapter{}
	result, err := RemoveSetup(context.Background(), adapter, t.TempDir())
	if err == nil && result.Removed {
		t.Fatalf("removal claimed success on an unrepairable document: %+v", result)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Errorf("a refused removal must leave the file byte-for-byte:\n%s", after)
	}
}

// TestParseHostJSONToleratesComments keeps the read paths usable for configs
// that legitimately contain comments.
func TestParseHostJSONToleratesComments(t *testing.T) {
	root, err := parseHostJSON([]byte("{\n // a comment\n \"a\": 1,\n /* block */\n \"b\": {\"c\": 2}\n}"))
	if err != nil {
		t.Fatalf("commented config must parse: %v", err)
	}
	if root["a"] != float64(1) {
		t.Errorf("value lost: %+v", root)
	}
	if _, err := parseHostJSON([]byte(`{"a": `)); err == nil {
		t.Error("genuinely invalid JSON must still be refused")
	}
}

var _ = domain.ScopeUser // keep the domain import meaningful for readers of this file
