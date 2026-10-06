package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// DetectConfig resolves the global OpenCode config file.
//
// OpenCode resolves its config directory through xdg-basedir, so
// $XDG_CONFIG_HOME applies on every OS including Windows, and OPENCODE_CONFIG_DIR
// overrides the directory outright. Both `opencode.json` and `opencode.jsonc`
// are read, `.jsonc` first (source: ConfigPaths.files() returns
// ["${name}.jsonc", "${name}.json"]).
//
// Two paths this adapter used to probe were removed because no OpenCode release
// reads them: `~/.opencode.json` and `%APPDATA%\OpenCode\opencode.json`. Probing
// them was not harmless — when the real file was absent they became the default
// target, so the bridge was written somewhere OpenCode never looks while setup
// reported success.
func (a *OpenCodeAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	dir := ""
	if v := os.Getenv("OPENCODE_CONFIG_DIR"); v != "" {
		dir = v
	} else {
		dir = filepath.Join(xdgConfigDir(resolveHomeDir()), "opencode")
	}
	candidates := []string{
		filepath.Join(dir, "opencode.jsonc"),
		filepath.Join(dir, "opencode.json"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return candidates[1], nil
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

	// OpenCode has ONE documented layout: servers are direct members of the
	// `mcp` object. The published schema types `mcp` as additionalProperties of
	// McpLocalConfig/McpRemoteConfig and contains no `servers` key, and the
	// runtime rejects a member without `type`
	// ("Ignoring MCP config entry without type"). A two-level `mcp.servers`
	// path was therefore invented here and silently produced a config
	// OpenCode refuses to load, while this adapter's own verify looked for the
	// same invented path and reported ready.
	orig := pruneEmptyLegacyServersObject(origContent)

	bridgeEntry := map[string]any{
		"type": "local",
		// OpenCode local servers use a combined string array command
		// (executable followed by its arguments).
		"command": append([]string{filepath.ToSlash(binaryPath)}, "bridge", "stdio", "--host", "opencode"),
	}

	proposed, err := renderBridgeEntryJSON("opencode", orig, []string{"mcp"}, bridgeEntry)
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

// pruneEmptyLegacyServersObject removes the `mcp.servers` object that earlier
// LiteSPM versions wrote, but only once it is empty. OpenCode treats every
// member of `mcp` as a server definition and rejects one without `type`, so a
// leftover `servers` object is a config error the user cannot see the cause of.
// A `servers` object that still holds someone else's entries is left alone.
func pruneEmptyLegacyServersObject(orig string) string {
	out := orig
	for _, name := range []string{legacyServerName, litespmServerName} {
		if stripped, removed, err := stripJSONEntryNamed(out, []string{"mcp", "servers"}, name); err == nil && removed {
			out = stripped
		}
	}
	innerStart, innerEnd, found, spanErr := jsonValueSpan(stripJSONComments(out), []string{"mcp", "servers"})
	if spanErr == nil && found && !isEmptyJSONObject(strings.TrimSpace(out[innerStart:innerEnd])) {
		// Someone else's servers are still configured there: leave it be.
		return out
	}
	if pruned, prunedOK, err := stripJSONEntryNamed(out, []string{"mcp"}, "servers"); err == nil && prunedOK {
		return pruned
	}
	return out
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
