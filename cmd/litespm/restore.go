package main

// restore.go — `litespm restore`: roll an install back to its EXACT
// pre-install bytes, or refuse.
//
// This is the strict counterpart to `install.remove`:
//
//   - `install.remove` is surgical. It splices out only LiteSPM's own node
//     (restoring the user entry it replaced) and DETACHES anything the user
//     edited. It works on a live config that has moved on since the install.
//   - `litespm restore` is byte-exact. It replays the pre-write backup file
//     recorded for each owned write (deployment_mutations.backup_path), so
//     the config ends up exactly as it was before the install — formatting,
//     comments and all. Because a whole-file replay would also discard
//     everything that changed in that file since, it is guarded twice and
//     refuses unless both guards pass:
//
//     1. every node LiteSPM owns must still match what it wrote (or be
//        gone): a node the user edited after the install is refused, never
//        overwritten;
//     2. every difference between the current file and the backup must lie
//        inside a node LiteSPM owns: a sibling entry the user — or the host
//        application — changed since the install is refused, because the
//        restore would silently revert it.
//
//     On refusal the command prints what differs and points at
//     `install.remove`, which removes only LiteSPM's entries.
//
// A file the install CREATED has no backup; restoring it means removing it,
// under the same guards (its current contents must be entirely ours). A file
// the user deleted since the install is reported and left alone — LiteSPM
// never recreates a file the user removed.
//
// HONEST LIMITATION: guard 2 compares parsed content, not raw bytes, because
// the install itself re-renders the region it writes. A comment-only or
// whitespace-only edit OUTSIDE LiteSPM-owned entries is therefore invisible
// to the guard, and a restore would revert it along with everything else. If
// you keep meaning in comments, prefer `litespm install remove`, which
// touches only the owned node.
//
// Usage:
//   litespm restore <installId>            roll one install back
//   litespm restore --host <host-id>       roll back every install on one host
//   ... [--dry-run]

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/lifecycle"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// fileAction restores one host config: from Backup when it is non-empty,
// otherwise by removing the file the install created.
type fileAction struct {
	Path      string
	Backup    string
	Created   bool
	HostID    string
	Scope     domain.InstallScope
	EntryKeys []string // managed entry keys to deregister after restore
}

// restorePlan is the full, guarded description of one install's rollback.
type restorePlan struct {
	InstallID string
	Files     []fileAction
	Dirs      []string // skill directories to remove through the skills ledger
	Notes     []string // already rolled back / nothing to do
}

// restoreOutcome reports what a restore did.
type restoreOutcome struct {
	InstallID string
	Restored  []string
	Removed   []string
	Dirs      []string
	Notes     []string
}

// planRestore evaluates both guards without touching anything. A non-nil
// error is a refusal with the reasons attached; the machine is left exactly
// as it is.
func planRestore(ctx context.Context, db *state.DB, installID string) (*restorePlan, error) {
	if _, err := db.GetInstall(ctx, installID); err != nil {
		return nil, err
	}
	muts, err := deployment.NewLedger(db.Raw()).MutationsForInstall(ctx, installID)
	if err != nil {
		return nil, fmt.Errorf("read deployment ledger: %w", err)
	}
	plan := &restorePlan{InstallID: installID}
	var live []deployment.Mutation
	for _, m := range muts {
		if m.Detached {
			// Ownership was already given up (the node is the user's now);
			// restoring under it would be clobbering.
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: %s was detached from LiteSPM ownership earlier and is yours now", m.FilePath, m.Locator))
			continue
		}
		live = append(live, m)
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("install %s has no LiteSPM-owned writes to restore (installed before the deployment ledger, or already restored)", installID)
	}

	// Group rows by file/directory, preserving first-seen order.
	var order []string
	byFile := map[string][]deployment.Mutation{}
	for _, m := range live {
		if _, seen := byFile[m.FilePath]; !seen {
			order = append(order, m.FilePath)
		}
		byFile[m.FilePath] = append(byFile[m.FilePath], m)
	}

	for _, path := range order {
		rows := byFile[path]
		if rows[0].StructureType == "skill-dir" {
			if err := planSkillRestore(ctx, rows, plan); err != nil {
				return nil, err
			}
			continue
		}
		action, note, err := planFileRestore(ctx, rows)
		if err != nil {
			return nil, err
		}
		if action != nil {
			plan.Files = append(plan.Files, *action)
		}
		if note != "" {
			plan.Notes = append(plan.Notes, note)
		}
	}
	return plan, nil
}

