package skills

// ledger.go — the install ledger.
//
// `litepsm skills add` copies SKILL.md directories into each agent's own skills
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
//   - a directory containing anything beyond the files we recorded is left
//     alone, so user-added content is never destroyed;
//   - the ledger entry is dropped only after the directory is gone.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// LedgerEntry records one directory this tool created.
type LedgerEntry struct {
	SkillName   string    `json:"skillName"`
	AgentID     string    `json:"agentId,omitempty"`
	HostLabel   string    `json:"hostLabel"`
	DestDir     string    `json:"destDir"`
	Source      string    `json:"source"`
	Scope       string    `json:"scope"`
	InstalledAt time.Time `json:"installedAt"`
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

// Remove deletes the recorded directories for the named skills (all skills when
// names is empty) and updates the ledger. It is deliberately conservative: a
// directory that no longer looks like one we installed is reported, not deleted.
func (l *Ledger) Remove(names []string, dryRun bool) ([]RemovalOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.read()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
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
		reason := removableReason(e.DestDir)
		if reason != "" {
			outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: false, Reason: reason})
			keep = append(keep, e) // leave it tracked so the user can inspect it
			continue
		}
		if !dryRun {
			if err := os.RemoveAll(e.DestDir); err != nil {
				outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: false, Reason: err.Error()})
				keep = append(keep, e)
				continue
			}
		}
		outcomes = append(outcomes, RemovalOutcome{Entry: e, Removed: true})
	}

	if !dryRun {
		if err := l.write(keep); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

// removableReason returns "" when the directory is safe to delete, or an
// explanation of why it was left alone.
func removableReason(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "already gone"
		}
		return err.Error()
	}
	if !info.IsDir() {
		return "not a directory"
	}
	// Must still be a skill we installed, not a directory the user repurposed.
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return "no SKILL.md present; left alone in case this directory was repurposed"
	}
	return ""
}
