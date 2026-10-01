package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// install.go — `litepsm skills add` engine.
//
// Installs portable SKILL.md skill directories from a GitHub repository (or a
// local directory) into the universal `.agents/skills` tree plus any mapped
// host skill directories. This file holds the pure, testable layer: source
// parsing, skill discovery, install planning, and safe directory copy. All
// prompting lives in cmd/litepsm/skills_add.go.

// MaxSkillBytes bounds a single installed skill tree (skills are
// instructions plus small assets; anything larger is almost certainly not a
// skill and is refused rather than truncated).
const MaxSkillBytes = 32 << 20 // 32 MiB

var validSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// SkillSource is a parsed `--skill` installer source.
type SkillSource struct {
	// Kind is "repo" (owner/name), "url" (https git remote), or "local".
	Kind string
	// Display is the human-readable source label shown in the UI.
	Display string
	// CloneURL is set for repo/url kinds.
	CloneURL string
	// LocalDir is set for the local kind.
	LocalDir string
	// RepoPath is "owner/name" for repo/url kinds, "" otherwise.
	RepoPath string
}

// ParseSkillSource parses an installer source: "owner/repo", an https GitHub
// (or any https git remote) URL, or a local directory path.
func ParseSkillSource(raw string) (SkillSource, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return SkillSource{}, fmt.Errorf("empty skill source")
	}

	// Local directory (exists on disk).
	if fi, err := os.Stat(s); err == nil && fi.IsDir() {
		abs, err := filepath.Abs(s)
		if err != nil {
			return SkillSource{}, err
		}
		return SkillSource{Kind: "local", Display: abs, LocalDir: abs}, nil
	}

	// owner/repo shorthand.
	if !strings.Contains(s, "://") {
		parts := strings.Split(s, "/")
		if len(parts) == 2 && validOwnerRepo(parts[0]) && validOwnerRepo(parts[1]) {
			return SkillSource{
				Kind:     "repo",
				Display:  fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1]),
				CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1]),
				RepoPath: parts[0] + "/" + parts[1],
			}, nil
		}
		return SkillSource{}, fmt.Errorf("invalid skill source %q: expected owner/repo, an https git URL, or a local directory", s)
	}

	// https URL only (never http, ssh, or file: the installer clones over TLS).
	if strings.HasPrefix(s, "https://") {
		clone := s
		if !strings.HasSuffix(clone, ".git") {
			clone += ".git"
		}
		return SkillSource{Kind: "url", Display: strings.TrimSuffix(s, ".git"), CloneURL: clone, RepoPath: guessRepoPath(s)}, nil
	}
	return SkillSource{}, fmt.Errorf("invalid skill source %q: only https git URLs are accepted", s)
}

var validOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString

// guessRepoPath extracts "owner/name" from a github.com URL for display and
// skills.sh detail links. Returns "" when the URL is not a GitHub repo URL.
func guessRepoPath(rawURL string) string {
	rest := strings.TrimPrefix(rawURL, "https://github.com/")
	rest = strings.TrimSuffix(rest, ".git")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// DiscoveredSkill is one SKILL.md directory found under a source tree.
type DiscoveredSkill struct {
	// Dir is the absolute skill directory.
	Dir string
	// Rel is the path relative to the source root (for display).
	Rel string
	// Pkg is the parsed skill frontmatter.
	Pkg *SkillPackage
}

// DiscoverSkills walks root (max depth 2, like the reference installer) and
// returns every directory containing SKILL.md/skill.md with valid frontmatter
// (name and description required). Invalid skills are skipped, never fatal.
func DiscoverSkills(root string) ([]DiscoveredSkill, error) {
	var out []DiscoveredSkill
	rootDepth := len(strings.Split(filepath.Clean(root), string(filepath.Separator)))
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees
		}
		if !d.IsDir() {
			return nil
		}
		depth := len(strings.Split(filepath.Clean(path), string(filepath.Separator))) - rootDepth
		if depth > 2 {
			return filepath.SkipDir
		}
		// Never descend into .git.
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if depth == 0 {
			return nil
		}
		pkg, lerr := LoadSkillFromDirectory(path)
		if lerr != nil {
			return nil
		}
		if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.Description) == "" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, DiscoveredSkill{Dir: path, Rel: rel, Pkg: pkg})
		return filepath.SkipDir // a skill dir is never a parent of another skill
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SanitizeSkillName validates a skill directory name for installation.
func SanitizeSkillName(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if !validSkillName.MatchString(n) {
		return "", fmt.Errorf("invalid skill name %q: must match [a-z0-9-]", name)
	}
	return n, nil
}

// pathExists is a seam for tests.
var pathExists = func(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// AgentSkillDir, HostSkillDir, LookupAgent and the agent table live in
// agents.go. HostSkillDir is kept as a compatibility alias.
func HostSkillDir(agentID, scope, projectRoot, home string) (string, bool) {
	return AgentSkillDir(agentID, scope, projectRoot, home)
}

// UniversalSkillDir is the always-installed canonical skill tree.
func UniversalSkillDir(scope, projectRoot, home string) string {
	if scope == "global" {
		return filepath.Join(home, ".agents", "skills")
	}
	return filepath.Join(projectRoot, ".agents", "skills")
}

// InstallOp is one planned skill copy.
type InstallOp struct {
	SkillName string
	FromDir   string
	ToDir     string
	// HostLabel is shown in the summary ("universal" or the agent id).
	HostLabel string
}

// PlanInstall builds copy operations: every selected skill is installed into
// each selected agent's own skill directory (repo-local for scope=project,
// user-global for scope=global). Agents that share a directory (several read
// the universal `.agents/skills` tree) collapse to a single write, so a second
// copy never collides with the first.
func PlanInstall(skills []DiscoveredSkill, agents []string, scope, projectRoot, home string) []InstallOp {
	var ops []InstallOp
	seenDest := map[string]bool{}
	for _, sk := range skills {
		name, err := SanitizeSkillName(sk.Pkg.Name)
		if err != nil {
			continue
		}
		for _, ag := range agents {
			dir, ok := AgentSkillDir(ag, scope, projectRoot, home)
			if !ok {
				continue
			}
			dst := filepath.Join(dir, name)
			if seenDest[dst] {
				continue
			}
			seenDest[dst] = true
			ops = append(ops, InstallOp{
				SkillName: name,
				FromDir:   sk.Dir,
				ToDir:     dst,
				HostLabel: ag,
			})
		}
	}
	return ops
}

// CopySkillDir copies a skill directory tree to dst. Symlinks are refused
// (never followed, never recreated), hidden VCS dirs are skipped, and the
// total is bounded by MaxSkillBytes. dst must not exist.
func CopySkillDir(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	var total int64
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Refuse symlinks outright.
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink %s", rel)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > MaxSkillBytes {
			return fmt.Errorf("skill exceeds %d bytes", MaxSkillBytes)
		}
		return copyFile(path, target, info.Mode())
	})
	return err
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