// planSkillRestore: a skill directory is its own digest, so guard 1 is a
// whole-tree comparison — while the digest equals what LiteSPM wrote,
// removing the directory restores the pre-install state exactly.
func planSkillRestore(_ context.Context, rows []deployment.Mutation, plan *restorePlan) error {
	m := rows[0]
	current := currentOwnedImage(context.Background(), m)
	switch {
	case current == "":
		plan.Notes = append(plan.Notes, fmt.Sprintf("%s: already absent", m.FilePath))
	case current != m.PostImageHash:
		return fmt.Errorf("restore refused: skill directory %s changed since the install (its digest no longer matches what LiteSPM wrote); `litespm skills remove` reports per-file state if that is what you want to manage", m.FilePath)
	default:
		plan.Dirs = append(plan.Dirs, m.FilePath)
	}
	return nil
}

// planFileRestore runs both guards for one host config and returns the
// action (nil = nothing to do) or a refusal.
func planFileRestore(ctx context.Context, rows []deployment.Mutation) (*fileAction, string, error) {
	first := rows[0]
	owned := make([][]string, 0, len(rows))
	entryKeys := map[string]bool{}
	for _, m := range rows {
		// Guard 1: every owned node must still match what LiteSPM wrote (or
		// be exactly the pre-install node / gone).
		c := currentOwnedImage(ctx, m)
		if c != "" && c != m.PostImageHash && c != m.PreImageHash {
			return nil, "", fmt.Errorf("restore refused: %s in %s was edited after the install and no longer matches what LiteSPM wrote; use `litespm install remove` to remove only LiteSPM's entries and keep your edit",
				m.Locator, m.FilePath)
		}
		owned = append(owned, strings.Split(m.Locator, "."))
		entryKeys[locatorName(m)] = true
	}

	// Resolve the pre-install bytes: the earliest recorded backup for this
	// file. No backup at all means the install created the file.
	backupPath := ""
	for _, m := range rows {
		if m.BackupPath != "" {
			backupPath = m.BackupPath
			break
		}
	}
	action := &fileAction{
		Path:   first.FilePath,
		HostID: first.HostID,
		Scope:  mutationScope(first.Scope),
	}
	for n := range entryKeys {
		action.EntryKeys = append(action.EntryKeys, n)
	}
	sort.Strings(action.EntryKeys)

	currentBytes, err := os.ReadFile(first.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Never recreate a file the user deleted (or one already gone).
			return nil, fmt.Sprintf("%s: already absent; left gone (LiteSPM does not recreate deleted files)", first.FilePath), nil
		}
		return nil, "", fmt.Errorf("restore refused: cannot read %s: %w", first.FilePath, err)
	}

	var backupBytes []byte
	if backupPath == "" {
		action.Created = true
	} else {
		backupBytes, err = os.ReadFile(backupPath)
		if err != nil {
			return nil, "", fmt.Errorf("restore refused: the pre-install backup for %s is missing (%s): %w; nothing was changed", first.FilePath, backupPath, err)
		}
		action.Backup = backupPath
	}

	// Guard 2: every difference between current and pre-install must lie
	// inside a node LiteSPM owns.
	unowned, err := unownedDifferences(first.StructureType, currentBytes, backupBytes, owned)
	if err != nil {
		return nil, "", fmt.Errorf("restore refused: %w", err)
	}
	if len(unowned) > 0 {
		return nil, "", fmt.Errorf(
			"restore refused: %s changed outside LiteSPM-owned entries since the install (differs: %s); a byte-exact restore would discard those changes — use `litespm install remove` to take out only LiteSPM's entries, or replay the backup by hand: %s",
			first.FilePath, strings.Join(unowned, ", "), orAbsent(backupPath))
	}
	return action, "", nil
}

func orAbsent(p string) string {
	if p == "" {
		return "(none: the install created this file)"
	}
	return p
}

