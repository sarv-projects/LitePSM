package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// OpenCodeAdapter manages integration with OpenCode CLI.
// Supports both v1 root "mcp" and v2 nested "mcp.servers" configuration structures.
type OpenCodeAdapter struct{}

func (a *OpenCodeAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "opencode",
		DisplayName:            "OpenCode CLI",
		SupportedVersions:      []string{">=1.0.0", ">=2.0.0"},
		DefaultConfigFileName:  "opencode.json",
		ConfigFormat:           "json",
		SupportsFormElicit:     false,
		RequiresBootstrapSkill: false,
		SlashCommandTrigger:    "/marketplace",
	}
}

func (a *OpenCodeAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()

	var candidates []string
	if runtime.GOOS == "windows" {
		// Native Windows user scope is %USERPROFILE%\.config\opencode\opencode.json.
		// %APPDATA% is retained only as a legacy fallback for older installs.
		candidates = append(candidates, filepath.Join(homeDir, ".config", "opencode", "opencode.json"))
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates, filepath.Join(appData, "OpenCode", "opencode.json"))
		}
		candidates = append(candidates, filepath.Join(homeDir, ".opencode.json"))
	} else {
		candidates = append(candidates, filepath.Join(homeDir, ".config", "opencode", "opencode.json"))
		candidates = append(candidates, filepath.Join(homeDir, ".opencode.json"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return filepath.Join(homeDir, ".config", "opencode", "opencode.json"), nil
}

func (a *OpenCodeAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := "{}"
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "opencode")
	if err != nil {
		return nil, err
	}

	// Read tolerantly (a user may have left comments in the config) purely to
	// learn which layout is in use, then splice: everything else in the file
	// survives byte-for-byte.
	var existing map[string]any
	if strings.TrimSpace(origContent) != "" && strings.TrimSpace(origContent) != "{}" {
		existing, err = parseHostJSON([]byte(origContent))
		if err != nil {
			return nil, fmt.Errorf("failed to parse existing opencode config %s: %w", configPath, err)
		}
	}

	bridgeEntry := map[string]any{
		"type": "local",
		// OpenCode local servers use a combined string array command
		// (executable followed by its arguments).
		"command": append([]string{filepath.ToSlash(binaryPath)}, "bridge", "stdio", "--host", "opencode"),
	}

	// Detect whether v2 (mcp.servers) or v1 (mcp.<name>) is in use. A new
	// config defaults to the v2 nested layout.
	keyPath := []string{"mcp", "servers"}
	if mcpMap, ok := existing["mcp"].(map[string]any); ok {
		if _, hasServers := mcpMap["servers"].(map[string]any); hasServers {
			keyPath = []string{"mcp", "servers"}
		} else {
			keyPath = []string{"mcp"}
		}
	} else if _, hasFlat := existing["mcp"]; hasFlat {
		keyPath = []string{"mcp"}
	}

	proposed, err := renderBridgeEntryJSON("opencode", origContent, keyPath, bridgeEntry)
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "opencode",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

func (a *OpenCodeAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
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

func (a *OpenCodeAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "opencode", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "opencode", ConfigPath: configPath, Status: "missing"}, nil
	}

	rootMap, err := parseHostJSON(data)
	if err != nil {
		return &HostVerification{HostID: "opencode", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	registered := false
	if mcpVal, ok := rootMap["mcp"].(map[string]any); ok {
		if serversVal, ok := mcpVal["servers"].(map[string]any); ok {
			if _, ok := serversVal["litespm"]; ok {
				registered = true
			}
		} else if _, ok := mcpVal["litespm"]; ok {
			registered = true
		}
	}

	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "opencode",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *OpenCodeAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
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

	var results []PreExistingComponent
	mcpVal, ok := rootMap["mcp"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// Check if v2 nested
	if serversVal, ok := mcpVal["servers"].(map[string]any); ok {
		for name, details := range serversVal {
			if name == litespmServerName || name == legacyServerName {
				continue
			}
			comp := a.extractComponent(name, details, configPath)
			results = append(results, comp)
		}
	} else {
		// v1 root mcp
		for name, details := range mcpVal {
			if name == litespmServerName || name == legacyServerName || name == "servers" {
				continue
			}
			comp := a.extractComponent(name, details, configPath)
			results = append(results, comp)
		}
	}

	return results, nil
}

func (a *OpenCodeAdapter) extractComponent(name string, details any, configPath string) PreExistingComponent {
	comp := PreExistingComponent{
		Name:       name,
		Kind:       "mcp",
		ReadOnly:   true,
		SourcePath: configPath,
	}
	if dMap, ok := details.(map[string]any); ok {
		// OpenCode local entries store the executable and its arguments as a
		// single string array under "command"; older entries may use a string
		// "command" plus a separate "args" array.
		switch cmd := dMap["command"].(type) {
		case string:
			comp.Command = cmd
		case []any:
			for i, item := range cmd {
				s, ok := item.(string)
				if !ok {
					continue
				}
				if i == 0 {
					comp.Command = s
				} else {
					comp.Args = append(comp.Args, s)
				}
			}
		}
		if args, ok := dMap["args"].([]any); ok {
			for _, arg := range args {
				if argStr, ok := arg.(string); ok {
					comp.Args = append(comp.Args, argStr)
				}
			}
		}
	}
	return comp
}

func (a *OpenCodeAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`// Add to opencode.json (v2 layout):
"mcp": {
  "servers": {
    "litespm": {
      "type": "local",
      "command": [%q, "bridge", "stdio", "--host", "opencode"]
    }
  }
}
`, cleanBin)
}
