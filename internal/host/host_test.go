package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litepsm/internal/domain"
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
		ProposedContent: initialContent + "\n[mcp_servers.litepsm]\ncommand = \"/usr/local/bin/litepsm\"\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n",
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
	if !strings.Contains(content, "[mcp_servers.litepsm]") {
		t.Errorf("missing litepsm section")
	}
	if !strings.Contains(content, "[mcp_servers.github]") {
		t.Errorf("github section was lost")
	}

	manual := adapter.RenderManualSetup("/opt/litepsm/bin/litepsm")
	if !strings.Contains(manual, "[mcp_servers.litepsm]") {
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
	plan, err := adapter.PlanSetup(ctx, "/usr/bin/litepsm", backupDir)
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

	manual := adapter.RenderManualSetup("/usr/bin/litepsm")
	if !strings.Contains(manual, "claude-code") {
		t.Errorf("manual setup missing host id")
	}
}

func TestOpenCodeAdapter_V1_and_V2(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	backupDir := filepath.Join(tempDir, "backups")

	// Test V1 layout
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
		HostID:     "opencode",
		ConfigPath: v1File,
		ProposedContent: `{
  "mcp": {
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    },
    "litepsm": {
      "command": "litepsm",
      "args": ["bridge", "stdio", "--host", "opencode"]
    }
  }
}`,
	}
	_, err := adapter.ApplySetup(ctx, planV1)
	if err != nil {
		t.Fatalf("ApplySetup V1 failed: %v", err)
	}

	// Test V2 layout
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
		HostID:     "opencode",
		ConfigPath: v2File,
		ProposedContent: `{
  "mcp": {
    "servers": {
      "git": {
        "command": "mcp-git"
      },
      "litepsm": {
        "command": "litepsm",
        "args": ["bridge", "stdio", "--host", "opencode"]
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

	manual := adapter.RenderManualSetup("litepsm")
	if !strings.Contains(manual, "servers") {
		t.Errorf("v2 manual render expected servers key")
	}
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
    "litepsm": {
      "command": "litepsm",
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
		HostID:     "grok",
		ConfigPath: configFile,
		ProposedContent: `[mcp_servers.litepsm]
command = "litepsm"
args = ["bridge", "stdio", "--host", "grok"]
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
	if desc.HostID != "grok" || desc.ConfigFormat != "toml" {
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
	if err != nil || grok.Descriptor().HostID != "grok" {
		t.Errorf("failed to get grok adapter: %v", err)
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
