package porting

// plan.go — the copy planner: IR + target facts + options → CopyPlan.
//
// The planner is pure: it performs no I/O, opens no database, and writes
// nothing. Everything it inspects was read by the CLI before it was called,
// which is what makes the plan phase provably write-free (acceptance test
// TestPortingPlanCountsAndNoWrites) and lets the same planner back
// `profile create --from` later.
//
// Pipeline (ARCH/38 §5.3, the only order): normalize → filter (kinds/items/
// exclude) → support check → translate (shape comparison) → conflict
// detection → plan. Verification steps are declared here and executed by the
// CLI after a write.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/host"
)

// Item actions (CopyItem.Action).
const (
	// ActionDirect will be written as-is: source and target shapes agree.
	ActionDirect = "direct"
	// ActionTranslated will be written through a shape change (for example a
	// combined command array split into command + args).
	ActionTranslated = "translated"
	// ActionConflict names a target entry that differs and has no strategy
	// decision yet (only the default `ask` strategy produces it).
	ActionConflict = "conflict"
	// ActionSkipped will not be written: unchanged, excluded, or a conflict
	// whose strategy kept the target. Reason says which.
	ActionSkipped = "skipped"
	// ActionUnsupported cannot be written; Reason carries the LPSM-COPY
	// code and the honest explanation.
	ActionUnsupported = "unsupported"
)

// Verification levels reported per item. v1 ships L1 and L2 only; L3–L5
// (runtime/protocol/capability) reuse the internal/discover probe later and
// deliberately have no representation here, so nothing can claim them.
const (
	VerifyL1Config   = "L1 read-back: target entry fingerprint equals the plan"
	VerifyL2Exe      = "L2 executable: Command resolves on this machine"
	VerifyL1SkillDir = "L1 read-back: skill content digest equals the plan"
)

// CopyOptions is the CLI surface of `litespm copy` (ARCH/38 §5.3). Kinds,
// Items, Exclude and Conflict drive the planner; Apply, Yes, DryRun, Global
// and Project are approval/scope stages the CLI applies around it, and are
// carried here so one struct describes a copy invocation end to end.
type CopyOptions struct {
	From, To string // agent ids, validated against the adapter registry
	Kinds    []string
	Items    []string
	Exclude  []string
	Conflict string // ask (default) | keep-target | use-source | compatible | skip
	Apply    bool   // plan is the default; --apply executes
	Yes      bool   // non-interactive approval (requires --apply)
	DryRun   bool   // always accepted; identical to the default plan
	Global   bool   // scope: user (default)
	Project  string // project scope rooted at this directory (--project <dir>)
}

// CopyItem is one row of the plan.
type CopyItem struct {
	Kind string
	ID   string
	// IR is the canonical MCP IR for kind mcp (never env literals).
	IR host.ServerEntry
	// Skill is the canonical skills IR for kind skill; nil otherwise.
	Skill *SkillRecord
	// Action is one of the Action* constants.
	Action string
	// Reason explains a non-direct action; empty for a plain direct row.
	Reason string
	// TargetDiff is the human one-liner of the shape change (translated rows).
	TargetDiff string
	// Needs are the environment variable NAMES the target must provide.
	// Names only — a value never appears in a plan.
	Needs []string
	// Verify lists the verification steps this item will run after a write.
	Verify []string
}

// CopyPlan is the printed document: counts, rows, conflicts, unchanged.
type CopyPlan struct {
	Source, Target string
	Found          map[string]int // per kind, source inventory before filters
	Items          []CopyItem
	Conflicts      []Conflict
	Unchanged      int // already identical on target
	// UnsupportedNotes records kind-level refusals (a requested kind v1
	// cannot copy), so selecting plugins says so instead of planning nothing.
	UnsupportedNotes []string
	// NotFound records explicitly requested item ids the source does not have.
	NotFound []string
}

// TargetSkill is one skill already present at the target.
type TargetSkill struct {
	Dir string
	// Digest is the target directory's provenance digest; empty means the
	// directory exists but its content could not be fingerprinted, which is
	// never treated as "identical".
	Digest string
	// Managed reports whether the target directory was created through the
	// skills ledger. A use-source overwrite is refused for an unmanaged
	// directory: deleting a tree LiteSPM did not create is not copy's call.
	Managed bool
}

