package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// DetectConfig resolves Pi's user-level MCP file.
//
// Pi reads servers from `<agent-dir>/mcp.json`, where the agent directory is
// `~/.pi/agent` unless `PI_CODING_AGENT_DIR` overrides it (documented; also
// `agentDir` in the SDK). Two fallback paths this adapter used to probe
// (`~/.pi/config.json`, `~/.pi/mcp.json`) appear in no Pi documentation or
// source, and probing them was not harmless: when the real file was absent they
// became the write target, so the bridge landed in a file Pi never reads.
func (a *PiAgentAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	return filepath.Join(piAgentDir(), "mcp.json"), nil
}

// piAgentDir returns Pi's agent directory, honouring the documented override.
func piAgentDir() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(resolveHomeDir(), ".pi", "agent")
}

func (a *PiAgentAdapter) ExtensionPath() string {
	return filepath.Join(piAgentDir(), "extensions", "litespm.ts")
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

	// Read tolerantly (a user may have left comments in the config) purely to
	// learn which container is in use, then splice: everything else in the file
	// survives byte-for-byte.
	// Read tolerantly (a user may have left comments in the config) so a broken
	// document is reported before anything is written.
	if strings.TrimSpace(origContent) != "" && strings.TrimSpace(origContent) != "{}" {
		if _, perr := parseHostJSON([]byte(origContent)); perr != nil {
			return nil, fmt.Errorf("failed to parse existing pi-agent config %s: %w", configPath, perr)
		}
	}

	bridgeEntry := map[string]any{
		"command": filepath.ToSlash(binaryPath),
		"args":    []string{"bridge", "stdio", "--host", "pi-agent"},
	}

	// `mcpServers` is the only container Pi reads ("The format matches other MCP
	// clients"), so it is also the only one we may write. Earlier revisions
	// switched to `mcp` or `mcp.servers` whenever the file happened to contain an
	// unrelated top-level `mcp` object, which put the bridge somewhere Pi never
	// looks while verify reported ready.
	// A pre-rename `litepsm` entry is adopted (deleted) from that container, so
	// only one bridge server remains.
	proposed, err := renderBridgeEntryJSON("pi-agent", origContent, []string{"mcpServers"}, bridgeEntry)
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          "pi-agent",
		ConfigPath:      configPath,
		OriginalContent: origContent,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

func (a *PiAgentAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
	if err := ApplyPlanWrite(plan); err != nil {
		return nil, err
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

	rootMap, err := parseHostJSON(data)
	if err != nil {
		return &HostVerification{HostID: "pi-agent", ConfigPath: configPath, Status: "corrupted"}, nil
	}

	// `mcpServers` is the only container Pi reads (see PlanSetup), so verify
	// must look only there. Earlier revisions also accepted `mcp` /
	// `mcp.servers`, which let a bridge written to an unread container verify
	// as ready — the exact self-confirming failure this audit removes.
	registered := false
	if mcpServers, ok := rootMap["mcpServers"].(map[string]any); ok {
		_, registered = mcpServers["litespm"]
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

	rootMap, err := parseHostJSON(data)
	if err != nil {
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

	// Only `mcpServers` is read by Pi; entries under a foreign `mcp` object are
	// ignored by the host and must not be surfaced as installed components.
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
