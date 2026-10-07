package main

// list.go — `litespm list` (the human setup overview) and `litespm inventory`
// (machine-state truth), ARCH/38 §4.
//
// Two audiences, one truth: both commands read the SAME sources and differ
// only in what they surface. The sources are the existing state rows (installs
// and host registrations), the deployment-mutation ledger (internal/deployment),
// the skills ledger, and each host adapter's read-only detection — no state
// table is added here, and nothing in this file writes.
//
// Counting rule (§4): a capability installed for three agents counts ONCE as a
// capability and THREE times as deployments, and the human output says both
// out loud: "18 unique capabilities · 27 deployments".
//
// Honesty: a cell the recorded data cannot answer renders "—" in tables and an
// empty string in JSON — never a guess. `list` never health-checks, so no row
// ever claims Ready, running or verified.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/binding"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/inventory"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// inventorySchema is the schema id automation pins. TestInventoryJSONShape
// pins the shape underneath it.
const inventorySchema = "litespm.inventory/v1"

// State vocabulary (ARCH/38 §4): the same words a person reads and a machine
// receives.
const (
	stateManaged  = "managed"  // LiteSPM controls its lifecycle
	stateObserved = "observed" // LiteSPM found it; read-only
	stateAdopted  = "adopted"  // taken over from another agent, now managed
	stateDrifted  = "drifted"  // recorded write and the disk disagree
	stateBroken   = "broken"   // recorded but missing
	stateUnknown  = "unknown"  // the data cannot answer; the table shows —
)

// Placement: "deployed" is a capability sitting with one agent; "unplaced" is
// an install LiteSPM has state for but no recorded deployment (a kind that
// writes no agent entry, or an install older than the deployment ledger).
const (
	placementDeployed = "deployed"
	placementUnplaced = "unplaced"
)

// Origin: where the record came from — kept separate from its lifecycle state.
const (
	originCatalog      = "catalog"
	originAdopted      = "adopted"
	originHostConfig   = "host-config"
	originSkillsLedger = "skills-ledger"
)

// inventoryRecord is one record of the stable schema — one deployment, or one
// capability with no recorded placement. Every field is always present so
// automation can depend on the shape; a value the recorded data cannot answer
// is the empty string, never an estimate.
type inventoryRecord struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Agent      string `json:"agent"`
	Scope      string `json:"scope"`
	State      string `json:"state"`
	Placement  string `json:"placement"`
	Origin     string `json:"origin"`
	InstallID  string `json:"installId"`
	ConfigPath string `json:"configPath"`
	Locator    string `json:"locator"`
	// EntryStyle is the internal/binding vocabulary for how this deployment
	// reaches its agent: "native" is a per-package entry in the agent's own
	// config. Empty when the record names no host-config entry — a skill
	// lives as a directory in the agent's skills tree, and an observed entry
	// is not bound by LiteSPM at all.
	EntryStyle    string `json:"entryStyle"`
	Digest        string `json:"digest"`
	CurrentDigest string `json:"currentDigest"`
	InstalledAt   string `json:"installedAt"`
	UpdatedAt     string `json:"updatedAt"`
	Detail        string `json:"detail"`
}

// inventoryCounts carries the §4 counting rule as separate numbers.
type inventoryCounts struct {
	UniqueCapabilities int `json:"uniqueCapabilities"`
	Deployments        int `json:"deployments"`
	Unplaced           int `json:"unplaced"`
	Managed            int `json:"managed"`
	Observed           int `json:"observed"`
	Adopted            int `json:"adopted"`
	Drifted            int `json:"drifted"`
	Broken             int `json:"broken"`
	Unknown            int `json:"unknown"`
}

// inventoryFilters echoes the filters the document was produced under, so a
// stored answer never claims to be the whole machine when it is not.
type inventoryFilters struct {
	Kind       string `json:"kind"`
	Agent      string `json:"agent"`
	State      string `json:"state"`
	Scope      string `json:"scope"`
	ProjectDir string `json:"projectDir"`
}

// inventoryReport is the document `inventory --json` and `list --json` print.
type inventoryReport struct {
	Schema string `json:"schema"`
	// CapturedAt is when these observations were taken (RFC3339 UTC), stamped
	// by the internal/inventory collector that every located record is
	// observed through.
	CapturedAt string            `json:"capturedAt"`
	Filters    inventoryFilters  `json:"filters"`
	Counts     inventoryCounts   `json:"counts"`
	Records    []inventoryRecord `json:"records"`
	Warnings   []string          `json:"warnings"`
}

