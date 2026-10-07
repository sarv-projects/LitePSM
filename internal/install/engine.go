package install

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sarv-projects/litespm/internal/artifact"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/fslock"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/state"
)

// ArchiveSourceFunc retrieves an archive reader and format for a listing version.
type ArchiveSourceFunc func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error)

// TreeSourceFunc provides pre-extracted directory contents into staging.
type TreeSourceFunc func(ctx context.Context, targetStagingDir string) (*domain.ExtractedTreeInfo, error)

// InstallOptions holds parameters for an installation operation.
type InstallOptions struct {
	ListingID   string
	Version     string
	Scope       domain.InstallScope
	WorkspaceID string
	ProjectRoot string
	ApprovalID  string
	// Kind is the install kind recorded in installs.kind. Empty means mcp.
	Kind domain.ListingKind
	// PlanID, when set, binds this operation to a stored install plan: the
	// plan is re-verified (hash + expiry) before any work happens and its
	// request fields are used to fill or cross-check the fields above.
	PlanID        string
	ArchiveSource ArchiveSourceFunc
	TreeSource    TreeSourceFunc
	Limits        *artifact.ExtractionLimits
}

// planBinding is the verified outcome of loading a stored install plan.
type planBinding struct {
	plan *domain.InstallPlan
	// expectedArchiveDigest is the single artifact digest the plan declares
	// ("" when the plan declares none and nothing can be cross-checked).
	expectedArchiveDigest string
}

// Engine coordinates artifact acquisition, CAS placement, and SQLite transactional commits.
type Engine struct {
	db          *state.DB
	policy      *policy.Engine
	casRoot     string
	stagingRoot string
	// casLocks provides E6 single-flight per tree digest: concurrent installs
	// of the same content serialize on the digest so the stat-then-promote
	// sequence cannot race (two winners promoting different bytes under one
	// digest, or both deleting a shared tree on rollback).
	casMu    sync.Mutex
	casLocks map[string]*sync.Mutex
}

// NewEngine creates a new installation engine.
func NewEngine(db *state.DB, casRoot, stagingRoot string) (*Engine, error) {
	if err := os.MkdirAll(casRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create CAS root %s: %w", casRoot, err)
	}
	if err := os.MkdirAll(stagingRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create staging root %s: %w", stagingRoot, err)
	}

	return &Engine{
		db:          db,
		casRoot:     filepath.Clean(casRoot),
		stagingRoot: filepath.Clean(stagingRoot),
		casLocks:    map[string]*sync.Mutex{},
	}, nil
}

// casLockTimeout bounds how long an install waits for another process that is
// promoting or removing the same tree.
const casLockTimeout = 2 * time.Minute

// lockDigest serializes every promote/verify/remove of one CAS tree digest
// (E6 single-flight): first an in-process mutex, then a cross-process lock file
// so a CLI install and the daemon cannot race either. The returned func
// releases both; it is safe to call exactly once.
func (e *Engine) lockDigest(treeDigest string) (func(), error) {
	e.casMu.Lock()
	m, ok := e.casLocks[treeDigest]
	if !ok {
		m = &sync.Mutex{}
		e.casLocks[treeDigest] = m
	}
	e.casMu.Unlock()
	m.Lock()

	name := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(treeDigest)
	fl, err := fslock.Acquire(filepath.Join(e.casRoot, "locks", name+".lock"), fslock.Options{Timeout: casLockTimeout})
	if err != nil {
		m.Unlock()
		return nil, fmt.Errorf("lock CAS tree %s: %w", treeDigest, err)
	}
	return func() {
		_ = fl.Release()
		m.Unlock()
	}, nil
}

// SetPolicy attaches a policy engine for consent and security evaluation during installation.
func (e *Engine) SetPolicy(p *policy.Engine) {
	e.policy = p
}

