package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillSource(t *testing.T) {
	cases := []struct {
		raw      string
		kind     string
		cloneURL string
		wantErr  bool
	}{
		{"anthropics/skills", "repo", "https://github.com/anthropics/skills.git", false},
		{"https://github.com/mattpocock/skills.git", "url", "https://github.com/mattpocock/skills.git", false},
		{"https://github.com/mattpocock/skills", "url", "https://github.com/mattpocock/skills.git", false},
		{"not a source!!!", "", "", true},
		{"http://example.com/x.git", "", "", true},
		{"git@github.com:x/y.git", "", "", true},
	}
	for _, c := range cases {
		src, err := ParseSkillSource(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("expected error for %q", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("unexpected error for %q: %v", c.raw, err)
			continue
		}
		if src.Kind != c.kind || src.CloneURL != c.cloneURL {
			t.Errorf("for %q got kind=%q clone=%q", c.raw, src.Kind, src.CloneURL)
		}
	}

	// Local directory.
	dir := t.TempDir()
	src, err := ParseSkillSource(dir)
	if err != nil || src.Kind != "local" || src.LocalDir == "" {
		t.Fatalf("local source failed: %+v %v", src, err)
	}
}

func writeSkillTree(t *testing.T, root, rel, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverSkills(t *testing.T) {
	root := t.TempDir()
	writeSkillTree(t, root, "frontend-design", "frontend-design", "Design pages")
	writeSkillTree(t, root, "group/nested-skill", "nested-skill", "Nested ok")
	writeSkillTree(t, root, "group/deep/too-deep", "too-deep", "Too deep")
	// Invalid: no description.
	writeSkillTree(t, root, "bad-skill", "bad-skill", "")
	// .git must not be descended into.
	writeSkillTree(t, root, ".git/hooks/evil", "evil", "Evil skill")

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	names := map[string]bool{}
	for _, f := range found {
		names[f.Pkg.Name] = true
	}
	for _, want := range []string{"frontend-design", "nested-skill"} {
		if !names[want] {
			t.Errorf("missing skill %s (found %v)", want, names)
		}
	}
	for _, banned := range []string{"too-deep", "bad-skill", "evil", ""} {
		if names[banned] {
			t.Errorf("skill %q should have been skipped", banned)
		}
	}
}

func TestSanitizeSkillName(t *testing.T) {
	if _, err := SanitizeSkillName("frontend-design"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	for _, bad := range []string{"", "../evil", "a/b", "UPPER CASE!", "x.y"} {
		if _, err := SanitizeSkillName(bad); err == nil {
			t.Errorf("invalid name %q accepted", bad)
		}
	}
}

func TestPlanInstall(t *testing.T) {
	root := t.TempDir()
	writeSkillTree(t, root, "frontend-design", "frontend-design", "Design pages")
	found, err := DiscoverSkills(root)
	if err != nil || len(found) != 1 {
		t.Fatalf("discover failed: %v %d", err, len(found))
	}
	home := t.TempDir()
	project := t.TempDir()

	// Same XDG isolation as TestAgentSkillDirResolution: without it, CI runners
	// resolve XDG-style agents into /home/runner/.config instead of the sandbox.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ops := PlanInstall(found, []string{"claude-code", "codex"}, "project", project, home)

	byHost := map[string]string{}
	for _, op := range ops {
		byHost[op.HostLabel] = op.ToDir
	}
	if byHost["claude-code"] != filepath.Join(project, ".claude", "skills", "frontend-design") {
		t.Errorf("claude project target wrong: %v", byHost)
	}
	if byHost["codex"] != filepath.Join(project, ".agents", "skills", "frontend-design") {
		t.Errorf("codex project target wrong: %v", byHost)
	}

	// Global scope honours env overrides.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "custom-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "custom-codex"))
	ops = PlanInstall(found, []string{"claude-code", "codex", "opencode"}, "global", project, home)
	byHost = map[string]string{}
	for _, op := range ops {
		byHost[op.HostLabel] = op.ToDir
	}
	if byHost["claude-code"] != filepath.Join(home, "custom-claude", "skills", "frontend-design") {
		t.Errorf("claude global env override wrong: %v", byHost)
	}
	if byHost["codex"] != filepath.Join(home, "custom-codex", "skills", "frontend-design") {
		t.Errorf("codex global env override wrong: %v", byHost)
	}
	if byHost["opencode"] != filepath.Join(home, ".config", "opencode", "skills", "frontend-design") {
		t.Errorf("opencode global default wrong: %v", byHost)
	}
}

func TestCopySkillDir(t *testing.T) {
	src := t.TempDir()
	writeSkillTree(t, src, "s", "s", "d")
	if err := os.WriteFile(filepath.Join(src, "s", "ref.md"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out", "s")
	if err := CopySkillDir(filepath.Join(src, "s"), dst); err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "name: s") {
		t.Fatalf("copied skill unreadable: %v", err)
	}
	// Existing destination is refused.
	if err := CopySkillDir(filepath.Join(src, "s"), dst); err == nil {
		t.Fatal("expected error copying onto existing destination")
	}
}

func TestHostSkillDir(t *testing.T) {
	if _, ok := HostSkillDir("unknown-agent", "project", "/p", "/h"); ok {
		t.Error("unknown agent must not have a mapped dir")
	}
	dir, ok := HostSkillDir("claude-code", "global", "/p", "/h")
	if !ok || dir != filepath.Join("/h", ".claude", "skills") {
		t.Errorf("claude-code global mapping wrong: %q %v", dir, ok)
	}
	dir, ok = HostSkillDir("grok-build", "project", "/p", "/h")
	if !ok || dir != filepath.Join("/p", ".grok", "skills") {
		t.Errorf("grok-build project mapping wrong: %q %v", dir, ok)
	}
}
