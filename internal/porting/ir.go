package porting

// ir.go — the canonical intermediate representation and its normalization.
//
// D-028: porting is N readers → one IR → N writers. There are no pairwise
// translators; a source config is read through its adapter into
// host.ServerEntry (MCP) or a SkillRecord (skills), and the target's normal
// install path renders it. Shape variation stays data inside internal/host.
//
// The IR never carries a secret. Source env content is classified (see
// secrets.go) before it reaches the IR: references become EnvNames, literals
// are reported by name and dropped. host.ServerEntry.Env — the field that
// holds literal values found in an existing config — is left nil on every
// path through this package.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/host"
)

// Kinds copy v1 understands. Everything else is reported unsupported
// (LPSM-COPY-003), never silently dropped.
const (
	KindMCP    = "mcp"
	KindSkill  = "skill"
	KindPlugin = "plugin"
)

// SupportedKind reports whether v1 can copy a kind.
func SupportedKind(kind string) bool {
	return kind == KindMCP || kind == KindSkill
}

// SourceMCP is one MCP server as found in the source agent's config. Env holds
// the environment map exactly as read — references and literals mixed — and
// exists only so classification can happen once, here; it is never copied into
// the IR.
type SourceMCP struct {
	Name    string
	Command string
	Args    []string
	// Endpoint is the remote (URL) this entry registers instead of launching
	// a process. A source entry carries Command OR Endpoint, never both.
	Endpoint string
	// Transport is the transport of a remote entry. It may arrive in HOST
	// vocabulary (the discriminator read back from the config: "http",
	// "streamableHttp", "remote") or registry vocabulary; NormalizeMCP maps it
	// through host.RegistryRemoteTransport, so both spellings of the same fact
	// classify identically.
	Transport string
	Env       map[string]string
	EnvNames  []string
}

// SourceSkill is one skill as found on the source agent: its name, the
// directory holding its files, and a content digest for identity comparison.
type SourceSkill struct {
	Name string
	// SourceDir is the source agent's skill directory for this skill. Empty
	// means the ledger names a directory that no longer exists.
	SourceDir string
	// ContentDigest is the provenance digest of the files that would be
	// written. Captured live from SourceDir by the reader: the ledger's
	// recorded digest describes the install-time bytes, and a user editing the
	// directory afterwards would make a read-back compare fail for a copy that
	// in fact succeeded.
	ContentDigest string
}

// SourceOther is a component whose kind v1 does not copy (plugin, …), kept so
// the plan can report it with a reason instead of dropping it.
type SourceOther struct {
	Kind string
	Name string
}

// Source is the read side: everything copy found on the source agent, at the
// requested scope. The reader performs the I/O; the planner only inspects.
type Source struct {
	From string
	// Display is the source agent's display name, for output.
	Display string
	// EntryShape is the source host's MCP entry shape ("object",
	// "local-array", …). It decides direct vs translated at plan time; empty
	// when the source declares no MCP layout.
	EntryShape string
	MCP        []SourceMCP
	Skills     []SourceSkill
	Other      []SourceOther
	// Notes record read-side facts the user should see (for example a source
	// with no MCP container at all).
	Notes []string
}

// SkillRecord is the skills IR: name plus the provenance of the files that
// will be written.
type SkillRecord struct {
	Name          string
	ContentDigest string
	SourceDir     string
}

// NormalizeMCP folds one source MCP entry into the canonical IR and returns
// the names the target must provide.
//
// The returned entry deliberately has Env unset: literal values found in the
// source file are reported (needs) and dropped, and only reference forms
// survive as EnvNames (ARCH/38 §5.5, invariant 3).
func NormalizeMCP(src SourceMCP) (host.ServerEntry, []string) {
	class := ClassifyEnv(src.Env, src.EnvNames)
	entry := host.ServerEntry{
		Name:    strings.TrimSpace(src.Name),
		Command: strings.TrimSpace(src.Command),
		Args:    append([]string{}, src.Args...),
		// A remote entry's endpoint flows into the IR verbatim, and its
		// transport is normalised to the registry vocabulary so a source that
		// said "http" and a plan that says "streamable-http" are the same
		// fact (and fingerprint the same).
		Endpoint:  strings.TrimSpace(src.Endpoint),
		Transport: remoteTransportOf(src.Endpoint, src.Transport),
		EnvNames:  append([]string{}, class.Refs...),
	}
	return entry, class.Needs()
}