// step records a journal step for an operation. Journal telemetry is
// best-effort: the state transitions themselves are the authoritative record
// and still fail the operation when they cannot be written. status must
// satisfy the operation_steps schema CHECK (pending, running, done, failed);
// there is no "skipped" value, so steps that did not happen are represented
// by their absence (or by plan_id/artifacts in the persisted records), never
// by an insert the schema would reject.
func (e *Engine) step(ctx context.Context, opID, name, status, detail string) {
	_ = e.db.RecordOperationStep(ctx, opID, name, status, detail)
}

// loadPlan loads the stored plan referenced by opts.PlanID, re-verifies its
// hash and expiry, and fills in request fields the caller left empty. A
// caller-supplied field that disagrees with the plan is rejected rather than
// silently accepted. Returns (nil, nil) when opts.PlanID is empty.
func (e *Engine) loadPlan(ctx context.Context, opts *InstallOptions) (*planBinding, error) {
	if opts.PlanID == "" {
		return nil, nil
	}

	plan, err := e.db.GetPlan(ctx, opts.PlanID)
	if err != nil {
		return nil, err // domain.ErrNotFound("plan", ...) propagates unchanged
	}
	if plan.PlanID != opts.PlanID {
		return nil, domain.ErrPlanStale(opts.PlanID, "stored plan document declares a different planId")
	}

	computed, err := domain.ComputePlanHash(plan)
	if err != nil {
		return nil, domain.ErrInternal("failed to recompute plan hash", err)
	}
	if plan.PlanHash == "" || plan.PlanHash != computed {
		return nil, domain.ErrPlanStale(opts.PlanID,
			fmt.Sprintf("stored planHash %q does not match recomputed %q", plan.PlanHash, computed))
	}
	if !time.Now().UTC().Before(plan.ExpiresAt.UTC()) {
		return nil, domain.ErrPlanExpired(opts.PlanID)
	}

	if opts.ListingID == "" {
		opts.ListingID = plan.Request.ListingID
	} else if opts.ListingID != plan.Request.ListingID {
		return nil, domain.ErrStateConflict(fmt.Sprintf(
			"requested listing %q does not match plan %s listing %q", opts.ListingID, opts.PlanID, plan.Request.ListingID))
	}

	planVersion := plan.Request.RequestedVersion
	if planVersion == "" {
		planVersion = "latest"
	}
	switch {
	case opts.Version == "":
		// Record the concrete resolved version, not the possibly symbolic
		// requested one ("latest").
		if plan.Resolved.Version != "" {
			opts.Version = plan.Resolved.Version
		} else {
			opts.Version = planVersion
		}
	case opts.Version != planVersion && opts.Version != plan.Resolved.Version:
		return nil, domain.ErrStateConflict(fmt.Sprintf(
			"requested version %q matches neither plan %s requested version %q nor resolved version %q",
			opts.Version, opts.PlanID, planVersion, plan.Resolved.Version))
	}

	if opts.Scope == "" {
		opts.Scope = plan.Request.TargetScope
		if opts.Scope == "" {
			opts.Scope = domain.ScopeUser
		}
	} else if plan.Request.TargetScope != "" && opts.Scope != plan.Request.TargetScope {
		return nil, domain.ErrStateConflict(fmt.Sprintf(
			"requested scope %q does not match plan %s target scope %q", opts.Scope, opts.PlanID, plan.Request.TargetScope))
	}

	var withDigest []string
	for _, a := range plan.Resolved.Artifacts {
		if a.Digest != "" {
			withDigest = append(withDigest, a.Digest)
		}
	}
	if len(withDigest) > 1 {
		return nil, domain.ErrStateConflict(fmt.Sprintf(
			"plan %s declares %d artifact digests; the install engine verifies at most one archive per operation",
			opts.PlanID, len(withDigest)))
	}

	binding := &planBinding{plan: plan}
	if len(withDigest) == 1 {
		binding.expectedArchiveDigest = withDigest[0]
	}
	return binding, nil
}

