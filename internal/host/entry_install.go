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
	"sort"
	"strconv"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/envref"
)

// ServerEntry is one MCP server registration as it will appear in a host config.
type ServerEntry struct {
	// Name is the key the host stores the server under. It is also the name the
	// user sees in their agent's server list, so it must be the server's own
	// name — never `litespm`, which is reserved for the bridge.
	Name    string
	Command string
	Args    []string
	// Env holds environment values found in an EXISTING config, as literal
	// key/value pairs. It is populated on read-back and is never written from an
	// install: an install states variable NAMES (EnvNames) and the config gets a
	// reference, so a secret never passes through LiteSPM at all.
	Env map[string]string
	// EnvNames are the variables the user asked to forward. The reference text is
	// built per host at write time, because no two hosts spell it the same way and
	// one host takes no value whatsoever (see internal/envref).
	EnvNames []string
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
	// Fingerprint is the hash of the entry as read back from the written
	// config (see EntryState): the ledger's post-image for this node.
	Fingerprint string
	// PriorEntry is the canonical text of the user-authored entry this call
	// replaced ("" when none existed); RemoveServerEntry restores it.
	PriorEntry string
	// PriorFingerprint is the hash of PriorEntry ("" when none existed).
	PriorFingerprint string
}

// entrySpec is everything that varies between hosts for a server entry.
type entrySpec struct {
	// KeyPath is the container the server map lives at.
	KeyPath []string
	Format  ConfigFormat
	// Shape decides how the entry itself is expressed.
	Shape EntryShape
	// EnvRef is the host's documented environment-forwarding behaviour. A zero
	// value means no verified behaviour, which is why entrySpecFor records
	// whether it found one at all rather than assuming every host looks alike.
	EnvRef envref.Spec
	// HasEnvRef distinguishes "no verified row for this host" from a row that
	// happens to be empty.
	HasEnvRef bool
}

// entrySpecFor resolves the per-host entry shape. Data-driven targets already
// declare all three; the hand-written adapters declare them here, next to the
// removal specs that describe the same containers.
func entrySpecFor(adapter HostAdapter) (entrySpec, bool) {
	return entrySpecForScope(adapter, domain.ScopeUser)
}

// EntryLayout exposes the server-entry container key path and entry shape for
// any registered adapter — user scope, the same spec InstallServerEntry writes
// with. keyPath is dotted ("mcpServers", "mcp", "amp.mcpServers"); ok is false
// when the registry declares no layout for the host, which callers must treat
// as "fall back to the format's conventional default", never as an empty path.
//
// scripts/gen_hosts_ts.go emits this into web/data/hosts.json, which is what
// web/lib/hosts.ts imports, so the site can no longer disagree with the
// binary: it used to hard-code the six hand-written adapters and had OpenCode
// wrong (a two-level `mcp.servers` path that no OpenCode release reads —
// servers are direct members of `mcp`, see opencode.go).
func EntryLayout(adapter HostAdapter) (keyPath string, shape string, ok bool) {
	spec, ok := entrySpecFor(adapter)
	if !ok || len(spec.KeyPath) == 0 {
		return "", "", false
	}
	return strings.Join(spec.KeyPath, "."), string(spec.Shape), true
}

// entrySpecForScope is entrySpecFor with the install scope honoured. Hosts
// whose user and project containers differ (fx reads `mcp` in the user file
// and `mcpServers` in a project `.mcp.json`) must be written at the key the
// scope actually uses: entrySpecFor always returned the user key, so a
// project-scope install into fx landed under `mcp`, where fx never looks.
func entrySpecForScope(adapter HostAdapter, scope domain.InstallScope) (entrySpec, bool) {
	id := strings.ToLower(adapter.Descriptor().HostID)
	projectScope := scope == domain.ScopeProject
	var base entrySpec
	switch g := adapter.(type) {
	case *GenericAdapter:
		base = entrySpec{
			KeyPath: g.Target.keyPathFor(projectScope),
			Format:  g.Target.Format,
			Shape:   g.Target.Shape,
		}
	default:
		spec, ok := bespokeEntrySpecs[id]
		if !ok {
			return entrySpec{}, false
		}
		base = spec
	}
	if s, ok := envref.SpecFor(id); ok {
		base.EnvRef = s
		base.HasEnvRef = true
	}
	return base, true
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
		out := map[string]any{"type": "local", "command": argv}
		s.addEnvFields(out, e)
		return out
	case ShapeStdioTyped:
		out := map[string]any{"type": "stdio", "command": e.Command, "args": e.Args}
		s.addEnvFields(out, e)
		return out
	case ShapeCommandString:
		parts := append([]string{quoteCommandArg(e.Command)}, e.Args...)
		return strings.Join(parts, " ")
	default:
		out := map[string]any{"command": e.Command}
		if len(e.Args) > 0 {
			out["args"] = e.Args
		}
		s.addEnvFields(out, e)
		return out
	}
}