// remoteTransportOf maps a remote entry's transport onto the registry
// vocabulary, leaving stdio entries (no endpoint) with no transport at all.
func remoteTransportOf(endpoint, transport string) string {
	if strings.TrimSpace(endpoint) == "" {
		return ""
	}
	return host.RegistryRemoteTransport(transport)
}

// IRFromHostEntry rebuilds the IR from an entry as read back out of a config
// (the target's existing entry, or our own write during verification). The
// same classification is applied, so a fingerprint taken on either side
// describes the same portable facts.
func IRFromHostEntry(e host.HostServerEntry) host.ServerEntry {
	class := ClassifyEnv(e.Env, e.EnvNames)
	entry := host.ServerEntry{
		Name:     strings.TrimSpace(e.Name),
		Command:  strings.TrimSpace(e.Command),
		Args:     append([]string{}, e.Args...),
		EnvNames: append([]string{}, class.Refs...),
	}
	// A remote entry read back out of a config keeps its endpoint, and its
	// host discriminator (`http`, `streamableHttp`, `remote`, `sse`) is mapped
	// to the registry vocabulary — otherwise a Claude Code remote entry would
	// fingerprint differently from the same entry expressed for Cursor, and
	// the L1 read-back after a write would never match the plan.
	if ep := strings.TrimSpace(e.Endpoint); ep != "" {
		entry.Endpoint = ep
		entry.Transport = host.RegistryRemoteTransport(e.Transport)
	}
	return entry
}

// Fingerprint is the canonical identity of one IR entry: "sha256:" over the
// command, the argument vector, the endpoint and the forwarded names in sorted
// order.
//
// It is the single comparison used everywhere copy asks "are these the
// same?": conflict classification (identical → unchanged, different →
// conflict) and the L1 read-back after a write (entry fingerprint equals the
// plan's entry). It hashes only portable facts, so no secret can enter it —
// literal values are gone before this function is reached.
//
// The endpoint and transport are part of the identity: without them a remote
// entry and a stdio entry registered under the same name would compare
// "identical" while describing opposite transports. Both fields are omitted
// when empty, so every existing stdio fingerprint is byte-stable.
func Fingerprint(e host.ServerEntry) string {
	args := append([]string{}, e.Args...)
	names := append([]string{}, e.EnvNames...)
	if args == nil {
		args = []string{}
	}
	if names == nil {
		names = []string{}
	}
	sort.Strings(names)
	payload, err := json.Marshal(struct {
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		EnvNames  []string `json:"envNames"`
		Endpoint  string   `json:"endpoint,omitempty"`
		Transport string   `json:"transport,omitempty"`
	}{Command: e.Command, Args: args, EnvNames: names,
		Endpoint: strings.TrimSpace(e.Endpoint), Transport: strings.TrimSpace(e.Transport)})
	if err != nil {
		// json.Marshal on this shape cannot fail; keep the failure honest
		// rather than returning an empty (colliding) fingerprint.
		payload = []byte(fmt.Sprintf("%q", e.Command))
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AdoptedListingID builds the local-origin listing id for a copied install,
// under the existing catalog grammar (ARCH/38 §5.2): `mcp:adopted:<source>:
// <name>`, `skill:adopted:…`. A copied entry may have no catalog listing at
// all, and this id is what the install row, the deployment ledger and
// `install remove` key on until a discovery pass replaces it.
func AdoptedListingID(kind, sourceHost, name string) string {
	return fmt.Sprintf("%s:adopted:%s:%s", kind, strings.ToLower(sourceHost), name)
}
