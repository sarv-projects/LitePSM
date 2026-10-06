package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin host-resolution behaviour that the 2026-10-06 documentation
// audit found wrong. Each one corresponds to a defect where LiteSPM wrote a
// bridge entry that the host never read — and reported success, because our own
// verify re-read the same wrong path.

// TestManualSetupSnippetIsPasteableJSON is table-wide on purpose: the manual
// snippet is the wizard's last-resort fallback when it cannot write a config
// itself, so every row must hand the user something that can be pasted. Two
// defects lived here: the data-driven snippet emitted the entry object where
// the server name belongs (invalid JSON for all 44 rows), and a row whose
// config format is not JSON must not be parsed as JSON at all.
func TestManualSetupSnippetIsPasteableJSON(t *testing.T) {
	for _, adapter := range ListAdapters() {
		id := adapter.Descriptor().HostID
		snippet := adapter.RenderManualSetup("/opt/litespm/litespm")
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		if !strings.Contains(snippet, litespmServerName) {
			t.Errorf("%s: manual snippet never names the server:\n%s", id, snippet)
		}
		if adapter.Descriptor().ConfigFormat != "json" {
			continue
		}
		// A snippet may be a whole document or a member to merge; only a snippet
		// that presents itself as a document must parse as one.
		body := snippet
		if i := strings.Index(body, "\n"); i >= 0 && strings.HasPrefix(body, "#") {
			body = body[i+1:]
		}
		body = strings.TrimSpace(body)
		if !strings.HasPrefix(body, "{") {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(body), &root); err != nil {
			t.Errorf("%s: manual snippet is not valid JSON: %v\n%s", id, err, snippet)
		}
	}
}

// TestAmpWritesFlatContainerKey pins that a dotted container name is a literal
// member, not a nested object. Amp's published settings schema declares the
// property as "amp.mcpServers" with additionalProperties:false, so the nested
// form we used to write was both unread and schema-invalid.
func TestAmpWritesFlatContainerKey(t *testing.T) {
	home := useTempHome(t)
	generic, err := GetAdapter("amp")
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := generic.(*GenericAdapter)
	if !ok {
		t.Fatalf("amp is not data-driven: %T", generic)
	}
	path := configPathOf(t, adapter)
	writeConfig(t, path, `{"amp.keepMe":true}`)

	plan, err := adapter.PlanSetup(context.Background(), "/opt/litespm/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	out, err := parseHostJSON([]byte(plan.ProposedContent))
	if err != nil {
		t.Fatalf("proposed config does not parse: %v", err)
	}
	if _, nested := out["amp"]; nested {
		t.Errorf("wrote a nested amp object Amp's schema rejects:\n%s", plan.ProposedContent)
	}
	servers, ok := out["amp.mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("flat container missing:\n%s", plan.ProposedContent)
	}
	if _, ok := servers[litespmServerName]; !ok {
		t.Errorf("bridge entry missing:\n%s", plan.ProposedContent)
	}
	if v, ok := out["amp.keepMe"]; !ok || v != true {
		t.Errorf("pre-existing sibling key lost:\n%s", plan.ProposedContent)
	}
}

// TestCrushEntryCarriesRequiredType pins the one host whose published schema
// requires `type`: Crush's decoder has no default, so an entry without it
// matches no transport case and no server is ever started.
func TestCrushEntryCarriesRequiredType(t *testing.T) {
	home := useTempHome(t)
	generic, err := GetAdapter("crush")
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := generic.(*GenericAdapter)
	if !ok {
		t.Fatalf("crush is not data-driven: %T", generic)
	}
	path := configPathOf(t, adapter)
	writeConfig(t, path, `{"mcp":{}}`)

	plan, err := adapter.PlanSetup(context.Background(), "/opt/litespm/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("PlanSetup: %v", err)
	}
	out, err := parseHostJSON([]byte(plan.ProposedContent))
	if err != nil {
		t.Fatalf("proposed config does not parse: %v", err)
	}
	entry, ok := out["mcp"].(map[string]any)[litespmServerName].(map[string]any)
	if !ok {
		t.Fatalf("entry missing:\n%s", plan.ProposedContent)
	}
	if entry["type"] != "stdio" {
		t.Errorf("crush requires an explicit type; got %v:\n%s", entry["type"], plan.ProposedContent)
	}
}

// TestBespokeAdaptersHonourDocumentedHomeOverrides pins that a relocated home is
// honoured. Every one of these was a silent failure: the bridge went to the
// default location while the host read the overridden one.
func TestBespokeAdaptersHonourDocumentedHomeOverrides(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		id      string
		env     string
		dir     string
		wantEnd string
	}{
		{"codex", "CODEX_HOME", "alt-codex", "config.toml"},
		{"grok-build", "GROK_HOME", "alt-grok", "config.toml"},
		{"pi-agent", "PI_CODING_AGENT_DIR", "alt-pi", "mcp.json"},
		{"opencode", "OPENCODE_CONFIG_DIR", "alt-opencode", "opencode.json"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			home := useTempHome(t)
			dir := filepath.Join(home, tc.dir)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(tc.env, dir)
			adapter, err := GetAdapter(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			got, err := adapter.DetectConfig(ctx, "user")
			if err != nil {
				t.Fatalf("DetectConfig: %v", err)
			}
			if filepath.ToSlash(got) != filepath.ToSlash(filepath.Join(dir, tc.wantEnd)) {
				t.Errorf("%s ignored: got %s", tc.env, got)
			}
		})
	}
}

// TestPiExtensionPathFollowsAgentDir keeps the companion extension file in the
// same overridden directory as the MCP config.
func TestPiExtensionPathFollowsAgentDir(t *testing.T) {
	home := useTempHome(t)
	dir := filepath.Join(home, "alt-pi")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	adapter := &PiAgentAdapter{}
	want := filepath.Join(dir, "extensions", "litespm.ts")
	if adapter.ExtensionPath() != want {
		t.Errorf("extension path ignores the override: got %s want %s", adapter.ExtensionPath(), want)
	}
}

// TestClineUsesSharedSettingsPath pins the path Cline actually reads. The file
// moved out of the VS Code extension's globalStorage into a location shared by
// every Cline client; globalStorage is now read only by a one-shot migration, so
// writing there is a no-op on any current install.
func TestClineUsesSharedSettingsPath(t *testing.T) {
	home := useTempHome(t)
	want := filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json")

	t.Run("default", func(t *testing.T) {
		adapter := &ClineAdapter{}
		got, err := adapter.DetectConfig(context.Background(), "user")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	})

	t.Run("env_override", func(t *testing.T) {
		override := filepath.Join(home, "custom", "cline.json")
		t.Setenv("CLINE_MCP_SETTINGS_PATH", override)
		adapter := &ClineAdapter{}
		got, err := adapter.DetectConfig(context.Background(), "user")
		if err != nil {
			t.Fatal(err)
		}
		if got != override {
			t.Errorf("CLINE_MCP_SETTINGS_PATH ignored: got %s", got)
		}
	})

	t.Run("legacy_file_still_found", func(t *testing.T) {
		// A client too old to have migrated still reads globalStorage, so an
		// existing legacy file must win over creating the shared one.
		legacy := filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		adapter := &ClineAdapter{}
		got, err := adapter.DetectConfig(context.Background(), "user")
		if err != nil {
			t.Fatal(err)
		}
		if got != legacy {
			t.Errorf("legacy config not found: got %s want %s", got, legacy)
		}
	})
}