// listFlags is the parsed command line shared by `list` and `inventory`.
type listFlags struct {
	kind     string // "" | agents | mcp | skills | plugins
	agent    string
	managed  bool
	observed bool
	drifted  bool
	project  string // project directory, "" when unset
	global   bool
	jsonOut  bool
	showHelp bool
}

const listUsage = `Usage: litespm list [agents|mcp|skills|plugins] [flags]
       litespm inventory [mcp|skills|plugins] [flags]

Show what is deployed on this machine from recorded state only: install rows,
the deployment ledger, the skills ledger, and each agent adapter's read-only
detection. Nothing is started, changed or health-checked.

Positional filter:
  agents                   the agent roster: which agents this machine has and
                           what is deployed to each (not valid for inventory)
  mcp | skills | plugins   only that kind (same as --kind)

Flags:
  --agent <id>             only this agent
  --managed                only what LiteSPM controls (managed/adopted/drifted)
  --observed               only what LiteSPM found and does not control
  --drifted                only what changed or went missing since install
  --project <dir>          only project-scope rows for that directory
  --global                 only user-scope rows
  --kind <kind>            the same kind filter as the positional word
  --json                   machine-readable inventory (stable schema)
  --help, -h               show this help text

Counting: a capability installed for three agents counts once as a capability
and three times as deployments — the output always says both.

States: managed (LiteSPM controls it) · observed (LiteSPM found it) · adopted
(copied from another agent, now managed) · drifted (changed since install) ·
broken (recorded but missing) · — (not knowable from the recorded data).

Exit codes: 0 success (including nothing to do) · 1 a source could not be read
· 2 usage`

// parseListFlags parses `list`/`inventory` arguments strictly: an unknown
// flag, a missing flag value, a second positional word, a kind that does not
// exist, and --project together with --global are all usage errors (the caller
// exits 2). `--help` short-circuits so it can exit 0.
func parseListFlags(args []string) (listFlags, error) {
	var f listFlags
	positional := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "--help", "-h":
			f.showHelp = true
			return f, nil
		case "--json":
			f.jsonOut = true
		case "--managed":
			f.managed = true
		case "--observed":
			f.observed = true
		case "--drifted":
			f.drifted = true
		case "--global":
			f.global = true
		case "--agent":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.agent = strings.ToLower(strings.TrimSpace(v))
		case "--project":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.project = v
		case "--kind":
			v, err := next()
			if err != nil {
				return f, err
			}
			kind, err := normalizeListKind(v)
			if err != nil {
				return f, err
			}
			if f.kind != "" && f.kind != kind {
				return f, fmt.Errorf("--kind %s conflicts with the filter %s", kind, f.kind)
			}
			f.kind = kind
		default:
			if strings.HasPrefix(a, "-") {
				return f, fmt.Errorf("unknown flag %q", a)
			}
			kind, err := normalizeListKind(a)
			if err != nil {
				return f, err
			}
			positional++
			if positional > 1 {
				return f, fmt.Errorf("unexpected argument %q", a)
			}
			if f.kind != "" && f.kind != kind {
				return f, fmt.Errorf("%s conflicts with the kind filter %s", kind, f.kind)
			}
			f.kind = kind
		}
	}
	if f.global && f.project != "" {
		return f, fmt.Errorf("--project and --global select different scopes; pass only one")
	}
	return f, nil
}

// normalizeListKind maps the accepted kind words onto the four filters.
func normalizeListKind(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "agents", "agent":
		return "agents", nil
	case "mcp", "mcp-servers", "servers":
		return "mcp", nil
	case "skills", "skill":
		return "skills", nil
	case "plugins", "plugin":
		return "plugins", nil
	default:
		return "", fmt.Errorf("unknown filter %q: want agents, mcp, skills or plugins", raw)
	}
}

// runList is the dispatch entry point for `litespm list` / `litespm inventory`.
func runList(args []string, asInventory bool) int {
	return runListTo(os.Stdout, os.Stderr, args, asInventory)
}

