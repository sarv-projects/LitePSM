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
	// ShapeStdioTyped is {"type": "stdio", "command": "...", "args": [...]}.
	// Crush is the case: its published JSON schema marks `type` as required and
	// its Go config decoder has no omitempty and no default, so an entry without
	// it matches no transport case and the server is never started. Most hosts
	// default an absent `type` to stdio, so this shape is opt-in per row.
	ShapeStdioTyped EntryShape = "stdio-typed"
	// ShapeCommandString maps a server name straight to one stdio command
	// line, with no wrapper object (Xum/Mux).
	ShapeCommandString EntryShape = "command-string"
)

// The registry vocabulary for a remote (URL) MCP entry's transport. Host
// vocabularies differ per row (`http`, `streamableHttp`, `remote`, or no
// discriminator at all); these two tokens are what LiteSPM speaks, and the
// mapping onto a host's own spelling is explicit and closed — a token the host
// cannot express is a refusal, never a guess (LPSM-HOST-REMOTE-UNSUPPORTED).
const (
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
)

// remoteTransportVocab is the closed set RemoteEntrySpec.Transports may
// declare. Kept as data so the validation test and the writer share one list.
var remoteTransportVocab = []string{TransportStreamableHTTP, TransportSSE}

// normalizeRemoteTransport maps the caller's transport onto the registry
// vocabulary: the empty string defaults to streamable-http (the transport the
// official registry publishes in practice), and anything else is passed
// through so an unknown token fails the capability check instead of being
// silently upgraded.
func normalizeRemoteTransport(transport string) string {
	if strings.TrimSpace(transport) == "" {
		return TransportStreamableHTTP
	}
	return strings.TrimSpace(transport)
}

// RegistryRemoteTransport maps a transport value READ BACK from a host config
// onto the registry vocabulary. Host discriminators (`http` for Claude Code,
// `streamableHttp` for Cline, `remote` for OpenCode) all mean streamable-http;
// `sse` means sse. An unrecognised discriminator is returned unchanged so the
// capability check downstream fails closed rather than assuming a transport
// the catalog never asked for.
func RegistryRemoteTransport(hostTransport string) string {
	switch v := strings.TrimSpace(hostTransport); v {
	case TransportSSE:
		return TransportSSE
	case "", "http", "streamableHttp", "remote", TransportStreamableHTTP:
		return TransportStreamableHTTP
	default:
		return v
	}
}

// RemoteEntrySpec is how ONE host spells a remote (URL) MCP entry — an entry
// that names an endpoint instead of launching a process.
//
// It is deliberately a separate axis from EntryShape: a host's stdio shape
// (`object`, `local-array`, …) says nothing about how it discriminates a URL
// entry, and modelling remote as a fifth EntryShape would force one enum to
// express both axes (and break web/lib/hosts.ts's closed HostShape union) for
// no gain. A nil Remote means the host cannot express a URL entry at all —
// absence is the refusal.
//
// Honesty rule (the same rule the row's DocsURL lives by): a Remote spec must
// only be set when the EXACT entry object was read from the row's DocsURL or
// repository — the URL key, the discriminator key, its values and which
// transports the host accepts. A field name glimpsed in a doc is evidence of a
// URL key, not of a complete entry shape; the row stays Remote-free (Tier B in
// the B1 acceptance matrix) until the whole object is pinned. A guessed spec
// writes an entry the host silently ignores, which is worse than refusing.
type RemoteEntrySpec struct {
	// URLKey is the key holding the endpoint: "url", "serverUrl", "httpUrl".
	URLKey string
	// TypeKey is the discriminator key ("" when the host infers the transport
	// from the URL alone).
	TypeKey string
	// TypeValue is the value written for streamable-http ("http",
	// "streamableHttp", "remote", …). Only meaningful when TypeKey is set.
	TypeValue string
	// SSEValue is the value written for the sse discriminator, and only makes
	// sense when TypeKey is set. A bare-URL host (no discriminator) infers the
	// transport from the endpoint itself, so it may declare sse in Transports
	// while writing no SSEValue at all.
	SSEValue string
	// Transports is the registry vocabulary this host can express, e.g.
	// {streamable-http} or {streamable-http, sse}. It is the authority the
	// writer checks: a transport outside this list is refused, never rendered
	// as a bare URL or a defaulted type.
	Transports []string
}

