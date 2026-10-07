package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestAtomicBackup(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test_config.json")
	backupDir := filepath.Join(tempDir, "backups")

	err := os.WriteFile(configFile, []byte(`{"version": 1}`), 0600)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	backupPath, err := CreateAtomicBackup(configFile, backupDir, "test-host")
	if err != nil {
		t.Fatalf("failed to create backup: %v", err)
	}

	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file does not exist: %v", err)
	}

	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed to read backup: %v", err)
	}

	if string(data) != `{"version": 1}` {
		t.Fatalf("unexpected backup content: %s", string(data))
	}
}

func TestCodexAdapter(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.toml")
	backupDir := filepath.Join(tempDir, "backups")

	initialContent := `# Existing codex config
[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
`
	if err := os.WriteFile(configFile, []byte(initialContent), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	adapter := &CodexAdapter{}
	plan := &HostChangePlan{
		HostID:          "codex",
		ConfigPath:      configFile,
		OriginalContent: initialContent,
		ProposedContent: initialContent + "\n[mcp_servers.litespm]\ncommand = \"/usr/local/bin/litespm\"\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n",
		BackupPath:      filepath.Join(backupDir, "backup.toml"),
	}

	res, err := adapter.ApplySetup(ctx, plan)
	if err != nil {
		t.Fatalf("ApplySetup failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}

	// Verify pre-existing tools are detected and read-only
	// Manually inspect the file with custom adapter or logic
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[mcp_servers.litespm]") {
		t.Errorf("missing litespm section")
	}
	if !strings.Contains(content, "[mcp_servers.github]") {
		t.Errorf("github section was lost")
	}

	manual := adapter.RenderManualSetup("/opt/litespm/bin/litespm")
	if !strings.Contains(manual, "[mcp_servers.litespm]") {
		t.Errorf("manual setup missing section")
	}
}

func TestClaudeCodeAdapter(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".claude.json")
	backupDir := filepath.Join(tempDir, "backups")

	initialContent := `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    }
  }
}`
	if err := os.WriteFile(configFile, []byte(initialContent), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	adapter := &ClaudeCodeAdapter{}
	plan, err := adapter.PlanSetup(ctx, "/usr/bin/litespm", backupDir)
	if err != nil {
		t.Fatalf("PlanSetup failed: %v", err)
	}
	// override path for test
	plan.ConfigPath = configFile
	plan.OriginalContent = initialContent

	res, err := adapter.ApplySetup(ctx, plan)
	if err != nil {
		t.Fatalf("ApplySetup failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}

	manual := adapter.RenderManualSetup("/usr/bin/litespm")
	if !strings.Contains(manual, "claude-code") {
		t.Errorf("manual setup missing host id")
	}
}

// TestOpenCodeAdapter_V1_and_V2 pins the two shapes this adapter must cope
// with on disk: the one documented layout (servers as direct members of `mcp`)
// and the nested `mcp.servers` container older releases wrote, which is read
// but never written.
func TestOpenCodeAdapter_V1_and_V2(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	backupDir := filepath.Join(tempDir, "backups")

	// Documented layout: servers are direct members of `mcp`.
	v1File := filepath.Join(tempDir, "opencode_v1.json")
	v1Content := `{
  "mcp": {
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    }
  }
}`
	if err := os.WriteFile(v1File, []byte(v1Content), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	adapter := &OpenCodeAdapter{}
	planV1 := &HostChangePlan{
		HostID:          "opencode",
		ConfigPath:      v1File,
		OriginalContent: v1Content,
		ProposedContent: `{
  "mcp": {
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    },
    "litespm": {
      "type": "local",
      "command": ["litespm", "bridge", "stdio", "--host", "opencode"]
    }
  }
}`,
	}
	_, err := adapter.ApplySetup(ctx, planV1)
	if err != nil {
		t.Fatalf("ApplySetup V1 failed: %v", err)
	}

	// Legacy nested container written by older releases. Read compatibility
	// only: PlanSetup prunes it when empty and never writes it.
	v2File := filepath.Join(tempDir, "opencode_v2.json")
	v2Content := `{
  "mcp": {
    "servers": {
      "git": {
        "command": "mcp-git"
      }
    }
  }
}`
	if err := os.WriteFile(v2File, []byte(v2Content), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	planV2 := &HostChangePlan{
		HostID:          "opencode",
		ConfigPath:      v2File,
		OriginalContent: v2Content,
		ProposedContent: `{
  "mcp": {
    "servers": {
      "git": {
        "command": "mcp-git"
      },
      "litespm": {
        "type": "local",
        "command": ["litespm", "bridge", "stdio", "--host", "opencode"]
      }
    }
  }
}`,
		BackupPath: filepath.Join(backupDir, "opencode_v2.bak"),
	}
	_, err = adapter.ApplySetup(ctx, planV2)
	if err != nil {
		t.Fatalf("ApplySetup V2 failed: %v", err)
	}

	// The manual snippet is what a user pastes when the wizard cannot write the
	// config itself, so it must show exactly one thing: the documented flat
	// layout PlanSetup writes.
	manual := adapter.RenderManualSetup("/opt/litespm/litespm")
	assertManualSetupIsFlatMCP(t, manual)
}

// assertManualSetupIsFlatMCP pins the OpenCode manual snippet to the single
// documented layout — servers as direct members of `mcp`, each carrying
// `"type": "local"` and a combined command array — which is what
// bespokeEntrySpecs["opencode"] (KeyPath ["mcp"], ShapeLocalArray) writes.
//
// This must fail if anyone reintroduces the nested `mcp.servers` container: it
// is absent from OpenCode's published schema, and the runtime treats it as one
// server definition named "servers" with no `type`, so it refuses to load the
// file. The check decodes the snippet rather than grepping it, because a grep
// can be satisfied by a comment while the pasted JSON stays wrong.
func assertManualSetupIsFlatMCP(t *testing.T, manual string) {
	t.Helper()

	if !strings.Contains(manual, `"mcp"`) {
		t.Errorf("manual snippet does not name the mcp key:\n%s", manual)
	}
	if !strings.Contains(manual, `"type": "local"`) {
		t.Errorf("manual snippet does not declare type local:\n%s", manual)
	}
	if strings.Contains(manual, "servers") {
		t.Errorf("manual snippet names a servers container:\n%s", manual)
	}

	// Strip the leading comment line the way a reader pasting the body does,
	// then decode what is left.
	body := manual
	if i := strings.Index(body, "{"); i >= 0 {
		body = body[i:]
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("manual snippet is not pasteable JSON: %v\n%s", err, manual)
	}
	mcpMap, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("manual snippet missing the mcp object:\n%s", manual)
	}
	if _, nested := mcpMap["servers"]; nested {
		t.Fatalf("manual snippet reintroduced the undocumented mcp.servers nesting:\n%s", manual)
	}
	entry, ok := mcpMap[litespmServerName].(map[string]any)
	if !ok {
		t.Fatalf("manual snippet missing the %s entry directly under mcp:\n%s", litespmServerName, manual)
	}
	if entry["type"] != "local" {
		t.Errorf("manual snippet type = %v, want local", entry["type"])
	}
	command, ok := entry["command"].([]any)
	if !ok {
		t.Fatalf("manual snippet command must be a JSON array, got %T (%v)", entry["command"], entry["command"])
	}
	if len(command) < 2 {
		t.Fatalf("manual snippet command must carry the executable and its args, got %v", command)
	}
	if command[0] != filepath.ToSlash("/opt/litespm/litespm") {
		t.Errorf("manual snippet executable = %v, want the configured binary path", command[0])
	}
	var gotTail []string
	for _, item := range command[1:] {
		s, _ := item.(string)
		gotTail = append(gotTail, s)
	}
	if strings.Join(gotTail, " ") != "bridge stdio --host opencode" {
		t.Errorf("manual snippet bridge args = %v, want bridge stdio --host opencode", gotTail)
	}
	if _, hasArgs := entry["args"]; hasArgs {
		t.Errorf("local entry must not use a separate args key: %v", entry["args"])
	}
}

func TestOpenCodePlanSetup_EmitsLocalTypeAndArrayCommand(t *testing.T) {
	ctx := context.Background()

	// Redirect every home/config override into a throwaway directory so the test
	// never reads or writes the real user configuration. PlanSetup performs no
	// writes itself (ApplySetup does).
	newHermeticHome := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		return home
	}

	assertLocalEntry := func(t *testing.T, content string, nested bool) {
		t.Helper()
		// The entry is spliced in surgically, so its own formatting follows the
		// surrounding document rather than a fixed pretty-printer: assert the
		// semantics after decoding rather than a particular whitespace.
		var root map[string]any
		if err := json.Unmarshal([]byte(content), &root); err != nil {
			t.Fatalf("proposed content is not valid JSON: %v", err)
		}
		mcpMap, ok := root["mcp"].(map[string]any)
		if !ok {
			t.Fatalf("proposed content missing mcp object:\n%s", content)
		}
		container := mcpMap
		if servers, ok := mcpMap["servers"].(map[string]any); ok {
			if !nested {
				t.Fatalf("expected a flat v1 layout but found mcp.servers:\n%s", content)
			}
			container = servers
		} else if nested {
			t.Fatalf("expected a nested v2 layout with mcp.servers:\n%s", content)
		}
		entry, ok := container["litespm"].(map[string]any)
		if !ok {
			t.Fatalf("proposed content missing litespm entry:\n%s", content)
		}
		if entry["type"] != "local" {
			t.Errorf("expected litespm type %q, got %v", "local", entry["type"])
		}
		command, ok := entry["command"].([]any)
		if !ok {
			t.Fatalf("expected command to be a JSON array, got %T (%v)", entry["command"], entry["command"])
		}
		if len(command) < 2 {
			t.Fatalf("expected command array to include the executable and its args, got %v", command)
		}
		var gotTail []string
		for _, item := range command[1:] {
			s, _ := item.(string)
			gotTail = append(gotTail, s)
		}
		if strings.Join(gotTail, " ") != "bridge stdio --host opencode" {
			t.Errorf("unexpected bridge args: got %v", gotTail)
		}
		if _, hasArgs := entry["args"]; hasArgs {
			t.Errorf("local entry must not use a separate args key: %v", entry["args"])
		}
	}

	// OpenCode has one documented layout: servers are direct members of `mcp`.
	// A `mcp.servers` nesting is not in the published schema, and the runtime
	// rejects a member without `type`, so it must never be written.
	t.Run("new_config_defaults_to_flat_mcp", func(t *testing.T) {
		newHermeticHome(t)
		adapter := &OpenCodeAdapter{}
		plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
		if err != nil {
			t.Fatalf("PlanSetup failed: %v", err)
		}
		if plan.HostID != "opencode" {
			t.Errorf("unexpected plan HostID: %s", plan.HostID)
		}
		assertLocalEntry(t, plan.ProposedContent, false)
		if strings.Contains(plan.ProposedContent, `"servers"`) {
			t.Errorf("wrote an undocumented mcp.servers container:\n%s", plan.ProposedContent)
		}
	})

	t.Run("existing_flat_config", func(t *testing.T) {
		home := newHermeticHome(t)
		configDir := filepath.Join(home, ".config", "opencode")
		if err := os.MkdirAll(configDir, 0700); err != nil {
			t.Fatalf("failed to create config dir: %v", err)
		}
		v1 := `{"mcp":{"weather":{"type":"local","command":["python","-m","weather_mcp"]}}}`
		if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), []byte(v1), 0600); err != nil {
			t.Fatalf("failed to write v1 config: %v", err)
		}

		adapter := &OpenCodeAdapter{}
		plan, err := adapter.PlanSetup(ctx, "/opt/litespm/litespm", t.TempDir())
		if err != nil {
			t.Fatalf("PlanSetup failed: %v", err)
		}
		assertLocalEntry(t, plan.ProposedContent, false)
		// The pre-existing v1 entry must be preserved by the merge.
		if !strings.Contains(plan.ProposedContent, "weather_mcp") {
			t.Errorf("existing v1 entry was not preserved:\n%s", plan.ProposedContent)
		}
	})
}

func TestPiAgentAdapter(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "mcp.json")

	adapter := &PiAgentAdapter{}
	plan := &HostChangePlan{
		HostID:     "pi-agent",
		ConfigPath: configFile,
		ProposedContent: `{
  "mcpServers": {
    "litespm": {
      "command": "litespm",
      "args": ["bridge", "stdio", "--host", "pi-agent"]
    }
  }
}`,
	}

	res, err := adapter.ApplySetup(ctx, plan)
	if err != nil {
		t.Fatalf("ApplySetup failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}

	desc := adapter.Descriptor()
	if desc.HostID != "pi-agent" || !desc.SupportsFormElicit {
		t.Errorf("unexpected descriptor: %+v", desc)
	}
}

func TestGrokBuildAdapter(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.toml")

	adapter := &GrokBuildAdapter{}
	plan := &HostChangePlan{
		HostID:     "grok-build",
		ConfigPath: configFile,
		ProposedContent: `[mcp_servers.litespm]
command = "litespm"
args = ["bridge", "stdio", "--host", "grok-build"]
`,
	}

	res, err := adapter.ApplySetup(ctx, plan)
	if err != nil {
		t.Fatalf("ApplySetup failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}

	desc := adapter.Descriptor()
	if desc.HostID != "grok-build" || desc.ConfigFormat != "toml" {
		t.Errorf("unexpected descriptor: %+v", desc)
	}
}

func TestRegistry(t *testing.T) {
	adapters := ListAdapters()
	if len(adapters) < 6 {
		t.Fatalf("expected at least 6 adapters, got %d", len(adapters))
	}

	codex, err := GetAdapter("codex")
	if err != nil || codex.Descriptor().HostID != "codex" {
		t.Errorf("failed to get codex adapter: %v", err)
	}

	claude, err := GetAdapter("claude-code")
	if err != nil || claude.Descriptor().HostID != "claude-code" {
		t.Errorf("failed to get claude-code adapter: %v", err)
	}

	opencode, err := GetAdapter("opencode")
	if err != nil || opencode.Descriptor().HostID != "opencode" {
		t.Errorf("failed to get opencode adapter: %v", err)
	}

	cline, err := GetAdapter("cline")
	if err != nil || cline.Descriptor().HostID != "cline" {
		t.Errorf("failed to get cline adapter: %v", err)
	}

	pi, err := GetAdapter("pi")
	if err != nil || pi.Descriptor().HostID != "pi-agent" {
		t.Errorf("failed to get pi adapter: %v", err)
	}

	grok, err := GetAdapter("grok-build")
	if err != nil || grok.Descriptor().HostID != "grok-build" {
		t.Errorf("failed to get grok adapter: %v", err)
	}

	// The legacy "grok" registry alias must continue to resolve to the same
	// canonical grok-build adapter.
	grokAlias, err := GetAdapter("grok")
	if err != nil || grokAlias.Descriptor().HostID != "grok-build" {
		t.Errorf("failed to get grok alias adapter: %v", err)
	}

	_, err = GetAdapter("nonexistent")
	if err == nil {
		t.Errorf("expected error for nonexistent adapter")
	}

	ctx := context.Background()
	_ = domain.ScopeUser
	results, err := DetectInstalledHosts(ctx)
	if err != nil {
		t.Fatalf("DetectInstalledHosts failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected detection results")
	}
}

func TestGoldenFixturesCompliance(t *testing.T) {
	ctx := context.Background()

	// 1. Cline Golden Fixture
	t.Run("Cline", func(t *testing.T) {
		data, err := os.ReadFile("../../fixtures/hosts/cline/existing_servers_cline_mcp_settings.json")
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		tempFile := filepath.Join(t.TempDir(), "cline_mcp_settings.json")
		_ = os.WriteFile(tempFile, data, 0600)

		adapter := &ClineAdapter{}
		plan := &HostChangePlan{
			HostID:          "cline",
			ConfigPath:      tempFile,
			OriginalContent: string(data),
			ProposedContent: `{"mcpServers":{"filesystem":{"command":"npx"},"github":{"command":"npx"},"litespm":{"command":"litespm","args":["bridge","stdio","--host","cline"]}}}`,
		}
		res, err := adapter.ApplySetup(ctx, plan)
		if err != nil || !res.Success {
			t.Fatalf("ApplySetup failed: %v", err)
		}
		comps, err := adapter.DetectPreExistingComponents(ctx)
		_ = comps // verify detection method exists and runs
	})

	// 2. Pi Agent Golden Fixture (mcp.servers layout)
	t.Run("PiAgent_Nested", func(t *testing.T) {
		data, err := os.ReadFile("../../fixtures/hosts/pi/valid_mcp.json")
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		tempFile := filepath.Join(t.TempDir(), "mcp.json")
		_ = os.WriteFile(tempFile, data, 0600)

		// Test manual parsing of the fixture
		var rootMap map[string]any
		_ = json.Unmarshal(data, &rootMap)
		mcpVal, ok := rootMap["mcp"].(map[string]any)
		if !ok {
			t.Fatalf("expected mcp root key")
		}
		serversVal, ok := mcpVal["servers"].(map[string]any)
		if !ok || serversVal["local_bash"] == nil {
			t.Fatalf("expected local_bash under mcp.servers")
		}
	})

	// 3. Grok Golden Fixture
	t.Run("Grok_TOML", func(t *testing.T) {
		data, err := os.ReadFile("../../fixtures/hosts/grok/valid_grok_config.toml")
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		comps := parseTomlMcpComponents(string(data), "/path/to/config.toml")
		if len(comps) != 1 || comps[0].Name != "sqlite" {
			t.Fatalf("expected sqlite component, got %+v", comps)
		}
		if comps[0].Command != "uvx" || len(comps[0].Args) != 3 {
			t.Fatalf("unexpected sqlite command/args: %+v", comps[0])
		}
	})

	// 4. Codex Golden Fixture
	t.Run("Codex_TOML", func(t *testing.T) {
		data, err := os.ReadFile("../../fixtures/hosts/codex/config.toml")
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		comps := parseTomlMcpComponents(string(data), "/path/to/config.toml")
		if len(comps) != 1 || comps[0].Name != "memory" {
			t.Fatalf("expected memory component, got %+v", comps)
		}
		if comps[0].Command != "npx" || len(comps[0].Args) != 2 {
			t.Fatalf("unexpected memory command/args: %+v", comps[0])
		}
	})

	// 5. OpenCode Golden Fixtures (explicit local type + array command)
	t.Run("OpenCode_LocalShape", func(t *testing.T) {
		for _, name := range []string{"opencode_v1.json", "opencode_v2.json"} {
			data, err := os.ReadFile(filepath.Join("../../fixtures/hosts/opencode", name))
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", name, err)
			}
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("fixture %s is not valid JSON: %v", name, err)
			}
			mcpMap, ok := root["mcp"].(map[string]any)
			if !ok {
				t.Fatalf("fixture %s missing mcp object", name)
			}
			servers := mcpMap
			if nested, ok := mcpMap["servers"].(map[string]any); ok {
				servers = nested
			}
			for entryName, raw := range servers {
				entry, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("fixture %s entry %s is not an object", name, entryName)
				}
				if entry["type"] != "local" {
					t.Errorf("fixture %s entry %s missing type=local", name, entryName)
				}
				if _, ok := entry["command"].([]any); !ok {
					t.Errorf("fixture %s entry %s command must be a JSON array", name, entryName)
				}
			}
		}
	})
}
