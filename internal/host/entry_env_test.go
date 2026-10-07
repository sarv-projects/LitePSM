package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// installEnvInto writes an entry with forwarded variables into hostID's config and
// returns the raw file bytes, so the assertion is on what the user's editor will
// see rather than on an internal representation of it.
func installEnvInto(t *testing.T, hostID, envVarName string, path string) string {
	t.Helper()
	adapter, err := GetAdapter(hostID)
	if err != nil {
		t.Fatalf("adapter %s: %v", hostID, err)
	}
	_, err = InstallServerEntry(t.Context(), hostID, ServerEntry{
		Name:     "demo",
		Command:  "npx",
		Args:     []string{"-y", "demo-mcp"},
		EnvNames: []string{envVarName},
	}, EntryInstallOptions{
		BackupDir: t.TempDir(),
		Scope:     domain.ScopeUser,
		Force:     true,
	})
	if err != nil {
		t.Fatalf("install into %s: %v", hostID, err)
	}
	_ = adapter
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// The reference spelling is the whole point: an entry the host does not expand
// looks identical to one it does, right up until the server starts.
func TestEnvReferenceSpellingReachesTheConfig(t *testing.T) {
	cases := []struct {
		hostID string
		path   func(home string) string
		want   string
	}{
		{"claude-code", func(h string) string { return filepath.Join(h, ".claude.json") }, `"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}`},
		{"gemini-cli", func(h string) string { return filepath.Join(h, ".gemini", "settings.json") }, `"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}`},
		{"github-copilot", func(h string) string { return filepath.Join(h, ".copilot", "mcp-config.json") }, `"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}`},
		{"kiro-cli", func(h string) string { return filepath.Join(h, ".kiro", "settings", "mcp.json") }, `"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}`},
		{"pi-agent", func(h string) string { return filepath.Join(h, ".pi", "agent", "mcp.json") }, `"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}`},
		{"cursor", func(h string) string { return filepath.Join(h, ".cursor", "mcp.json") }, `"env":{"GITHUB_TOKEN":"${env:GITHUB_TOKEN}"}`},
		{"cline", func(h string) string {
			return filepath.Join(h, ".cline", "data", "settings", "cline_mcp_settings.json")
		}, `"env":{"GITHUB_TOKEN":"${env:GITHUB_TOKEN}"}`},
		// OpenCode names the key `environment` and has no `$` in its reference.
		{"opencode", func(h string) string {
			return filepath.Join(h, ".config", "opencode", "opencode.json")
		}, `"environment":{"GITHUB_TOKEN":"{env:GITHUB_TOKEN}"}`},
	}
	for _, c := range cases {
		t.Run(c.hostID, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("USERPROFILE", home)
			got := installEnvInto(t, c.hostID, "GITHUB_TOKEN", c.path(home))
			if !strings.Contains(got, c.want) {
				t.Errorf("config does not contain %s\n--- config ---\n%s", c.want, got)
			}
			// The literal secret must never appear: only the reference does.
			if strings.Contains(got, "ghp_") {
				t.Error("a literal credential value reached the config file")
			}
		})
	}
}

// Codex has no substitution at all. Its `env` table is documented as copied into
// the subprocess as-is, so writing a reference there would hand the child the
// twelve-character text "${VAR}" and call it a credential.
func TestCodexForwardsByNameNotBySubstitution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	path := filepath.Join(home, ".codex", "config.toml")
	got := installEnvInto(t, "codex", "GITHUB_TOKEN", path)

	if !strings.Contains(got, `env_vars = ["GITHUB_TOKEN"]`) {
		t.Errorf("codex config lacks the name-list form\n--- config ---\n%s", got)
	}
	if strings.Contains(got, "${GITHUB_TOKEN}") {
		t.Errorf("codex config contains a reference its host does not expand\n--- config ---\n%s", got)
	}
	if strings.Contains(got, "env = {") {
		t.Errorf("codex config gained an env table; that field is literal-only\n--- config ---\n%s", got)
	}
}

func TestGrokBuildTOMLUsesBraceReference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	path := filepath.Join(home, ".grok", "config.toml")
	got := installEnvInto(t, "grok-build", "GITHUB_TOKEN", path)
	if !strings.Contains(got, `env = { "GITHUB_TOKEN" = "${GITHUB_TOKEN}" }`) {
		t.Errorf("grok TOML entry lacks the documented reference\n--- config ---\n%s", got)
	}
}