// Execute performs safe installation with atomic CAS promotion and safe rollback.
//
// The journal walk for every operation is:
//
//	created -> resolving -> [awaiting_approval -> approved] -> fetching ->
//	staging -> commit_intent -> verified -> committing -> committed
//
// The approval states are only entered when an approval id was actually
// supplied and consumed; a rollback at any point moves the operation to
// rolled_back after removing only this operation's staging directory and CAS
// trees.
func (e *Engine) Execute(ctx context.Context, opts InstallOptions) (*domain.InstallRecord, error) {
	var consumed string
	rec, err := e.execute(ctx, opts, &consumed)
	if err != nil && consumed != "" {
		// The approval was spent but the install failed (and was rolled back)
		// before any effect was committed: give the approval back so a
		// transient fetch/extract failure does not burn the user's consent.
		if rerr := e.db.RefundApproval(context.WithoutCancel(ctx), consumed); rerr != nil {
			err = fmt.Errorf("%w (approval %s could not be refunded: %v)", err, consumed, rerr)
		}
	}
	return rec, err
}

func (e *Engine) execute(ctx context.Context, opts InstallOptions, consumed *string) (*domain.InstallRecord, error) {
	binding, err := e.loadPlan(ctx, &opts)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(opts.ListingID) == "" {
		return nil, domain.ErrInvalidIdentifier(opts.ListingID, "valid non-empty listing id")
	}
	if strings.Contains(opts.ListingID, "..") || strings.Contains(opts.ListingID, "/") || strings.Contains(opts.ListingID, "\\") {
		return nil, domain.ErrInvalidIdentifier(opts.ListingID, "listing ID must not contain path traversal characters ('..', '/', '\\')")
	}
	if strings.TrimSpace(opts.Version) == "" {
		opts.Version = "latest"
	}
	if opts.Scope == "" {
		opts.Scope = domain.ScopeUser
	}
	// Refuse before journaling an operation that could never do any work.
	if opts.TreeSource == nil && opts.ArchiveSource == nil {
		return nil, domain.ErrInternal("no artifact or tree source provided for installation", nil)
	}

	// Policy evaluation: enforce security tiers and human approval requirements
	if e.policy != nil {
		decision := e.policy.Evaluate(ctx, policy.PolicyInput{
			Actor:     "user",
			Operation: "install",
			TargetRef: opts.ListingID,
			Scope:     opts.Scope,
			Effects: []policy.EffectDeclaration{
				{
					Effect: policy.EffectPackageInstall,
					Target: opts.ListingID,
				},
			},
		})
		if decision.Decision == policy.DecisionDeny {
			return nil, domain.ErrUnauthorized("package.install", fmt.Sprintf("installation denied by policy: %s (%v)", decision.Detail, decision.ReasonCodes))
		}
		if decision.Decision == policy.DecisionAsk && opts.ApprovalID == "" {
			return nil, domain.ErrUnauthorized("package.install", "human approval required before installation")
		}
	}

	limits := artifact.DefaultExtractionLimits
	if opts.Limits != nil {
		limits = *opts.Limits
	}

	sum := sha256.Sum256([]byte(opts.ListingID + opts.Version))
	// The random suffix keeps ids unique when two installs of the same
	// listing start within one clock tick (coarse timers, concurrent callers).
	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, domain.ErrInternal("failed to generate operation id", err)
	}
	opID := fmt.Sprintf("op_%d_%x_%x", time.Now().UnixNano(), sum[:4], salt[:])
	stagingDir := filepath.Join(e.stagingRoot, opID)

	// Step 1: Initialize journal entry (state "created"), bound to the plan when one supplied.
	var planIDArg *string
	if binding != nil {
		planIDArg = &binding.plan.PlanID
	}
	if err := e.db.CreateOperation(ctx, opID, planIDArg, "install", ""); err != nil {
		return nil, fmt.Errorf("failed to initialize operation %s: %w", opID, err)
	}

	// Step 2: state "resolving"; the plan (if any) was resolved and verified above.
	if err := e.db.AdvanceOperationState(ctx, opID, "resolving"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}
	if binding != nil {
		e.step(ctx, opID, "plan_verified", "done",
			fmt.Sprintf("plan %s hash and expiry verified", binding.plan.PlanID))
	}
	// Without a plan no plan_verified row is written: the schema admits no
	// "skipped" status, and operations.plan_id IS NULL already records that
	// nothing was verified.

	// Step 3: Enforce approval consumption if required. The operation only
	// enters the approval states when an approval was actually presented.
	if opts.ApprovalID != "" {
		if err := e.db.AdvanceOperationState(ctx, opID, "awaiting_approval"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		// The approval is bound to the plan it was granted for. Without a
		// plan, it is bound to the (listing, version, scope) being installed.
		subjectHash := installSubjectHash(opts.ListingID, opts.Version, opts.Scope)
		if binding != nil {
			subjectHash = binding.plan.PlanHash
		}
		if err := e.db.ConsumeApproval(ctx, opts.ApprovalID, "install-plan", subjectHash); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		*consumed = opts.ApprovalID
		if err := e.db.AdvanceOperationState(ctx, opID, "approved"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		e.step(ctx, opID, "approval", "done", fmt.Sprintf("consumed approval %s", opts.ApprovalID))
	}

	// Step 4: Fetch & Stage artifact
	if err := e.db.AdvanceOperationState(ctx, opID, "fetching"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("failed to create staging dir %s: %w", stagingDir, err)
	}

	extractedDir := filepath.Join(stagingDir, "extracted")

	var treeInfo *domain.ExtractedTreeInfo
	var extractErr error

	if opts.TreeSource != nil {
		if binding != nil && binding.expectedArchiveDigest != "" {
			_ = e.Rollback(ctx, opID)
			return nil, domain.ErrStateConflict(fmt.Sprintf(
				"plan %s declares artifact digest %s but no archive is fetched for a tree source; the digest cannot be verified",
				binding.plan.PlanID, binding.expectedArchiveDigest))
		}
		if err := e.db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		treeInfo, extractErr = opts.TreeSource(ctx, extractedDir)
	} else if opts.ArchiveSource != nil {
		rc, format, err := opts.ArchiveSource(ctx, opts.ListingID, opts.Version)
		if err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to fetch archive source: %w", err)
		}
		defer rc.Close()

		archivePath := filepath.Join(stagingDir, "source.archive")
		archiveFile, err := os.Create(archivePath)
		if err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to create staging archive file: %w", err)
		}

		_, spoolDigest, spoolErr := artifact.SpoolDownloadBounded(rc, limits.MaxArchiveSize, archiveFile)
		archiveFile.Close()
		if spoolErr != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed during bounded spooling: %w", spoolErr)
		}

		if binding != nil && binding.expectedArchiveDigest != "" {
			if spoolDigest != binding.expectedArchiveDigest {
				_ = e.Rollback(ctx, opID)
				return nil, domain.ErrChecksumMismatch(binding.expectedArchiveDigest, spoolDigest)
			}
			e.step(ctx, opID, "archive_digest_verified", "done",
				fmt.Sprintf("spooled archive digest %s matches plan artifact", spoolDigest))
		}
		// A plan that declares no artifact digest writes no digest row: the
		// hash-sealed plan document itself records the empty artifacts list.

		if err := e.db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}

		treeInfo, extractErr = artifact.ExtractFileSafely(archivePath, format, extractedDir, limits)
	} else {
		_ = e.Rollback(ctx, opID)
		return nil, domain.ErrInternal("no artifact or tree source provided for installation", nil)
	}

	if extractErr != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("safe extraction failed: %w", extractErr)
	}
	if treeInfo == nil {
		_ = e.Rollback(ctx, opID)
		return nil, domain.ErrInternal("extraction produced no tree metadata", nil)
	}

	return e.promoteAndCommit(ctx, opID, stagingDir, extractedDir, treeInfo, opts)
}

