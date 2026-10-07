package porting

// secrets.go — env classification for cross-agent porting.
//
// The rule this file implements is invariant 3 of ARCH/38 §5 and §5.5:
// environment content found in a source config splits into two disjoint facts,
// and only one of them is portable.
//
//   - A REFERENCE is content that names a variable rather than holding a
//     value: a name-list entry (Codex `env_vars = ["X"]`) or a value written
//     in a host's documented substitution syntax (`${X}`, `${env:X}`,
//     `{env:X}`). The NAME is carried into the IR (host.ServerEntry.EnvNames)
//     and the target renders it in its own spelling.
//   - A LITERAL is any other value — a hand-typed credential. Its value never
//     enters the IR, never reaches plan output, and is never written
//     anywhere; only the NAME is reported under Needs so the user knows the
//     target must be given that variable out of band (install --env, OS
//     vault).
//
// The same classification is applied to the TARGET's existing entry when a
// conflict fingerprint is taken, so "identical" always means "identical as
// portable facts", never "identical secret bytes".

import (
	"regexp"
	"sort"
	"strings"
)

// varNameRe is the variable-name shape the reference forms accept. It mirrors
// internal/envref's own name rule: a name must look like a shell variable.
var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnvClass is the normalized environment fact set of one entry.
type EnvClass struct {
	// Refs are the names the source forwards by reference: the only
	// environment facts the IR carries.
	Refs []string
	// Dropped are names whose content is not portable (a literal value, or a
	// key/reference pair this IR cannot express). Reported by name only.
	Dropped []string
}

// ClassifyEnv normalizes one entry's environment content.
//
// nameList carries the names a name-list host stores with no value at all
// (Codex `env_vars`); env carries the map as found in the file, whose values
// may be references or literals. A name present in nameList is a reference
// even when the map also holds an empty value for it, because that is exactly
// how the read path records a name-list host.
//
// A value that references a DIFFERENT name than its key ({"TOKEN":
// "${SECRET}"}) is not expressible in the canonical IR, where key and
// reference name are the same variable. It is dropped and reported by key
// rather than silently rewritten into something the source never said.
func ClassifyEnv(env map[string]string, nameList []string) EnvClass {
	var out EnvClass
	seen := map[string]bool{}

	add := func(list *[]string, name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		*list = append(*list, name)
	}

	// Name-list content first: those are references by construction, and a
	// literal-looking empty value in env for the same key must not demote
	// them.
	for _, n := range nameList {
		add(&out.Refs, strings.TrimSpace(n))
	}
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		if ref, ok := referenceName(value); ok && ref == key {
			add(&out.Refs, key)
			continue
		}
		// Literal (or an inexpressible pair): the name survives, the content
		// does not.
		add(&out.Dropped, key)
	}
	sort.Strings(out.Refs)
	sort.Strings(out.Dropped)
	return out
}

// Needs unions the reference names and the dropped names: the plan always
// shows every name it will ask the target to provide (ARCH/38 §5.5.3).
func (c EnvClass) Needs() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string{}, c.Refs...), c.Dropped...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// referenceName recognises the substitution forms LiteSPM itself writes (see
// internal/envref) and returns the variable name the value refers to. A value
// that is not one of those forms is a literal — including every form the
// package deliberately does not emit (`{file:...}`, `!cmd`, interpolated
// strings), which are never treated as portable references.
func referenceName(value string) (string, bool) {
	v := strings.TrimSpace(value)
	// `${NAME}` and `${NAME:-default}` — the shell-style hosts.
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		inner := v[2 : len(v)-1]
		if idx := strings.Index(inner, ":-"); idx > 0 && varNameRe.MatchString(inner[:idx]) {
			return inner[:idx], true
		}
		if varNameRe.MatchString(inner) {
			return inner, true
		}
		// Not a plain shell variable: fall through so `${env:NAME}` — which
		// also starts with `${` — still reaches its own branch.
	}
	// `${env:NAME}` — the explicit-namespace hosts.
	if strings.HasPrefix(v, "${env:") && strings.HasSuffix(v, "}") {
		if n := v[len("${env:") : len(v)-1]; varNameRe.MatchString(n) {
			return n, true
		}
	}
	// `{env:NAME}` — OpenCode, which has no `$` at all.
	if strings.HasPrefix(v, "{env:") && strings.HasSuffix(v, "}") {
		if n := v[len("{env:") : len(v)-1]; varNameRe.MatchString(n) {
			return n, true
		}
	}
	return "", false
}