// addEnvFields writes the forwarded variables into an entry object, in whichever
// shape the host documents.
//
// The two shapes are not variants of one mechanism. A reference-host gets an
// environment object whose values are references; a name-list host gets a
// separate array of names and stores no value at all, because it has no
// substitution to offer and would otherwise forward the reference's literal text
// as if it were the credential.
// EnvForwarding reports whether a host can express forwarded variables at all,
// and returns its verified rules when it can.
//
// Two different things make a host unable, and they are reported separately
// because the user's next step differs. A host whose entry shape is a bare
// command string cannot carry an environment object no matter what syntax is
// used, so accepting `--env` there would write an entry that looks credentialed
// and is not. A host with no row in internal/envref has a shape that could hold
// one but no documented spelling to put in it. Both are refusals, not warnings.
func EnvForwarding(hostID string) (envref.Spec, bool) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return envref.Spec{}, false
	}
	spec, ok := entrySpecFor(adapter)
	if !ok {
		return envref.Spec{}, false
	}
	if !spec.HasEnvRef {
		return envref.Spec{}, false
	}
	if spec.Shape == ShapeCommandString {
		return envref.Spec{}, false
	}
	return spec.EnvRef, true
}

func (s entrySpec) addEnvFields(out map[string]any, e ServerEntry) {
	if len(e.EnvNames) > 0 && s.HasEnvRef && s.EnvRef.Style == envref.StyleEnvNameList {
		out[s.EnvRef.NameListField] = envref.SortedNames(e.EnvNames)
		return
	}
	if m := s.EnvRef.EnvMap(e.EnvNames); len(m) > 0 {
		out[s.EnvRef.Field] = m
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
	if len(e.EnvNames) > 0 && s.HasEnvRef && s.EnvRef.Style == envref.StyleEnvNameList {
		// A name-list host takes no value, so there is nothing to quote.
		names := make([]string, 0, len(e.EnvNames))
		for _, n := range envref.SortedNames(e.EnvNames) {
			names = append(names, fmt.Sprintf("%q", n))
		}
		fmt.Fprintf(&b, "%s = [%s]\n", s.EnvRef.NameListField, strings.Join(names, ", "))
	} else if m := s.EnvRef.EnvMap(e.EnvNames); len(m) > 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		// Deterministic order: a config that reorders itself on every install is
		// unreviewable in a diff.
		sortStrings(keys)
		fmt.Fprintf(&b, "%s = { %s }\n", s.EnvRef.Field, joinTOMLPairs(keys, m))
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

// parseTOMLStringArray reads a TOML array of quoted strings, such as
// `["-y", "demo"]` or `["GITHUB_TOKEN"]`.
//
// It does not use strconv.Unquote on the whole value: that expects a single Go
// string literal and can never succeed on an array, so an array read that way
// silently yields nothing. Splitting has to be quote-aware, because an argument
// may itself contain a comma.
func parseTOMLStringArray(value string) []string {
	body := strings.TrimSpace(value)
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		return nil
	}
	var out []string
	for _, item := range splitTOMLPairs(body[1 : len(body)-1]) {
		if s, err := strconv.Unquote(item); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// splitTOMLPairs splits the body of an inline table or an array into its
// comma-separated items.
//
// It tracks quoting rather than splitting on commas, because a reference or a
// hand-written value may legitimately contain one and cutting mid-value would
// produce keys that do not exist and silently drop the variable.
func splitTOMLPairs(body string) []string {
	var out []string
	var cur strings.Builder
	inQuote, escaped := false, false
	for _, r := range body {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
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
	spec, ok := entrySpecForScope(adapter, opts.Scope)
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
	priorEntry := ""
	if _, present := lookupServerEntry(spec, original, entry.Name); present {
		priorEntry, _, _ = entryStateFromContent(spec, original, entry.Name)
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
	writtenRaw, ok, serr := entryStateFromContent(spec, string(written), entry.Name)
	if serr != nil || !ok {
		return nil, fmt.Errorf("%s: wrote %s but the entry %q cannot be fingerprinted: %v", adapter.Descriptor().HostID, configPath, entry.Name, serr)
	}
	priorFP := ""
	if priorEntry != "" {
		priorFP = fingerprintOf(priorEntry)
	}

	return &EntryInstallResult{
		HostID:           adapter.Descriptor().HostID,
		ConfigPath:       configPath,
		Name:             entry.Name,
		Replaced:         replaced,
		Created:          created,
		Fingerprint:      fingerprintOf(writtenRaw),
		PriorEntry:       priorEntry,
		PriorFingerprint: priorFP,
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

// HostServerEntry is a registered server as it exists in a host config, with the
// listing it came from when LiteSPM wrote it. Reading it back is how a caller
// recovers the command to run without the catalog: the config the user can see
// is the single source of truth for what is installed.
type HostServerEntry struct {
	Name    string
	Command string
	Args    []string
	// Env is what the config actually stores, which is a reference on a
	// host that substitutes one and empty on a name-list host that stores no
	// value. Either way it is a name to resolve, never a secret to read.
	Env       map[string]string
	EnvNames  []string
	Transport string
	ListingID string
}

// ListServerEntriesWithValues returns the registered servers with their launch
// lines, skipping the bridge. listingID is resolved from the install records
// passed in, so a server LiteSPM did not install comes back without one.
func ListServerEntriesWithValues(ctx context.Context, hostID string, scope domain.InstallScope) ([]HostServerEntry, error) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecForScope(adapter, scope)
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
		return tomlServerEntries(spec, string(data)), nil
	}

	root, perr := parseConfigJSON(BridgeTarget{TolerateComments: true}, data)
	if perr != nil {
		return nil, fmt.Errorf("%s: %w", adapter.Descriptor().DisplayName, perr)
	}
	var out []HostServerEntry
	for name, value := range lookupInMap(root, spec.KeyPath) {
		if name == litespmServerName || name == legacyServerName {
			continue
		}
		entry := HostServerEntry{Name: name, Transport: "stdio"}
		if m, ok := value.(map[string]any); ok {
			entry.Command, _ = m["command"].(string)
			entry.Transport, _ = m["type"].(string)
			if argv, ok := m["command"].([]any); ok {
				// ShapeLocalArray hosts store a combined argv array.
				entry.Command = ""
				for i, item := range argv {
					if s, ok := item.(string); ok {
						if i == 0 {
							entry.Command = s
						} else {
							entry.Args = append(entry.Args, s)
						}
					}
				}
			}
			if raw, ok := m["args"].([]any); ok {
				for _, item := range raw {
					if s, ok := item.(string); ok {
						entry.Args = append(entry.Args, s)
					}
				}
			}
			// Read every key this host could have used, not just `env`: OpenCode
			// names it `environment`, and a name-list host stores names in an
			// array with no value at all. Missing either form here means the probe
			// would spawn the server without the variable the user forwarded.
			for _, field := range []string{"env", "environment"} {
				if env, ok := m[field].(map[string]any); ok {
					if entry.Env == nil {
						entry.Env = map[string]string{}
					}
					for k, v := range env {
						if s, ok := v.(string); ok {
							entry.Env[k] = s
						}
					}
				}
			}
			if names, ok := m[spec.EnvRef.NameListField].([]any); ok && spec.HasEnvRef {
				if entry.Env == nil {
					entry.Env = map[string]string{}
				}
				for _, item := range names {
					if n, ok := item.(string); ok {
						// The value is not in the config; record the name so the
						// probe resolves it from this process's environment.
						entry.EnvNames = append(entry.EnvNames, n)
						if _, seen := entry.Env[n]; !seen {
							entry.Env[n] = ""
						}
					}
				}
			}
		} else if s, ok := value.(string); ok && spec.Shape == ShapeCommandString {
			// ShapeCommandString hosts store one bare command line.
			fields := strings.Fields(s)
			if len(fields) > 0 {
				entry.Command = fields[0]
				entry.Args = fields[1:]
			}
		}
		if entry.Command == "" {
			continue
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// tomlServerEntries reads [mcp_servers.<name>] tables back into entries.
func tomlServerEntries(spec entrySpec, content string) []HostServerEntry {
	prefix := "[" + strings.Join(spec.KeyPath, ".") + "."
	var out []HostServerEntry
	var current *HostServerEntry
	flush := func() {
		if current != nil && current.Command != "" {
			out = append(out, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, "]") {
			flush()
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), "]")
			if name == litespmServerName || name == legacyServerName {
				continue
			}
			current = &HostServerEntry{Name: name, Transport: "stdio"}
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "command":
			if unquoted, err := strconv.Unquote(value); err == nil {
				current.Command = unquoted
			}
		case "args":
			current.Args = parseTOMLStringArray(value)
		case "env":
			// `env = { "NAME" = "value" }`. The value is a reference on a
			// substitution host and a literal on one the user hand-edited; either
			// way the caller resolves it, so it is preserved as written.
			inner := strings.TrimSpace(strings.Trim(value, "{}"))
			if inner == "" {
				break
			}
			if current.Env == nil {
				current.Env = map[string]string{}
			}
			for _, pair := range splitTOMLPairs(inner) {
				k, v, ok := strings.Cut(pair, "=")
				if !ok {
					continue
				}
				key, err1 := strconv.Unquote(strings.TrimSpace(k))
				val, err2 := strconv.Unquote(strings.TrimSpace(v))
				if err1 == nil && err2 == nil {
					current.Env[key] = val
				}
			}
		case spec.EnvRef.NameListField:
			// A name-list host stores names and no values. Record the names so the
			// probe can resolve them from the environment it runs in.
			if spec.EnvRef.NameListField == "" {
				break
			}
			for _, n := range parseTOMLStringArray(value) {
				current.EnvNames = append(current.EnvNames, n)
				if current.Env == nil {
					current.Env = map[string]string{}
				}
				if _, seen := current.Env[n]; !seen {
					current.Env[n] = ""
				}
			}
		}
	}
	flush()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListServerEntries returns the entry names currently registered for a host, so
// an install can report what is already configured and refuse a collision before
// touching a file.
func ListServerEntries(ctx context.Context, hostID string, scope domain.InstallScope) ([]string, error) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecForScope(adapter, scope)
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
