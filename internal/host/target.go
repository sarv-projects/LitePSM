package host

// target.go — data-driven agent host (bridge) targets.
//
// Why this exists: the first six hosts were each written as a bespoke Go
// adapter, ~170-290 lines of mostly identical backup/merge/verify boilerplate,
// with the per-agent knowledge (config path, format, MCP key, entry shape)
// buried in code and carrying no evidence trail. Scaling to the wider agent
// ecosystem that way is unsustainable, and worse, an unverified path is
// silently wrong.
//
// A BridgeTarget is that knowledge as data. One GenericAdapter implements the
// HostAdapter contract for every target, so adding an agent is a table row
// plus a test, and the row records the documentation URL it came from.
//
// Honesty rule: a target must not be registered unless its config path, key and
// entry shape were read from that agent's own docs or repository. Unverified
// agents stay out of the table and remain unsupported. `Note` records why.

import (
	"fmt"
	"strings"
)

// ConfigFormat is the on-disk format of a host's MCP configuration.
type ConfigFormat string

const (
	// FormatJSON is a JSON object keyed by MCP server name.
	FormatJSON ConfigFormat = "json"
	// FormatTOML is TOML with one table per MCP server.
	FormatTOML ConfigFormat = "toml"
)

// EntryShape is how a single MCP server is expressed.
type EntryShape string

const (
	// ShapeObject is {"command": "...", "args": [...]} — the common shape.
	ShapeObject EntryShape = "object"
	// ShapeLocalArray is {"type": "local", "command": ["bin", "arg", ...]} —
	// a single combined argv array with an explicit transport type (OpenCode,
	// Kilo, fx, CodeArts, Posit Assistant).
	ShapeLocalArray EntryShape = "local-array"
	// ShapeCommandString maps a server name straight to one stdio command
	// line, with no wrapper object (Xum/Mux).
	ShapeCommandString EntryShape = "command-string"
)

// BridgeTarget is the verified, per-agent description of an MCP config.
type BridgeTarget struct {
	ID   string
	Name string

	// SlashCommandTrigger is the in-agent command this host is told to use.
	SlashCommandTrigger string

	// UserPath resolves the user-scope config file. Required.
	UserPath func(home string) string
	// ProjectPath resolves the repo-local config file. May be nil for hosts
	// that only accept user-scope configuration.
	ProjectPath func(root string) string

	// Format selects the JSON or TOML writer.
	Format ConfigFormat
	// UserKey is the nested JSON key path (or TOML table prefix) for user scope.
	UserKey []string
	// ProjectKey is the key path for project scope; falls back to UserKey.
	ProjectKey []string
	// Shape is how one server entry is written.
	Shape EntryShape

	// TolerateComments marks hosts whose config permits `//` and `/* */`
	// comments (JSONC). We strip comments before parsing and never rewrite
	// them, so hand-authored comments in those files are left untouched.
	TolerateComments bool

	// DetectPaths are home-relative paths whose presence indicates the agent
	// is installed (first match is enough).
	DetectPaths []string
	// DetectBinaries are executable names looked up on PATH, if any.
	DetectBinaries []string

	// SharedConfigWith lists other target ids that intentionally resolve to the
	// same config file. Both sides must declare the overlap and the row's Note
	// must explain it: two hosts silently sharing one file means setting up
	// either one rewrites the other's configuration.
	SharedConfigWith []string

	// DocsURL is the source this row was verified against. Required.
	DocsURL string
	// Note records caveats, deprecations, or unresolved ambiguity.
	Note string
}

// litepsmServerName is the MCP server name injected into every host config.
const litepsmServerName = "litepsm"

// bridgeArgs is the argv LitePSM injects into every host.
func bridgeArgs(hostID string) []string {
	return []string{"bridge", "stdio", "--host", hostID}
}

// jsonEntryValue returns the JSON value for one MCP server entry.
func (t BridgeTarget) jsonEntryValue(binaryPath string) any {
	bin := filepathSlash(binaryPath)
	switch t.Shape {
	case ShapeLocalArray:
		argv := append([]string{bin}, bridgeArgs(t.ID)...)
		return map[string]any{"type": "local", "command": argv}
	case ShapeCommandString:
		return quoteCommandArg(bin) + " " + strings.Join(bridgeArgs(t.ID), " ")
	default:
		return map[string]any{"command": bin, "args": bridgeArgs(t.ID)}
	}
}

// quoteCommandArg wraps a binary path in double quotes when it contains
// characters that a shell-style command line would otherwise split on.
func quoteCommandArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// tomlEntryBlock returns the TOML block for one MCP server table.
func (t BridgeTarget) tomlEntryBlock(binaryPath string) string {
	bin := filepathSlash(binaryPath)
	quoted := make([]string, len(bridgeArgs(t.ID)))
	for i, a := range bridgeArgs(t.ID) {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf("[mcp_servers.litepsm]\ncommand = %q\nargs = [%s]\n",
		bin, strings.Join(quoted, ", "))
}

func filepathSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// keyPathFor returns the key path for a scope. Hosts that only document
// repo-local configuration declare no user key, so user scope falls back to the
// project key rather than producing an empty path.
func (t BridgeTarget) keyPathFor(projectScope bool) []string {
	if projectScope && len(t.ProjectKey) > 0 {
		return t.ProjectKey
	}
	if len(t.UserKey) > 0 {
		return t.UserKey
	}
	return t.ProjectKey
}

// lookupEntry returns the stored value for the litepsm server at a key path.
func lookupEntry(root map[string]any, keyPath []string) (any, bool) {
	if len(keyPath) == 0 {
		return nil, false
	}
	cur := any(root)
	for _, k := range keyPath {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// mergeJSONEntry writes the litepsm entry into the object at keyPath, creating
// intermediate objects. It refuses to overwrite an intermediate level that
// holds a non-object value, so an unrelated scalar is never silently replaced.
func mergeJSONEntry(root map[string]any, keyPath []string, value any) error {
	if len(keyPath) == 0 {
		return fmt.Errorf("empty key path")
	}
	cur := root
	for _, k := range keyPath {
		next, ok := cur[k]
		if !ok || next == nil {
			m := map[string]any{}
			cur[k] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("key %q holds a non-object value; refusing to overwrite", k)
		}
		cur = m
	}
	cur[litepsmServerName] = value
	return nil
}

// inspectEntry reports whether a parsed config already registers litepsm.
func inspectEntry(root map[string]any, keyPath []string) bool {
	v, ok := lookupEntry(root, keyPath)
	if !ok || v == nil {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, present := m[litepsmServerName]
	return present
}

// listEntryNames returns the other MCP server names declared at keyPath.
func listEntryNames(root map[string]any, keyPath []string) []string {
	v, ok := lookupEntry(root, keyPath)
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(m))
	for k := range m {
		if k == litepsmServerName {
			continue
		}
		names = append(names, k)
	}
	return names
}
