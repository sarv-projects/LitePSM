package skills

// ledger.go — the install ledger.
//
// `litespm skills add` copies SKILL.md directories into each agent's own skills
// tree. Before this file it kept no record of what it wrote, which made removal
// impossible: the tool could create directories across a dozen agent trees and
// had no way to enumerate them again.
//
// The ledger is the record. It is written only after a copy succeeds, and it is
// the only thing `skills remove` trusts — removal never guesses a path from a
// skill name, because a wrong guess deletes somebody else's directory.
//
// Safety rules for deletion:
//   - only directories named in the ledger are ever removed;
//   - a directory is removed only if it still contains a SKILL.md, so a path
//     that was repurposed by the user is left alone;
//   - a directory is refused as symlinked (never followed out of the skill
//     tree);
//   - a directory containing files beyond the recorded inventory is left
//     alone, so user-added content is never destroyed;
//   - removal is refused, per entry, when the recorded scope does not match
//     the requested scope, so `--all` cannot cross scopes silently;
//   - the ledger entry is dropped only after the directory is gone.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LedgerEntry records one directory this tool created.
//
// The provenance fields are optional and backwards compatible: ledgers written
// before they existed still load with zero values, and entries produced by the
// pre-provenance CLI remain removable via the legacy checks.
type LedgerEntry struct {
	SkillName   string    `json:"skillName"`
	AgentID     string    `json:"agentId,omitempty"`
	HostLabel   string    `json:"hostLabel"`
	DestDir     string    `json:"destDir"`
	Source      string    `json:"source"`
	Scope       string    `json:"scope"`
	InstalledAt time.Time `json:"installedAt"`

	// ContentDigest is the tree digest captured at install time (see
	// CaptureProvenance). Empty for legacy entries and for empty trees.
	ContentDigest string `json:"contentDigest,omitempty"`
	// SourceRef is the source pin/commit recorded at install time. Empty when
	// the source was unpinned or unknown.
	SourceRef string `json:"sourceRef,omitempty"`
	// Inventory lists the regular files this tool created, relative to
	// DestDir. Nil for legacy entries; used by Remove to detect user-added
	// files.
	Inventory []FileRecord `json:"inventory,omitempty"`
}

// Ledger is a JSON file listing every directory this tool has created.
type Ledger struct {
	path string
	mu   sync.Mutex
}

// OpenLedger loads the ledger at path. A missing file is not an error: it means
// nothing has been installed yet.
func OpenLedger(path string) (*Ledger, error) {
	return &Ledger{path: path}, nil
}

// LedgerPath returns the conventional ledger location for a data root.
func LedgerPath(dataRoot string) string {
	return filepath.Join(dataRoot, "skills-ledger.json")
}

func (l *Ledger) read() ([]LedgerEntry, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills ledger: %w", err)
	}
	var entries []LedgerEntry
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("skills ledger is corrupt (%s): %w", l.path, err)
	}
	return entries, nil
}

func (l *Ledger) write(entries []LedgerEntry) error {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SkillName != entries[j].SkillName {
			return entries[i].SkillName < entries[j].SkillName
		}
		return entries[i].DestDir < entries[j].DestDir
	})
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating ledger directory: %w", err)
	}
	blob, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".ledger_%d.tmp", time.Now().UnixNano()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating temporary ledger file: %w", err)
	}
	if _, err := f.Write(append(blob, '\n')); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("writing skills ledger: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, l.path)
}

// Add records newly created directories. Entries with a destination already in
// the ledger are ignored, so re-running an install cannot double-count.
func (l *Ledger) Add(entries []LedgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(entries) == 0 {
		return nil
	}
	existing, err := l.read()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[e.DestDir] = true
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if e.DestDir == "" || seen[e.DestDir] {
			continue
		}
		if e.InstalledAt.IsZero() {
			e.InstalledAt = now
		}
		existing = append(existing, e)
		seen[e.DestDir] = true
	}
	return l.write(existing)
}

// All returns every recorded entry.
func (l *Ledger) All() ([]LedgerEntry, error) {
	return l.read()
}

// Skills returns the distinct skill names that have been installed.
func (l *Ledger) Skills() ([]string, error) {
	entries, err := l.read()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, e := range entries {
		if !seen[e.SkillName] {
			seen[e.SkillName] = true
			names = append(names, e.SkillName)
		}
	}
	sort.Strings(names)
	return names, nil
}

// RemovalOutcome reports what happened to one directory.
type RemovalOutcome struct {
	Entry   LedgerEntry
	Removed bool
	Reason  string
}

