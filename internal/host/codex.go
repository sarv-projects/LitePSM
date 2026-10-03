package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// CodexAdapter manages integration with OpenAI Codex CLI.
type CodexAdapter struct{}

func (a *CodexAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "codex",
		DisplayName:            "OpenAI Codex CLI",
		SupportedVersions:      []string{">=0.1.0"},
		DefaultConfigFileName:  "config.toml",
		ConfigFormat:           "toml",
		SupportsFormElicit:     false,
		RequiresBootstrapSkill: true,
		SlashCommandTrigger:    "/marketplace",
	}
}

func (a *CodexAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()

	var candidatePaths []string
	if runtime.GOOS == "windows" {
		// Native Windows user scope is %USERPROFILE%\.codex\config.toml.
		// %APPDATA% is retained only as a legacy fallback for older installs.
		candidatePaths = append(candidatePaths, filepath.Join(homeDir, ".codex", "config.toml"))
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidatePaths = append(candidatePaths, filepath.Join(appData, "Codex", "config.toml"))
		}
	} else {
		candidatePaths = append(candidatePaths, filepath.Join(homeDir, ".codex", "config.toml"))
	}

	for _, p := range candidatePaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Fallback default
	if len(candidatePaths) > 0 {
		return candidatePaths[0], nil
	}
	return filepath.Join(homeDir, ".codex", "config.toml"), nil
}

func (a *CodexAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := ""
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "codex")
	if err != nil {
		return nil, err
	}

	cleanBin := filepath.ToSlash(binaryPath)
	entry := fmt.Sprintf("\n[mcp_servers.litepsm]\ncommand = %q\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n", cleanBin)

	proposed := origContent
	if strings.Contains(origContent, "[mcp_servers.litepsm]") {
		// Update existing section
		lines := strings.Split(origContent, "\n")
		var newLines []string
		skip := false
		for _, l := range lines {
			if strings.TrimSpace(l) == "[mcp_servers.litepsm]" {
				skip = true
				newLines = append(newLines, strings.TrimRight(entry, "\n"))
				continue
			}
			if skip && strings.HasPrefix(strings.TrimSpace(l), "[") {
				skip = false
			}
			if !skip {
				newLines = append(newLines, l)
			}
		}
		proposed = strings.Join(newLines, "\n")
	} else {
		proposed = strings.TrimRight(origContent, "\n") + entry
	}

	return &HostChangePlan{
		HostID:          "codex",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

func (a *CodexAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
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

func (a *CodexAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "codex", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "codex", ConfigPath: configPath, Status: "missing"}, nil
	}

	content := string(data)
	registered := strings.Contains(content, "[mcp_servers.litepsm]") && strings.Contains(content, "bridge")
	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "codex",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *CodexAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil
	}

	return parseTomlMcpComponents(string(data), configPath), nil
}

func parseTomlMcpComponents(content string, configPath string) []PreExistingComponent {
	var results []PreExistingComponent
	lines := strings.Split(content, "\n")
	var currentComp *PreExistingComponent

	flush := func() {
		if currentComp != nil && currentComp.Name != "" && currentComp.Name != "litepsm" {
			results = append(results, *currentComp)
		}
		currentComp = nil
	}

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flush()
			if strings.HasPrefix(trimmed, "[mcp_servers.") {
				name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[mcp_servers."), "]")
				if name != "litepsm" {
					currentComp = &PreExistingComponent{
						Name:       name,
						Kind:       "mcp",
						ReadOnly:   true,
						SourcePath: configPath,
					}
				}
			}
			continue
		}

		if currentComp != nil {
			if strings.HasPrefix(trimmed, "command") && strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				currentComp.Command = val
			} else if strings.HasPrefix(trimmed, "args") && strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				val := strings.TrimSpace(parts[1])
				if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
					inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
					items := strings.Split(inner, ",")
					for _, it := range items {
						it = strings.TrimSpace(it)
						it = strings.Trim(it, `"'`)
						if it != "" {
							currentComp.Args = append(currentComp.Args, it)
						}
					}
				}
			}
		}
	}
	flush()
	return results
}

func (a *CodexAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`# Add to ~/.codex/config.toml
[mcp_servers.litepsm]
command = %q
args = ["bridge", "stdio", "--host", "codex"]
`, cleanBin)
}
