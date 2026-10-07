package host

// generic.go — a HostAdapter implemented from a BridgeTarget.
//
// One implementation serves every data-driven target: it resolves the config
// path for a scope, backs it up, merges the single `litespm` bridge entry in
// the host's own format, and verifies the result. The per-agent knowledge lives
// entirely in the BridgeTarget row.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// GenericAdapter serves any BridgeTarget through the HostAdapter contract.
type GenericAdapter struct{ Target BridgeTarget }

// NewGenericAdapter wraps a target.
func NewGenericAdapter(t BridgeTarget) *GenericAdapter { return &GenericAdapter{Target: t} }

// Descriptor implements HostAdapter.
func (a *GenericAdapter) Descriptor() HostDescriptor {
	trigger := a.Target.SlashCommandTrigger
	if trigger == "" {
		trigger = "/marketplace"
	}
	return HostDescriptor{
		HostID:                a.Target.ID,
		DisplayName:           a.Target.Name,
		DefaultConfigFileName: a.Target.ID + configExt(a.Target.Format),
		ConfigFormat:          string(a.Target.Format),
		SlashCommandTrigger:   trigger,
	}
}

func configExt(f ConfigFormat) string {
	if f == FormatTOML {
		return ".toml"
	}
	return ".json"
}

// DetectConfig implements HostAdapter.
func (a *GenericAdapter) DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error) {
	home := resolveHomeDir()
	// User scope uses UserPath; hosts that only document repo-local config fall
	// back to the project file rather than inventing a user path. Project scope
	// always uses ProjectPath.
	if scope == domain.ScopeProject {
		if a.Target.ProjectPath == nil {
			return "", fmt.Errorf("%s has no documented project-scope config path", a.Target.Name)
		}
		root, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return a.Target.ProjectPath(root), nil
	}
	if a.Target.UserPath != nil {
		return a.Target.UserPath(home), nil
	}
	if a.Target.ProjectPath != nil {
		root, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return a.Target.ProjectPath(root), nil
	}
	return "", fmt.Errorf("%s has no documented config path", a.Target.Name)
}

// PlanSetup implements HostAdapter.
func (a *GenericAdapter) PlanSetup(ctx context.Context, binaryPath, backupDir string) (*HostChangePlan, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, err
	}

	orig := ""
	if data, err := os.ReadFile(configPath); err == nil {
		orig = string(data)
	}

	backupPath, err := CreateAtomicBackup(configPath, backupDir, a.Target.ID)
	if err != nil {
		return nil, err
	}

	proposed, err := a.renderConfig(orig, binaryPath, false)
	if err != nil {
		return nil, err
	}

	return &HostChangePlan{
		HostID:          a.Target.ID,
		ConfigPath:      configPath,
		OriginalContent: orig,
		ProposedContent: proposed,
		BackupPath:      backupPath,
	}, nil
}

// parseHostJSON parses a host config for READING, tolerating `//` and `/* */`
// comments. A host's own config may legitimately be JSONC — Cline's file lives
// inside VS Code's globalStorage settings, where users hand-edit — and refusing
// to read it would strand the user with no diagnostics and no setup at all.
func parseHostJSON(data []byte) (map[string]any, error) {
	return parseConfigJSON(BridgeTarget{TolerateComments: true}, data)
}

// renderBridgeEntryJSON produces the new content for a bespoke JSON/JSONC host
// config by surgical splice: the bridge entry is upserted and the pre-rename
// `litepsm` entry is removed surgically, so user comments, key order,
// indentation and the file's trailing newline all survive byte-for-byte.
//
// The bespoke adapters previously parsed into a map and re-serialized, which
// reordered every key, reflowed the whole document and dropped a trailing
// newline — a rewrite of someone else's file far beyond the one entry that
// changed. The merged text is re-parsed and the entry asserted before it is
// returned, so a writer bug fails loudly instead of shipping a corrupt config.
func renderBridgeEntryJSON(hostName, orig string, keyPath []string, entry any) (string, error) {
	cleaned, _, err := stripJSONEntryNamed(orig, keyPath, legacyServerName)
	if err != nil {
		return "", fmt.Errorf("%s: %w", hostName, err)
	}
	merged, err := mergeJSONEntrySurgical(cleaned, keyPath, entry)
	if err != nil {
		return "", fmt.Errorf("%s: %w", hostName, err)
	}
	verify, err := parseHostJSON([]byte(merged))
	if err != nil {
		return "", fmt.Errorf("%s: merged config would be invalid JSON, refusing to write: %w", hostName, err)
	}
	if !inspectEntry(verify, keyPath) {
		return "", fmt.Errorf("%s: merged config is missing the bridge entry, refusing to write", hostName)
	}
	return merged, nil
}

