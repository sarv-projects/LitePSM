package main

// install_skill.go routes catalog skill listings onto the same ledger- and
// policy-gated install path as `litespm skills add`.
//
// Skills are files, not package archives: there is no artifact to spool or
// CAS tree to stage, so the archive install engine has no role for them. The
// agent-facing marketplace install therefore branches by kind instead of
// packaging a synthetic archive, which is what the previous code did — it
// reported "Installed" for a locally generated SKILL.md that no upstream ever
// published.
//
// Kinds without an install path (MCP, plugin) fail closed with
// LPSM-ARTIFACT-UNAVAILABLE: the catalog does not carry artifact locators for
// them yet, and inventing a source would be the same lie in a new place.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// skillInstallOutcome is the honest result of a skill install: every
// destination directory that was written, the resolved source and revision,
// and the content digest recorded in the ledger.
type skillInstallOutcome struct {
	InstallID     string
	SkillName     string
	ListingID     string
	Version       string
	Scope         domain.InstallScope
	Source        string
	SourceRef     string
	ContentDigest string
	Destinations  []string
	LedgerEntries []skills.LedgerEntry
}

// installSkillFromListing installs one catalog skill listing. It fetches the
// source, discovers the skill (honouring a browse-URL subpath), plans copies
// into the selected agents' skill directories, writes them through the ledger,
// and records an InstallRecord so `list_installed` can see it.
//
// projectRoot and home are explicit so the target directories are computable
// and testable rather than read from the ambient process state here.
func installSkillFromListing(
	ctx context.Context,
	db *state.DB,
	dataRoot, projectRoot, home string,
	listing *domain.Listing,
	version string,
	scope domain.InstallScope,
	agents []string,
) (*skillInstallOutcome, error) {
	if listing.Kind != domain.KindSkill {
		return nil, fmt.Errorf("installSkillFromListing: %s is kind %q, not skill", listing.ID, listing.Kind)
	}
	// Installability gate (Phase 0.1/T3): discovery-only rows are searchable
	// metadata with no verified source. Refuse rather than fetching an
	// unproven URL.
	if !listing.IsInstallable() {
		return nil, domain.ErrNotInstallable(listing.ID,
			"this listing is discovery-only metadata with no verified source; no installable artifact was proven")
	}
	// Authorization gate: a skill install writes into agent trees, so it runs
	// only under a consumed approval for a recorded plan (see
	// install_authz.go). Checked before any network fetch.
	auth, authorized := installAuthorizationFrom(ctx)
	if !authorized {
		return nil, domain.ErrUnauthorized("skill.install",
			"no recorded plan and approval authorize this install; run `litespm install` or have a human approve the plan with `litespm approve <plan-id>`")
	}
	sourceURL := strings.TrimSpace(listing.Source.URL)
	if sourceURL == "" {
		return nil, domain.ErrArtifactUnavailable(listing.ID, "the listing carries no skill source URL")
	}

	src, err := skills.ParseSkillSource(sourceURL)
	if err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			fmt.Sprintf("skill source %q is not a usable repository reference: %v", sourceURL, err))
	}
	if !src.Git && src.Kind != "local" {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			fmt.Sprintf("skill source %q is not a git repository, so it cannot be fetched", sourceURL))
	}

	workRoot, cleanup, err := fetchSkillSourceTree(src)
	if err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID, err.Error())
	}
	defer cleanup()

	observedRef := ""
	if src.Kind != "local" {
		observedRef = gitHeadCommit(workRoot)
	}

	scanRoot := workRoot
	if src.Subpath != "" {
		scanRoot = filepath.Join(workRoot, filepath.FromSlash(src.Subpath))
	}
	found, err := discoverSkillsIncludingRoot(scanRoot)
	if err != nil {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			fmt.Sprintf("scanning %s: %v", src.Display, err))
	}
	if len(found) == 0 {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			fmt.Sprintf("no installable skill (SKILL.md with name and description) found in %s", src.Display))
	}

	selected, err := selectSkillForListing(found, listing)
	if err != nil {
		return nil, err
	}

	skillScope := "project"
	if scope == domain.ScopeUser {
		skillScope = "global"
	}

	if len(agents) == 0 {
		for _, target := range skills.AgentTargets() {
			if skills.AgentInstalled(target, projectRoot, home) {
				agents = append(agents, target.ID)
			}
		}
	}
	if len(agents) == 0 {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"no agent skill directories were detected on this machine; nothing to install into")
	}

	ops := skills.PlanInstall([]skills.DiscoveredSkill{selected}, agents, skillScope, projectRoot, home)
	if len(ops) == 0 {
		return nil, domain.ErrArtifactUnavailable(listing.ID,
			"none of the selected agent hosts has a skill directory for this scope")
	}

	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		return nil, fmt.Errorf("open skill ledger: %w", err)
	}

	// The policy engine runs with its real tiers (capability grants from the
	// state database, the user's deny rules). Its "ask" is answered only by the
	// approval that was consumed for this install; there is no
	// "the request itself is the authorization" shortcut.
	cfg, _ := config.LoadCurrentConfig()
	engine, perr := newPolicyEngine(db, dataRoot, policyDefaultsFrom(cfg))
	if perr != nil {
		return nil, fmt.Errorf("load policy: %w", perr)
	}
	checker := enginePolicyChecker(engine, func(askCtx context.Context, _ skills.PolicyRequest, detail string) bool {
		got, ok := installAuthorizationFrom(askCtx)
		if !ok || got.ApprovalID != auth.ApprovalID {
			fmt.Fprintf(os.Stderr, "Policy: skill writes require approval (%s); no consumed approval for this install.\n", detail)
			return false
		}
		return true
	})
	installer := skills.NewInstaller(ledger, checker)

	outcome := &skillInstallOutcome{
		ListingID: listing.ID,
		Version:   version,
		Scope:     scope,
		Source:    src.Display,
		SourceRef: src.RecordedRef(observedRef),
	}
	for _, op := range ops {
		entry, err := installer.Install(ctx, skills.InstallRequest{
			Op:          op,
			Source:      src,
			Scope:       skillScope,
			ObservedRef: observedRef,
		})
		if err != nil {
			return nil, fmt.Errorf("installing skill %s into %s: %w", op.SkillName, op.ToDir, err)
		}
		outcome.LedgerEntries = append(outcome.LedgerEntries, entry)
		outcome.Destinations = append(outcome.Destinations, entry.DestDir)
	}
	if len(outcome.LedgerEntries) > 0 {
		last := outcome.LedgerEntries[len(outcome.LedgerEntries)-1]
		outcome.SkillName = last.SkillName
		outcome.ContentDigest = last.ContentDigest
		outcome.SourceRef = last.SourceRef
	}

	now := time.Now().UTC()
	outcome.InstallID = fmt.Sprintf("inst_%s_%s_%s", scope, safeInstallIDPart(listing.ID), shortDigest(outcome.ContentDigest))
	installPath := ""
	if len(outcome.Destinations) > 0 {
		installPath = outcome.Destinations[0]
	}
	rec := &domain.InstallRecord{
		InstallID:   outcome.InstallID,
		ListingID:   listing.ID,
		Kind:        domain.KindSkill,
		Version:     version,
		TreeDigest:  outcome.ContentDigest,
		InstallPath: installPath,
		Scope:       scope,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := db.SaveInstall(ctx, rec); err != nil {
		return nil, fmt.Errorf("record install %s: %w", outcome.InstallID, err)
	}
	// S2/S4: one component row with a distinct component id, plus one
	// deployment-ledger row per written skill directory so removal is
	// ledger-driven and symmetric.
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID:     outcome.InstallID,
		Kind:          domain.ComponentSkill,
		ComponentName: outcome.SkillName,
		Path:          installPath,
	}); err != nil {
		return nil, fmt.Errorf("record the installed component: %w", err)
	}
	if orch := newLifecycleOrchestrator(db); orch != nil {
		for _, dest := range outcome.Destinations {
			_ = orch.RecordHostWrite(ctx, outcome.InstallID, listing.ID,
				"skills-ledger", string(scope), dest, "skill-dir",
				"skills."+outcome.SkillName, "")
		}
	}
	return outcome, nil
}