// RemoveOptions controls a removal pass.
type RemoveOptions struct {
	// Names selects the skills to remove. Empty means every skill matching the
	// other filters.
	Names []string
	// Scope, when set ("project" or "global"), refuses any entry whose
	// recorded scope differs. An entry with no recorded scope cannot be proven
	// to match, so it is refused as well rather than deleted across scopes.
	Scope string
	// DryRun reports what would happen without touching the filesystem.
	DryRun bool
	// Force overrides the inventory guard and allows deletion of a directory
	// that contains files not recorded at install time. It never overrides the
	// scope check or the symlink refusal.
	Force bool
}

// Remove deletes the recorded directories for the named skills (all skills when
// names is empty) and updates the ledger. It is deliberately conservative: a
// directory that no longer looks like one we installed is reported, not deleted.
//
// Remove performs no scope filtering. Callers that know the requested scope
// must use RemoveScoped so a cross-scope `--all` cannot delete silently.
func (l *Ledger) Remove(names []string, dryRun bool) ([]RemovalOutcome, error) {
	return l.RemoveScoped(RemoveOptions{Names: names, DryRun: dryRun})
}

// RemoveScoped deletes recorded directories subject to the scope and inventory
// guards in RemoveOptions. Every refusal is reported per entry; nothing is
// deleted silently.
func (l *Ledger) RemoveScoped(opts RemoveOptions) ([]RemovalOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.read()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range opts.Names {
		want[n] = true
	}
	removeAll := len(want) == 0

	var outcomes []RemovalOutcome
	var keep []LedgerEntry
	for _, e := range entries {
		if !removeAll && !want[e.SkillName] {
			keep = append(keep, e)
			continue
		}
		if opts.Scope != "" && e.Scope != opts.Scope {
			outcomes = append(outcomes, RemovalOutcome{
				Entry:  e,
				Reason: fmt.Sprintf("scope mismatch: installed scope %q, requested %q; left in place", e.Scope, opts.Scope),
			})
			keep = append(keep, e)
			continue
		}
		reason := removableReason(e, opts.Force)
		if reason != "" {
			outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: false, Reason: reason})
			keep = append(keep, e) // leave it tracked so the user can inspect it
			continue
		}
		if !opts.DryRun {
			if err := os.RemoveAll(e.DestDir); err != nil {
				outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: false, Reason: err.Error()})
				keep = append(keep, e)
				continue
			}
		}
		outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: true})
	}

	if !opts.DryRun {
		if err := l.write(keep); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

// ReplaceEntry overwrites the recorded entry whose DestDir matches, preserving
// order. It is used by updates to refresh provenance after an atomic swap.
func (l *Ledger) ReplaceEntry(destDir string, e LedgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.read()
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].DestDir == destDir {
			entries[i] = e
			return l.write(entries)
		}
	}
	return fmt.Errorf("no ledger entry recorded for %s", destDir)
}

// removableReason returns "" when the directory is safe to delete, or an
// explanation of why it was left alone.
func removableReason(e LedgerEntry, force bool) string {
	dir := e.DestDir
	// Lstat, never Stat: a symlinked destination must be refused, not followed
	// out of the skills tree.
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "already gone"
		}
		return err.Error()
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "refusing to remove symlinked skill directory"
	}
	if !info.IsDir() {
		return "not a directory"
	}
	// Must still be a skill we installed, not a directory the user repurposed.
	if _, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil {
		if _, lerr := os.Lstat(filepath.Join(dir, "skill.md")); lerr != nil {
			return "no SKILL.md present; left alone in case this directory was repurposed"
		}
	}
	return inventoryGuardReason(dir, e.Inventory, force)
}

// inventoryGuardReason reports the first reason why dir is not safe to delete.
// Symlinks are always refused, with or without Force. Files not present in the
// recorded inventory are refused unless force is set. A nil inventory means the
// entry predates provenance capture: only the legacy checks apply, so extra
// files cannot be detected and are not refused.
func inventoryGuardReason(dir string, inventory []FileRecord, force bool) string {
	known := make(map[string]bool, len(inventory))
	for _, f := range inventory {
		known[filepath.ToSlash(f.Path)] = true
	}
	strict := inventory != nil && !force
	var extra []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("contains symlink %s", rel)
		}
		if d.IsDir() || !strict {
			return nil
		}
		if !known[rel] {
			extra = append(extra, rel)
		}
		return nil
	})
	if err != nil {
		return err.Error()
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Sprintf("contains %d file(s) not recorded at install (%s); refusing to delete user content (pass Force to override)",
			len(extra), strings.Join(extra, ", "))
	}
	return ""
}