// runListTo runs the command against explicit writers so tests can capture
// both streams and assert the exit code: 0 success (including nothing to do),
// 1 a source could not be read, 2 usage.
func runListTo(out, errOut io.Writer, args []string, asInventory bool) int {
	fl, err := parseListFlags(args)
	if err != nil {
		fmt.Fprintf(errOut, "list: %v\n\n", err)
		fmt.Fprintln(errOut, listUsage)
		return 2
	}
	if fl.showHelp {
		fmt.Fprintln(out, listUsage)
		return 0
	}
	if asInventory && fl.kind == "agents" {
		fmt.Fprintln(errOut, "inventory: `agents` is the roster view and has no per-deployment records; run `litespm list agents` for it")
		fmt.Fprintln(errOut)
		fmt.Fprintln(errOut, listUsage)
		return 2
	}
	if fl.project != "" {
		abs, perr := resolveProjectDir(fl.project)
		if perr != nil {
			fmt.Fprintf(errOut, "list: --project %s: %v\n", fl.project, perr)
			return 2
		}
		fl.project = abs
	}

	ctx := context.Background()
	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(errOut, "list: failed to resolve platform paths: %v\n", err)
		return 1
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(errOut, "list: failed to open the state database: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	report, err := gatherInventory(ctx, db, paths.DataRoot, fl)
	if err != nil {
		fmt.Fprintf(errOut, "list: %v\n", err)
		return 1
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(errOut, "list: warning: %s\n", warning)
	}

	if asInventory || fl.jsonOut {
		payload, merr := marshalInventory(report)
		if merr != nil {
			fmt.Fprintf(errOut, "list: failed to encode the inventory: %v\n", merr)
			return 1
		}
		fmt.Fprintln(out, string(payload))
		return 0
	}
	if fl.kind == "agents" {
		renderAgentRoster(ctx, out, report)
		return 0
	}
	renderList(out, report, fl)
	return 0
}

// resolveProjectDir resolves --project the way `copy` does: an absolute path
// to a directory that exists.
func resolveProjectDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("no such directory")
	}
	return abs, nil
}

// gatherInventory reads every source and builds the report: records first,
// then the filters, the counts and the stable order.
func gatherInventory(ctx context.Context, db *state.DB, dataRoot string, fl listFlags) (*inventoryReport, error) {
	report := &inventoryReport{
		Schema: inventorySchema,
		Filters: inventoryFilters{
			Kind:       fl.kind,
			Agent:      fl.agent,
			State:      stateFilterLabel(fl),
			Scope:      scopeFilterLabel(fl),
			ProjectDir: fl.project,
		},
		Warnings: []string{},
		Records:  []inventoryRecord{},
	}

	// ---- source 1: install rows, both scopes (ListInstalls answers one
	// scope per call).
	installRows := map[string]*domain.InstallRecord{}
	for _, scope := range []domain.InstallScope{domain.ScopeUser, domain.ScopeProject} {
		rows, err := db.ListInstalls(ctx, scope, "")
		if err != nil {
			return nil, fmt.Errorf("read install rows: %w", err)
		}
		for _, rec := range rows {
			if rec.Status == domain.InstallRemoved {
				continue
			}
			installRows[rec.InstallID] = rec
		}
	}

	// ---- source 2: the deployment-mutation ledger, one pass per install.
	mutLedger := deployment.NewLedger(db.Raw())
	skillDirs := map[string]deployment.Mutation{} // skill writes, keyed by directory
	linkedInstalls := map[string]bool{}
	managedNames := map[string]map[string]bool{} // host -> entry names LiteSPM wrote
	records := make([]inventoryRecord, 0, len(installRows))

	for installID, inst := range installRows {
		componentName := installComponentName(ctx, db, installID)
		muts, err := mutLedger.MutationsForInstall(ctx, installID)
		if err != nil {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("deployment ledger for %s could not be read: %v", installID, err))
			continue
		}
		for _, m := range muts {
			if m.Detached {
				// Ownership was already given up: the node is the user's now,
				// so it is not ours to report as managed. If the entry is
				// still in a config, detection below reports it as observed.
				continue
			}
			if m.StructureType == "skill-dir" {
				if _, seen := skillDirs[m.FilePath]; !seen {
					skillDirs[m.FilePath] = m
				}
				continue
			}
			records = append(records, configRecordFor(ctx, inst, componentName, m))
			linkedInstalls[installID] = true
			if managedNames[m.HostID] == nil {
				managedNames[m.HostID] = map[string]bool{}
			}
			managedNames[m.HostID][locatorName(m)] = true
		}
	}

	// ---- source 3: the skills ledger — the authoritative record of which
	// agent each skill directory lives in (and of directories `skills add`
	// wrote without an install row).
	ledgerEntries := []skills.LedgerEntry{}
	if ledger, lerr := skills.OpenLedger(skills.LedgerPath(dataRoot)); lerr != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("skills ledger could not be opened: %v", lerr))
	} else if entries, rerr := ledger.All(); rerr != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("skills ledger could not be read: %v", rerr))
	} else {
		ledgerEntries = entries
	}

	coveredDirs := map[string]bool{}
	for _, entry := range ledgerEntries {
		coveredDirs[entry.DestDir] = true
		mut, hasMut := skillDirs[entry.DestDir]
		installID := ""
		if hasMut {
			installID = mut.InstallID
		}
		if installID == "" {
			installID = installIDForPath(installRows, entry.DestDir)
		}
		var inst *domain.InstallRecord
		if installID != "" {
			inst = installRows[installID]
			linkedInstalls[installID] = true
		}
		records = append(records, skillRecordFor(installID, inst, entry, mut, hasMut))
	}
	for dest, mut := range skillDirs {
		if !coveredDirs[dest] {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("a skill directory written for %s has no skills-ledger entry and is not listed (%s)", mut.InstallID, dest))
		}
	}

	// ---- source 4: adapter detection (read-only) for what LiteSPM does not
	// manage. A managed entry shows up in detection too, so it is dropped
	// here rather than counted a second time.
	for _, adapter := range host.ListAdapters() {
		hostID := adapter.Descriptor().HostID
		comps, err := adapter.DetectPreExistingComponents(ctx)
		if err != nil {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("detection on %s could not run: %v", hostID, err))
			continue
		}
		for _, comp := range comps {
			if managedNames[hostID][comp.Name] {
				continue
			}
			records = append(records, observedRecordFor(ctx, hostID, comp))
		}
	}

	// ---- installs with no recorded placement: real state, no deployment.
	// Listed honestly instead of silently dropped.
	for installID, inst := range installRows {
		if linkedInstalls[installID] {
			continue
		}
		records = append(records, unplacedRecordFor(ctx, db, installID, inst))
	}

	// ---- filters, counts, stable order.
	kept := make([]inventoryRecord, 0, len(records))
	for _, rec := range records {
		if fl.matches(rec, installRows) {
			kept = append(kept, rec)
		}
	}
	sortRecords(kept)
	report.Records = kept
	report.Counts = countRecords(kept)
	report.CapturedAt = observeRecords(report, kept)
	return report, nil
}

