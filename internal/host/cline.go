package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// ClineAdapter manages integration with Cline VS Code Extension.
type ClineAdapter struct{}

func (a *ClineAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "cline",
		DisplayName:            "Cline (VS Code Extension)",
		SupportedVersions:      []string{">=2.0.0"},
		DefaultConfigFileName:  "cline_mcp_settings.json",
		ConfigFormat:           "json",
		SupportsFormElicit:     true,
		RequiresBootstrapSkill: false,
		SlashCommandTrigger:    "/litepsm",
	}
}

func (a *ClineAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()

	var candidates []string
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates, filepath.Join(appData, "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
			candidates = append(candidates, filepath.Join(appData, "Code - Insiders", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
		}
		candidates = append(candidates, filepath.Join(homeDir, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, filepath.Join(homeDir, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
		candidates = append(candidates, filepath.Join(homeDir, "Library", "Application Support", "Code - Insiders", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
	} else {
		// Linux / Unix
		candidates = append(candidates, filepath.Join(homeDir, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".config", "Code - Insiders", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return filepath.Join(homeDir, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), nil
}

func (a *ClineAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := "{}"
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "cline")
	if err != nil {
		return nil, err
	}

	var rootMap map[string]any
	if err := json.Unmarshal([]byte(origContent), &rootMap); err != nil {
		rootMap = make(map[string]any)
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	mcpServers["litepsm"] = map[string]any{
		"command": filepath.ToSlash(binaryPath),
		"args":    []string{"bridge", "stdio", "--host", "cline"},
	}
	rootMap["mcpServers"] = mcpServers

	proposedBytes, err := json.MarshalIndent(rootMap, "", "  ")
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "cline",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: string(proposedBytes),
		BackupPath:      backupPath,
	}, nil
}

func (a *ClineAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
	dir := filepath.Dir(plan.ConfigPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(plan.ConfigPath, []byte(plan.ProposedContent), 0600); err != nil {
		return nil, fmt.Errorf("failed to write config %s: %w", plan.ConfigPath, err)
	}

	return &HostApplyResult{
		HostID:     plan.HostID,
		ConfigPath: plan.ConfigPath,
		BackupPath: plan.BackupPath,
		Success:    true,
	}, nil
}

func (a *ClineAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "cline", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "cline", ConfigPath: configPath, Status: "missing"}, nil
	}

	var rootMap map[string]any
	if err := json.Unmarshal(data, &rootMap); err != nil {
		return &HostVerification{HostID: "cline", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	mcpServers, ok := rootMap["mcpServers"].(map[string]any)
	if !ok {
		return &HostVerification{HostID: "cline", ConfigPath: configPath, Status: "missing"}, nil
	}

	_, registered := mcpServers["litepsm"]
	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "cline",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *ClineAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
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

func (a *ClineAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`// Add to cline_mcp_settings.json:
"mcpServers": {
  "litepsm": {
    "command": %q,
    "args": ["bridge", "stdio", "--host", "cline"]
  }
}
`, cleanBin)
}
