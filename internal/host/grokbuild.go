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

// GrokBuildAdapter manages integration with Grok Build (xAI Dev Tool).
type GrokBuildAdapter struct{}

func (a *GrokBuildAdapter) Descriptor() HostDescriptor {
	return HostDescriptor{
		HostID:                 "grok-build",
		DisplayName:            "Grok Build (xAI Dev Tool)",
		SupportedVersions:      []string{">=0.1.0"},
		DefaultConfigFileName:  "config.toml",
		ConfigFormat:           "toml",
		SupportsFormElicit:     false,
		RequiresBootstrapSkill: false,
		SlashCommandTrigger:    "/marketplace",
	}
}

func (a *GrokBuildAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	homeDir := resolveHomeDir()

	var candidatePaths []string
	if runtime.GOOS == "windows" {
		// Native Windows user scope is %USERPROFILE%\.grok\config.toml.
		// %APPDATA% is retained only as a legacy fallback for older installs.
		candidatePaths = append(candidatePaths, filepath.Join(homeDir, ".grok", "config.toml"))
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidatePaths = append(candidatePaths, filepath.Join(appData, "Grok", "config.toml"))
		}
	} else {
		candidatePaths = append(candidatePaths, filepath.Join(homeDir, ".grok", "config.toml"))
	}

	for _, p := range candidatePaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if len(candidatePaths) > 0 {
		return candidatePaths[0], nil
	}
	return filepath.Join(homeDir, ".grok", "config.toml"), nil
}

func (a *GrokBuildAdapter) PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	origContent := ""
	if data, err := os.ReadFile(configPath); err == nil {
		origContent = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, "grok-build")
	if err != nil {
		return nil, err
	}

	cleanBin := filepath.ToSlash(binaryPath)
	entry := fmt.Sprintf("\n[mcp_servers.litepsm]\ncommand = %q\nargs = [\"bridge\", \"stdio\", \"--host\", \"grok-build\"]\n", cleanBin)

	proposed := origContent
	if strings.Contains(origContent, "[mcp_servers.litepsm]") {
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
		HostID:          "grok-build",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

func (a *GrokBuildAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
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

func (a *GrokBuildAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: "grok-build", Status: "missing"}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: "grok-build", ConfigPath: configPath, Status: "missing"}, nil
	}

	content := string(data)
	registered := strings.Contains(content, "[mcp_servers.litepsm]") && strings.Contains(content, "bridge")
	status := "missing"
	if registered {
		status = "ready"
	}

	return &HostVerification{
		HostID:     "grok-build",
		ConfigPath: configPath,
		Registered: registered,
		Status:     status,
	}, nil
}

func (a *GrokBuildAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
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

func (a *GrokBuildAdapter) RenderManualSetup(binaryPath string) string {
	cleanBin := filepath.ToSlash(binaryPath)
	return fmt.Sprintf(`# Add to ~/.grok/config.toml
[mcp_servers.litepsm]
command = %q
args = ["bridge", "stdio", "--host", "grok-build"]
`, cleanBin)
}
