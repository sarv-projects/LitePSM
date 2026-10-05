package skills

// installer.go — policy-gated install and update.
//
// CopySkillDir is the raw copy primitive; this file adds the lifecycle layer
// that the CLI needs:
//
//   - Install consults an injectable policy checker BEFORE it writes anything,
//     then records provenance in the ledger;
//   - Update replaces an installed directory with freshly fetched content via
//     an atomic swap, so a failure at any point leaves the previous install
//     intact, and reports an honest no-op when the content is already current.
//
// Fetching is deliberately out of scope. The package performs no network I/O
// and spawns no processes: the caller (the CLI) clones/downloads to a local
// directory and passes that directory in. That keeps this layer hermetic and
// testable and avoids a hidden second transport.
//
// Policy is an injected function rather than an import of internal/policy.
// internal/policy does not import internal/skills, so a direct import would not
// create a cycle today, but a function seam keeps this leaf package free of the
// policy/state dependency chain and lets each host wire the decision source it
// actually uses. A nil checker means "policy not wired" and is recorded as
// such by the caller; it never fabricates an approval.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PolicyRequest describes one skill write for a policy consult.
type PolicyRequest struct {
	// Operation is "install" or "update".
	Operation string
	// SkillName is the sanitized skill directory name.
	SkillName string
	// Source is the human-readable source label.
	Source string
	// Scope is "project" or "global".
	Scope string
	// DestDir is the destination skill directory.
	DestDir string
	// Effects are canonical effect identifiers, for example
	// "filesystem.write" or "network.outbound".
	Effects []string
}

// PolicyVerdict is the outcome of a policy consult.
type PolicyVerdict struct {
	// Allowed must be true for the write to proceed. "ask" is not allowed:
	// approval must be obtained out of band and the operation retried.
	Allowed bool
	// Decision is the raw decision string ("allow", "deny", "ask").
	Decision string
	// Reason is a human-readable explanation.
	Reason string
	// Reasons are structured reason codes from the policy engine.
	Reasons []string
}

// PolicyChecker consults the security policy before a skill is written. A nil
// checker means the host has not wired policy (see the integration notes); it
// is never treated as an implicit approval by the package itself.
type PolicyChecker func(ctx context.Context, req PolicyRequest) PolicyVerdict

// PolicyDeniedError carries the policy decision that stopped an operation.
type PolicyDeniedError struct {
	Request PolicyRequest
	Verdict PolicyVerdict
}

func (e *PolicyDeniedError) Error() string {
	decision := e.Verdict.Decision
	if decision == "" {
		decision = "deny"
	}
	detail := e.Verdict.Reason
	if detail == "" && len(e.Verdict.Reasons) > 0 {
		detail = strings.Join(e.Verdict.Reasons, ", ")
	}
	if detail == "" {
		detail = "no reason supplied"
	}
	return fmt.Sprintf("%s of skill %q denied by policy (%s): %s", e.Request.Operation, e.Request.SkillName, decision, detail)
}

// Installer records every directory it creates in a Ledger and, when a policy
// checker is configured, consults it before writing.
type Installer struct {
	Ledger *Ledger
	Policy PolicyChecker
}

// NewInstaller builds an installer over a ledger. A nil checker leaves policy
// unwired.
func NewInstaller(ledger *Ledger, checker PolicyChecker) *Installer {
	return &Installer{Ledger: ledger, Policy: checker}
}

// InstallRequest describes one policy-gated install.
type InstallRequest struct {
	Op     InstallOp
	Source SkillSource
	Scope  string
	// ObservedRef is the commit SHA the caller resolved for the checkout, when
	// available. It is recorded only when the source carries no explicit pin.
	ObservedRef string
}

