package skills

import "testing"

// TestParseSkillSourceURLForms pins the URL handling that used to corrupt
// normal HTTPS URLs: `.git` is inferred only for recognized git hosts, and an
// optional @ref / ?ref= pin is parsed out and recorded.
func TestParseSkillSourceURLForms(t *testing.T) {
	cases := []struct {
		raw      string
		kind     string
		cloneURL string
		repoPath string
		git      bool
		pin      string
	}{
		// Plain HTTPS must be left exactly as written (no blind `.git`).
		{"https://example.com/skills.tar.gz", "url", "https://example.com/skills.tar.gz", "", false, ""},
		{"https://example.com/skills", "url", "https://example.com/skills", "", false, ""},
		{"https://skills.sh/anthropics/skills", "url", "https://skills.sh/anthropics/skills", "", false, ""},
		{"https://example.com/skills?foo=bar", "url", "https://example.com/skills?foo=bar", "", false, ""},
		// Recognized git hosts get the clone suffix.
		{"https://github.com/mattpocock/skills", "url", "https://github.com/mattpocock/skills.git", "mattpocock/skills", true, ""},
		{"https://github.com/mattpocock/skills.git", "url", "https://github.com/mattpocock/skills.git", "mattpocock/skills", true, ""},
		{"https://github.com/mattpocock/skills/", "url", "https://github.com/mattpocock/skills.git", "mattpocock/skills", true, ""},
		{"https://gitlab.com/group/repo", "url", "https://gitlab.com/group/repo.git", "", true, ""},
		// Pins: @ref and ?ref= are stripped from the clone URL and recorded.
		{"anthropics/skills@v1.2.3", "repo", "https://github.com/anthropics/skills.git", "anthropics/skills", true, "v1.2.3"},
		{"https://github.com/anthropics/skills@abc123", "url", "https://github.com/anthropics/skills.git", "anthropics/skills", true, "abc123"},
		{"https://github.com/anthropics/skills?ref=abc123", "url", "https://github.com/anthropics/skills.git", "anthropics/skills", true, "abc123"},
		{"https://example.com/skills@deadbeef", "url", "https://example.com/skills", "", false, "deadbeef"},
	}
	for _, c := range cases {
		src, err := ParseSkillSource(c.raw)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.raw, err)
			continue
		}
		if src.Kind != c.kind || src.CloneURL != c.cloneURL || src.RepoPath != c.repoPath || src.Git != c.git || src.Pin != c.pin {
			t.Errorf("%q: got kind=%q clone=%q repo=%q git=%v pin=%q; want kind=%q clone=%q repo=%q git=%v pin=%q",
				c.raw, src.Kind, src.CloneURL, src.RepoPath, src.Git, src.Pin,
				c.kind, c.cloneURL, c.repoPath, c.git, c.pin)
		}
	}
}

func TestParseSkillSourceRejectsNonHTTPS(t *testing.T) {
	for _, raw := range []string{"http://example.com/x.git", "git@github.com:x/y.git", "ftp://example.com/x"} {
		if _, err := ParseSkillSource(raw); err == nil {
			t.Errorf("%q should be rejected", raw)
		}
	}
}

func TestSkillSourceRecordedRef(t *testing.T) {
	// An explicit pin wins over an observed commit.
	src := SkillSource{Pin: "v2.0.0"}
	if got := src.RecordedRef("deadbeef"); got != "v2.0.0" {
		t.Errorf("explicit pin should win, got %q", got)
	}
	// Without a pin, the observed commit is recorded.
	src = SkillSource{}
	if got := src.RecordedRef("deadbeef"); got != "deadbeef" {
		t.Errorf("observed commit should be recorded, got %q", got)
	}
	// Unpinned and unobserved stays empty.
	if got := (SkillSource{}).RecordedRef(""); got != "" {
		t.Errorf("expected empty ref, got %q", got)
	}
}