// observeRecords runs every record that names a location through the
// internal/inventory collector — the observation seam ARCH/38 §4 names — and
// returns the capture timestamp it stamps. A record that names no location (an
// install with no recorded placement, an entry LiteSPM never wrote) is not an
// observation of a file, so it stays out of the collector instead of being
// padded with an invented path or locator.
func observeRecords(report *inventoryReport, records []inventoryRecord) string {
	collector := inventory.New()
	for _, rec := range records {
		if rec.ID == "" || rec.ConfigPath == "" || rec.Locator == "" {
			continue
		}
		if err := collector.Observe(inventory.ObservedNode{
			CapabilityID: rec.ID,
			HostID:       rec.Agent,
			FilePath:     rec.ConfigPath,
			Locator:      rec.Locator,
			ImageHash:    rec.CurrentDigest,
		}); err != nil {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("observation of %s was rejected: %v", rec.ID, err))
		}
	}
	return collector.Snapshot().TakenAt.UTC().Format(time.RFC3339)
}

// matches applies the §4 filters to one record.
func (fl listFlags) matches(rec inventoryRecord, installRows map[string]*domain.InstallRecord) bool {
	if fl.kind != "" && fl.kind != "agents" {
		want := map[string]string{"mcp": "mcp", "skills": "skill", "plugins": "plugin"}[fl.kind]
		if rec.Kind != want {
			return false
		}
	}
	if fl.agent != "" && !strings.EqualFold(rec.Agent, fl.agent) {
		return false
	}
	switch {
	case fl.managed:
		// Everything LiteSPM controls, whatever its condition.
		if rec.State == stateObserved {
			return false
		}
	case fl.observed:
		if rec.State != stateObserved {
			return false
		}
	case fl.drifted:
		if rec.State != stateDrifted && rec.State != stateBroken {
			return false
		}
	}
	switch {
	case fl.project != "":
		if rec.Scope != "project" {
			return false
		}
		// A project row that records its root is matched against --project;
		// a row with no recorded root stays, because it cannot be attributed
		// to a different project and hiding it would be a guess.
		if rec.InstallID != "" {
			if inst := installRows[rec.InstallID]; inst != nil && inst.ProjectRoot != "" && inst.ProjectRoot != fl.project {
				return false
			}
		}
	case fl.global:
		if rec.Scope != "user" {
			return false
		}
	}
	return true
}

// stateFilterLabel / scopeFilterLabel name the active filters for the JSON
// document ("" / "all" when that filter is not in play).
func stateFilterLabel(fl listFlags) string {
	switch {
	case fl.managed:
		return "managed"
	case fl.observed:
		return "observed"
	case fl.drifted:
		return "drifted"
	}
	return ""
}

func scopeFilterLabel(fl listFlags) string {
	switch {
	case fl.project != "":
		return "project"
	case fl.global:
		return "user"
	}
	return "all"
}

// ---- record construction -------------------------------------------------