// Supports reports whether the host can express the registry transport.
// An empty transport counts as streamable-http (the default the writer uses).
func (r *RemoteEntrySpec) Supports(transport string) bool {
	if r == nil {
		return false
	}
	want := normalizeRemoteTransport(transport)
	for _, t := range r.Transports {
		if t == want {
			return true
		}
	}
	return false
}

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

	// Remote is how this host spells a remote (URL) MCP entry, or nil when it
	// cannot express one — in which case a remote install, copy or plan for
	// this host is refused fail-closed (LPSM-HOST-REMOTE-UNSUPPORTED), never
	// rendered as a stdio entry and never written with a guessed URL key.
	//
	// The honesty rule above applies verbatim: Remote may only be set when the
	// exact entry object was read from this row's DocsURL or repository. An
	// unverified row keeps Remote nil and records the gap in Note.
	Remote *RemoteEntrySpec

	// FlatKey marks a host whose container is a single literal member name that
	// happens to contain a dot, rather than a chain of nested objects. Amp is the
	// case: its published settings schema declares the property literally as
	// "amp.mcpServers" with additionalProperties:false, so writing the nested
	// object {"amp":{"mcpServers":{...}}} produces a file Amp both ignores and
	// rejects. Hosts that genuinely nest (`zcode` uses mcp.servers) must leave
	// this false, because for them the dot is structure, not punctuation.
	FlatKey bool

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

// litespmServerName is the MCP server name injected into every host config.
const litespmServerName = "litespm"

// legacyServerName is the MCP server name written by releases before the
// legacy "LitePSM"/"litepsm" -> current "LiteSPM"/"litespm" rename.
//
// A pre-existing legacy entry is detected and adopted (removed and replaced by
// the current name) during setup, and is deleted during removal, so a host that
// predates the rename ends up with exactly one bridge entry instead of two.
const legacyServerName = "litepsm"

// bridgeArgs is the argv LiteSPM injects into every host.
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
	case ShapeStdioTyped:
		return map[string]any{"type": "stdio", "command": bin, "args": bridgeArgs(t.ID)}
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
	return fmt.Sprintf("[mcp_servers.litespm]\ncommand = %q\nargs = [%s]\n",
		bin, strings.Join(quoted, ", "))
}

func filepathSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// keyPathFor returns the key path for a scope. Hosts that only document
// repo-local configuration declare no user key, so user scope falls back to the
// project key rather than producing an empty path.
func (t BridgeTarget) keyPathFor(projectScope bool) []string {
	keys := t.ProjectKey
	if !(projectScope && len(t.ProjectKey) > 0) && len(t.UserKey) > 0 {
		keys = t.UserKey
	}
	if t.FlatKey && len(keys) > 1 {
		return []string{strings.Join(keys, ".")}
	}
	return keys
}

// lookupEntry returns the stored value for the litespm server at a key path.
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

// mergeJSONEntry writes the litespm entry into the object at keyPath, creating
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
	cur[litespmServerName] = value
	return nil
}

// inspectEntry reports whether a parsed config already registers litespm.
func inspectEntry(root map[string]any, keyPath []string) bool {
	return inspectNamedEntry(root, keyPath, litespmServerName)
}

// inspectNamedEntry reports whether the object at keyPath contains a member
// named name. It is used to check for both the current and the legacy bridge
// names.
func inspectNamedEntry(root map[string]any, keyPath []string, name string) bool {
	v, ok := lookupEntry(root, keyPath)
	if !ok || v == nil {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, present := m[name]
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
		if k == litespmServerName || k == legacyServerName {
			continue
		}
		names = append(names, k)
	}
	return names
}
