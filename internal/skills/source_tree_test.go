package skills

import "testing"

// TestParseSkillSourceGitHubTreeURLs pins the browse-URL handling: a GitHub
// /tree/<ref>/<path> URL names a directory inside a repository, not a
// repository, so it must resolve to a repository clone target plus the ref and
// subpath. Before this, the generic handling appended ".git" to the whole
// browse path and the clone failed.
func TestParseSkillSourceGitHubTreeURLs(t *testing.T) {
	cases := []struct {
		raw      string
		cloneURL string
		repoPath string
		pin      string
		subpath  string
	}{
		{
			raw:      "https://github.com/coreyhaines31/marketingskills/tree/main/skills/ab-testing",
			cloneURL: "https://github.com/coreyhaines31/marketingskills.git",
			repoPath: "coreyhaines31/marketingskills",
			pin:      "main",
			subpath:  "skills/ab-testing",
		},
		{
			raw:      "https://github.com/google/skills/tree/main/skills/cloud/agent-platform-skill-registry",
			cloneURL: "https://github.com/google/skills.git",
			repoPath: "google/skills",
			pin:      "main",
			subpath:  "skills/cloud/agent-platform-skill-registry",
		},
		{
			raw:      "https://github.com/owner/repo/tree/v1.2.3",
			cloneURL: "https://github.com/owner/repo.git",
			repoPath: "owner/repo",
			pin:      "v1.2.3",
			subpath:  "",
		},
		{
			// A blob URL addresses a file, but the repository and ref are still
			// unambiguous; the subpath is the file's directory context as given.
			raw:      "https://github.com/owner/repo/blob/main/skills/thing/SKILL.md",
			cloneURL: "https://github.com/owner/repo.git",
			repoPath: "owner/repo",
			pin:      "main",
			subpath:  "skills/thing/SKILL.md",
		},
	}
	for _, c := range cases {
		src, err := ParseSkillSource(c.raw)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.raw, err)
			continue
		}
		if !src.Git {
			t.Errorf("%q: expected a git source", c.raw)
		}
		if src.CloneURL != c.cloneURL || src.RepoPath != c.repoPath || src.Pin != c.pin || src.Subpath != c.subpath {
			t.Errorf("%q: got clone=%q repo=%q pin=%q subpath=%q; want clone=%q repo=%q pin=%q subpath=%q",
				c.raw, src.CloneURL, src.RepoPath, src.Pin, src.Subpath,
				c.cloneURL, c.repoPath, c.pin, c.subpath)
		}
	}
}

// TestParseSkillSourceExplicitPinBeatsTreeRef keeps the documented precedence:
// an explicit @ref / ?ref= wins over the ref embedded in a browse URL.
func TestParseSkillSourceExplicitPinBeatsTreeRef(t *testing.T) {
	src, err := ParseSkillSource("https://github.com/owner/repo/tree/main/skills/x?ref=abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Pin != "abc123" {
		t.Errorf("explicit pin lost: got %q", src.Pin)
	}
	if src.Subpath != "skills/x" {
		t.Errorf("subpath not parsed: got %q", src.Subpath)
	}
}