// Install consults policy, copies one planned skill atomically, and records
// provenance. The destination must not already exist; use Update for that.
// On a ledger failure the freshly written directory is removed again so the
// ledger remains the single source of truth.
func (in *Installer) Install(ctx context.Context, req InstallRequest) (LedgerEntry, error) {
	if in == nil || in.Ledger == nil {
		return LedgerEntry{}, fmt.Errorf("installer: a ledger is required")
	}
	op := req.Op
	if op.ToDir == "" || op.FromDir == "" || op.SkillName == "" {
		return LedgerEntry{}, fmt.Errorf("installer: incomplete install operation for skill %q", op.SkillName)
	}
	if err := requireSkillFile(op.FromDir); err != nil {
		return LedgerEntry{}, err
	}

	if err := in.checkPolicy(ctx, "install", req.Source, op.SkillName, req.Scope, op.ToDir); err != nil {
		return LedgerEntry{}, err
	}

	if err := CopySkillDir(op.FromDir, op.ToDir); err != nil {
		return LedgerEntry{}, err
	}

	prov, err := CaptureProvenance(op.ToDir)
	if err != nil {
		_ = os.RemoveAll(op.ToDir)
		return LedgerEntry{}, fmt.Errorf("capturing provenance for %s: %w", op.ToDir, err)
	}
	entry := LedgerEntry{
		SkillName:     op.SkillName,
		AgentID:       op.HostLabel,
		HostLabel:     op.HostLabel,
		DestDir:       op.ToDir,
		Source:        req.Source.Display,
		Scope:         req.Scope,
		ContentDigest: prov.Digest,
		SourceRef:     req.Source.RecordedRef(req.ObservedRef),
		Inventory:     prov.Inventory,
	}
	if err := in.Ledger.Add([]LedgerEntry{entry}); err != nil {
		_ = os.RemoveAll(op.ToDir)
		return LedgerEntry{}, fmt.Errorf("recording install of %s: %w", op.SkillName, err)
	}
	return entry, nil
}

// UpdateRequest describes one skill update. NewDir is the caller-fetched
// directory holding the replacement content; this package performs no fetch.
type UpdateRequest struct {
	// Entry is the existing ledger entry to update.
	Entry LedgerEntry
	// NewDir is a local directory containing the replacement skill content.
	NewDir string
	// NewSource optionally replaces the recorded source label.
	NewSource string
	// NewRef optionally records a new source pin/commit.
	NewRef string
	// Force overrides the inventory guard on the destination. It is only
	// needed when the installed directory was modified locally.
	Force bool
	// DryRun evaluates policy and reports what would happen without touching
	// the filesystem or the ledger.
	DryRun bool
}

// UpdateOutcome reports the result of an update.
type UpdateOutcome struct {
	Entry          LedgerEntry
	Updated        bool
	AlreadyCurrent bool
	// WouldUpdate is set by a dry-run when an update would have proceeded.
	WouldUpdate bool
	Reason      string
}

