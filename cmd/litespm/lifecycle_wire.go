package main

// lifecycle_wire.go — the cmd-side wiring of the ARCH/33 lifecycle: how the
// daemon reads the deployment ledger's three-way decision and how it undoes an
// install symmetrically.
//
// Two rules this file exists to enforce:
//
//  1. The reconcile image is always computed over the OWNED structure — the
//     entry node inside a host config, the content digest of a skill
//     directory — never over the whole file. Fingerprints in
//     internal/host are taken over the entry's canonical text for the same
//     reason: a user editing some other part of their config must not read as
//     "modified our node", or every uninstall after any sibling edit would
//     detach and leave LiteSPM's entry behind forever.
//  2. Removal goes through lifecycle.Orchestrator.RemoveInstall for every
//     install that has ledger rows. Only an install that predates the ledger
//     falls back to a best-effort strip (host_registrations for MCP) and
//     state deletion — the pre-ledger ceiling, not a shortcut.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/lifecycle"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// newLifecycleOrchestrator returns the install/remove coordinator.
func newLifecycleOrchestrator(db *state.DB) *lifecycle.Orchestrator {
	if db == nil {
		return nil
	}
	return lifecycle.New(db)
}

// hostStructureTypeFor maps a host config format onto the ARCH/33 §3
// structure type vocabulary.
func hostStructureTypeFor(hostID string) string {
	adapter, err := host.GetAdapter(hostID)
	if err != nil {
		return "json-object"
	}
	switch strings.ToLower(adapter.Descriptor().ConfigFormat) {
	case "toml":
		return "toml-table"
	case "yaml", "yml":
		return "yaml-path"
	default:
		return "json-object"
	}
}

// hostLocatorFor builds the ARCH/33 §3 in-structure address of an owned MCP
// entry (e.g. mcpServers.demo-mcp).
func hostLocatorFor(hostID, entryName string) string {
	containers := map[string]string{
		"claude-code": "mcpServers",
		"cline":       "mcpServers",
		"pi-agent":    "mcpServers",
		"pi":          "mcpServers",
		"opencode":    "mcp",
		"codex":       "mcp_servers",
		"grok":        "mcp_servers",
		"grok-build":  "mcp_servers",
	}
	if c, ok := containers[strings.ToLower(hostID)]; ok {
		return c + "." + entryName
	}
	return "mcpServers." + entryName
}

// mutationScope maps a ledger scope string onto the install-scope enum.
func mutationScope(s string) domain.InstallScope {
	if s == "project" {
		return domain.ScopeProject
	}
	return domain.ScopeUser
}

// skillLedgerScope maps a ledger scope ("user"/"project") onto the skills
// ledger's own vocabulary ("global"/"project").
func skillLedgerScope(s string) string {
	if s == "project" {
		return "project"
	}
	return "global"
}

// locatorName is the owned name inside a locator: the last dotted segment for
// a config entry (mcpServers.demo-mcp → demo-mcp), and everything after the
// fixed "skills." prefix for a skill directory (a skill name may itself
// contain dots, so TrimPrefix is used there rather than a split).
func locatorName(m deployment.Mutation) string {
	if m.StructureType == "skill-dir" {
		return strings.TrimPrefix(m.Locator, "skills.")
	}
	parts := strings.Split(m.Locator, ".")
	return parts[len(parts)-1]
}

// currentOwnedImage is the C image of the ARCH/33 §4 three-way decision:
// what the owned structure looks like on disk right now ("" = missing).
func currentOwnedImage(ctx context.Context, m deployment.Mutation) string {
	if m.StructureType == "skill-dir" {
		prov, err := skills.CaptureProvenance(m.FilePath)
		if err != nil {
			return "" // missing or unreadable directory: reported as orphan
		}
		return prov.Digest
	}
	fp, err := host.EntryFingerprintNow(ctx, m.HostID, locatorName(m), mutationScope(m.Scope))
	if err != nil {
		// The answer could not be established (unparseable config, unknown
		// host). Return the unreadable sentinel so reconcile classifies the
		// node as user-edited and never strips what cannot be proved ours.
		if fp == "" {
			return host.EntryUnreadable
		}
	}
	return fp
}

// backupDirFor is where entry removals take their pre-edit copies.
func backupDirFor(dataRoot string) string {
	if dataRoot != "" {
		return filepath.Join(dataRoot, "backups")
	}
	if paths, _ := config.ResolvePlatformPaths(); paths != nil {
		return paths.BackupsPath()
	}
	return ""
}