// configRecordFor turns one non-skill deployment mutation (an entry LiteSPM
// wrote into an agent config) into a record, classified against what that
// config holds right now.
func configRecordFor(ctx context.Context, inst *domain.InstallRecord, componentName string, m deployment.Mutation) inventoryRecord {
	name := componentName
	if name == "" {
		name = locatorName(m)
	}
	current := currentOwnedImage(ctx, m)
	st, detail, currentDigest := classifyOwned(m, current)
	origin := originForListing(inst.ListingID)
	if st == stateManaged && origin == originAdopted {
		st = stateAdopted
	}
	rec := inventoryRecord{
		Kind:          installKindOf(inst),
		ID:            inst.ListingID,
		Name:          name,
		Agent:         m.HostID,
		Scope:         m.Scope,
		State:         st,
		Placement:     placementDeployed,
		Origin:        origin,
		InstallID:     inst.InstallID,
		ConfigPath:    m.FilePath,
		Locator:       m.Locator,
		Digest:        m.PostImageHash,
		CurrentDigest: currentDigest,
		InstalledAt:   formatRecordTime(inst.InstalledAt),
		UpdatedAt:     formatRecordTime(inst.UpdatedAt),
		Detail:        detail,
	}
	rec.EntryStyle = entryStyleFor(rec)
	return rec
}

// entryStyleFor names how a deployment reaches its agent, in the
// internal/binding vocabulary. The mode is taken from what the deployment
// ledger actually recorded — a per-package entry in the agent's own config is
// the native mode — never from a default the state does not hold. A record
// with no host-config entry of its own (a skill directory, a capability with
// no recorded placement) and an entry LiteSPM never wrote have no binding to
// report, so the field stays empty instead of guessing.
func entryStyleFor(rec inventoryRecord) string {
	if rec.Locator == "" || strings.HasPrefix(rec.Locator, "skills.") {
		return ""
	}
	if rec.Origin != originCatalog && rec.Origin != originAdopted {
		return ""
	}
	decision, err := binding.Resolve(binding.Request{
		ListingID: domain.ListingID(rec.ID),
		Mode:      binding.ModeNative,
		HostID:    rec.Agent,
	})
	if err != nil {
		return ""
	}
	return decision.EntryStyle
}

// skillRecordFor turns one skills-ledger entry into a record. The ledger
// carries the agent attribution; a matching deployment mutation carries the
// install link and the recorded image (and the ARCH/33 three-way table for
// its condition). A ledger entry with no mutation — a directory `skills add`
// wrote — is compared against its own recorded digest.
func skillRecordFor(installID string, inst *domain.InstallRecord, entry skills.LedgerEntry, mut deployment.Mutation, hasMut bool) inventoryRecord {
	agent := entry.AgentID
	if agent == "" {
		agent = entry.HostLabel
	}
	scope := "user"
	if entry.Scope == "project" {
		scope = "project"
	}
	recorded := entry.ContentDigest
	if recorded == "" && hasMut {
		recorded = mut.PostImageHash
	}
	st, detail, current := skillImageState(entry.DestDir, recorded, mut, hasMut)

	origin := originSkillsLedger
	id := "skill:" + entry.SkillName
	kind := string(domain.KindSkill)
	installedAt, updatedAt := "", ""
	if inst != nil {
		origin = originForListing(inst.ListingID)
		id = inst.ListingID
		kind = installKindOf(inst)
		installedAt = formatRecordTime(inst.InstalledAt)
		updatedAt = formatRecordTime(inst.UpdatedAt)
	}
	if st == stateManaged && origin == originAdopted {
		st = stateAdopted
	}

	locator := ""
	if hasMut {
		locator = mut.Locator
	}
	return inventoryRecord{
		Kind:          kind,
		ID:            id,
		Name:          entry.SkillName,
		Agent:         agent,
		Scope:         scope,
		State:         st,
		Placement:     placementDeployed,
		Origin:        origin,
		InstallID:     installID,
		ConfigPath:    entry.DestDir,
		Locator:       locator,
		Digest:        recorded,
		CurrentDigest: current,
		InstalledAt:   installedAt,
		UpdatedAt:     updatedAt,
		Detail:        detail,
	}
}

