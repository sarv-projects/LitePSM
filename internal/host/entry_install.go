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
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/envref"
	"github.com/sarv-projects/litespm/internal/fslock"
)

// ServerEntry is one MCP server registration as it will appear in a host config.
type ServerEntry struct {
	// Name is the key the host stores the server under. It is also the name the
	// user sees in their agent's server list, so it must be the server's own
	// name — never `litespm`, which is reserved for the bridge.
	Name    string
	Command string
	Args    []string
	// Endpoint is the remote (URL) MCP server to REGISTER rather than launch.
	// An entry carries EITHER Command (stdio) OR Endpoint (remote), never both:
	// an entry is one transport, and a host that received a URL and a command
	// would start one of them by its own rule, not by ours. Empty for every
	// stdio entry.
	Endpoint string
	// Transport is the registry vocabulary for a remote entry:
	// "streamable-http" (the default when empty) or "sse". It is only read
	// when Endpoint is set; the per-host discriminator (`http`,
	// `streamableHttp`, `remote`, or nothing) is derived from it at write time
	// and read back into HostServerEntry.Transport.
	Transport string
	// Env holds environment values found in an EXISTING config, as literal
	// key/value pairs. It is populated on read-back and is never written from an
	// install: an install states variable NAMES (EnvNames) and the config gets a
	// reference, so a secret never passes through LiteSPM at all.
	Env map[string]string
	// EnvNames are the variables the user asked to forward. The reference text is
	// built per host at write time, because no two hosts spell it the same way and
	// one host takes no value whatsoever (see internal/envref). A remote entry
	// refuses EnvNames outright: no host documents a mapping from a bare
	// variable name to a remote-header spelling, so accepting it would write an
	// entry that looks credentialed and is not.
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
	// WrittenDigest binds rollback to the exact whole-file post-image. If a
	// later step fails after another writer edits the config, compensation must
	// refuse to overwrite that newer content.
	WrittenDigest string
	// PriorEntry is the canonical text of the user-authored entry this call
	// replaced ("" when none existed); RemoveServerEntry restores it.
	PriorEntry string
	// PriorFingerprint is the hash of PriorEntry ("" when none existed).
	PriorFingerprint string
	// BackupPath is the pre-edit copy of the config this write was taken
	// against ("" when the config did not exist, i.e. Created). Callers use it
	// to restore the file byte-for-byte when a later install step fails.
	BackupPath string
}

// entrySpec is everything that varies between hosts for a server entry.
type entrySpec struct {
	// KeyPath is the container the server map lives at.
	KeyPath []string
	Format  ConfigFormat
	// Shape decides how the entry itself is expressed (stdio axis).
	Shape EntryShape
	// Remote is how this host spells a remote (URL) entry, or nil when it
	// cannot express one. It rides beside Shape because the two axes are
	// independent: a host with ShapeObject for stdio may still want
	// type:"streamableHttp" for remote, or no remote spelling at all.
	Remote *RemoteEntrySpec
	// HostID is the registry id this spec was resolved for; it exists so a
	// refusal can name the host the user actually asked for.
	HostID string
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
			Remote:  g.Target.Remote,
		}
	default:
		spec, ok := bespokeEntrySpecs[id]
		if !ok {
			return entrySpec{}, false
		}
		base = spec
	}
	base.HostID = id
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
//
// The Remote specs mirror the vendor's own remote-entry objects, each fetched
// live from the URL in that vendor's evidence row (see the B1 acceptance
// matrix, PART 1 §1.3, for the exact document per host). They are constructors
// rather than shared pointers so no caller can mutate one host's spec through
// another's alias.
var bespokeEntrySpecs = map[string]entrySpec{
	"claude-code": {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject, Remote: claudeRemoteSpec()},
	"cline":       {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject, Remote: clineRemoteSpec()},
	"pi-agent":    {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject, Remote: bareURLRemoteSpec(TransportStreamableHTTP)},
	"pi":          {Format: FormatJSON, KeyPath: []string{"mcpServers"}, Shape: ShapeObject, Remote: bareURLRemoteSpec(TransportStreamableHTTP)},
	// OpenCode requires `type` and a combined argv array; its `mcp` container is
	// a flat map of server entries (there is no nested `servers` object).
	"opencode":   {Format: FormatJSON, KeyPath: []string{"mcp"}, Shape: ShapeLocalArray, Remote: opencodeRemoteSpec()},
	"codex":      {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject, Remote: bareURLRemoteSpec(TransportStreamableHTTP)},
	"grok":       {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject, Remote: bareURLRemoteSpec(TransportStreamableHTTP)},
	"grok-build": {Format: FormatTOML, KeyPath: []string{"mcp_servers"}, Shape: ShapeObject, Remote: bareURLRemoteSpec(TransportStreamableHTTP)},
}

