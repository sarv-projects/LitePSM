package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
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

// DetectConfig resolves Grok Build's user-level settings file.
//
// The documented user path is `$GROK_HOME/config.toml` with `$GROK_HOME`
// defaulting to `~/.grok` ("Home for config, auth, sessions, skills, plugins,
// and logs"). This adapter previously ignored the variable, so a relocated home
// received the bridge in a file Grok never reads. internal/skills already
// honoured GROK_HOME.
//
// The `%APPDATA%\Grok\config.toml` fallback is gone: it appears in no xAI
// documentation, and the documented Windows user path is
// `%USERPROFILE%\.grok\config.toml`, which the default already covers.
func (a *GrokBuildAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	dir := os.Getenv("GROK_HOME")
	if dir == "" {
		dir = filepath.Join(resolveHomeDir(), ".grok")
	}
	return filepath.Join(dir, "config.toml"), nil
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
	entry := fmt.Sprintf("\n[mcp_servers.litespm]\ncommand = %q\nargs = [\"bridge\", \"stdio\", \"--host\", \"grok-build\"]\n", cleanBin)

	// Adopt a pre-rename `[mcp_servers.litepsm]` table before writing the
	// current one, so an upgraded host keeps exactly one bridge table.
	base := origContent
	if cleaned, removed, err := stripTOMLEntryNamed(base, []string{"mcp_servers"}, legacyServerName); err == nil && removed {
		base = cleaned
	}

	proposed := base
	if strings.Contains(base, "[mcp_servers.litespm]") {
		lines := strings.Split(base, "\n")
		var newLines []string
		skip := false
		for _, l := range lines {
			if strings.TrimSpace(l) == "[mcp_servers.litespm]" {
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
		proposed = strings.TrimRight(base, "\n") + entry
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
	registered := strings.Contains(content, "[mcp_servers.litespm]") && strings.Contains(content, "bridge")
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
[mcp_servers.litespm]
command = %q
args = ["bridge", "stdio", "--host", "grok-build"]
`, cleanBin)
}