// Update replaces an installed skill directory via an atomic swap. On any
// failure the previous install remains in place. When the replacement content
// is byte-identical (same tree digest) it reports an honest already-current
// no-op instead of rewriting the tree.
func (in *Installer) Update(ctx context.Context, req UpdateRequest) (UpdateOutcome, error) {
	if in == nil || in.Ledger == nil {
		return UpdateOutcome{}, fmt.Errorf("installer: a ledger is required")
	}
	entry := req.Entry
	if entry.DestDir == "" {
		return UpdateOutcome{}, fmt.Errorf("installer: update entry has no destination")
	}
	if req.NewDir == "" {
		return UpdateOutcome{}, fmt.Errorf("installer: update requires NewDir (the caller must fetch the source)")
	}
	if err := requireSkillFile(req.NewDir); err != nil {
		return UpdateOutcome{Entry: entry}, err
	}

	info, err := os.Lstat(entry.DestDir)
	if err != nil {
		return UpdateOutcome{Entry: entry}, fmt.Errorf("destination %s is not installed: %w", entry.DestDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return UpdateOutcome{Entry: entry}, fmt.Errorf("refusing to update symlinked directory %s", entry.DestDir)
	}
	if !info.IsDir() {
		return UpdateOutcome{Entry: entry}, fmt.Errorf("destination %s is not a directory", entry.DestDir)
	}

	newProv, err := CaptureProvenance(req.NewDir)
	if err != nil {
		return UpdateOutcome{Entry: entry}, fmt.Errorf("capturing provenance for %s: %w", req.NewDir, err)
	}
	if entry.ContentDigest != "" && entry.ContentDigest == newProv.Digest {
		return UpdateOutcome{
			Entry:          entry,
			AlreadyCurrent: true,
			Reason:         "already current: content digest matches the installed copy",
		}, nil
	}

	// Replacing the tree deletes the old one, so apply the same inventory guard
	// as Remove: never destroy files the user added unless Force is set.
	if reason := inventoryGuardReason(entry.DestDir, entry.Inventory, req.Force); reason != "" {
		return UpdateOutcome{Entry: entry}, fmt.Errorf("refusing to update %s: %s", entry.DestDir, reason)
	}

	src := SkillSource{Display: req.NewSource, Pin: req.NewRef}
	if src.Display == "" {
		src.Display = entry.Source
	}
	if err := in.checkPolicy(ctx, "update", src, entry.SkillName, entry.Scope, entry.DestDir); err != nil {
		return UpdateOutcome{Entry: entry}, err
	}

	if req.DryRun {
		return UpdateOutcome{
			Entry:       entry,
			WouldUpdate: true,
			Reason:      fmt.Sprintf("would update: digest %s -> %s", shortDigest(entry.ContentDigest), shortDigest(newProv.Digest)),
		}, nil
	}

	oldDigest := entry.ContentDigest
	backup, err := swapDir(req.NewDir, entry.DestDir)
	if err != nil {
		return UpdateOutcome{Entry: entry}, err
	}

	updated := entry
	updated.ContentDigest = newProv.Digest
	updated.Inventory = newProv.Inventory
	if req.NewSource != "" {
		updated.Source = req.NewSource
	}
	if ref := src.RecordedRef(""); ref != "" {
		updated.SourceRef = ref
	}

	if err := in.Ledger.ReplaceEntry(entry.DestDir, updated); err != nil {
		// Roll the filesystem back so the ledger and disk stay consistent.
		_ = os.RemoveAll(entry.DestDir)
		if rerr := os.Rename(backup, entry.DestDir); rerr != nil {
			return UpdateOutcome{Entry: entry}, fmt.Errorf("ledger update failed (%v) and rollback failed (%v); previous copy left at %s", err, rerr, backup)
		}
		return UpdateOutcome{Entry: entry}, err
	}
	if err := os.RemoveAll(backup); err != nil {
		return UpdateOutcome{
			Entry:   updated,
			Updated: true,
			Reason:  fmt.Sprintf("updated; previous copy could not be removed from %s: %v", backup, err),
		}, nil
	}
	return UpdateOutcome{
		Entry:   updated,
		Updated: true,
		Reason:  fmt.Sprintf("updated: digest %s -> %s", shortDigest(oldDigest), shortDigest(newProv.Digest)),
	}, nil
}

func (in *Installer) checkPolicy(ctx context.Context, operation string, src SkillSource, skillName, scope, destDir string) error {
	if in.Policy == nil {
		return nil
	}
	effects := []string{"filesystem.write"}
	if operation == "update" {
		effects = append(effects, "filesystem.delete")
	}
	req := PolicyRequest{
		Operation: operation,
		SkillName: skillName,
		Source:    src.Display,
		Scope:     scope,
		DestDir:   destDir,
		Effects:   effects,
	}
	verdict := in.Policy(ctx, req)
	if !verdict.Allowed {
		return &PolicyDeniedError{Request: req, Verdict: verdict}
	}
	return nil
}

// requireSkillFile ensures dir holds a SKILL.md (or skill.md) before it is
// installed or used as an update source.
func requireSkillFile(dir string) error {
	for _, name := range []string{"SKILL.md", "skill.md"} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf("no SKILL.md in %s", dir)
}

// swapDir atomically replaces dest with a copy of newDir and returns the path
// of the previous directory, which is moved aside rather than deleted. The
// caller removes that backup only after the ledger is updated; a failure at any
// step restores the previous install.
func swapDir(newDir, dest string) (backup string, err error) {
	parent := filepath.Dir(dest)
	base := filepath.Base(dest)
	stamp := time.Now().UnixNano()
	tmp := filepath.Join(parent, fmt.Sprintf(".%s.litespm-new-%d", base, stamp))
	backup = filepath.Join(parent, fmt.Sprintf(".%s.litespm-old-%d", base, stamp))

	if err := CopySkillDir(newDir, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(dest, backup); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("moving current install aside: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		if rerr := os.Rename(backup, dest); rerr != nil {
			return "", fmt.Errorf("installing update (%v); rollback failed (%v); previous install left at %s", err, rerr, backup)
		}
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("installing update: %w", err)
	}
	return backup, nil
}

func shortDigest(d string) string {
	if d == "" {
		return "(none)"
	}
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
