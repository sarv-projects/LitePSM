package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentTableIntegrity(t *testing.T) {
	targets := AgentTargets()

	// The reference installer ships 79 agent entries plus a synthetic
	// "universal" pseudo-agent. We track every real agent with a project dir.
	if len(targets) < 70 {
		t.Errorf("expected the full agent table (>=70), got %d", len(targets))
	}

	seenID := map[string]bool{}
	seenDisplay := map[string]bool{}
	for _, a := range targets {
		if a.ID == "" {
			t.Errorf("agent with empty id: %+v", a)
			continue
		}
		if seenID[a.ID] {
			t.Errorf("duplicate agent id %q", a.ID)
		}
		seenID[a.ID] = true
		if seenDisplay[strings.ToLower(a.DisplayName)] {
			t.Errorf("duplicate agent display name %q", a.DisplayName)
		}
		seenDisplay[strings.ToLower(a.DisplayName)] = true
		if a.ProjectDir == "" {
			t.Errorf("agent %q has no project skills dir", a.ID)
		}
		if strings.HasPrefix(a.ProjectDir, "/") || strings.Contains(a.ProjectDir, "..") {
			t.Errorf("agent %q project dir must be relative and traversal-free: %q", a.ID, a.ProjectDir)
		}
		if a.GlobalDir != "" {
			if strings.HasPrefix(a.GlobalDir, "/") || strings.Contains(a.GlobalDir, "..") {
				t.Errorf("agent %q global dir must be relative and traversal-free: %q", a.ID, a.GlobalDir)
			}
			if a.Base == BaseEnv && a.EnvVar == "" {
				t.Errorf("agent %q uses BaseEnv without EnvVar", a.ID)
			}
			if a.Base == BaseEnv && a.DefaultBase == "" && a.EnvVar == "" {
				t.Errorf("agent %q has no fallback base", a.ID)
			}
		}
		if a.Universal != (a.ProjectDir == ".agents/skills") {
			t.Errorf("agent %q Universal flag disagrees with ProjectDir %q", a.ID, a.ProjectDir)
		}
		if len(a.DetectPaths) == 0 && len(a.DetectCwdPaths) == 0 {
			t.Errorf("agent %q has no detection signal", a.ID)
		}
	}
}

func TestAgentSkillDirResolution(t *testing.T) {
	home := useTempHome(t)
	project := t.TempDir()

	cases := []struct {
		agent string
		scope string
		want  string
		ok    bool
	}{
		{"claude-code", "project", filepath.Join(project, ".claude", "skills"), true},
		{"claude-code", "global", filepath.Join(home, ".claude", "skills"), true},
		{"codex", "project", filepath.Join(project, ".agents", "skills"), true},
		{"codex", "global", filepath.Join(home, ".codex", "skills"), true},
		{"opencode", "global", filepath.Join(home, ".config", "opencode", "skills"), true},
		{"grok-build", "project", filepath.Join(project, ".grok", "skills"), true},
		{"cline", "project", filepath.Join(project, ".agents", "skills"), true},
		{"pi-agent", "project", filepath.Join(project, ".agents", "skills"), true},
		{"zed", "project", filepath.Join(project, ".agents", "skills"), true},
		{"windsurf", "global", filepath.Join(home, ".codeium", "windsurf", "skills"), true},
		{"windsurf", "project", filepath.Join(project, ".windsurf", "skills"), true},
		{"kimi-code-cli", "global", filepath.Join(home, ".agents", "skills"), true},
		{"amp", "global", filepath.Join(home, ".config", "agents", "skills"), true},
		// No documented global location.
		{"eve", "global", "", false},
		{"promptscript", "global", "", false},
		// Unknown.
		{"not-an-agent", "project", "", false},
	}
	for _, c := range cases {
		got, ok := AgentSkillDir(c.agent, c.scope, project, home)
		if ok != c.ok || got != c.want {
			t.Errorf("AgentSkillDir(%q,%q) = %q,%v want %q,%v", c.agent, c.scope, got, ok, c.want, c.ok)
		}
	}
}

func TestAgentSkillDirEnvOverrides(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	t.Setenv("CODEX_HOME", filepath.Join(home, "cx"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "cc"))
	t.Setenv("GROK_HOME", filepath.Join(home, "gr"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	for _, c := range []struct{ agent, want string }{
		{"codex", filepath.Join(home, "cx", "skills")},
		{"claude-code", filepath.Join(home, "cc", "skills")},
		{"grok-build", filepath.Join(home, "gr", "skills")},
		{"opencode", filepath.Join(home, "xdg", "opencode", "skills")},
	} {
		got, ok := AgentSkillDir(c.agent, "global", project, home)
		if !ok || got != c.want {
			t.Errorf("env override %q = %q,%v want %q", c.agent, got, ok, c.want)
		}
	}
}

func TestOpenClawRenamedHome(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	got, ok := AgentSkillDir("openclaw", "global", project, home)
	if !ok || got != filepath.Join(home, ".openclaw", "skills") {
		t.Fatalf("default openclaw dir wrong: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".clawdbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, _ = AgentSkillDir("openclaw", "global", project, home)
	if got != filepath.Join(home, ".clawdbot", "skills") {
		t.Errorf("expected renamed clawdbot home to win, got %q", got)
	}
}

func TestAgentInstalled(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	claude, ok := LookupAgent("claude-code")
	if !ok {
		t.Fatal("claude-code missing from table")
	}
	if AgentInstalled(claude, project, home) {
		t.Error("claude-code should not be detected in an empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !AgentInstalled(claude, project, home) {
		t.Error("claude-code should be detected once ~/.claude exists")
	}

	// Project-scoped detection (replit lives in the repo).
	replit, _ := LookupAgent("replit")
	if AgentInstalled(replit, project, home) {
		t.Error("replit should not be detected in an empty project")
	}
	if err := os.MkdirAll(filepath.Join(project, ".replit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !AgentInstalled(replit, project, home) {
		t.Error("replit should be detected from the project .replit dir")
	}
}

func TestPlanInstallMultipleAgentsSharedUniversal(t *testing.T) {
	root := t.TempDir()
	writeSkillTree(t, root, "s", "s", "d")
	found, _ := DiscoverSkills(root)
	home := t.TempDir()
	project := t.TempDir()

	// codex and opencode both read .agents/skills at project scope, so the
	// planner must collapse them into a single write to avoid a collision.
	ops := PlanInstall(found, []string{"codex", "opencode"}, "project", project, home)
	if len(ops) != 1 {
		t.Fatalf("expected shared destination to collapse to 1 op, got %d (%+v)", len(ops), ops)
	}
	if ops[0].ToDir != filepath.Join(project, ".agents", "skills", "s") {
		t.Errorf("unexpected collapsed destination: %s", ops[0].ToDir)
	}

	// Distinct destinations stay separate.
	ops = PlanInstall(found, []string{"codex", "claude-code"}, "project", project, home)
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops for distinct destinations, got %d", len(ops))
	}
}