// removeInstalledPackage reverses one install as a single ledger-driven unit
// (ARCH/33 §5): reconcile every owned structure three-way against what is on
// disk now, strip only structures still matching what LiteSPM wrote, detach
// (keep in place) anything the user edited, and only then delete state in one
// transaction. Removing an install twice is honest not-found, never a second
// silent delete.
//
// An install with no deployment-ledger rows predates the ledger (or wrote
// nothing); it falls back to bestEffortRemove.
func removeInstalledPackage(ctx context.Context, db *state.DB, dataRoot string, installID string) error {
	if db == nil {
		return fmt.Errorf("remove %s: no state database", installID)
	}
	rec, err := db.GetInstall(ctx, installID)
	if err != nil {
		return err // LPSM-STATE-NOT-FOUND maps to the handler's invalid params
	}
	orch := newLifecycleOrchestrator(db)
	if orch == nil {
		return db.DeleteInstall(ctx, installID)
	}
	current := func(m deployment.Mutation) string { return currentOwnedImage(ctx, m) }
	plan, err := orch.PlanRemove(ctx, installID, current)
	if err != nil {
		return err
	}
	if len(plan.Remove) == 0 && len(plan.Detach) == 0 && len(plan.Missing) == 0 {
		return bestEffortRemove(ctx, db, dataRoot, rec)
	}
	_, err = orch.RemoveInstall(ctx, installID, current, func(m deployment.Mutation) error {
		return stripOwnedImage(ctx, db, dataRoot, m)
	})
	return err
}

// stripOwnedImage undoes one owned write that reconcile proved unmodified.
// A host-config node is spliced out — restoring the user's prior entry when
// this install replaced one — and only then is its registration row dropped;
// a skill directory goes through the skills ledger so its confinement and
// inventory guards still apply. Both paths refuse rather than guess when the
// recorded structure cannot be found where reconcile said it was.
func stripOwnedImage(ctx context.Context, db *state.DB, dataRoot string, m deployment.Mutation) error {
	if m.StructureType == "skill-dir" {
		return stripSkillDir(dataRoot, m)
	}
	name := locatorName(m)
	scope := mutationScope(m.Scope)
	if _, err := host.RemoveServerEntry(ctx, m.HostID, name, scope, backupDirFor(dataRoot), m.PriorEntry); err != nil {
		return fmt.Errorf("strip entry %q from %s: %w", name, m.FilePath, err)
	}
	// The registration row describes an entry that is now gone (or restored
	// to the user's own text); it must not outlive it. A row that has already
	// vanished is fine — DeleteHostRegistration only fails on real errors.
	if err := db.DeleteHostRegistration(ctx, m.HostID, scope, "", name); err != nil {
		return fmt.Errorf("entry %q stripped from %s but its registration row could not be dropped: %w", name, m.FilePath, err)
	}
	return nil
}

// stripSkillDir removes one skill directory through the skills ledger.
func stripSkillDir(dataRoot string, m deployment.Mutation) error {
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		return fmt.Errorf("open skills ledger: %w", err)
	}
	name := locatorName(m)
	outcomes, err := ledger.RemoveScoped(skills.RemoveOptions{
		Names: []string{name},
		Scope: skillLedgerScope(m.Scope),
	})
	if err != nil {
		return err
	}
	for _, o := range outcomes {
		if o.Entry.DestDir != m.FilePath {
			continue
		}
		if o.Removed {
			return nil
		}
		return fmt.Errorf("refusing to remove skill %q at %s: %s", name, m.FilePath, o.Reason)
	}
	// Reconcile proved the directory matches what LiteSPM wrote, but no
	// provenance row names it: deleting an unrecorded tree is exactly what
	// the ledger exists to prevent, so this is an error, not a fallback.
	return fmt.Errorf("skills ledger has no entry for %q at %s; refusing to delete an unrecorded directory", name, m.FilePath)
}

// bestEffortRemove handles an install that has no deployment-ledger rows.
// For MCP the recorded host registrations still name every config LiteSPM
// spliced an entry into, so the entry really is stripped before state goes.
// For every other kind there is only state to delete: their files were never
// ledgered, and guessing which directories an old install owned is how an
// installer ends up deleting something it does not own.
func bestEffortRemove(ctx context.Context, db *state.DB, dataRoot string, rec *domain.InstallRecord) error {
	if rec.Kind == domain.KindMCP {
		comps, err := db.ListInstallComponents(ctx, rec.InstallID)
		if err != nil {
			return fmt.Errorf("list components of %s: %w", rec.InstallID, err)
		}
		backupDir := backupDirFor(dataRoot)
		for _, comp := range comps {
			regs, err := db.ListHostRegistrationsForEntry(ctx, rec.Scope, comp.ComponentName)
			if err != nil {
				return fmt.Errorf("list host registrations for %q: %w", comp.ComponentName, err)
			}
			for _, reg := range regs {
				if _, err := host.RemoveServerEntry(ctx, reg.HostID, comp.ComponentName, reg.Scope, backupDir, ""); err != nil {
					return fmt.Errorf("strip %q from host %s: %w", comp.ComponentName, reg.HostID, err)
				}
				if err := db.DeleteHostRegistration(ctx, reg.HostID, reg.Scope, reg.WorkspaceID, comp.ComponentName); err != nil {
					return fmt.Errorf("drop registration of %q on host %s: %w", comp.ComponentName, reg.HostID, err)
				}
			}
		}
	}
	return db.DeleteInstall(ctx, rec.InstallID)
}