// claudeRemoteSpec — Claude Code: {"type":"http","url":"https://…"}; `type` is
// mandatory (a url with no type is a configuration error the host skips and
// reports), and `type` ∈ http | sse | ws.
func claudeRemoteSpec() *RemoteEntrySpec {
	return &RemoteEntrySpec{
		URLKey: "url", TypeKey: "type", TypeValue: "http", SSEValue: "sse",
		Transports: []string{TransportStreamableHTTP, TransportSSE},
	}
}

// clineRemoteSpec — Cline: {"type":"streamableHttp","url":"https://…"};
// omitting `type` defaults to the LEGACY sse transport, so writing no
// discriminator would silently register the wrong transport.
func clineRemoteSpec() *RemoteEntrySpec {
	return &RemoteEntrySpec{
		URLKey: "url", TypeKey: "type", TypeValue: "streamableHttp", SSEValue: "sse",
		Transports: []string{TransportStreamableHTTP, TransportSSE},
	}
}

// opencodeRemoteSpec — OpenCode: {"type":"remote","url":"https://…"}; the
// published schema defines no sse value, so sse is refused here.
func opencodeRemoteSpec() *RemoteEntrySpec {
	return &RemoteEntrySpec{
		URLKey: "url", TypeKey: "type", TypeValue: "remote",
		Transports: []string{TransportStreamableHTTP},
	}
}

// bareURLRemoteSpec — the hosts whose documented remote entry is a bare `url`
// member with no discriminator (Codex, Grok Build, Pi, Cursor, Zed): the host
// infers the transport from the endpoint itself. sse is only listed where the
// vendor docs actually document it for URL entries (Cursor); Codex documents
// the key as streamable-HTTP-only and Pi explicitly rejects sse.
func bareURLRemoteSpec(transports ...string) *RemoteEntrySpec {
	return &RemoteEntrySpec{URLKey: "url", Transports: transports}
}

// RemoteUnsupportedCode is the typed refusal for a remote (URL) operation a
// host cannot express. It names the host, the transport and the reason, and
// never falls back to "write it as stdio" or to a guessed URL key.
const RemoteUnsupportedCode = "LPSM-HOST-REMOTE-UNSUPPORTED"

// remoteTypeValue resolves the discriminator to write for a remote entry, or
// refuses. hostID names the host in the refusal; spec is nil when the host
// declares no remote spelling at all.
func remoteTypeValue(hostID string, spec *RemoteEntrySpec, transport string) (string, error) {
	want := normalizeRemoteTransport(transport)
	if spec == nil {
		return "", fmt.Errorf("%s: host %q cannot express a remote (URL) MCP entry: no URL key or transport "+
			"discriminator was ever verified for it, so a remote install there would be refused fail-closed rather than guessed",
			RemoteUnsupportedCode, hostID)
	}
	if !spec.Supports(want) {
		return "", fmt.Errorf("%s: host %q cannot express transport %q for a remote (URL) MCP entry (it documents %v)",
			RemoteUnsupportedCode, hostID, want, spec.Transports)
	}
	switch want {
	case TransportSSE:
		if spec.TypeKey != "" && strings.TrimSpace(spec.SSEValue) == "" {
			return "", fmt.Errorf("%s: host %q requires a discriminator for sse but the spec records none; refusing rather than writing a bare url",
				RemoteUnsupportedCode, hostID)
		}
		return spec.SSEValue, nil
	default:
		return spec.TypeValue, nil
	}
}

