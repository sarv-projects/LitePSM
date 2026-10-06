package host

// entry_install.go — installing a catalog MCP server into agent host configs.
//
// The bridge entry (`litespm`) is what puts LiteSPM itself into an agent. This
// file is the other direction: registering a THIRD-PARTY MCP server, by name,
// in the same container, so the agent can spawn it.
//
// It is deliberately one package-level function rather than a method on
// HostAdapter. The bridge entry is identical on every host, so the adapter
// interface could express it as `PlanSetup(binaryPath)`. A server entry is not
// identical: the container key, the file format and the entry shape all vary per
// host (a plain `{command,args}`, OpenCode's `{type:"local",command:[...]}`, a
// TOML table), and a fourth field on the interface would push that variation
// into six hand-written adapters that would then differ only by a table lookup.
// The variation is data, so it lives in data: `entrySpecFor` resolves it from the
// data-driven target table where the host is data-driven, and from
// `bespokeEntrySpecs` for the six hand-written ones.
//
// Every write goes through the same discipline the bridge uses: surgical splice,
// atomic backup, and a re-parse that refuses to write a document it cannot read
// back.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// ServerEntry is one MCP server registration as it will appear in a host config.
type ServerEntry struct {
	// Name is the key the host stores the server under. It is also the name the
	// user sees in their agent's server list, so it must be the server's own
	// name — never `litespm`, which is reserved for the bridge.
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// EntryInstallOptions controls a single host write.
type EntryInstallOptions struct {
	// BackupDir receives a pre-edit copy of every config that is modified.
	BackupDir string
	Scope     domain.InstallScope
	// Force replaces an existing entry with the same name instead of refusing.
	Force bool
}

// EntryInstallResult reports what happened to one host config.
type EntryInstallResult struct {
	HostID     string
	ConfigPath string
	Name       string
	// Replaced is true when an existing entry of the same name was overwritten.
	Replaced bool
	// Created is true when this call created the config file.
	Created bool
}

// entrySpec is everything that varies between hosts for a server entry.
type entrySpec struct {
	// KeyPath is the container the server map lives at.
	KeyPath []string
	Format  ConfigFormat
	// Shape decides how the entry itself is expressed.
	Shape EntryShape
}

// entrySpecFor resolves the per-host entry shape. Data-driven targets already
// declare all three; the hand-written adapters declare them here, next to the
// removal specs that describe the same containers.
func entrySpecFor(adapter HostAdapter) (entrySpec, bool) {
	id := strings.ToLower(adapter.Descriptor().HostID)
	if g, ok := adapter.(*GenericAdapter); ok {
		return entrySpec{
			KeyPath: g.Target.keyPathFor(false),
			Format:  g.Target.Format,
			Shape:   g.Target.Shape,
		}, true
	}
	if spec, ok := bespokeEntrySpecs[id]; ok {
		return spec, true
	}
	return entrySpec{}, false
}

// bespokeEntrySpecs covers the six hand-written adapters, which do not expose a
// BridgeTarget. The key paths mirror what each adapter writes for the bridge,
// and the shapes mirror that host's documented entry requirements (see
// ARCH/30 §8.1 for where each was confirmed against the vendor).
var bespokeEntrySpecs = map[string]entrySpec{
	"claude-code": {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject},
	"cline":       {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject},
	"pi-agent":    {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject},
	"pi":          {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject},
	// OpenCode requires `type` and a combined argv array; its `mcp` container is
	// a flat map of server entries (there is no nested `servers` object).
	"opencode":   {Format: FormatJSON, KeyPath: []string{"mcp"}, Shape: ShapeLocalArray},
	"codex":      {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject},
	"grok":       {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject},
	"grok-build": {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject},
}

// entryValue renders the entry in the host's own shape.
func (s entrySpec) entryValue(e ServerEntry) any {
	switch s.Shape {
	case ShapeLocalArray:
		argv := append([]string{e.Command}, e.Args...)
		return map[string]any{"type": "local", "command": argv}
	case ShapeStdioTyped:
		out := map[string]any{"type": "stdio", "command": e.Command, "args": e.Args}
		if len(e.Env) > 0 {
			out["env"] = e.Env
		}
		return out
	case ShapeCommandString:
		parts := append([]string{quoteCommandArg(e.Command)}, e.Args...)
		return strings.Join(parts, " ")
	default:
		out := map[string]any{"command": e.Command}
		if len(e.Args) > 0 {
			out["args"] = e.Args
		}
		if len(e.Env) > 0 {
			out["env"] = e.Env
		}
		return out
	}
}

// tomlEntryBlock renders a `[mcp_servers.<name>]` table for a server entry.
func (s entrySpec) tomlEntryBlock(name string, e ServerEntry) string {
	quoted := make([]string, 0, len(e.Args))
	for _, a := range e.Args {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\ncommand = %q\n", strings.Join(append(append([]string{}, s.KeyPath...), name), "."), e.Command)
	if len(quoted) > 0 {
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
	}
	if len(e.Env) > 0 {
		keys := make([]string, 0, len(e.Env))
		for k := range e.Env {
			keys = append(keys, k)
		}
		// Deterministic order: a config that reorders itself on every install is
		// unreviewable in a diff.
		sortStrings(keys)
		fmt.Fprintf(&b, "env = { %s }\n", joinTOMLPairs(keys, e.Env))
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func joinTOMLPairs(keys []string, env map[string]string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%q = %q", k, env[k]))
	}
	return strings.Join(parts, ", ")
}

// ValidateServerEntryName rejects names a host could not store or a user could
// not read back. Several hosts document the same restriction (letters, digits,
// underscore, dash), so it is applied everywhere rather than per host: a name
// that is valid on one host and not another would make an install portable only
// by accident.
func ValidateServerEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("server name is empty")
	}
	if name == litespmServerName || name == legacyServerName {
		return fmt.Errorf("%q is reserved for the LiteSPM bridge entry", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return fmt.Errorf("server name %q may only contain letters, digits, '_' and '-'", name)
		}
	}
	return nil
}

// InstallServerEntry registers one MCP server in one host's config.
//
// It refuses rather than guesses: an existing entry of the same name is an error
// unless Force is set (so an install can never silently reconfigure a server the
// user set up by hand), and the merged document is re-parsed before anything is
// written.
func InstallServerEntry(ctx context.Context, hostID string, entry ServerEntry, opts EntryInstallOptions) (*EntryInstallResult, error) {
	if err := ValidateServerEntryName(entry.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(entry.Command) == "" {
		return nil, fmt.Errorf("server %q has no command to run", entry.Name)
	}

	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecFor(adapter)
	if !ok {
		return nil, fmt.Errorf("host %q has no documented MCP entry shape, so a server entry cannot be written to it", hostID)
	}

	configPath, err := adapter.DetectConfig(ctx, opts.Scope)
	if err != nil {
		return nil, err
	}

	original := ""
	created := false
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		original = string(data)
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read %s: %w", configPath, readErr)
	} else {
		created = true
	}

	replaced := false
	if _, present := lookupServerEntry(spec, original, entry.Name); present {
		if !opts.Force {
			return nil, domain.ErrNameConflict(fmt.Sprintf(
				"host %q already has an entry named %q in %s; re-run with force to replace it",
				adapter.Descriptor().HostID, entry.Name, configPath))
		}
		replaced = true
	}

	var proposed string
	if spec.Format == FormatTOML {
		block := spec.tomlEntryBlock(entry.Name, entry)
		if created {
			proposed = block
		} else {
			merged, mergeErr := mergeTOMLEntry(original, "["+strings.Join(append(append([]string{}, spec.KeyPath...), entry.Name), ".")+"]", block)
			if mergeErr != nil {
				return nil, fmt.Errorf("%s: %w", adapter.Descriptor().HostID, mergeErr)
			}
			proposed = merged
		}
	} else {
		merged, mergeErr := mergeJSONEntrySurgicalNamed(original, spec.KeyPath, entry.Name, spec.entryValue(entry))
		if mergeErr != nil {
			return nil, fmt.Errorf("%s: %w", adapter.Descriptor().HostID, mergeErr)
		}
		// Re-parse the merged text exactly as this host would, and assert the
		// entry is really there: a writer bug must fail here, not in the agent.
		verify, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(merged))
		if perr != nil {
			return nil, fmt.Errorf("%s: the modified config would not parse, refusing to write: %w", adapter.Descriptor().HostID, perr)
		}
		if _, ok := lookupInMap(verify, spec.KeyPath)[entry.Name]; !ok {
			return nil, fmt.Errorf("%s: the entry %q is missing from the merged config, refusing to write", adapter.Descriptor().HostID, entry.Name)
		}
		proposed = merged
	}

	if !created {
		if _, backupErr := CreateAtomicBackup(configPath, opts.BackupDir, adapter.Descriptor().HostID); backupErr != nil {
			return nil, backupErr
		}
	}
	if err := AtomicWriteFile(configPath, []byte(proposed), 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", configPath, err)
	}

	// Read back what the host will actually see.
	written, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read back %s: %w", configPath, err)
	}
	if _, present := lookupServerEntry(spec, string(written), entry.Name); !present {
		return nil, fmt.Errorf("%s: wrote %s but the entry %q is not readable from it", adapter.Descriptor().HostID, configPath, entry.Name)
	}

	return &EntryInstallResult{
		HostID:     adapter.Descriptor().HostID,
		ConfigPath: configPath,
		Name:       entry.Name,
		Replaced:   replaced,
		Created:    created,
	}, nil
}