// planHostRestore collects every install with owned writes on one host.
func planHostRestore(ctx context.Context, db *state.DB, hostID string) ([]string, error) {
	muts, err := deployment.NewLedger(db.Raw()).MutationsForHost(ctx, hostID)
	if err != nil {
		return nil, fmt.Errorf("read deployment ledger for host %s: %w", hostID, err)
	}
	seen := map[string]bool{}
	var ids []string
	for _, m := range muts {
		if !seen[m.InstallID] {
			seen[m.InstallID] = true
			ids = append(ids, m.InstallID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no LiteSPM-owned writes recorded for host %s", hostID)
	}
	return ids, nil
}

// applyRestore executes a plan in two phases.
//
// Phase A (host configs) runs inside an install transaction: each file's
// current bytes are captured as the compensation, so a failure restores every
// touched config and the state below stays untouched. Once phase A commits,
// phase B (skill directories, registrations, state rows) is convergent
// instead: every step is safe to re-run, so a failure there is reported and
// retrying the whole restore no-ops phase A and re-attempts phase B.
func applyRestore(ctx context.Context, db *state.DB, dataRoot string, plan *restorePlan) (*restoreOutcome, error) {
	out := &restoreOutcome{InstallID: plan.InstallID, Notes: plan.Notes}

	txn := lifecycle.BeginInstall(ctx)
	defer txn.RollbackUnlessCommitted()
	for _, a := range plan.Files {
		snapshot, err := os.ReadFile(a.Path)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", a.Path, err)
		}
		target := a.Path
		rolled := append([]byte(nil), snapshot...)
		txn.Undo(func(ctx context.Context) error {
			return host.AtomicWriteFile(target, rolled, 0600)
		})
		if a.Created {
			if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove install-created %s: %w", a.Path, err)
			}
			out.Removed = append(out.Removed, a.Path)
		} else {
			if err := host.RestoreBackup(a.Path, a.Backup); err != nil {
				return nil, fmt.Errorf("restore %s from %s: %w", a.Path, a.Backup, err)
			}
			out.Restored = append(out.Restored, a.Path)
		}
	}
	// Phase A is durable; from here a retry converges rather than rolls back.
	if err := txn.Commit(); err != nil {
		return nil, err
	}

	// Phase B: skill directories through the skills ledger (its confineDest
	// and inventory guards still apply; the digest guard above proved the
	// tree unmodified).
	for _, dir := range plan.Dirs {
		if err := restoreSkillDir(dataRoot, dir); err != nil {
			return nil, err
		}
		out.Dirs = append(out.Dirs, dir)
	}

	// Deregister the managed entries whose configs were rolled back, then
	// delete the install (which cascades its deployment rows).
	for _, a := range plan.Files {
		for _, key := range a.EntryKeys {
			if err := db.DeleteHostRegistration(ctx, a.HostID, a.Scope, "", key); err != nil {
				return nil, fmt.Errorf("drop registration of %q on %s: %w", key, a.HostID, err)
			}
		}
	}
	if err := db.DeleteInstall(ctx, plan.InstallID); err != nil {
		return nil, fmt.Errorf("delete install state %s: %w", plan.InstallID, err)
	}
	return out, nil
}

// restoreSkillDir removes one rolled-back skill directory through the skills
// ledger, keyed by the directory itself.
func restoreSkillDir(dataRoot, dir string) error {
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		return fmt.Errorf("open skills ledger: %w", err)
	}
	entries, err := ledger.All()
	if err != nil {
		return fmt.Errorf("read skills ledger: %w", err)
	}
	for _, e := range entries {
		if e.DestDir == dir {
			// DeleteDest is digest-bound and confine-checked; RemoveAll then
			// takes the directory the row described.
			return undoSkillInstall(ledger, e)
		}
	}
	// No provenance row: guard 1 proved the digest matches what LiteSPM
	// wrote, but deleting an unrecorded tree is what the ledger exists to
	// prevent — report, do not guess.
	return fmt.Errorf("skills ledger has no row for %s; refusing to delete it", dir)
}

// restoreInstall plans and applies one install's rollback.
func restoreInstall(ctx context.Context, db *state.DB, dataRoot, installID string) (*restoreOutcome, error) {
	plan, err := planRestore(ctx, db, installID)
	if err != nil {
		return nil, err
	}
	return applyRestore(ctx, db, dataRoot, plan)
}