// selectSkillForListing picks the discovered skill that matches the listing
// name. A source scoped to a single skill directory is unambiguous even when
// the catalog name differs in punctuation.
func selectSkillForListing(found []skills.DiscoveredSkill, listing *domain.Listing) (skills.DiscoveredSkill, error) {
	want, _ := skills.SanitizeSkillName(listing.Name)
	for _, f := range found {
		if name, err := skills.SanitizeSkillName(f.Pkg.Name); err == nil && name == want {
			return f, nil
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, f.Pkg.Name)
	}
	return skills.DiscoveredSkill{}, domain.ErrArtifactUnavailable(listing.ID,
		fmt.Sprintf("skill %q was not found in the source; available skills: %s", listing.Name, strings.Join(names, ", ")))
}

// safeInstallIDPart mirrors the install engine's id sanitization so both paths
// produce ids of the same shape.
func safeInstallIDPart(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// shortDigest renders the first 8 hex characters of a sha256: digest, or
// "nodigest" when none was captured.
func shortDigest(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > 8 {
		hex = hex[:8]
	}
	if hex == "" {
		return "nodigest"
	}
	return hex
}

// printSkillInstall reports a completed skill install in the same shape as the
// other lifecycle commands: what was written, where, from which source, and at
// which revision. It claims nothing beyond what the ledger recorded.
func printSkillInstall(outcome *skillInstallOutcome) {
	fmt.Printf("✓ Installed skill %s\n", outcome.SkillName)
	fmt.Printf("  • Listing ID:  %s\n", outcome.ListingID)
	fmt.Printf("  • Install ID:  %s\n", outcome.InstallID)
	fmt.Printf("  • Version:     %s\n", outcome.Version)
	fmt.Printf("  • Scope:       %s\n", outcome.Scope)
	fmt.Printf("  • Source:      %s\n", outcome.Source)
	if outcome.SourceRef != "" {
		fmt.Printf("  • Revision:    %s\n", outcome.SourceRef)
	}
	if outcome.ContentDigest != "" {
		fmt.Printf("  • Digest:      %s\n", outcome.ContentDigest)
	}
	for _, dest := range outcome.Destinations {
		fmt.Printf("  • Written to:  %s\n", dest)
	}
}
