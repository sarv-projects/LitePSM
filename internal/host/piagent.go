package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// PiAgentAdapter manages integration with Pi Agent (pi-coding-agent).
type PiAgentAdapter struct{}

func (a *PiAgentAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "pi-agent",
		DisplayName:            "Pi Agent (Terminal Agent)",
		SupportedVersions:      []string{">=0.5.0"},
		DefaultConfigFileName:  "mcp.json",
		ConfigFormat:           "json",
		SupportsFormElicit:     true,
		RequiresBootstrapSkill: false,
		SlashCommandTrigger:    "/marketplace",
	}
}

func (a *PiAgentAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()

	var candidates []string
	if runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "agent", "mcp.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "config.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "mcp.json"))
	} else {
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "agent", "mcp.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "config.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".pi", "mcp.json"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return filepath.Join(homeDir, ".pi", "agent", "mcp.json"), nil
}

func (a *PiAgentAdapter) ExtensionPath() string {
	return filepath.Join(resolveHomeDir(), ".pi", "agent", "extensions", "litespm.ts")
}

func (a *PiAgentAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := "{}"
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "pi-agent")
	if err != nil {
		return nil, err
	}

	var rootMap map[string]any
	if strings.TrimSpace(origContent) != "" && strings.TrimSpace(origContent) != "{}" {
		if err := json.Unmarshal([]byte(origContent), &rootMap); err != nil {
			return nil, fmt.Errorf("failed to parse existing pi-agent config %s: %w", configPath, err)
		}
	}
	if rootMap == nil {
		rootMap = make(map[string]any)
	}

	bridgeEntry := map[string]any{
		"command": filepath.ToSlash(binaryPath),
		"args":    []string{"bridge", "stdio", "--host", "pi-agent"},
	}

	// Check if "mcp.servers" or "mcp" exists vs "mcpServers". A pre-rename
	// `litepsm` entry is adopted (deleted) from whichever container is used, so
	// only one bridge server remains.
	if mcpVal, ok := rootMap["mcp"].(map[string]any); ok {
		if serversVal, ok := mcpVal["servers"].(map[string]any); ok {
			delete(serversVal, legacyServerName)
			serversVal["litespm"] = bridgeEntry
			mcpVal["servers"] = serversVal
			rootMap["mcp"] = mcpVal
		} else {
			delete(mcpVal, legacyServerName)
			mcpVal["litespm"] = bridgeEntry
			rootMap["mcp"] = mcpVal
		}
	} else if mcpServers, ok := rootMap["mcpServers"].(map[string]any); ok {
		delete(mcpServers, legacyServerName)
		mcpServers["litespm"] = bridgeEntry
		rootMap["mcpServers"] = mcpServers
	} else {
		// Default to mcpServers
		rootMap["mcpServers"] = map[string]any{
			"litespm": bridgeEntry,
		}
	}

	proposedBytes, err := json.MarshalIndent(rootMap, "", "  ")
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "pi-agent",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: string(proposedBytes),
		BackupPath:      backupPath,
	}, nil
}

func (a *PiAgentAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
	if err := AtomicWriteFile(plan.ConfigPath, []byte(plan.ProposedContent), 0600); err != nil {
		return nil, fmt.Errorf("failed to write config %s: %w", plan.ConfigPath, err)
	}

	// Also ensure companion extension directory and file exists for Pi agent
	extPath := a.ExtensionPath()
	extDir := filepath.Dir(extPath)
	if err := os.MkdirAll(extDir, 0700); err == nil {
		if _, err := os.Stat(extPath); os.IsNotExist(err) {
			extensionCode := `// LiteSPM companion extension for Pi Agent
export default function (pi: any) {
  pi.registerCommand("litespm", {
    description: "Launch LiteSPM capability manager",
    async execute(args: string[]) {
      return pi.mcp.callTool("litespm", "list_installed", {});
    }
  });
}
`
			_ = os.WriteFile(extPath, []byte(extensionCode), 0600)
		}
	}

	return &HostApplyResult{
		HostID:     plan.HostID,
		ConfigPath: plan.ConfigPath,
		BackupPath: plan.BackupPath,
		Success:    true,
	}, nil
}

func (a *PiAgentAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "pi-agent", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "pi-agent", ConfigPath: configPath, Status: "missing"}, nil
	}

	var rootMap map[string]any
	if err := json.Unmarshal(data, &rootMap); err != nil {
		return &HostVerification{HostID: "pi-agent", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	registered := false
	if mcpVal, ok := rootMap["mcp"].(map[string]any); ok {
		if serversVal, ok := mcpVal["servers"].(map[string]any); ok {
			_, registered = serversVal["litespm"]
		} else {
			_, registered = mcpVal["litespm"]
		}
	}
	if !registered {
		if mcpServers, ok := rootMap["mcpServers"].(map[string]any); ok {
			_, registered = mcpServers["litespm"]
		}
	}

	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "pi-agent",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *PiAgentAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
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

	var results []PreExistingComponent
	extractComp := func(name string, details any) {
		if name == litespmServerName || name == legacyServerName {
			return
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

	if mcpVal, ok := rootMap["mcp"].(map[string]any); ok {
		if serversVal, ok := mcpVal["servers"].(map[string]any); ok {
			for name, details := range serversVal {
				extractComp(name, details)
			}
		} else {
			for name, details := range mcpVal {
				extractComp(name, details)
			}
		}
	}
	if mcpServers, ok := rootMap["mcpServers"].(map[string]any); ok {
		for name, details := range mcpServers {
			extractComp(name, details)
		}
	}

	return results, nil
}

func (a *PiAgentAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`// Add to ~/.pi/agent/mcp.json:
"mcpServers": {
  "litespm": {
    "command": %q,
    "args": ["bridge", "stdio", "--host", "pi-agent"]
  }
}
`, cleanBin)
}