// TargetCaps is what the planner needs to know about the target agent. The
// CLI reads it through the same adapter seams the write path uses.
type TargetCaps struct {
	To      string
	Display string
	// HasEntrySpec is the support check (ARCH/16 §6.3): the target declares
	// an MCP entry layout this install path can render.
	HasEntrySpec bool
	// EntryShape is the target's MCP entry shape; with the source shape it
	// decides direct vs translated.
	EntryShape string
	// CanForwardEnv is host.EnvForwarding(to): false means the target has no
	// documented spelling for a forwarded variable, so an entry carrying
	// EnvNames cannot be represented there honestly.
	CanForwardEnv bool
	// Remote is host.RemoteEntrySpecFor(to): the target's verified remote
	// (URL) entry spelling, or nil when the target cannot express one. A
	// remote source row against a nil Remote is reported unsupported
	// ("not copyable") — never rendered as a stdio entry, never dropped.
	Remote *host.RemoteEntrySpec
	// ExistingMCP are the target's current MCP entries, by name.
	ExistingMCP map[string]host.HostServerEntry
	// SkillDir is the target's skill directory; empty means the target has no
	// documented skills location and skills are unsupported there.
	SkillDir string
	// ExistingSkills holds target skills by name (absent = not present).
	ExistingSkills map[string]TargetSkill
}

// The per-kind verification ladders v1 actually runs.
var (
	mcpVerifySteps   = []string{VerifyL1Config, VerifyL2Exe}
	skillVerifySteps = []string{VerifyL1SkillDir}
)

// CanonicalKind maps a kind selector spelling onto the kind vocabulary.
// It reports false for a word that is not a kind at all, so the CLI can tell
// a kind selector from an item id.
func CanonicalKind(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "mcp", "mcps", "mcp-server", "mcp-servers":
		return KindMCP, true
	case "skill", "skills":
		return KindSkill, true
	case "plugin", "plugins":
		return KindPlugin, true
	case "connector", "connectors":
		return "connector", true
	case "agent", "agents":
		return "agent", true
	case "rule", "rules":
		return "rule", true
	case "hook", "hooks":
		return "hook", true
	case "tool", "tools":
		return "tool", true
	case "lsp", "lsps":
		return "lsp", true
	default:
		return "", false
	}
}

