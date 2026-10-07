// Package envref records how each supported agent host lets an MCP server entry
// name an environment variable instead of carrying its value.
//
// # Why this is a table and not a constant
//
// Ten of the eleven hosts documented for MCP installation expand a variable
// *reference* inside the entry's environment field, but no two spell it the same
// way, they name the field differently, and three of them differ in what they do
// when the variable is unset. Writing `${GITHUB_TOKEN}` into a host that expects
// `{env:GITHUB_TOKEN}` produces an entry that looks configured and fails at first
// launch; writing the same reference into Codex produces a literal eleven-
// character credential string passed to the subprocess. Neither failure is
// visible in the config, so the only way to be right is to know each host.
//
// Every row here was read out of that host's own documentation or source, and
// each carries the URL it was read from. A host that is not in this table has no
// *verified* substitution behaviour, and EnvNames for it are refused rather than
// guessed at — an entry written on an assumption is an entry that fails silently.
//
// # Why names only, never values
//
// The caller supplies variable NAMES. The value stays in the environment of
// whatever launches the agent, and the config file carries a reference. So a
// secret never passes through LiteSPM: not into a backup, not into the install
// ledger, not into a config file, and not into a shell history, because it was
// never accepted on the command line. The config file on disk is plain text and
// that is the point — it stays worth reading, diffing and committing.
//
// # The second reader
//
// A reference has two readers. The host expands it when the agent spawns the
// server; LiteSPM expands it when `litespm capabilities refresh` probes the same
// server itself. Resolve is that second reader, and it deliberately resolves
// only the forms this package emits. Two of the hosts also document a form that
// reads an arbitrary file (`{file:/path}`) or runs a command (`!cmd`) from
// inside `env`. Those are never resolved here: honouring them during a probe
// would turn discovering an installed server into a local file-read and
// command-execution primitive triggered by config contents LiteSPM did not
// author. An unrecognised form is passed through untouched.
package envref

import (
	"fmt"
	"sort"
	"strings"
)

// Style is how a host accepts a forwarded environment variable.
type Style string

const (
	// StyleEnvMapReference writes the variable into the entry's environment
	// object as a reference in the host's own syntax (the common case).
	StyleEnvMapReference Style = "env-map-reference"
	// StyleEnvNameList writes the variable NAME into a separate name list, for
	// hosts that forward by name and deliberately take no value at all.
	StyleEnvNameList Style = "env-name-list"
)

// UnsetBehaviour is what a host does when the referenced variable is not set.
// It matters because two of these are silent.
type UnsetBehaviour string

const (
	// UnsetUnknown means the docs did not say. Not a synonym for UnsetEmpty.
	UnsetUnknown     UnsetBehaviour = "unverified"
	UnsetEmptyString UnsetBehaviour = "empty-string"
	UnsetPassthrough UnsetBehaviour = "literal-passthrough"
	// UnsetAbsent is for name-list hosts: a name that is not in the parent
	// environment is simply not forwarded. Nothing to expand, nothing to fail.
	UnsetAbsent UnsetBehaviour = "absent"
	// UnsetRefused is Kiro: expansion is gated on an allowlist, so an
	// unlisted name is left alone rather than substituted.
	UnsetRefused UnsetBehaviour = "refused-unless-allowlisted"
)

// Spec is one host's documented environment-forwarding behaviour.
type Spec struct {
	HostID string
	// Field is the key the host reads the environment from inside a server
	// entry. It is not always `env`.
	Field string
	// NameListField is the key for StyleEnvNameList hosts, where the variable
	// name is listed and no value is ever stored.
	NameListField string
	Style         Style
	// Reference renders one variable NAME as the text stored in the config.
	// Empty for StyleEnvNameList, which stores the name itself.
	Reference func(name string) string
	// DefaultSyntax is recorded for documentation: whether the host also
	// accepts a `${VAR:-fallback}` form. LiteSPM never writes one, because it
	// does not know the host's sensible fallback value.
	DefaultSyntax bool
	Unset         UnsetBehaviour
	// ApprovalGate names the host setting that must list the variable before it
	// will expand, when the host has one.
	ApprovalGate string
	// Evidence is the vendor URL this row was read from.
	Evidence string
}

// brace is the reference form used by hosts that document a plain shell-style
// variable: ${NAME}.
func brace(name string) string { return "${" + name + "}" }

// envPrefixed is the reference form used by hosts that document an explicit
// `env:` namespace: ${env:NAME}.
func envPrefixed(name string) string { return "${env:" + name + "}" }

