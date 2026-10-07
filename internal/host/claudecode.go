package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sarv-projects/litespm/internal/domain"
)

// ClaudeCodeAdapter manages integration with Claude Code CLI.
type ClaudeCodeAdapter struct{}

func (a *ClaudeCodeAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "claude-code",
		DisplayName:            "Claude Code CLI",
		SupportedVersions:      []string{">=0.1.0"},
		DefaultConfigFileName:  ".claude.json",
		ConfigFormat:           "json",
		SupportsFormElicit:     false,
		RequiresBootstrapSkill: true,
		SlashCommandTrigger:    "/marketplace",
	}
}

func (a *ClaudeCodeAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()
	return filepath.Join(homeDir, ".claude.json"), nil
}

func (a *ClaudeCodeAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := "{}"
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "claude-code")
	if err != nil {
		return nil, err
	}

	// Surgical splice, not parse-and-re-serialize: `.claude.json` is a large
	// shared settings document, so comments, key order, indentation and the
	// trailing newline must survive byte-for-byte.
	proposed, err := renderBridgeEntryJSON("claude-code", origContent, []string{"mcpServers"}, map[string]any{
		"command": filepath.ToSlash(binaryPath),
		"args":    []string{"bridge", "stdio", "--host", "claude-code"},
	})
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "claude-code",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

func (a *ClaudeCodeAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
	if err := ApplyPlanWrite(plan); err != nil {
		return nil, err
	}

	return &HostApplyResult{
		HostID:     plan.HostID,
		ConfigPath: plan.ConfigPath,
		BackupPath: plan.BackupPath,
		Success:    true,
	}, nil
}

func (a *ClaudeCodeAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "claude-code", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "claude-code", ConfigPath: configPath, Status: "missing"}, nil
	}

	rootMap, err := parseHostJSON(data)
	if err != nil {
		return &HostVerification{HostID: "claude-code", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		return &HostVerification{HostID: "claude-code", ConfigPath: configPath, Status: "missing"}, nil
	}

	_, registered := mcpServers["litespm"]
	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "claude-code",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *ClaudeCodeAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil
	}

	rootMap, err := parseHostJSON(data)
	if err != nil {
		return nil, nil
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		return nil, nil
	}

	var results []PreExistingComponent
	for name, details := range mcpServers {
		if name == litespmServerName || name == legacyServerName {
			continue
		}
		comp := PreExistingComponent{
			Name:       name,
			Kind:       "mcp",
			ReadOnly:   true,
			SourcePath: configPath,
		}
		if dMap, ok := details.(map[string]any); ok {
			if cmd, ok := dMap["command"].(string); ok {
				comp.Command = cmd
			}
			if args, ok := dMap["args"].([]any); ok {
				for _, arg := range args {
					if argStr, ok := arg.(string); ok {
						comp.Args = append(comp.Args, argStr)
					}
				}
			}
		}
		results = append(results, comp)
	}

	return results, nil
}

func (a *ClaudeCodeAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`// Add to ~/.claude.json under "mcpServers":
"mcpServers": {
  "litespm": {
    "command": %q,
    "args": ["bridge", "stdio", "--host", "claude-code"]
  }
}
`, cleanBin)
}