// entryValue renders the entry in the host's own shape.
//
// A remote entry renders ONLY the URL key plus its discriminator — never
// command, args or env — because a remote entry is a different transport, not
// a stdio entry with a funny command. The discriminator is resolved through
// remoteTypeValue, so an unsupported transport fails here as well as at the
// write gate: the typeless-url hazard (Claude Code skips it, Cline silently
// defaults it to legacy sse) can never be rendered by accident.
func (s entrySpec) entryValue(e ServerEntry) (any, error) {
	if s.Remote != nil && strings.TrimSpace(e.Endpoint) != "" {
		typeValue, err := remoteTypeValue(s.HostID, s.Remote, e.Transport)
		if err != nil {
			return nil, err
		}
		out := map[string]any{s.Remote.URLKey: e.Endpoint}
		if s.Remote.TypeKey != "" {
			out[s.Remote.TypeKey] = typeValue
		}
		return out, nil
	}
	switch s.Shape {
	case ShapeLocalArray:
		argv := append([]string{e.Command}, e.Args...)
		out := map[string]any{"type": "local", "command": argv}
		s.addEnvFields(out, e)
		return out, nil
	case ShapeStdioTyped:
		out := map[string]any{"type": "stdio", "command": e.Command, "args": e.Args}
		s.addEnvFields(out, e)
		return out, nil
	case ShapeCommandString:
		parts := append([]string{quoteCommandArg(e.Command)}, e.Args...)
		return strings.Join(parts, " "), nil
	default:
		out := map[string]any{"command": e.Command}
		if len(e.Args) > 0 {
			out["args"] = e.Args
		}
		s.addEnvFields(out, e)
		return out, nil
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
// It is a per-HOST capability (stdio entries only): a remote (URL) entry
// refuses EnvNames outright, because no host documents a mapping from a bare
// variable name to a remote-header spelling (Codex takes `bearer_token_env_var`,
// Pi/Grok take `${VAR}` inside `headers`, OpenCode takes `{env:VAR}`).
// InstallServerEntry enforces that refusal; callers that only hold a host id
// still get the stdio answer here.
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

// RemoteEntrySpecFor is the single capability query for "can this host express
// a remote (URL) MCP entry, and in what spelling?". Plan-time target filtering,
// install, and copy all resolve the capability through this one function, so
// three call sites can never disagree about which hosts are capable: ok=false
// means the host is refused, fail-closed, with no fallback to a stdio write or
// a guessed URL key.
//
// The returned spec is a copy of the registry's, so a caller cannot mutate the
// compiled-in table through it.
func RemoteEntrySpecFor(hostID string) (RemoteEntrySpec, bool) {
	adapter, err := GetAdapter(hostID)
	if err != nil {
		return RemoteEntrySpec{}, false
	}
	spec, ok := entrySpecFor(adapter)
	if !ok || spec.Remote == nil {
		return RemoteEntrySpec{}, false
	}
	return *spec.Remote, true
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
//
// A remote entry renders ONLY its URL key (plus a discriminator when the host
// documents one) — never command, args or env — for the same reason
// entryValue does: a remote entry is one transport, spelled the way this host
// spells it.
func (s entrySpec) tomlEntryBlock(name string, e ServerEntry) (string, error) {
	header := strings.Join(append(append([]string{}, s.KeyPath...), name), ".")
	if s.Remote != nil && strings.TrimSpace(e.Endpoint) != "" {
		typeValue, err := remoteTypeValue(s.HostID, s.Remote, e.Transport)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[%s]\n%s = %q\n", header, s.Remote.URLKey, e.Endpoint)
		if s.Remote.TypeKey != "" {
			fmt.Fprintf(&b, "%s = %q\n", s.Remote.TypeKey, typeValue)
		}
		return b.String(), nil
	}
	quoted := make([]string, 0, len(e.Args))
	for _, a := range e.Args {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\ncommand = %q\n", header, e.Command)
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
	return b.String(), nil
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
	// One entry, one transport. An entry with neither launch line nor endpoint
	// names nothing to connect to; an entry with both would leave the host to
	// pick which one it honours, by its own rule rather than ours.
	hasCommand := strings.TrimSpace(entry.Command) != ""
	hasEndpoint := strings.TrimSpace(entry.Endpoint) != ""
	switch {
	case !hasCommand && !hasEndpoint:
		return nil, fmt.Errorf("server %q has no command to run and no remote endpoint to connect to", entry.Name)
	case hasCommand && hasEndpoint:
		return nil, fmt.Errorf("server %q names both a command and a remote endpoint; an entry is one transport — "+
			"register either a stdio command or a URL, not both", entry.Name)
	}
	// No host documents a name→header mapping for remote entries (see the
	// per-host spellings: bearer_token_env_var, ${VAR} in headers, {env:VAR}),
	// so --env on a remote entry is a refusal, not a silent drop: an entry that
	// looks credentialed and is not is worse than an honest error.
	if hasEndpoint && len(entry.EnvNames) > 0 {
		return nil, fmt.Errorf("LPSM-REMOTE-ENV-REFUSED: --env cannot be forwarded for the remote (URL) entry %q: "+
			"no host documents how a bare variable name maps to a remote header, so writing it would look credentialed and would not be; "+
			"add the header to %s's config by hand", entry.Name, hostID)
	}

	adapter, err := GetAdapter(hostID)
	if err != nil {
		return nil, err
	}
	spec, ok := entrySpecForScope(adapter, opts.Scope)
	if !ok {
		return nil, fmt.Errorf("host %q has no documented MCP entry shape, so a server entry cannot be written to it", hostID)
	}
	// Capability gate: a remote entry is written only in the exact spelling
	// this host's spec verified, or not at all (LPSM-HOST-REMOTE-UNSUPPORTED).
	// It runs before any file is read, so a refusal never leaves a backup,
	// a lock or a half-checked config behind.
	if hasEndpoint {
		if _, err := remoteTypeValue(spec.HostID, spec.Remote, entry.Transport); err != nil {
			return nil, err
		}
	}

	configPath, err := adapter.DetectConfig(ctx, opts.Scope)
	if err != nil {
		return nil, err
	}
	// Serialize LiteSPM read/merge/write transactions across processes. The
	// byte-snapshot check below additionally refuses edits observed before the
	// final replacement; the lock prevents two LiteSPM writers from both
	// merging against the same stale image.
	configLock, err := fslock.Acquire(configPath+hostConfigWriteLockSuffix, fslock.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("lock host config %s: %w", configPath, err)
	}
	defer func() { _ = configLock.Release() }()

	original := ""
	var originalBytes []byte
	created := false
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		originalBytes = append([]byte(nil), data...)
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
		block, blockErr := spec.tomlEntryBlock(entry.Name, entry)
		if blockErr != nil {
			return nil, blockErr
		}
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
		value, valueErr := spec.entryValue(entry)
		if valueErr != nil {
			return nil, valueErr
		}
		merged, mergeErr := mergeJSONEntrySurgicalNamed(original, spec.KeyPath, entry.Name, value)
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

	backupPath := ""
	if !created {
		p, backupErr := CreateAtomicBackup(configPath, opts.BackupDir, adapter.Descriptor().HostID)
		if backupErr != nil {
			return nil, backupErr
		}
		backupPath = p
	}
	// Everything from the first byte on disk onward can still fail: a read-back
	// that will not parse, an entry that did not land, a fingerprint that
	// cannot be taken. Those failures must not leave a half-verified config
	// behind, so this function undoes its own write before returning. The
	// caller's install transaction therefore only ever sees a result that is
	// either fully written or fully restored.
	rollbackWrite := func() error {
		return rollbackConfigWriteLocked(configPath, backupPath, domain.ComputeBytesDigest([]byte(proposed)))
	}
	fail := func(cause error) (*EntryInstallResult, error) {
		if rbErr := rollbackWrite(); rbErr != nil {
			return nil, errors.Join(cause,
				fmt.Errorf("additionally, restoring %s after the failed install did not succeed: %w", configPath, rbErr))
		}
		return nil, cause
	}
	if err := AtomicWriteFileIfUnchanged(configPath, []byte(proposed), 0600, originalBytes, !created); err != nil {
		return nil, fmt.Errorf("write %s: %w", configPath, err)
	}

	// Read back what the host will actually see.
	written, err := os.ReadFile(configPath)
	if err != nil {
		return fail(fmt.Errorf("read back %s: %w", configPath, err))
	}
	if _, present := lookupServerEntry(spec, string(written), entry.Name); !present {
		return fail(fmt.Errorf("%s: wrote %s but the entry %q is not readable from it", adapter.Descriptor().HostID, configPath, entry.Name))
	}
	writtenRaw, ok, serr := entryStateFromContent(spec, string(written), entry.Name)
	if serr != nil || !ok {
		return fail(fmt.Errorf("%s: wrote %s but the entry %q cannot be fingerprinted: %v", adapter.Descriptor().HostID, configPath, entry.Name, serr))
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
		WrittenDigest:    domain.ComputeBytesDigest(written),
		PriorEntry:       priorEntry,
		PriorFingerprint: priorFP,
		BackupPath:       backupPath,
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
	// Endpoint is the remote (URL) this entry registers instead of a command.
	// It is kept separate from Command on purpose: copy and capabilities must
	// never conflate a URL with a launch line (an endpoint entry has no
	// command, and a stdio entry has no endpoint).
	Endpoint string
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
			// A remote (URL) entry is read through this host's verified URL key
			// — never a guessed one. A url member on a host with no Remote spec
			// stays invisible (Endpoint stays empty and the both-empty skip
			// below keeps the old behaviour): we do not invent URL keys.
			if spec.Remote != nil {
				if u, ok := m[spec.Remote.URLKey].(string); ok {
					entry.Endpoint = u
				}
				if entry.Endpoint != "" {
					// Transport is the host's own discriminator ("http",
					// "streamableHttp", "remote", "sse"), or the registry token
					// when the host infers the transport from the URL alone.
					entry.Transport = TransportStreamableHTTP
					if spec.Remote.TypeKey != "" {
						if v, ok := m[spec.Remote.TypeKey].(string); ok && v != "" {
							entry.Transport = v
						}
					}
				}
			}
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
		// Skip only when the entry names NEITHER a command nor an endpoint.
		// The old rule skipped on an empty Command alone, which made every
		// remote (URL) entry — a valid stdio-less registration — invisible to
		// copy, capabilities and remove. An entry with no launch line at all
		// still carries nothing a caller could use, so it stays skipped.
		if entry.Command == "" && entry.Endpoint == "" {
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
		// A table is kept when it names a launch line OR a remote endpoint;
		// only a table with neither carries nothing a caller could use. The
		// old Command-only check hid every remote entry from read-back.
		if current != nil && (current.Command != "" || current.Endpoint != "") {
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
		// The remote URL/discriminator keys come from this host's verified
		// spec; when it declares none, the empty case label can only match an
		// empty key, which the guard inside rejects. A url line on a host with
		// no Remote spec is therefore never reclassified as a remote entry.
		urlKey, typeKey := "", ""
		if spec.Remote != nil {
			urlKey, typeKey = spec.Remote.URLKey, spec.Remote.TypeKey
		}
		switch key {
		case "command":
			if unquoted, err := strconv.Unquote(value); err == nil {
				current.Command = unquoted
			}
		case urlKey:
			if urlKey == "" {
				break
			}
			if unquoted, err := strconv.Unquote(value); err == nil && strings.TrimSpace(unquoted) != "" {
				if current.Endpoint == "" {
					// Default first: a discriminator line may appear before or
					// after the url line, and the later `type` case overrides.
					current.Transport = TransportStreamableHTTP
				}
				current.Endpoint = unquoted
			}
		case typeKey:
			if typeKey == "" {
				break
			}
			if unquoted, err := strconv.Unquote(value); err == nil && strings.TrimSpace(unquoted) != "" {
				current.Transport = unquoted
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