// lookupServerEntry reports whether a named entry is already registered.
func lookupServerEntry(spec entrySpec, content, name string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return "", false
	}
	if spec.Format == FormatTOML {
		table := "[" + strings.Join(append(append([]string{}, spec.KeyPath...), name), ".") + "]"
		for _, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(line) == table {
				return table, true
			}
		}
		return "", false
	}
	root, err := parseConfigJSON(BridgeTarget{TolerateComments: true}, []byte(content))
	if err != nil {
		// An unparseable config is handled by the merge step, which reports it.
		return "", false
	}
	value, ok := lookupInMap(root, spec.KeyPath)[name]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", value), true
}

func lookupInMap(root map[string]any, keyPath []string) map[string]any {
	cur := any(root)
	for _, k := range keyPath {
		m, ok := cur.(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur, ok = m[k]
		if !ok {
			return map[string]any{}
		}
	}
	if m, ok := cur.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// ListServerEntries returns the entry names currently registered for a host, so
// an install can report what is already configured and refuse a collision before
// touching a file.
func ListServerEntries(ctx context.Context, hostID string, scope domain.InstallScope) ([]string, error) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecFor(adapter)
	if !ok {
		return nil, fmt.Errorf("host %q has no documented MCP entry shape", hostID)
	}
	configPath, err := adapter.DetectConfig(ctx, scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if spec.Format == FormatTOML {
		var names []string
		prefix := "[" + strings.Join(spec.KeyPath, ".") + "."
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, "]") {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), "]"))
			}
		}
		return names, nil
	}
	root, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, data)
	if perr != nil {
		return nil, perr
	}
	var names []string
	for name := range lookupInMap(root, spec.KeyPath) {
		if name == litespmServerName || name == legacyServerName {
			continue
		}
		names = append(names, name)
	}
	sortStrings(names)
	return names, nil
}

// RegisteredBridgeHosts returns the hosts whose LiteSPM bridge entry verifies as
// present. It is the default target set for a server install: a host the user
// never set LiteSPM up in is not a host we should be editing.
func RegisteredBridgeHosts(ctx context.Context, scope domain.InstallScope) []string {
	var hosts []string
	for _, adapter := range ListAdapters() {
		id := adapter.Descriptor().HostID
		verification, err := adapter.VerifySetup(ctx)
		if err != nil || verification == nil || !verification.Registered {
			continue
		}
		if _, ok := entrySpecFor(adapter); !ok {
			continue
		}
		hosts = append(hosts, id)
	}
	sortStrings(hosts)
	return hosts
}
