package skills

import (
	"path/filepath"
	"testing"
)

// useTempHome pins every environment variable the skill-directory resolvers
// consult to one temporary directory, and returns it.
//
// The resolvers deliberately honour per-agent home overrides (CODEX_HOME,
// CLAUDE_CONFIG_DIR, XDG_CONFIG_HOME, ...) because that is correct behaviour on
// a real machine. It means a test that only pins HOME is not hermetic: CI
// runners export several of these, so resolution silently lands in the runner
// account instead of the sandbox and the assertion fails -- or worse, the test
// writes outside its temp tree.
//
// The override list is derived from the agent table rather than hand-copied, so
// a newly added agent cannot silently reintroduce this.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	for _, a := range AgentTargets() {
		if a.EnvVar != "" {
			t.Setenv(a.EnvVar, "")
		}
	}
	// OpenClaw's home has been renamed three times; the aliases are read outside
	// the main table.
	for _, key := range []string{"OPENCLAW_HOME", "CLAWDBOT_HOME", "MOLTBOT_HOME"} {
		t.Setenv(key, "")
	}
	return home
}

// testLedger opens a ledger whose removal roots are the ledger's own directory,
// so tests that keep skill directories beside ledger.json stay confined to the
// temp dir. Confinement behaviour itself is tested in ledger_confine_test.go
// against OpenLedger's defaults.
func testLedger(t *testing.T, path string) (*Ledger, error) {
	t.Helper()
	l, err := OpenLedger(path)
	if err != nil {
		return nil, err
	}
	l.SetRoots(filepath.Dir(path))
	return l, nil
}