// skillImageState reports what a recorded skill directory looks like on disk
// right now. The directory's content digest is its owned image (ARCH/33 §5);
// a mutation-backed row then goes through the same three-way table as a
// config entry, so both kinds word drift the same way.
func skillImageState(dest, recorded string, mut deployment.Mutation, hasMut bool) (st, detail, current string) {
	prov, err := skills.CaptureProvenance(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return stateBroken, "recorded by LiteSPM but the skill directory is no longer on disk", ""
		}
		return stateUnknown, fmt.Sprintf("the skill directory could not be fingerprinted right now: %v", err), ""
	}
	current = prov.Digest
	if hasMut {
		return classifyOwned(mut, current)
	}
	if recorded == "" {
		return stateUnknown, "no digest was recorded when this skill was written, so drift is not knowable", current
	}
	if current == recorded {
		return stateManaged, "", current
	}
	return stateDrifted, "the skill directory no longer matches what LiteSPM recorded", current
}

// classifyOwned applies the ARCH/33 §4 decision table (deployment.Reconcile)
// to a recorded write and the image on disk now, and words the result for a
// person. An unreadable config is "unknown", never "drifted": LiteSPM refuses
// to claim a condition it could not observe.
func classifyOwned(m deployment.Mutation, current string) (st, detail, currentDigest string) {
	if current == host.EntryUnreadable {
		return stateUnknown, "the agent config could not be read right now, so its condition is not knowable", ""
	}
	switch deployment.Reconcile(m, current).Outcome {
	case deployment.ReconcileMissing:
		return stateBroken, "recorded by LiteSPM but no longer present where it was written", ""
	case deployment.ReconcileUserEdited:
		return stateDrifted, "changed since the install; LiteSPM reports it and changes nothing", current
	default: // deployment.ReconcileUnmodified
		return stateManaged, "", current
	}
}

// observedRecordFor reports one component found in an agent's own config:
// read-only, with no install behind it and no digest LiteSPM ever wrote.
func observedRecordFor(ctx context.Context, hostID string, comp host.PreExistingComponent) inventoryRecord {
	current := ""
	if fp, err := host.EntryFingerprintNow(ctx, hostID, comp.Name, domain.ScopeUser); err == nil && fp != host.EntryUnreadable {
		current = fp
	}
	detail := "found in the agent's own config; LiteSPM reports it read-only"
	if comp.Description != "" {
		detail = comp.Description + "; LiteSPM reports it read-only"
	}
	kind := comp.Kind
	if kind == "" {
		kind = string(domain.KindMCP)
	}
	return inventoryRecord{
		Kind:          kind,
		ID:            "observed:" + hostID + ":" + comp.Name,
		Name:          comp.Name,
		Agent:         hostID,
		Scope:         "user",
		State:         stateObserved,
		Placement:     placementDeployed,
		Origin:        originHostConfig,
		InstallID:     "",
		ConfigPath:    comp.SourcePath,
		Locator:       "",
		Digest:        "",
		CurrentDigest: current,
		InstalledAt:   "",
		UpdatedAt:     "",
		Detail:        detail,
	}
}

// unplacedRecordFor reports an install LiteSPM has state for but no recorded
// deployment: a capability without a placement, never a phantom deployment.
func unplacedRecordFor(ctx context.Context, db *state.DB, installID string, inst *domain.InstallRecord) inventoryRecord {
	name := installComponentName(ctx, db, installID)
	if name == "" {
		name = inst.ListingID
	}
	origin := originForListing(inst.ListingID)
	st := stateManaged
	if origin == originAdopted {
		st = stateAdopted
	}
	if inst.Status == domain.InstallBroken {
		st = stateBroken
	}
	return inventoryRecord{
		Kind:          installKindOf(inst),
		ID:            inst.ListingID,
		Name:          name,
		Agent:         "",
		Scope:         string(inst.Scope),
		State:         st,
		Placement:     placementUnplaced,
		Origin:        origin,
		InstallID:     inst.InstallID,
		ConfigPath:    inst.InstallPath,
		Locator:       "",
		Digest:        inst.TreeDigest,
		CurrentDigest: "",
		InstalledAt:   formatRecordTime(inst.InstalledAt),
		UpdatedAt:     formatRecordTime(inst.UpdatedAt),
		Detail:        "installed, but no deployment is recorded for it (a kind that writes no agent entry, or an install older than the deployment ledger)",
	}
}

// ---- helpers -------------------------------------------------------------

// installKindOf is the install's real kind; an unrecorded kind stays empty
// rather than being assumed to be one.
func installKindOf(inst *domain.InstallRecord) string {
	if inst.Kind != "" {
		return string(inst.Kind)
	}
	if lid, err := domain.ParseListingID(inst.ListingID); err == nil {
		return string(lid.Kind())
	}
	return ""
}

// originForListing distinguishes a catalog install from one `copy` adopted
// from another agent (ARCH/38 §5 local-origin grammar).
func originForListing(listingID string) string {
	if strings.Contains(listingID, ":adopted:") {
		return originAdopted
	}
	return originCatalog
}

