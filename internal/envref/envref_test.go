package envref

import "testing"

func TestValidateName(t *testing.T) {
	valid := []string{"GITHUB_TOKEN", "_x", "A", "TOKEN2", "my_key"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{"", "2TOKEN", "has space", "has-dash", "has.dot", "tok\n", "A=B"}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", n)
		}
	}
}

func TestValidateNamesRejectsDuplicates(t *testing.T) {
	if err := ValidateNames([]string{"A", "B"}); err != nil {
		t.Fatalf("distinct names rejected: %v", err)
	}
	if err := ValidateNames([]string{"A", "A"}); err == nil {
		t.Fatal("duplicate name accepted; it would silently collapse to one entry")
	}
}

// The reference text is what lands in a user's config file, so the exact spelling
// is the contract. These are the four documented forms.
func TestReferenceSpellingPerHost(t *testing.T) {
	cases := map[string]string{
		"claude-code":    "${GITHUB_TOKEN}",
		"github-copilot": "${GITHUB_TOKEN}",
		"gemini-cli":     "${GITHUB_TOKEN}",
		"kiro-cli":       "${GITHUB_TOKEN}",
		"pi-agent":       "${GITHUB_TOKEN}",
		"grok-build":     "${GITHUB_TOKEN}",
		"cursor":         "${env:GITHUB_TOKEN}",
		"cline":          "${env:GITHUB_TOKEN}",
		"opencode":       "{env:GITHUB_TOKEN}",
	}
	for hostID, want := range cases {
		s, ok := SpecFor(hostID)
		if !ok {
			t.Errorf("SpecFor(%q) not found; the host is registered but has no verified row", hostID)
			continue
		}
		if got := s.EnvMap([]string{"GITHUB_TOKEN"})["GITHUB_TOKEN"]; got != want {
			t.Errorf("%s reference = %q, want %q", hostID, got, want)
		}
	}
}

func TestCodexUsesNameListNotSubstitution(t *testing.T) {
	s, ok := SpecFor("codex")
	if !ok {
		t.Fatal("SpecFor(codex) not found")
	}
	if s.Style != StyleEnvNameList {
		t.Errorf("codex style = %q, want %q", s.Style, StyleEnvNameList)
	}
	if s.NameListField != "env_vars" {
		t.Errorf("codex name-list field = %q, want env_vars", s.NameListField)
	}
	// Codex has no reference form at all; emitting one would put the literal
	// text `${GITHUB_TOKEN}` into the child's environment as the credential.
	if m := s.EnvMap([]string{"GITHUB_TOKEN"}); m != nil {
		t.Errorf("codex EnvMap = %v, want nil: codex must forward by name, never by substituted value", m)
	}
}

// OpenCode names the key `environment` and spells the reference without a `$`.
// Getting either wrong writes an entry the host ignores.
func TestOpenCodeFieldAndSyntax(t *testing.T) {
	s, _ := SpecFor("opencode")
	if s.Field != "environment" {
		t.Errorf("opencode field = %q, want environment", s.Field)
	}
	if got := s.Reference("X"); got != "{env:X}" {
		t.Errorf("opencode reference = %q, want {env:X}", got)
	}
}

func TestKnownHosts(t *testing.T) {
	for _, id := range []string{
		"claude-code", "codex", "cline", "pi-agent", "cursor", "opencode",
		"github-copilot", "kiro-cli", "gemini-cli", "grok-build",
	} {
		if !Known(id) {
			t.Errorf("Known(%q) = false; every host we write MCP entries for must have a verified row", id)
		}
	}
	// A host with no verified behaviour must report absent so callers refuse
	// rather than guess a syntax.
	for _, id := range []string{"", "windsurf", "not-a-host", "crush"} {
		if Known(id) {
			t.Errorf("Known(%q) = true; an unverified host must not be advertised as supporting references", id)
		}
	}
}