// renderConfig produces the full new file content for a config.
//
// Every JSON target is written by surgical splice rather than parse-and-
// re-serialize: the rest of the file (user comments, key order, indentation,
// blank lines) survives byte-for-byte. That matters even for strict-JSON hosts,
// because several of them keep MCP inside a large shared settings document.
// mergeJSONEntrySurgical is followed by a re-parse plus an assertion that the
// entry is actually present, so a writer bug fails loudly instead of shipping a
// corrupt config file.
func (a *GenericAdapter) renderConfig(orig, binaryPath string, projectScope bool) (string, error) {
	keyPath := a.Target.keyPathFor(projectScope)
	if a.Target.Format == FormatTOML {
		// Adopt a pre-rename `[prefix.litepsm]` table before writing the current
		// `[prefix.litespm]` one, so a host that predates the rename ends up with
		// exactly one bridge table rather than a duplicate.
		cleaned, _, err := stripTOMLEntryNamed(orig, a.Target.keyPathFor(false), legacyServerName)
		if err != nil {
			return "", fmt.Errorf("%s: %w", a.Target.Name, err)
		}
		return mergeTOMLEntry(cleaned, a.Target.tomlTable(), a.Target.tomlEntryBlock(binaryPath))
	}
	// Same adoption for JSON/JSONC: drop the legacy member, then splice in the
	// current one. The legacy member is removed surgically so comments and key
	// order elsewhere survive.
	cleaned, _, err := stripJSONEntryNamed(orig, keyPath, legacyServerName)
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.Target.Name, err)
	}
	entry := a.Target.jsonEntryValue(binaryPath)
	merged, err := mergeJSONEntrySurgical(cleaned, keyPath, entry)
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.Target.Name, err)
	}
	verify, err := parseConfigJSON(a.Target, []byte(merged))
	if err != nil {
		return "", fmt.Errorf("%s: merged config would be invalid JSON, refusing to write: %w", a.Target.Name, err)
	}
	if !inspectEntry(verify, keyPath) {
		return "", fmt.Errorf("%s: merged config is missing the bridge entry, refusing to write", a.Target.Name)
	}
	return merged, nil
}

// tomlTable renders the table path as `[mcp_servers.litespm]`.
func (t BridgeTarget) tomlTable() string {
	parts := append([]string{}, t.keyPathFor(false)...)
	parts = append(parts, litespmServerName)
	return "[" + strings.Join(parts, ".") + "]"
}

// mergeTOMLEntry replaces or appends a single TOML table block, leaving every
// other section untouched (including comments).
func mergeTOMLEntry(orig, table, block string) (string, error) {
	lines := strings.Split(orig, "\n")
	var out []string
	skipping := false
	replaced := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == table {
			skipping = true
			if !replaced {
				out = append(out, strings.TrimRight(block, "\n"))
				replaced = true
			}
			continue
		}
		if skipping {
			// A new table header (or EOF) ends our section.
			if strings.HasPrefix(trimmed, "[") {
				skipping = false
			} else if trimmed != "" {
				continue // drop the old keys of our own table
			}
		}
		out = append(out, l)
	}
	result := strings.Join(out, "\n")
	if replaced {
		return strings.TrimRight(result, "\n") + "\n", nil
	}
	base := strings.TrimRight(result, "\n")
	if base == "" {
		return block, nil
	}
	return base + "\n\n" + block, nil
}

// ApplySetup implements HostAdapter. The plan is validated (an empty
// ProposedContent is never written), checked against the current file
// (a stale plan is refused, not applied over the intervening edit), and only
// then written atomically.
func (a *GenericAdapter) ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error) {
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

// VerifySetup implements HostAdapter.
func (a *GenericAdapter) VerifySetup(ctx context.Context) (*HostVerification, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return &HostVerification{HostID: a.Target.ID, Status: "missing"}, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return &HostVerification{HostID: a.Target.ID, ConfigPath: configPath, Status: "missing"}, nil
	}
	keyPath := a.Target.keyPathFor(false)
	if a.Target.Format == FormatTOML {
		registered := strings.Contains(string(data), a.Target.tomlTable())
		return a.verification(configPath, registered), nil
	}
	root, err := parseConfigJSON(a.Target, data)
	if err != nil {
		return &HostVerification{HostID: a.Target.ID, ConfigPath: configPath, Status: "corrupted"}, nil
	}
	return a.verification(configPath, inspectEntry(root, keyPath)), nil
}