// promoteAndCommit runs steps 5-7: CAS placement, verification, and the
// metadata commit. It holds the tree's single-flight lock for the whole span so
// a concurrent install of the same digest either waits and reuses a verified
// tree, or runs after this one has finished; a concurrent rollback cannot
// delete the tree between our promote and our commit.
func (e *Engine) promoteAndCommit(ctx context.Context, opID, stagingDir, extractedDir string, treeInfo *domain.ExtractedTreeInfo, opts InstallOptions) (*domain.InstallRecord, error) {
	// Step 5: CAS Store Placement & Deduplication
	if err := e.db.AdvanceOperationState(ctx, opID, "commit_intent"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	casTreePath, err := e.TreePath(treeInfo.TreeDigest)
	if err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	unlock, err := e.lockDigest(treeInfo.TreeDigest)
	if err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}
	locked := true
	release := func() {
		if locked {
			locked = false
			unlock()
		}
	}
	defer release()
	// fail rolls back while holding the digest lock (rollbackLocked skips
	// re-locking the held digest).
	fail := func(err error) (*domain.InstallRecord, error) {
		_ = e.rollback(ctx, opID, treeInfo.TreeDigest)
		return nil, err
	}

	createdByOp := false
	existing := false
	if _, statErr := os.Stat(casTreePath); statErr == nil {
		existing = true
	} else if !os.IsNotExist(statErr) {
		return fail(fmt.Errorf("stat CAS tree %s: %w", casTreePath, statErr))
	}

	if existing {
		// Never trust bytes that merely share a name: re-hash the reused tree.
		// A tree that no longer matches its digest is quarantined (moved aside,
		// not deleted) and replaced by the freshly extracted one.
		actual, derr := artifact.ComputeCanonicalTreeDigest(casTreePath)
		if derr == nil && actual == treeInfo.TreeDigest {
			if err := e.db.RecordOperationTree(ctx, opID, treeInfo.TreeDigest, false); err != nil {
				return fail(err)
			}
			_ = os.RemoveAll(stagingDir)
		} else {
			qpath, qerr := e.quarantineTree(treeInfo.TreeDigest, casTreePath)
			if qerr != nil {
				return fail(fmt.Errorf("CAS tree %s is corrupt and could not be quarantined: %w", treeInfo.TreeDigest, qerr))
			}
			reason := "digest mismatch"
			if derr != nil {
				reason = derr.Error()
			}
			e.step(ctx, opID, "cas_quarantine", "done",
				fmt.Sprintf("existing tree %s failed verification (%s); moved to %s", treeInfo.TreeDigest, reason, qpath))
			existing = false
		}
	}

	if !existing {
		// New (or replaced-after-quarantine) tree: place it atomically and mark
		// created_by_op only if this call really created the directory. A rename
		// that loses to a concurrent promoter (cross-process, lock-less writer)
		// reports created=false so a later rollback never deletes the winner's tree.
		if err := os.MkdirAll(filepath.Dir(casTreePath), 0700); err != nil {
			return fail(fmt.Errorf("failed to create CAS tree parent dir: %w", err))
		}
		created, perr := promoteTree(extractedDir, casTreePath)
		if perr != nil {
			return fail(fmt.Errorf("failed to promote tree to CAS store %s: %w", casTreePath, perr))
		}
		if err := e.db.RecordOperationTree(ctx, opID, treeInfo.TreeDigest, created); err != nil {
			if created {
				// The journal does not know about this tree, so rollback will
				// not remove it: remove it here rather than orphan it.
				_ = os.RemoveAll(casTreePath)
			}
			return fail(err)
		}
		createdByOp = created
		_ = os.RemoveAll(stagingDir)
	}

	// Step 6: verify the bytes that actually landed in the CAS store before
	// any metadata is committed ("verified" state).
	actualDigest, err := artifact.ComputeCanonicalTreeDigest(casTreePath)
	if err != nil {
		return fail(fmt.Errorf("failed to recompute tree digest for verification: %w", err))
	}
	if actualDigest != treeInfo.TreeDigest {
		return fail(domain.ErrChecksumMismatch(treeInfo.TreeDigest, actualDigest))
	}
	if err := e.db.AdvanceOperationState(ctx, opID, "verified"); err != nil {
		return fail(err)
	}
	e.step(ctx, opID, "tree_verified", "done",
		fmt.Sprintf("recomputed CAS digest %s matches staged tree (created_by_op=%t)", actualDigest, createdByOp))

	// Step 7: Commit metadata to SQLite
	if err := e.db.AdvanceOperationState(ctx, opID, "committing"); err != nil {
		return fail(err)
	}

	digestShort := strings.TrimPrefix(treeInfo.TreeDigest, "sha256:")
	if len(digestShort) > 8 {
		digestShort = digestShort[:8]
	}
	safeListing := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, opts.ListingID)
	installID := fmt.Sprintf("inst_%s_%s_%s", opts.Scope, safeListing, digestShort)

	kind := opts.Kind
	if kind == "" {
		kind = domain.KindMCP
	}
	compKind := domain.ComponentMCPProvider
	if kind == domain.KindSkill {
		compKind = domain.ComponentSkill
	}

	now := time.Now().UTC()
	installRec := &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   opts.ListingID,
		Kind:        kind,
		Version:     opts.Version,
		TreeDigest:  treeInfo.TreeDigest,
		InstallPath: casTreePath,
		Scope:       opts.Scope,
		WorkspaceID: opts.WorkspaceID,
		ProjectRoot: opts.ProjectRoot,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}

	// Main component record
	compRec := &domain.InstallComponentRecord{
		InstallID:     installID,
		Kind:          compKind,
		ComponentName: "default",
		Path:          casTreePath,
	}

	// Install record, component record, and the journal transition to
	// "committed" are written in one transaction: a crash leaves either no
	// install row (recovery rolls back) or a complete one (recovery finalizes).
	if err := e.db.CommitInstallOperation(ctx, installRec, compRec, opID); err != nil {
		return fail(fmt.Errorf("failed to commit install metadata: %w", err))
	}

	return installRec, nil
}