func TestResolve(t *testing.T) {
	env := map[string]string{"GITHUB_TOKEN": "secret", "EMPTY": ""}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	cases := []struct {
		in       string
		want     string
		expanded bool
		why      string
	}{
		{"${GITHUB_TOKEN}", "secret", true, "claude-code / copilot / kiro / pi / grok / gemini form"},
		{"${env:GITHUB_TOKEN}", "secret", true, "cursor and cline form"},
		{"{env:GITHUB_TOKEN}", "secret", true, "opencode form, no dollar sign"},
		{"${MISSING}", "${MISSING}", false, "unset reference is left intact, not blanked"},
		{"${GITHUB_TOKEN:-fallback}", "secret", true, "fallback form, variable wins when set"},
		{"${MISSING:-fallback}", "${MISSING:-fallback}", false, "hostless Resolve honours no fallback form; see the spec test"},
		{"${EMPTY}", "", true, "a variable that is set but empty resolves to empty, not to the reference"},
		{"literal-value", "literal-value", false, "a hand-written literal must survive untouched"},
		{"{file:/etc/passwd}", "{file:/etc/passwd}", false, "file indirection is never resolved during a probe"},
		{"!rm -rf /", "!rm -rf /", false, "command indirection is never resolved during a probe"},
		{"", "", false, "empty string is not a reference"},
		{"${}", "${}", false, "empty variable name is not a reference"},
	}
	for _, c := range cases {
		got, ok := Resolve(c.in, lookup)
		if got != c.want || ok != c.expanded {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v) — %s", c.in, got, ok, c.want, c.expanded, c.why)
		}
	}
}

// Only three of the documented hosts expand `${VAR:-fallback}`. Resolving it for a
// host that does not would make LiteSPM's probe succeed where the agent then
// fails, so the decision is per-host and defaults to not resolving.
func TestFallbackOnlyWhereTheHostDocumentsIt(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	const in = "${MISSING:-fallback}"

	spec, ok := SpecFor("claude-code")
	if !ok {
		t.Fatal("claude-code row missing")
	}
	if got, ok := spec.Resolve(in, lookup); got != "fallback" || !ok {
		t.Errorf("claude-code Resolve(%q) = (%q, %v), want (\"fallback\", true)", in, got, ok)
	}

	spec, ok = SpecFor("gemini-cli")
	if !ok {
		t.Fatal("gemini-cli row missing")
	}
	if got, ok := spec.Resolve(in, lookup); got != in || ok {
		t.Errorf("gemini-cli Resolve(%q) = (%q, %v), want it unchanged: this host documents no fallback form", in, got, ok)
	}
}

// A reference must not be resolved from a nested or interpolated string: only a
// whole-value reference is a reference. Resolving a substring would let a value
// that merely mentions ${SECRET} exfiltrate the process environment into a
// config-derived argument.
func TestResolveRejectsInterpolatedStrings(t *testing.T) {
	lookup := func(k string) (string, bool) { return "leaked", true }
	for _, in := range []string{
		"prefix-${GITHUB_TOKEN}",
		"${GITHUB_TOKEN}-suffix",
		"https://${HOST}/path",
	} {
		got, ok := Resolve(in, lookup)
		if ok {
			t.Errorf("Resolve(%q) expanded a substring; only a whole-value reference may be expanded", in)
		}
		if got != in {
			t.Errorf("Resolve(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestCaveats(t *testing.T) {
	get := func(id string) []string {
		s, ok := SpecFor(id)
		if !ok {
			t.Fatalf("SpecFor(%q) not found", id)
		}
		return Caveats(s, []string{"GITHUB_TOKEN"})
	}
	if len(get("gemini-cli")) == 0 || !contains(get("gemini-cli"), "EMPTY STRING") {
		t.Error("gemini's silent empty-string substitution must be reported")
	}
	if !contains(get("kiro-cli"), "approvedEnvVars") {
		t.Error("kiro's allowlist gate must be reported; an unlisted reference silently does nothing")
	}
	if !contains(get("codex"), "by name") {
		t.Error("codex forwards by name from the agent's own environment; that must be stated")
	}
	if !contains(get("opencode"), "`environment`") {
		t.Error("opencode's field is `environment`, not `env`; that difference must reach the user")
	}
	if c := Caveats(Spec{Field: "env"}, nil); c != nil {
		t.Errorf("no names means no caveats, got %v", c)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if indexOf(h, needle) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