func (a *GenericAdapter) verification(path string, registered bool) *HostVerification {
	status := "missing"
	if registered {
		status = "ready"
	}
	return &HostVerification{
		HostID:     a.Target.ID,
		ConfigPath: path,
		Registered: registered,
		Status:     status,
	}
}

// DetectPreExistingComponents implements HostAdapter. Detection is read-only:
// foreign servers are listed, never modified.
func (a *GenericAdapter) DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error) {
	configPath, err := a.DetectConfig(ctx, domain.ScopeUser)
	if err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil
	}
	keyPath := a.Target.keyPathFor(false)
	if a.Target.Format == FormatTOML {
		return parseTOMLComponents(string(data), configPath), nil
	}
	root, err := parseConfigJSON(a.Target, data)
	if err != nil {
		return nil, nil
	}
	names := listEntryNames(root, keyPath)
	out := make([]PreExistingComponent, 0, len(names))
	for _, n := range names {
		out = append(out, PreExistingComponent{
			Name:        n,
			Kind:        "mcp",
			ReadOnly:    true,
			SourcePath:  configPath,
			Description: fmt.Sprintf("Pre-existing MCP server from %s", a.Target.Name),
		})
	}
	return out, nil
}

// parseTOMLComponents extracts `[mcp_servers.<name>]` headers read-only.
func parseTOMLComponents(content, path string) []PreExistingComponent {
	var out []PreExistingComponent
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "[mcp_servers.") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(t, "[mcp_servers."), "]")
		if name == "" || name == litespmServerName || name == legacyServerName {
			continue
		}
		out = append(out, PreExistingComponent{
			Name:        name,
			Kind:        "mcp",
			ReadOnly:    true,
			SourcePath:  path,
			Description: "Pre-existing MCP server",
		})
	}
	return out
}

// RenderManualSetup implements HostAdapter.
func (a *GenericAdapter) RenderManualSetup(binaryPath string) string {
	bin := filepathSlash(binaryPath)
	key := strings.Join(a.Target.keyPathFor(false), ".")
	if a.Target.Format == FormatTOML {
		return fmt.Sprintf("# %s\n%s", a.Target.ID, a.Target.tomlEntryBlock(bin))
	}
	var snippet string
	switch a.Target.Shape {
	case ShapeLocalArray:
		argv := append([]string{bin}, bridgeArgs(a.Target.ID)...)
		snippet = fmt.Sprintf("{\n  \"type\": \"local\",\n  \"command\": [\"%s\"]\n}", strings.Join(argv, "\", \""))
	case ShapeStdioTyped:
		snippet = fmt.Sprintf("{\n  \"type\": \"stdio\",\n  \"command\": %q,\n  \"args\": [\"bridge\", \"stdio\", \"--host\", %q]\n}", bin, a.Target.ID)
	default:
		snippet = fmt.Sprintf("{\n  \"command\": %q,\n  \"args\": [\"bridge\", \"stdio\", \"--host\", %q]\n}", bin, a.Target.ID)
	}
	// The snippet must be pasteable as-is, so it names the server: an entry
	// object in place of the server name is not valid JSON, and a user who
	// pastes it gets a config the host cannot read.
	return fmt.Sprintf("# %s\n{\n  %q: {\n    %q: %s\n  }\n}", a.Target.ID, key, litespmServerName, indentJSON(snippet, "    "))
}

func indentJSON(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// stripJSONComments removes `//` and `/* */` comments that fall outside string
// literals, replacing them with equivalent whitespace so byte offsets of the
// remaining content are unchanged. Comment markers inside a quoted string are
// preserved, which matters because URLs and paths contain `//`.
func stripJSONComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(s) {
			if s[i+1] == '/' {
				for i < len(s) && s[i] != '\n' {
					b.WriteByte(' ')
					i++
				}
				if i < len(s) {
					b.WriteByte('\n')
				}
				continue
			}
			if s[i+1] == '*' {
				b.WriteString("  ")
				i += 2
				for i < len(s) {
					if s[i] == '*' && i+1 < len(s) && s[i+1] == '/' {
						b.WriteString("  ")
						i++
						break
					}
					if s[i] != '\n' {
						b.WriteByte(' ')
					}
					i++
				}
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// parseConfigJSON parses a host config, tolerating comments when the target
// declares that the format allows them.
func parseConfigJSON(t BridgeTarget, raw []byte) (map[string]any, error) {
	text := string(raw)
	if t.TolerateComments {
		text = stripJSONComments(text)
	}
	if strings.TrimSpace(text) == "" {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		return nil, err
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}
