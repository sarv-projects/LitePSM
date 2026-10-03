package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
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

	var rootMap map[string]any
	if strings.TrimSpace(origContent) != "" && strings.TrimSpace(origContent) != "{}" {
		if err := json.Unmarshal([]byte(origContent), &rootMap); err != nil {
			return nil, fmt.Errorf("failed to parse existing claude config %s: %w", configPath, err)
		}
	}
	if rootMap == nil {
		rootMap = make(map[string]any)
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	mcpServers["litepsm"] = map[string]any{
		"command": filepath.ToSlash(binaryPath),
		"args":    []string{"bridge", "stdio", "--host", "claude-code"},
	}
	rootMap["mcpServers"] = mcpServers

	proposedBytes, err := json.MarshalIndent(rootMap, "", "  ")
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "claude-code",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: string(proposedBytes),
		BackupPath:      backupPath,
	}, nil
}

func (a *ClaudeCodeAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
	if err := AtomicWriteFile(plan.ConfigPath, []byte(plan.ProposedContent), 0600); err != nil {
		return nil, fmt.Errorf("failed to write config %s: %w", plan.ConfigPath, err)
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

	var rootMap map[string]any
	if err := json.Unmarshal(data, &rootMap); err != nil {
		return &HostVerification{HostID: "claude-code", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		return &HostVerification{HostID: "claude-code", ConfigPath: configPath, Status: "missing"}, nil
	}

	_, registered := mcpServers["litepsm"]
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

	var rootMap map[string]any
	if err := json.Unmarshal(data, &rootMap); err != nil {
		return nil, nil
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		return nil, nil
	}

	var results []PreExistingComponent
	for name, details := range mcpServers {
		if name == "litepsm" {
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
  "litepsm": {
    "command": %q,
    "args": ["bridge", "stdio", "--host", "claude-code"]
  }
}
`, cleanBin)
}