// runRestore is the CLI entry point.
func runRestore(ctx context.Context, args []string) {
	var installID, hostID string
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--host":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --host requires a host id (see `litespm host list`)")
				os.Exit(1)
			}
			i++
			hostID = args[i]
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			restoreUsage()
			return
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n\n", a)
				restoreUsage()
				os.Exit(1)
			}
			if installID != "" {
				fmt.Fprintln(os.Stderr, "Error: give one install id, or --host, not both")
				os.Exit(1)
			}
			installID = a
		}
	}
	if installID == "" && hostID == "" {
		restoreUsage()
		os.Exit(1)
	}
	if installID != "" && hostID != "" {
		fmt.Fprintln(os.Stderr, "Error: give one install id, or --host, not both")
		os.Exit(1)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving paths: %v\n", err)
		os.Exit(1)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening state: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	ids := []string{installID}
	if hostID != "" {
		ids, err = planHostRestore(ctx, db, hostID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	failed := false
	for _, id := range ids {
		plan, perr := planRestore(ctx, db, id)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", id, perr)
			failed = true
			continue
		}
		if dryRun {
			fmt.Printf("Would restore %s:\n", id)
			for _, a := range plan.Files {
				if a.Created {
					fmt.Printf("  - remove install-created %s\n", a.Path)
				} else {
					fmt.Printf("  - restore %s from %s\n", a.Path, a.Backup)
				}
			}
			for _, d := range plan.Dirs {
				fmt.Printf("  - remove skill directory %s\n", d)
			}
			for _, n := range plan.Notes {
				fmt.Printf("  · %s\n", n)
			}
			continue
		}
		out, aerr := applyRestore(ctx, db, paths.DataRoot, plan)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", id, aerr)
			failed = true
			continue
		}
		fmt.Printf("✓ Restored %s\n", id)
		for _, p := range out.Restored {
			fmt.Printf("  - %s restored to its pre-install bytes\n", p)
		}
		for _, p := range out.Removed {
			fmt.Printf("  - %s removed (the install created it)\n", p)
		}
		for _, d := range out.Dirs {
			fmt.Printf("  - skill directory %s removed\n", d)
		}
		for _, n := range out.Notes {
			fmt.Printf("  · %s\n", n)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func restoreUsage() {
	fmt.Println(`Usage:
  litespm restore <installId>        roll one install back to its exact pre-install state
  litespm restore --host <host-id>   roll back every install registered on one host

Flags:
  --dry-run      show what would be restored without touching anything

Restore is byte-exact and therefore strict: if any file changed outside
LiteSPM-owned entries since the install, or an entry LiteSPM wrote was edited
afterwards, restore refuses and changes nothing. Use
"litespm install remove" for the surgical removal that keeps your edits.

Known limitation: the difference check compares parsed content, so a change
that only affects comments or whitespace outside LiteSPM entries is not
detected and would be reverted too.`)
}

// --- guards -----------------------------------------------------------------

// diffLeaf is one difference between the current file and its pre-install
// backup, addressed by path segments (never a joined string, so a key that
// contains dots cannot impersonate an owned locator's prefix).
type diffLeaf struct {
	segments []string
	display  string
}

// unownedDifferences returns the differences that do NOT lie inside an owned
// locator. Empty result = safe to replay the backup.
func unownedDifferences(structure string, current, backup []byte, owned [][]string) ([]string, error) {
	var leaves []diffLeaf
	switch structure {
	case "json-object":
		l, err := jsonDiffLeaves(current, backup)
		if err != nil {
			return nil, err
		}
		leaves = l
	case "toml-table":
		l, err := tomlDiffLeaves(current, backup)
		if err != nil {
			return nil, err
		}
		leaves = l
	default:
		return nil, fmt.Errorf("structure type %q has no whole-file comparison; refusing to guess", structure)
	}
	var unowned []string
	for _, leaf := range leaves {
		if !isOwnedSegments(leaf.segments, owned) {
			unowned = append(unowned, leaf.display)
		}
	}
	sort.Strings(unowned)
	return unowned, nil
}

// jsonDiffLeaves diffs two JSON documents down to leaf paths. A subtree
// present on only one side is reported at its leaves, so "the install
// created this entry" reads as an owned difference rather than a
// whole-container difference.
//
// An unparseable document on either side is an error: the guard must never
// pass because a file could not be read.
func jsonDiffLeaves(current, backup []byte) ([]diffLeaf, error) {
	cur, err := parseJSONDoc(current, "the current config")
	if err != nil {
		return nil, err
	}
	old, err := parseJSONDoc(backup, "the pre-install backup")
	if err != nil {
		return nil, err
	}
	var leaves []diffLeaf
	diffJSON(cur, old, nil, &leaves)
	return leaves, nil
}

func parseJSONDoc(data []byte, what string) (map[string]any, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is not parseable JSON (%v); refusing to compare", what, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// diffJSON records the segment-paths at which a and b differ.
func diffJSON(a, b any, path []string, out *[]diffLeaf) {
	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	record := func() {
		*out = append(*out, diffLeaf{segments: clonePath(path), display: displayPath(path)})
	}
	switch {
	case aIsMap && bIsMap:
		for k := range am {
			bv, ok := bm[k]
			if !ok {
				diffJSON(am[k], nil, append(path, k), out)
				continue
			}
			diffJSON(am[k], bv, append(path, k), out)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				diffJSON(nil, bm[k], append(path, k), out)
			}
		}
	case aIsMap && !bIsMap:
		if b == nil {
			// Present on one side only: descend so ownership is judged at
			// leaf level (the whole added subtree is reported underneath).
			for k := range am {
				diffJSON(am[k], nil, append(path, k), out)
			}
			return
		}
		record() // map vs scalar
	case !aIsMap && bIsMap:
		if a == nil {
			for k := range bm {
				diffJSON(nil, bm[k], append(path, k), out)
			}
			return
		}
		record()
	default:
		if !reflect.DeepEqual(a, b) {
			record()
		}
	}
}

func clonePath(p []string) []string { return append([]string(nil), p...) }

func displayPath(p []string) string {
	if len(p) == 0 {
		return "(document root)"
	}
	return strings.Join(p, ".")
}

// isOwnedSegments reports whether path is inside (or equal to) one of the
// owned locators, compared segment by segment.
func isOwnedSegments(path []string, owned [][]string) bool {
	for _, o := range owned {
		if len(path) < len(o) {
			continue
		}
		match := true
		for i, seg := range o {
			if path[i] != seg {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// tomlDiffLeaves compares two TOML documents section by section (the
// preamble before the first header is section ""). A section that differs —
// or exists on only one side — is a leaf; ownership is judged against the
// section name split on dots, which is exactly the locator shape.
func tomlDiffLeaves(current, backup []byte) ([]diffLeaf, error) {
	cur, err := tomlSections(string(current))
	if err != nil {
		return nil, fmt.Errorf("the current config is not readable as TOML (%v); refusing to compare", err)
	}
	old, err := tomlSections(string(backup))
	if err != nil {
		return nil, fmt.Errorf("the pre-install backup is not readable as TOML (%v); refusing to compare", err)
	}
	var leaves []diffLeaf
	for name, body := range cur {
		oldBody, ok := old[name]
		if !ok || oldBody != body {
			leaves = append(leaves, tomlLeaf(name, len(body) > 0))
		}
	}
	for name, body := range old {
		if _, ok := cur[name]; !ok && len(body) > 0 {
			leaves = append(leaves, tomlLeaf(name, true))
		}
	}
	return leaves, nil
}

func tomlLeaf(name string, nonEmpty bool) diffLeaf {
	if name == "" {
		return diffLeaf{segments: nil, display: "(content before the first table header)"}
	}
	segs := strings.Split(name, ".")
	d := name
	if !nonEmpty {
		d = name + " (empty in one side)"
	}
	return diffLeaf{segments: segs, display: d}
}

// tomlSections parses a TOML document into section name -> body (the lines
// under the header, comments included). Syntax errors are surfaced: this is
// used only on the comparison path, where an unreadable file must refuse
// rather than pass.
func tomlSections(content string) (map[string]string, error) {
	sections := map[string]string{}
	current := ""
	var buf strings.Builder
	flush := func() { sections[current] = strings.TrimRight(buf.String(), "\n") }
	seenHeader := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if !strings.HasSuffix(trimmed, "]") && !strings.HasSuffix(trimmed, "]]") {
				return nil, fmt.Errorf("line %q looks like a malformed table header", trimmed)
			}
			flush()
			buf.Reset()
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			name = strings.TrimPrefix(name, "[")
			name = strings.TrimSuffix(name, "]")
			current = strings.TrimSpace(name)
			seenHeader = true
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	if buf.Len() > 0 || seenHeader || content != "" {
		flush()
	}
	return sections, nil
}
