package skills

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// install.go — `litespm skills add` engine.
//
// Installs portable SKILL.md skill directories from a GitHub repository (or a
// local directory) into the universal `.agents/skills` tree plus any mapped
// host skill directories. This file holds the pure, testable layer: source
// parsing, skill discovery, install planning, and safe directory copy. All
// prompting lives in cmd/litespm/skills_add.go.

// MaxSkillBytes bounds a single installed skill tree (skills are
// instructions plus small assets; anything larger is almost certainly not a
// skill and is refused rather than truncated).
const MaxSkillBytes = 32 << 20 // 32 MiB

var validSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// SkillSource is a parsed `--skill` installer source.
type SkillSource struct {
	// Kind is "repo" (owner/name), "url" (https), or "local".
	Kind string
	// Display is the human-readable source label shown in the UI.
	Display string
	// CloneURL is the fetch target for repo/url kinds.
	CloneURL string
	// LocalDir is set for the local kind.
	LocalDir string
	// RepoPath is "owner/name" for repo/url kinds, "" otherwise.
	RepoPath string
	// Git is true when CloneURL is a git remote (a `.git` URL or a recognized
	// git host). False means the URL was left exactly as given.
	Git bool
	// Pin is the optional revision to install: `owner/repo@ref`,
	// `https://host/repo@ref`, or a `?ref=` query parameter. Empty means the
	// source default branch.
	Pin string
	// Subpath is the directory inside the repository that holds the skill,
	// set when the source was a browse URL such as
	// `https://github.com/owner/repo/tree/<ref>/skills/name`. Empty means the
	// repository root. Callers that scan the checkout must join this onto the
	// clone root before discovering skills.
	Subpath string
}

// knownGitHosts are hosts where a bare `owner/name` path is a git remote and a
// missing `.git` suffix is safely inferred. Other HTTPS URLs are left verbatim:
// appending `.git` blindly corrupts normal URLs such as archive or landing
// pages.
var knownGitHosts = map[string]bool{
	"github.com":     true,
	"www.github.com": true,
	"gitlab.com":     true,
	"bitbucket.org":  true,
	"codeberg.org":   true,
	"git.sr.ht":      true,
	"gitea.com":      true,
}