// spec is the verified table. Keys are LiteSPM host ids.
//
// A host absent from this map has no verified behaviour. `windsurf` is
// deliberately absent: it is not a registered target (its documented path
// belongs to the `devin` target), so there is no LiteSPM host id to key on.
var spec = map[string]Spec{
	"github-copilot": {
		HostID: "github-copilot", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, DefaultSyntax: true, Unset: UnsetUnknown,
		Evidence: "https://docs.github.com/enterprise-cloud@latest/copilot/reference/copilot-cli-reference/cli-command-reference",
	},
	"claude-code": {
		HostID: "claude-code", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, DefaultSyntax: true, Unset: UnsetPassthrough,
		Evidence: "https://code.claude.com/docs/en/mcp-servers.md",
	},
	"gemini-cli": {
		HostID: "gemini-cli", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, Unset: UnsetEmptyString,
		Evidence: "https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md",
	},
	"cursor": {
		HostID: "cursor", Field: "env", Style: StyleEnvMapReference,
		Reference: envPrefixed, Unset: UnsetUnknown,
		Evidence: "https://cursor.com/docs/context/mcp",
	},
	"opencode": {
		// OpenCode's field is `environment`, not `env`, and its reference form
		// has no `$` at all: `{env:NAME}`. Both are load-bearing.
		HostID: "opencode", Field: "environment", Style: StyleEnvMapReference,
		Reference: func(name string) string { return "{env:" + name + "}" }, Unset: UnsetUnknown,
		Evidence: "https://opencode.ai/docs/config/",
	},
	"cline": {
		HostID: "cline", Field: "env", Style: StyleEnvMapReference,
		Reference: envPrefixed, Unset: UnsetPassthrough,
		Evidence: "https://github.com/cline/cline/blob/9dea336c/src/utils/envExpansion.ts",
	},
	"kiro-cli": {
		HostID: "kiro-cli", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, Unset: UnsetRefused,
		ApprovalGate: "mcp.approvedEnvVars",
		Evidence:     "https://kiro.dev/docs/mcp/configuration/",
	},
	"pi-agent": {
		HostID: "pi-agent", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, Unset: UnsetUnknown,
		Evidence: "https://pi.dev/docs/latest/mcp",
	},
	"grok-build": {
		HostID: "grok-build", Field: "env", Style: StyleEnvMapReference,
		Reference: brace, DefaultSyntax: true, Unset: UnsetUnknown,
		Evidence: "https://docs.x.ai/build/features/mcp-servers",
	},
	"codex": {
		// Codex is the one host with no value-bearing substitution: its `env`
		// table is documented as copied into the subprocess as-is, so a
		// `${VAR}` written there reaches the child as that literal text. Codex
		// forwards by NAME through a separate `env_vars` list instead, which is
		// what this row uses.
		HostID: "codex", NameListField: "env_vars", Style: StyleEnvNameList,
		Unset:    UnsetAbsent,
		Evidence: "https://developers.openai.com/codex/config-reference",
	},
}

// SpecFor returns a host's verified behaviour. The bool is false for a host with
// no documented substitution, which callers must treat as a refusal.
func SpecFor(hostID string) (Spec, bool) {
	s, ok := spec[strings.ToLower(strings.TrimSpace(hostID))]
	return s, ok
}

// Known reports whether the table has a row for this host.
func Known(hostID string) bool {
	_, ok := SpecFor(hostID)
	return ok
}

// EnvMap renders the entry environment object for a host: variable name to the
// reference text that host expands. It is empty for name-list hosts.
func (s Spec) EnvMap(names []string) map[string]string {
	if s.Style != StyleEnvMapReference || len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = s.Reference(n)
	}
	return out
}

// SortedNames returns names in a stable order, so a config that reorders itself
// on every install is unreviewable in a diff.
func SortedNames(names []string) []string {
	out := append([]string{}, names...)
	sort.Strings(out)
	return out
}

// ValidateName rejects a variable name no shell or host would accept, so a
// typo fails at the command line instead of producing a reference that can
// never resolve.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("environment variable name is empty")
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return fmt.Errorf("environment variable name %q may not start with a digit", name)
			}
		default:
			return fmt.Errorf("environment variable name %q may only contain letters, digits and '_'", name)
		}
	}
	return nil
}

// ValidateNames checks every name and rejects duplicates, which would otherwise
// silently collapse into one entry in the config.
func ValidateNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if err := ValidateName(n); err != nil {
			return err
		}
		if seen[n] {
			return fmt.Errorf("environment variable %q was given twice", n)
		}
		seen[n] = true
	}
	return nil
}

// Resolve expands a reference that this package emits, using lookup for the
// variable's value. It returns the resolved value and whether anything was
// expanded.
//
// A value that is not a reference — a literal the user hand-wrote into the
// config, or a form this package does not emit — is returned unchanged with
// expanded=false, so a hand-configured server keeps working exactly as written.
//
// Hostless Resolve honours no `${VAR:-fallback}`, because whether a host expands
// the fallback form is host-specific (only three of the documented hosts do).
// Resolving one the host would not expand makes LiteSPM's probe succeed where the
// agent then fails, which is the more damaging direction to be wrong in. Callers
// that know the host should use Spec.Resolve.
func Resolve(value string, lookup func(string) (string, bool)) (string, bool) {
	return Spec{}.Resolve(value, lookup)
}

// Spec.Resolve is Resolve with the host's own fallback support taken into
// account.
func (s Spec) Resolve(value string, lookup func(string) (string, bool)) (string, bool) {
	name, fallback, hasFallback, ok := referenceName(value)
	if !ok {
		return value, false
	}
	if v, found := lookup(name); found {
		return v, true
	}
	if hasFallback && s.DefaultSyntax {
		return fallback, true
	}
	return value, false
}

// referenceName recognises the reference forms LiteSPM writes, returning the
// variable name, any `:-fallback` text, and whether such a fallback was present.
func referenceName(value string) (name, fallback string, hasFallback, ok bool) {
	v := strings.TrimSpace(value)
	// `${NAME}` and `${NAME:-default}` — the shell-style hosts.
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		inner := v[2 : len(v)-1]
		if idx := strings.Index(inner, ":-"); idx > 0 && isVarName(inner[:idx]) {
			return inner[:idx], inner[idx+2:], true, true
		}
		if isVarName(inner) {
			return inner, "", false, true
		}
	}
	// `${env:NAME}` — the explicit-namespace hosts.
	if strings.HasPrefix(v, "${env:") && strings.HasSuffix(v, "}") {
		if n := v[len("${env:") : len(v)-1]; isVarName(n) {
			return n, "", false, true
		}
	}
	// `{env:NAME}` — OpenCode, which has no `$` at all.
	if strings.HasPrefix(v, "{env:") && strings.HasSuffix(v, "}") {
		if n := v[len("{env:") : len(v)-1]; isVarName(n) {
			return n, "", false, true
		}
	}
	return "", "", false, false
}

func isVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