// installComponentName is the recorded component name of an install — the
// name a person knows it by. Empty when no component row exists (the caller
// falls back to another recorded value).
func installComponentName(ctx context.Context, db *state.DB, installID string) string {
	comps, err := db.ListInstallComponents(ctx, installID)
	if err != nil || len(comps) == 0 {
		return ""
	}
	return comps[0].ComponentName
}

// installIDForPath links a directory to the install that recorded it as its
// install path.
func installIDForPath(installRows map[string]*domain.InstallRecord, dest string) string {
	for id, inst := range installRows {
		if inst.InstallPath == dest {
			return id
		}
	}
	return ""
}

// formatRecordTime renders a recorded timestamp as RFC3339 UTC, or "" when
// the row carries none.
func formatRecordTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// sortRecords orders records for both outputs: by agent (rows with no agent
// last), then kind, then name, then id — deterministic for automation.
func sortRecords(records []inventoryRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Agent != b.Agent {
			if a.Agent == "" {
				return false
			}
			if b.Agent == "" {
				return true
			}
			return a.Agent < b.Agent
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// countRecords computes the §4 counts over exactly the records shown.
func countRecords(records []inventoryRecord) inventoryCounts {
	var counts inventoryCounts
	unique := map[string]bool{}
	for _, rec := range records {
		unique[rec.ID] = true
		switch rec.State {
		case stateManaged:
			counts.Managed++
		case stateObserved:
			counts.Observed++
		case stateAdopted:
			counts.Adopted++
		case stateDrifted:
			counts.Drifted++
		case stateBroken:
			counts.Broken++
		case stateUnknown:
			counts.Unknown++
		}
		if rec.Placement == placementUnplaced {
			counts.Unplaced++
		} else {
			counts.Deployments++
		}
	}
	counts.UniqueCapabilities = len(unique)
	return counts
}

// marshalInventory encodes the stable document.
func marshalInventory(report *inventoryReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// anyFilterActive reports whether the document is a subset of the machine.
func (fl listFlags) anyFilterActive() bool {
	return fl.kind != "" || fl.agent != "" || fl.managed || fl.observed ||
		fl.drifted || fl.project != "" || fl.global
}

// countLine is the §4 counting sentence, verbatim in shape:
// "18 unique capabilities · 27 deployments".
func countLine(counts inventoryCounts, fl listFlags) string {
	parts := []string{
		fmt.Sprintf("%d unique capabilities", counts.UniqueCapabilities),
		fmt.Sprintf("%d deployments", counts.Deployments),
	}
	if counts.Unplaced > 0 {
		parts = append(parts, fmt.Sprintf("%d with no recorded deployment", counts.Unplaced))
	}
	line := strings.Join(parts, " · ")
	if fl.anyFilterActive() {
		line += " (filtered)"
	}
	return line
}

// ---- human output --------------------------------------------------------

// renderList prints the overview: counts, rows grouped by agent and kind, and
// the vocabulary the states come from.
func renderList(w io.Writer, report *inventoryReport, fl listFlags) {
	fmt.Fprintln(w, "LiteSPM setup overview")
	fmt.Fprintln(w)
	fmt.Fprintln(w, countLine(report.Counts, fl))
	fmt.Fprintln(w)

	if len(report.Records) == 0 {
		fmt.Fprintln(w, "Nothing recorded yet: no installs, deployments or agent-config entries matched.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Try `litespm host detect` to see which agents this machine has,")
		fmt.Fprintln(w, "`litespm catalog sync` then `litespm search <term>` to find something,")
		fmt.Fprintln(w, "and `litespm list` again.")
		return
	}

	byAgent := map[string][]inventoryRecord{}
	agents := []string{}
	for _, rec := range report.Records {
		if _, seen := byAgent[rec.Agent]; !seen {
			agents = append(agents, rec.Agent)
		}
		byAgent[rec.Agent] = append(byAgent[rec.Agent], rec)
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i] == "" {
			return false
		}
		if agents[j] == "" {
			return true
		}
		return agents[i] < agents[j]
	})

	for _, agent := range agents {
		rows := byAgent[agent]
		title := "AGENT " + agent
		if agent == "" {
			title = "NO RECORDED AGENT"
		}
		fmt.Fprintf(w, "%s (%d)\n", title, len(rows))
		fmt.Fprintf(w, "  %-7s %-22s %-9s %-7s %s\n", "KIND", "NAME", "STATE", "SCOPE", "ID / DETAIL")
		for _, rec := range rows {
			cell := rec.ID
			if rec.Detail != "" {
				cell = rec.ID + " — " + rec.Detail
			}
			fmt.Fprintf(w, "  %-7s %-22s %-9s %-7s %s\n",
				rec.KindDash(), truncateRunes(rec.Name, 22), rec.StateDash(), scopeDash(rec.Scope),
				truncateRunes(cell, 56))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "State: %d managed · %d observed · %d adopted · %d drifted · %d broken · %d not knowable\n",
		report.Counts.Managed, report.Counts.Observed, report.Counts.Adopted,
		report.Counts.Drifted, report.Counts.Broken, report.Counts.Unknown)
	if len(report.Warnings) > 0 {
		fmt.Fprintf(w, "Read warnings: %d source(s) could not be read — details were printed on stderr.\n", len(report.Warnings))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  managed   LiteSPM controls it; install remove / restore can undo it")
	fmt.Fprintln(w, "  observed  found in an agent's own config; read-only, never edited or removed")
	fmt.Fprintln(w, "  adopted   taken from another agent with `copy`; managed now, origin recorded")
	fmt.Fprintln(w, "  drifted   changed since LiteSPM wrote it; reported, never repaired here")
	fmt.Fprintln(w, "  broken    recorded by LiteSPM but missing where it was written")
	fmt.Fprintln(w, "  —         the recorded data cannot answer; shown, never guessed")
}

// KindDash / StateDash / scopeDash keep an unknown cell visibly empty instead
// of printing a word the data does not support.
func (r inventoryRecord) KindDash() string {
	if r.Kind == "" {
		return "—"
	}
	return r.Kind
}

func (r inventoryRecord) StateDash() string {
	if r.State == "" {
		return "—"
	}
	return r.State
}

func scopeDash(scope string) string {
	if scope == "" {
		return "—"
	}
	return scope
}

// renderAgentRoster prints `list agents`: which agent hosts this machine has,
// whether the LiteSPM bridge is registered in each, and how many deployments
// each one holds.
func renderAgentRoster(ctx context.Context, w io.Writer, report *inventoryReport) {
	fmt.Fprintln(w, "Agent hosts on this machine")
	fmt.Fprintln(w)

	deployments := map[string]int{}
	for _, rec := range report.Records {
		if rec.Agent != "" {
			deployments[rec.Agent]++
		}
	}

	type rosterRow struct {
		hostID      string
		configPath  string
		bridge      string
		deployments int
	}
	rows := []rosterRow{}
	covered := map[string]bool{}

	verifications, err := host.DetectInstalledHosts(ctx)
	if err != nil {
		// Detection failing is reported, not hidden: the roster below then
		// only reflects what the records know.
		fmt.Fprintf(w, "Detection could not run: %v\n\n", err)
	}
	for _, v := range verifications {
		covered[v.HostID] = true
		configExists := v.ConfigPath != "" && fileExists(v.ConfigPath)
		if !configExists && deployments[v.HostID] == 0 {
			// An agent this machine has never been configured for is not a
			// row; `litespm host list` prints the full adapter table.
			continue
		}
		bridge := map[string]string{"ready": "registered", "missing": "not set up", "corrupted": "corrupted"}[v.Status]
		if bridge == "" {
			bridge = "—"
		}
		config := "not found"
		if configExists {
			config = v.ConfigPath
		} else if v.ConfigPath == "" {
			config = "—"
		}
		rows = append(rows, rosterRow{hostID: v.HostID, configPath: config, bridge: bridge, deployments: deployments[v.HostID]})
	}
	// Agents that hold deployments but did not come back from detection
	// (an id recorded in the skills ledger, for instance).
	extra := []string{}
	for hostID := range deployments {
		if !covered[hostID] {
			extra = append(extra, hostID)
		}
	}
	sort.Strings(extra)
	for _, hostID := range extra {
		rows = append(rows, rosterRow{hostID: hostID, configPath: "—", bridge: "—", deployments: deployments[hostID]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].hostID < rows[j].hostID })

	if len(rows) == 0 {
		fmt.Fprintln(w, "No agent host was detected on this machine, and no deployment names one.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Run `litespm host list` for the supported agents, then `litespm host setup <host-id>`.")
		return
	}

	fmt.Fprintf(w, "  %-16s %-44s %-11s %s\n", "AGENT", "CONFIG FILE", "BRIDGE", "DEPLOYMENTS")
	for _, row := range rows {
		fmt.Fprintf(w, "  %-16s %-44s %-11s %d\n",
			row.hostID, truncateRunes(row.configPath, 44), row.bridge, row.deployments)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Deployments counts the records `litespm list` shows for that agent;")
	fmt.Fprintln(w, "run `litespm list --agent <id>` for the rows themselves.")
}

// fileExists is a plain stat; a missing config is a fact, not an error.
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