// ParseSkillSource parses an installer source: "owner/repo", an https URL, or a
// local directory path. An optional `@<ref>` suffix (or `ref`/`pin`/`commit`
// query parameter) pins the source to a revision; the pin is recorded in the
// ledger but not resolved here (resolution needs the fetcher).
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

	// owner/repo shorthand, with an optional @ref pin.
	if !strings.Contains(s, "://") {
		base, pin := splitAtRef(s)
		parts := strings.Split(base, "/")
		if len(parts) == 2 && validOwnerRepo(parts[0]) && validOwnerRepo(parts[1]) {
			clone := fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1])
			return SkillSource{
				Kind:     "repo",
				Display:  clone,
				CloneURL: clone,
				RepoPath: parts[0] + "/" + parts[1],
				Git:      true,
				Pin:      pin,
			}, nil
		}
		return SkillSource{}, fmt.Errorf("invalid skill source %q: expected owner/repo, an https git URL, or a local directory", s)
	}

	// https URL only (never http, ssh, or file: the installer clones over TLS).
	if !strings.HasPrefix(s, "https://") {
		return SkillSource{}, fmt.Errorf("invalid skill source %q: only https git URLs are accepted", s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return SkillSource{}, fmt.Errorf("invalid skill source %q: not a valid https URL", s)
	}

	// Pin from a query parameter, then from a path @ref. The path form is
	// stripped so the ref is not treated as part of the repository path.
	pin := ""
	q := u.Query()
	for _, key := range []string{"ref", "pin", "commit", "version"} {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			pin = v
			q.Del(key)
			break
		}
	}
	if pin == "" {
		if idx := strings.LastIndex(u.Path, "@"); idx > 0 {
			pin = u.Path[idx+1:]
			u.Path = u.Path[:idx]
			u.RawPath = ""
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""

	// A GitHub *browse* URL names a directory inside a repository
	// (/<owner>/<repo>/tree/<ref>/<path>), not the repository itself. The
	// generic ".git" suffixing below would produce a clone URL like
	// "/owner/repo/tree/main/skills/x.git", which no git server serves, so
	// split it into a repository clone target plus the pinned ref and subpath.
	if isGitHubHost(u.Host) {
		if repoPath, ref, sub, ok := splitGitTreeBrowsePath(u.Path); ok {
			clone := fmt.Sprintf("https://%s%s.git", u.Host, repoPath)
			display := strings.TrimSuffix(clone, ".git")
			if sub != "" {
				display += "/" + sub
			}
			if pin == "" {
				pin = ref
			}
			return SkillSource{
				Kind:     "url",
				Display:  display,
				CloneURL: clone,
				RepoPath: strings.TrimPrefix(repoPath, "/"),
				Git:      true,
				Pin:      pin,
				Subpath:  sub,
			}, nil
		}
	}

	// Only infer a git clone URL on a recognized git host; otherwise leave the
	// URL exactly as the user wrote it.
	if knownGitHosts[u.Host] && !strings.HasSuffix(u.Path, ".git") {
		u.Path += ".git"
		u.RawPath = ""
	}
	clone := u.String()
	git := strings.HasSuffix(u.Path, ".git")
	return SkillSource{
		Kind:     "url",
		Display:  strings.TrimSuffix(clone, ".git"),
		CloneURL: clone,
		RepoPath: guessRepoPath(u),
		Git:      git,
		Pin:      pin,
	}, nil
}

// splitAtRef splits "base@ref" on the last '@'. A leading '@' (or none) leaves
// the whole string as base and returns an empty ref.
func splitAtRef(s string) (string, string) {
	idx := strings.LastIndex(s, "@")
	if idx <= 0 || idx == len(s)-1 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

// RecordedRef returns the ref to persist for this source: an explicit pin wins,
// otherwise the commit SHA the caller observed for the checkout (for example
// from `git rev-parse HEAD`). Empty means the source is unpinned.
func (s SkillSource) RecordedRef(observedCommit string) string {
	if s.Pin != "" {
		return s.Pin
	}
	return observedCommit
}

var validOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString

// isGitHubHost reports whether the host is GitHub's web front end, whose
// /tree/<ref>/<path> browse URLs address a directory rather than a repository.
func isGitHubHost(host string) bool {
	return host == "github.com" || host == "www.github.com"
}

// splitGitTreeBrowsePath splits a GitHub browse path
// /<owner>/<repo>/tree/<ref>[/<subpath>...] (or /blob/) into the repository
// path, the ref and the subpath. It reports false for anything that is not a
// browse path.
//
// The ref is taken as a single path segment: browse URLs whose ref contains a
// slash (a feature branch) are ambiguous to parse and are left to the generic
// handling rather than guessed at.
func splitGitTreeBrowsePath(p string) (repoPath, ref, subpath string, ok bool) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || parts[3] == "" {
		return "", "", "", false
	}
	if parts[2] != "tree" && parts[2] != "blob" {
		return "", "", "", false
	}
	return "/" + parts[0] + "/" + parts[1], parts[3], strings.Join(parts[4:], "/"), true
}

// guessRepoPath extracts "owner/name" from a github.com URL for display and
// skills.sh detail links. Returns "" when the URL is not a GitHub repo URL.
func guessRepoPath(u *url.URL) string {
	if u == nil || u.Host != "github.com" {
		return ""
	}
	rest := strings.TrimSuffix(u.Path, ".git")
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

// Note (D1): there is deliberately no separate UniversalSkillDir helper. The
// universal `.agents/skills` tree is just the ProjectDir of every agent whose
// AgentTarget has Universal=true, and AgentSkillDir resolves it -- including
// the env-override precedence those agents follow. A second resolver would be
// dead code that could silently drift from the agent table.

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
		// Refuse symlinks and non-regular files outright (FIFOs, devices, sockets)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink %s", rel)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("refusing to copy non-regular file %s", rel)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink %s", rel)
		}
		total += info.Size()
		if total > MaxSkillBytes {
			return fmt.Errorf("skill exceeds %d bytes", MaxSkillBytes)
		}
		return copyFile(path, target, info.Mode(), info.Size())
	})
	if err != nil {
		// dst did not exist when we started and everything under it was
		// created by this call, so a failed copy must not leave a partial
		// tree behind: it would be indistinguishable from an installed skill,
		// and every retry would refuse with "destination already exists".
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}

func copyFile(src, dst string, mode fs.FileMode, expectedSize int64) error {
	in, err := os.OpenFile(src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer in.Close()

	// Verify opened file is still regular (no TOCTOU swap to symlink or pipe)
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("file %s is not regular after opening", src)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, expectedSize+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