// quarantineTree moves a corrupt CAS tree aside under casRoot/quarantine so the
// evidence survives and the digest path is free for a verified tree. It never
// deletes the corrupt tree.
func (e *Engine) quarantineTree(treeDigest, treePath string) (string, error) {
	dir := filepath.Join(e.casRoot, "quarantine")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(treeDigest)
	dest := filepath.Join(dir, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()))
	if err := os.Rename(treePath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// promoteTree moves src to dst without ever merging into an existing dst.
// created reports whether dst was created by this call; false means dst already
// existed (this caller lost a promotion race) and src is left for the caller to
// discard.
func promoteTree(src, dst string) (created bool, err error) {
	if err := os.Rename(src, dst); err == nil {
		return true, nil
	}
	if _, statErr := os.Lstat(dst); statErr == nil {
		return false, nil
	}
	// Cross-device or similar: copy beside the destination, then rename into
	// place so dst never exists half-populated.
	tmp := fmt.Sprintf("%s.tmp-%d", dst, time.Now().UnixNano())
	if err := moveOrCopyDir(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return false, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		if _, statErr := os.Lstat(dst); statErr == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// TreePath computes the local filesystem path for a given Merkle tree digest.
func (e *Engine) TreePath(treeDigest string) (string, error) {
	parts := strings.SplitN(treeDigest, ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" || len(parts[1]) != 64 {
		return "", fmt.Errorf("invalid tree digest format %q: expected sha256:<64-hex>", treeDigest)
	}
	for _, c := range parts[1] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("invalid hex in tree digest %q", treeDigest)
		}
	}
	return filepath.Join(e.casRoot, "trees", parts[0], parts[1]), nil
}

// Rollback safely reverses an incomplete or failed operation.
//
// It touches only this operation's state: the operation's own staging
// directory and CAS trees with operation_trees.created_by_op = 1. Shared
// pre-existing trees and other operations' staging are never removed.
//
// The journal moves created→…→rolling_back first, so a rollback that dies
// half-way leaves the operation in "rolling_back" — a state startup recovery
// finishes — instead of pretending the cleanup already succeeded.
func (e *Engine) Rollback(ctx context.Context, opID string) error {
	return e.rollback(ctx, opID, "")
}

// rollback is Rollback with one digest the caller already holds the
// single-flight lock for (heldDigest, "" when none), so it is not re-locked.
//
// A tree is removed only when this operation created it AND nothing else
// depends on it: an install row referencing the digest, or another live
// operation that reused it, keeps the tree (a stale created_by_op=1 can never
// delete bytes someone else committed against).
func (e *Engine) rollback(ctx context.Context, opID, heldDigest string) error {
	if err := e.db.AdvanceOperationState(ctx, opID, "rolling_back"); err != nil {
		return err
	}

	stagingDir := filepath.Join(e.stagingRoot, opID)
	if err := os.RemoveAll(stagingDir); err != nil {
		return fmt.Errorf("failed to remove staging directory %s during rollback: %w", stagingDir, err)
	}

	trees, err := e.db.GetOperationTrees(ctx, opID)
	if err != nil {
		return err
	}
	var removed, kept []string
	for _, t := range trees {
		if !t.CreatedByOp {
			continue
		}
		path, pathErr := e.TreePath(t.TreeDigest)
		if pathErr != nil {
			return pathErr
		}
		didRemove, rmErr := e.removeCreatedTree(ctx, opID, t.TreeDigest, path, heldDigest)
		if rmErr != nil {
			return fmt.Errorf("failed to remove tree %s created by operation %s: %w", t.TreeDigest, opID, rmErr)
		}
		if didRemove {
			removed = append(removed, t.TreeDigest)
		} else {
			kept = append(kept, t.TreeDigest)
		}
	}

	if err := e.db.AdvanceOperationState(ctx, opID, "rolled_back"); err != nil {
		return err
	}
	detail := fmt.Sprintf("removed staging and %d tree(s) created by this operation", len(removed))
	if len(removed) > 0 {
		detail = fmt.Sprintf("removed staging and trees %s", strings.Join(removed, ", "))
	}
	if len(kept) > 0 {
		detail += fmt.Sprintf("; kept in-use tree(s) %s", strings.Join(kept, ", "))
	}
	_ = e.db.RecordOperationStep(ctx, opID, "rollback", "done", detail)
	return nil
}

func (e *Engine) removeCreatedTree(ctx context.Context, opID, digest, path, heldDigest string) (bool, error) {
	if digest != heldDigest {
		unlock, err := e.lockDigest(digest)
		if err != nil {
			return false, err
		}
		defer unlock()
	}
	inUse, err := e.db.TreeInUse(ctx, opID, digest)
	if err != nil {
		return false, err
	}
	if inUse {
		return false, nil
	}
	if err := os.RemoveAll(path); err != nil {
		return false, err
	}
	return true, nil
}

func moveOrCopyDir(src, dst string) error {
	// Try fast atomic rename first
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback to recursive copy if cross-device link
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := moveOrCopyDir(s, d); err != nil {
				return err
			}
		} else {
			in, err := os.Open(s)
			if err != nil {
				return err
			}
			out, err := os.Create(d)
			if err != nil {
				in.Close()
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				in.Close()
				out.Close()
				return err
			}
			in.Close()
			out.Close()
		}
	}

	return os.RemoveAll(src)
}

// installSubjectHash is the approval subject for an install that has no
// persisted plan: a digest of exactly what is being installed.
func installSubjectHash(listingID, version string, scope domain.InstallScope) string {
	sum := sha256.Sum256([]byte("install\x00" + listingID + "\x00" + version + "\x00" + string(scope)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// InstallSubjectHash exposes the plan-less approval subject so a human
// approval flow can bind to it.
func InstallSubjectHash(listingID, version string, scope domain.InstallScope) string {
	return installSubjectHash(listingID, version, scope)
}