// BuildPlan normalizes the source, applies the filters, support-checks every
// item against the target, classifies conflicts and returns the plan. It
// touches nothing outside its arguments.
func BuildPlan(src *Source, tgt *TargetCaps, opts CopyOptions) (*CopyPlan, error) {
	if src == nil {
		return nil, fmt.Errorf("porting: nil source")
	}
	if tgt == nil {
		return nil, fmt.Errorf("porting: nil target")
	}
	strategy, err := ParseStrategy(opts.Conflict)
	if err != nil {
		return nil, err
	}

	plan := &CopyPlan{
		Source: src.From,
		Target: tgt.To,
		Found:  map[string]int{},
	}
	for range src.MCP {
		plan.Found[KindMCP]++
	}
	for range src.Skills {
		plan.Found[KindSkill]++
	}
	for _, o := range src.Other {
		plan.Found[o.Kind]++
	}

	// Kind selection: no selector means every kind is considered; a selector
	// narrows the run to what the user named. Selecting a kind v1 cannot copy
	// is reported (LPSM-COPY-003), never treated as "nothing there".
	kinds := map[string]bool{}
	explicitKinds := len(opts.Kinds) > 0
	for _, k := range opts.Kinds {
		canonical, ok := CanonicalKind(k)
		if !ok {
			canonical = strings.ToLower(strings.TrimSpace(k))
		}
		kinds[canonical] = true
		if !SupportedKind(canonical) {
			plan.UnsupportedNotes = append(plan.UnsupportedNotes, fmt.Sprintf(
				"kind %q is not portable in v1 — LPSM-COPY-003: copy supports mcp and skill; %s is reported, never converted",
				canonical, canonical))
		}
	}
	selected := func(kind string) bool {
		return !explicitKinds || kinds[kind]
	}

	excluded := map[string]bool{}
	for _, e := range opts.Exclude {
		excluded[strings.TrimSpace(e)] = true
	}
	wanted := map[string]bool{}
	for _, i := range opts.Items {
		wanted[strings.TrimSpace(i)] = true
	}
	matchesItems := func(id string) bool {
		return len(wanted) == 0 || wanted[id]
	}

	for _, m := range src.MCP {
		if !selected(KindMCP) {
			continue
		}
		plan.add(planMCPItem(plan, src, tgt, m, strategy, excluded, matchesItems))
	}
	for _, s := range src.Skills {
		if !selected(KindSkill) {
			continue
		}
		plan.add(planSkillItem(plan, tgt, s, strategy, excluded, matchesItems))
	}
	// Kinds v1 does not copy are reported, not dropped — with the user's
	// explicit narrowing honored: a kind or item filter the user typed is the
	// scope they asked for, and Found still shows what was on the source.
	for _, o := range src.Other {
		if explicitKinds && !selected(o.Kind) {
			continue
		}
		if !matchesItems(o.Name) {
			continue
		}
		if excluded[o.Name] {
			plan.add(CopyItem{
				Kind: o.Kind, ID: o.Name, Action: ActionSkipped,
				Reason: "excluded by --exclude",
			})
			continue
		}
		plan.add(CopyItem{
			Kind: o.Kind, ID: o.Name, Action: ActionUnsupported,
			Reason: fmt.Sprintf("LPSM-COPY-003: kind %q is not portable in v1 (supported: mcp, skill)", o.Kind),
		})
	}

	// Requested ids the source does not have: reported, never silently
	// ignored — a typo must not look like a successful empty copy. Matched
	// against the source inventory, not the filtered rows, so an id that
	// exists but was narrowed away by a kind filter is not called missing.
	if len(wanted) > 0 {
		available := map[string]bool{}
		for _, m := range src.MCP {
			available[m.Name] = true
		}
		for _, s := range src.Skills {
			available[s.Name] = true
		}
		for _, o := range src.Other {
			available[o.Name] = true
		}
		for _, id := range opts.Items {
			if trimmed := strings.TrimSpace(id); !available[trimmed] {
				plan.NotFound = append(plan.NotFound, trimmed)
			}
		}
	}
	return plan, nil
}

// add appends a row, folding in the conflict/unchanged bookkeeping so no
// planner path can forget it. A zero row is a filtered-out id and produces
// nothing at all.
func (p *CopyPlan) add(item CopyItem) {
	if item.Kind == "" && item.ID == "" && item.Action == "" {
		return
	}
	if item.Action == ActionSkipped && item.Reason == "already identical on the target" {
		p.Unchanged++
	}
	p.Items = append(p.Items, item)
}