// Whatever the host, the entry must still parse back and still be recognisable as
// the server we installed — an env field that corrupts the document is worse than
// no env field.
func TestEnvEntryReadsBackIntact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// resolveHomeDir prefers USERPROFILE on Windows; without pinning it the
	// install writes the runner's real home instead of the sandbox.
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(home, ".claude.json")
	installEnvInto(t, "claude-code", "GITHUB_TOKEN", path)

	entries, err := ListServerEntriesWithValues(t.Context(), "claude-code", domain.ScopeUser)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var found *HostServerEntry
	for i := range entries {
		if entries[i].Name == "demo" {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatal("entry did not read back")
	}
	if got := found.Env["GITHUB_TOKEN"]; got != "${GITHUB_TOKEN}" {
		t.Errorf("read-back Env[GITHUB_TOKEN] = %q, want the reference text so the probe can resolve it", got)
	}
}

// Codex stores no value, so read-back must still surface the NAME — otherwise the
// probe spawns the server without the variable the user forwarded.
func TestCodexNameListReadsBackAsResolvableName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	installEnvInto(t, "codex", "GITHUB_TOKEN", filepath.Join(home, ".codex", "config.toml"))

	entries, err := ListServerEntriesWithValues(t.Context(), "codex", domain.ScopeUser)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var found *HostServerEntry
	for i := range entries {
		if entries[i].Name == "demo" {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatal("entry did not read back")
	}
	if len(found.EnvNames) != 1 || found.EnvNames[0] != "GITHUB_TOKEN" {
		t.Errorf("EnvNames = %v, want [GITHUB_TOKEN]", found.EnvNames)
	}
}

// An entry with no forwarded variables must gain no environment field at all, so
// existing configs do not grow a `"env": {}` they never had.
func TestNoEnvNamesWritesNoEnvField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// USERPROFILE: resolveHomeDir prefers it on Windows (see the sibling test).
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(home, ".claude.json")
	adapter, err := GetAdapter("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallServerEntry(t.Context(), "claude-code", ServerEntry{
		Name: "demo", Command: "npx", Args: []string{"-y", "demo-mcp"},
	}, EntryInstallOptions{BackupDir: t.TempDir(), Scope: domain.ScopeUser, Force: true}); err != nil {
		t.Fatalf("install: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"env"`) {
		t.Errorf("an env field appeared without --env\n%s", string(raw))
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Errorf("written config is not valid JSON: %v", err)
	}
	_ = adapter
}

// TOML read-back used to return no arguments at all: the parser tried to unquote
// a whole array as if it were a single string literal, which can never succeed, so
// a server installed for Codex or Grok Build probed with no arguments. Any
// argument containing a comma needs the same quote-aware split, so both cases are
// pinned here.
func TestTOMLArgsAndEnvReadBack(t *testing.T) {
	spec, ok := entrySpecFor(mustAdapter(t, "codex"))
	if !ok {
		t.Fatal("no entry spec for codex")
	}
	content := `[mcp_servers.demo]
command = "npx"
args = ["-y", "demo-mcp", "--flag=a,b c"]
env = { "A" = "${A}", "B" = "plain" }
env_vars = ["GITHUB_TOKEN"]
`
	entries := tomlServerEntries(spec, content)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	want := []string{"-y", "demo-mcp", "--flag=a,b c"}
	if len(e.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", e.Args, want)
	}
	for i := range want {
		if e.Args[i] != want[i] {
			t.Errorf("Args[%d] = %q, want %q", i, e.Args[i], want[i])
		}
	}
	if e.Env["A"] != "${A}" {
		t.Errorf("Env[A] = %q, want the reference preserved verbatim", e.Env["A"])
	}
	if e.Env["B"] != "plain" {
		t.Errorf("Env[B] = %q, want a hand-written literal preserved", e.Env["B"])
	}
	if len(e.EnvNames) != 1 || e.EnvNames[0] != "GITHUB_TOKEN" {
		t.Errorf("EnvNames = %v, want [GITHUB_TOKEN]", e.EnvNames)
	}
}

func mustAdapter(t *testing.T, id string) HostAdapter {
	t.Helper()
	a, err := GetAdapter(id)
	if err != nil {
		t.Fatalf("adapter %s: %v", id, err)
	}
	return a
}