// planMCPItem runs one MCP server through normalize → support → translate →
// conflict. Filters are applied first: an excluded or unselected id produces
// no row at all (or an explicit `skipped` row for --exclude).
func planMCPItem(plan *CopyPlan, src *Source, tgt *TargetCaps, m SourceMCP, strategy string,
	excluded map[string]bool, matchesItems func(string) bool) CopyItem {
	if !matchesItems(m.Name) {
		return CopyItem{}
	}
	if excluded[m.Name] {
		return CopyItem{
			Kind: KindMCP, ID: m.Name, Action: ActionSkipped,
			Reason: "excluded by --exclude",
		}
	}
	entry, needs := NormalizeMCP(m)
	item := CopyItem{
		Kind:   KindMCP,
		ID:     entry.Name,
		IR:     entry,
		Needs:  needs,
		Verify: mcpVerifySteps,
	}
	unsupported := func(reason string) CopyItem {
		item.Action = ActionUnsupported
		item.Reason = reason
		return item
	}

	// Support checks (ARCH/16 §6.3): a shape the target cannot represent is
	// reported with the reason, never dropped and never guessed.
	if !tgt.HasEntrySpec {
		return unsupported(fmt.Sprintf(
			"LPSM-COPY-002: %s declares no MCP entry layout, so this entry cannot be represented there", tgt.To))
	}
	if item.ID == "" {
		return unsupported("LPSM-COPY-002: the source entry has no name to store it under")
	}
	if err := host.ValidateServerEntryName(entry.Name); err != nil {
		return unsupported(fmt.Sprintf("LPSM-COPY-002: %v", err))
	}
	// One entry, one transport: an IR with neither command nor endpoint names
	// nothing to run, and one with both would leave the target to choose.
	if entry.Command == "" && entry.Endpoint == "" {
		return unsupported("LPSM-COPY-002: the source entry has no command to run and no endpoint (URL) to connect to")
	}
	if entry.Command != "" && entry.Endpoint != "" {
		return unsupported(fmt.Sprintf(
			"LPSM-COPY-002: the source entry %q names both a command and a remote endpoint; an entry is one transport", item.ID))
	}
	if entry.Endpoint != "" {
		// Remote support check: the target must have a verified remote
		// spelling AND must accept this transport. Either miss is the
		// explicit not-copyable refusal — the row is reported with its
		// reason, and Writable() never includes it, so nothing is written.
		if tgt.Remote == nil {
			return unsupported(fmt.Sprintf(
				"LPSM-COPY-002: %s is not copyable to %s: that host cannot express a remote (URL) MCP entry "+
					"(no URL key or transport discriminator was ever verified for it), and copy never rewrites a remote entry as stdio",
				item.ID, tgt.To))
		}
		if !tgt.Remote.Supports(entry.Transport) {
			return unsupported(fmt.Sprintf(
				"LPSM-COPY-002: %s is not copyable to %s: transport %q is not one that host documents for a remote entry (it supports %v)",
				item.ID, tgt.To, entry.Transport, tgt.Remote.Transports))
		}
		// No host documents a name→header mapping for remote entries, so
		// forwarded variables cannot travel with a URL (they are still listed
		// under Needs for the user to bind out of band).
		if len(entry.EnvNames) > 0 {
			return unsupported(fmt.Sprintf(
				"LPSM-COPY-002: %s could not carry the forwarded variables %s to %s: a remote (URL) entry has no documented "+
					"environment field on any host; the names remain listed under Needs",
				item.ID, strings.Join(entry.EnvNames, ", "), tgt.To))
		}
	}
	if len(entry.EnvNames) > 0 && !tgt.CanForwardEnv {
		return unsupported(fmt.Sprintf(
			"LPSM-COPY-002: %s has no documented environment-forwarding behaviour, so %s could not be carried; the names remain listed under Needs",
			tgt.To, strings.Join(entry.EnvNames, ", ")))
	}

	// Translate: a stdio shape change is the only translation there is
	// (D-028 — the IR is the same object on both sides). A remote entry is
	// written through the TARGET's own remote spelling, so its argv shape is
	// irrelevant: there is nothing to translate, and reporting one would
	// describe an argv change that never happens.
	writeAction := ActionDirect
	if entry.Endpoint == "" &&
		src.EntryShape != "" && tgt.EntryShape != "" && src.EntryShape != tgt.EntryShape {
		writeAction = ActionTranslated
		item.TargetDiff = shapeChange(src.EntryShape, tgt.EntryShape)
	}
	// L2 ("does the command resolve on this machine") is a stdio question; a
	// remote entry has no command to resolve, so only L1 is declared for it.
	if entry.Endpoint != "" {
		item.Verify = []string{VerifyL1Config}
	}

	// Conflict detection by name, classification by fingerprint.
	existing, ok := tgt.ExistingMCP[item.ID]
	if !ok {
		item.Action = writeAction
		return item
	}
	targetIR := IRFromHostEntry(existing)
	c := Conflict{
		Kind:              KindMCP,
		ID:                item.ID,
		SourceFingerprint: Fingerprint(entry),
		TargetFingerprint: Fingerprint(targetIR),
		Detail:            "target exists, sources differ",
	}
	if c.SourceFingerprint == c.TargetFingerprint {
		item.Action = ActionSkipped
		item.Reason = "already identical on the target"
		return item
	}
	return applyStrategy(plan, &item, c, strategy, writeAction)
}

// planSkillItem runs one skill through support → conflict.
func planSkillItem(plan *CopyPlan, tgt *TargetCaps, s SourceSkill, strategy string,
	excluded map[string]bool, matchesItems func(string) bool) CopyItem {
	if !matchesItems(s.Name) {
		return CopyItem{}
	}
	if excluded[s.Name] {
		return CopyItem{
			Kind: KindSkill, ID: s.Name, Action: ActionSkipped,
			Reason: "excluded by --exclude",
		}
	}
	item := CopyItem{
		Kind:   KindSkill,
		ID:     s.Name,
		Skill:  &SkillRecord{Name: s.Name, ContentDigest: s.ContentDigest, SourceDir: s.SourceDir},
		Verify: skillVerifySteps,
	}
	unsupported := func(reason string) CopyItem {
		item.Action = ActionUnsupported
		item.Reason = reason
		return item
	}

	if tgt.SkillDir == "" {
		return unsupported(fmt.Sprintf(
			"LPSM-COPY-002: %s has no documented skills directory, so a skill cannot be written there", tgt.To))
	}
	if s.SourceDir == "" {
		return unsupported("LPSM-COPY-002: the source skill's directory is missing or unreadable, so there is nothing to copy; reinstall it on the source agent to make it portable")
	}
	if s.ContentDigest == "" {
		return unsupported("LPSM-COPY-002: the source skill's content digest could not be captured, so a copy could not be verified")
	}

	existing, ok := tgt.ExistingSkills[s.Name]
	if !ok {
		item.Action = ActionDirect
		return item
	}
	c := Conflict{
		Kind:              KindSkill,
		ID:                s.Name,
		SourceFingerprint: s.ContentDigest,
		TargetFingerprint: existing.Digest,
		Detail:            "target already has this skill, with different content",
	}
	if existing.Digest != "" && existing.Digest == s.ContentDigest {
		// Same content already at the target: not a conflict.
		item.Action = ActionSkipped
		item.Reason = "already identical on the target"
		return item
	}
	if existing.Digest == "" {
		c.Detail = "target already has this skill, and its content digest is unknown"
	}
	if strategy == StrategyUseSource && !existing.Managed {
		// Copy never deletes a tree LiteSPM did not create: an unmanaged
		// target directory can be replaced only by the user, so the strategy
		// cannot be honored and the row says so instead of half-doing it.
		c.Resolution = StrategyUseSource
		plan.Conflicts = append(plan.Conflicts, c)
		item.Action = ActionSkipped
		item.Reason = "conflict: use-source refused — the target skill directory was not created by LiteSPM, so copy will not overwrite it"
		return item
	}
	return applyStrategy(plan, &item, c, strategy, ActionDirect)
}

// applyStrategy records the conflict on the plan and turns it into the
// item's action under the chosen strategy.
func applyStrategy(plan *CopyPlan, item *CopyItem, c Conflict, strategy, writeAction string) CopyItem {
	action, reason, err := ResolveConflict(strategy, c, writeAction)
	if err != nil {
		// BuildPlan validated the strategy up front, so this is unreachable;
		// an unknown strategy must still never look like a writable row.
		action, reason = ActionUnsupported, "LPSM-COPY-004: "+err.Error()
	}
	item.Action = action
	item.Reason = reason
	if strategy != StrategyAsk {
		c.Resolution = strategy
	}
	plan.Conflicts = append(plan.Conflicts, c)
	return *item
}

// shapeChange renders the human one-liner for a translation row.
func shapeChange(from, to string) string {
	switch {
	case from == string(host.ShapeLocalArray) && to == string(host.ShapeObject):
		return "combined command array → command + args"
	case from == string(host.ShapeObject) && to == string(host.ShapeLocalArray):
		return "command + args → combined command array"
	case from == string(host.ShapeObject) && to == string(host.ShapeStdioTyped):
		return "command + args → command + args + type:\"stdio\""
	case from == string(host.ShapeStdioTyped) && to == string(host.ShapeObject):
		return "command + args + type → command + args"
	default:
		return fmt.Sprintf("%s entry → %s entry", from, to)
	}
}

// itemIndex finds a row by kind and id; -1 when there is none.
func (p *CopyPlan) itemIndex(kind, id string) int {
	for i, it := range p.Items {
		if it.Kind == kind && it.ID == id {
			return i
		}
	}
	return -1
}

// Unresolved returns the conflicts still waiting for a decision — the empty
// slice for every strategy but `ask`.
func (p *CopyPlan) Unresolved() []Conflict {
	var out []Conflict
	for _, c := range p.Conflicts {
		if c.Resolution == "" {
			out = append(out, c)
		}
	}
	return out
}

// ResolveConflicts applies explicit per-conflict decisions — the answers an
// interactive `ask` pass collected, or the non-interactive default — to the
// plan, rewriting each unresolved row and recording its resolution.
// decisions is keyed by Conflict.Key(); a conflict with no decision is an
// error, because an unresolved conflict must never reach the apply phase
// (LPSM-COPY-004).
func (p *CopyPlan) ResolveConflicts(decisions map[string]string) error {
	for i := range p.Conflicts {
		c := p.Conflicts[i]
		if c.Resolution != "" {
			continue
		}
		decision, ok := decisions[c.Key()]
		if !ok {
			return fmt.Errorf("LPSM-COPY-004: conflict %s %s has no resolution; choose --conflict keep-target|use-source|compatible|skip, or answer the prompt",
				c.Kind, c.ID)
		}
		strategy, err := ParseStrategy(decision)
		if err != nil {
			return fmt.Errorf("LPSM-COPY-004: conflict %s %s: %w", c.Kind, c.ID, err)
		}
		if strategy == StrategyAsk {
			return fmt.Errorf("LPSM-COPY-004: conflict %s %s is still unresolved (strategy ask)", c.Kind, c.ID)
		}
		idx := p.itemIndex(c.Kind, c.ID)
		if idx < 0 {
			return fmt.Errorf("conflict %s %s has no plan row", c.Kind, c.ID)
		}
		// The write action the row would take if the source wins: a
		// translated row stays translated through a use-source resolution.
		writeAction := ActionDirect
		if p.Items[idx].TargetDiff != "" {
			writeAction = ActionTranslated
		}
		action, reason, rerr := ResolveConflict(strategy, c, writeAction)
		if rerr != nil {
			return fmt.Errorf("LPSM-COPY-004: conflict %s %s: %w", c.Kind, c.ID, rerr)
		}
		p.Items[idx].Action = action
		p.Items[idx].Reason = reason
		c.Resolution = strategy
		p.Conflicts[i] = c
	}
	return nil
}

// Overwrites returns the kind/id keys whose resolution replaces the target
// entry (strategy use-source), so apply can pass Force to the normal install
// path — ordinary force semantics, with the backup and ledger rows that
// implies.
func (p *CopyPlan) Overwrites() map[string]bool {
	out := map[string]bool{}
	for _, c := range p.Conflicts {
		if c.Resolution == StrategyUseSource {
			out[c.Key()] = true
		}
	}
	return out
}

// Writable returns the rows apply will write: direct and translated only.
func (p *CopyPlan) Writable() []CopyItem {
	var out []CopyItem
	for _, it := range p.Items {
		if it.Action == ActionDirect || it.Action == ActionTranslated {
			out = append(out, it)
		}
	}
	return out
}

// Count returns how many rows carry an action.
func (p *CopyPlan) Count(action string) int {
	n := 0
	for _, it := range p.Items {
		if it.Action == action {
			n++
		}
	}
	return n
}

// CountKind returns per-kind counts for an action.
func (p *CopyPlan) CountKind(action string) map[string]int {
	out := map[string]int{}
	for _, it := range p.Items {
		if it.Action == action {
			out[it.Kind]++
		}
	}
	return out
}

// AllNeeds returns the sorted union of every row's Needs: the names this copy
// will ask the target to provide. Names only.
func (p *CopyPlan) AllNeeds() []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range p.Items {
		for _, n := range it.Needs {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}
