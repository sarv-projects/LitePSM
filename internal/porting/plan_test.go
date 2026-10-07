package porting

import (
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/host"
)

func testSource() *Source {
	return &Source{
		From:       "opencode",
		Display:    "OpenCode",
		EntryShape: string(host.ShapeLocalArray),
		MCP: []SourceMCP{
			{Name: "engram", Command: "engram", Args: []string{"mcp", "--tools=agent,graph"}},
			{Name: "github", Command: "gh", Args: []string{"mcp"},
				Env: map[string]string{"GITHUB_TOKEN": "ghp_literal_value"}},
		},
	}
}

func testTarget() *TargetCaps {
	return &TargetCaps{
		To:             "codex",
		Display:        "Codex",
		HasEntrySpec:   true,
		EntryShape:     string(host.ShapeObject),
		CanForwardEnv:  true,
		ExistingMCP:    map[string]host.HostServerEntry{},
		ExistingSkills: map[string]TargetSkill{},
		SkillDir:       "/tmp/target/skills",
	}
}

// TestBuildPlanCountsAndShapes is the direct/translated split: translation is
// a property of the two hosts' entry shapes, so an OpenCode combined command
// array is a translated row against Codex's command + args shape, and a
// shape-identical pair produces direct rows.
func TestBuildPlanCountsAndShapes(t *testing.T) {
	plan, err := BuildPlan(testSource(), testTarget(), CopyOptions{From: "opencode", To: "codex"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Found[KindMCP] != 2 {
		t.Errorf("Found[mcp] = %d, want 2", plan.Found[KindMCP])
	}
	if got := plan.CountKind(ActionTranslated); got[KindMCP] != 2 {
		t.Errorf("translated mcp = %d, want 2 (local-array source → object target)", got[KindMCP])
	}
	if got := plan.CountKind(ActionDirect); got[KindMCP] != 0 {
		t.Errorf("direct mcp = %d, want 0", got[KindMCP])
	}
	for _, it := range plan.Items {
		if it.Action == ActionTranslated && !strings.Contains(it.TargetDiff, "command + args") {
			t.Errorf("translated row %s has no shape diff: %q", it.ID, it.TargetDiff)
		}
	}
	sameShape := testTarget()
	sameShape.EntryShape = string(host.ShapeLocalArray)
	plan, err = BuildPlan(testSource(), sameShape, CopyOptions{From: "opencode", To: "kilo"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := plan.CountKind(ActionDirect); got[KindMCP] != 2 {
		t.Errorf("shape-identical direct mcp = %d, want 2", got[KindMCP])
	}
	if plan.Unchanged != 0 || len(plan.Conflicts) != 0 {
		t.Errorf("empty target produced %d unchanged / %d conflicts", plan.Unchanged, len(plan.Conflicts))
	}
	// The literal env name is reported on the row.
	for _, it := range plan.Items {
		if it.ID == "github" && len(it.Needs) == 1 && it.Needs[0] == "GITHUB_TOKEN" {
			return
		}
	}
	t.Error("github row does not report its Needs name")
}

// TestBuildPlanConflictClassification pins the classification rule:
// identical fingerprint → unchanged (no conflict), different → conflict
// awaiting the `ask` strategy's decision.
func TestBuildPlanConflictClassification(t *testing.T) {
	src := testSource()
	tgt := testTarget()
	// Identical engram on the target, divergent github.
	tgt.ExistingMCP["engram"] = host.HostServerEntry{
		Name: "engram", Command: "engram", Args: []string{"mcp", "--tools=agent,graph"},
	}
	tgt.ExistingMCP["github"] = host.HostServerEntry{
		Name: "github", Command: "something-else",
	}

	plan, err := BuildPlan(src, tgt, CopyOptions{From: "opencode", To: "codex"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1", plan.Unchanged)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].ID != "github" {
		t.Fatalf("Conflicts = %+v, want exactly github", plan.Conflicts)
	}
	if plan.Conflicts[0].Resolution != "" {
		t.Errorf("ask strategy must leave the conflict unresolved, got %q", plan.Conflicts[0].Resolution)
	}
	if plan.Conflicts[0].SourceFingerprint == plan.Conflicts[0].TargetFingerprint {
		t.Error("a conflict must carry differing fingerprints")
	}
	if len(plan.Unresolved()) != 1 {
		t.Errorf("Unresolved() = %d, want 1", len(plan.Unresolved()))
	}

	// keep-target resolves it through the plan, and the row says why.
	if err := plan.ResolveConflicts(map[string]string{
		ConflictKey(KindMCP, "github"): StrategyKeepTarget,
	}); err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if len(plan.Unresolved()) != 0 {
		t.Errorf("still %d unresolved after a decision", len(plan.Unresolved()))
	}
	for _, it := range plan.Items {
		if it.ID == "github" && it.Action != ActionSkipped {
			t.Errorf("github action = %q, want skipped after keep-target", it.Action)
		}
	}
	// A second resolution pass with no decisions must refuse, never guess.
	plan2, _ := BuildPlan(src, tgt, CopyOptions{From: "opencode", To: "codex"})
	if err := plan2.ResolveConflicts(map[string]string{}); err == nil || !strings.Contains(err.Error(), "LPSM-COPY-004") {
		t.Errorf("missing decision must fail with LPSM-COPY-004, got %v", err)
	}
}

func TestBuildPlanFilters(t *testing.T) {
	src := testSource()
	src.Skills = []SourceSkill{{Name: "demo-skill", SourceDir: "/src/demo-skill", ContentDigest: "sha256:aa"}}
	tgt := testTarget()

	// A kind selector narrows the rows; Found still reports the inventory.
	plan, err := BuildPlan(src, tgt, CopyOptions{Kinds: []string{KindSkill}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Kind != KindSkill {
		t.Errorf("kind filter rows = %+v, want only the skill", plan.Items)
	}
	if plan.Found[KindMCP] != 2 {
		t.Errorf("Found must still report the source inventory, got %d", plan.Found[KindMCP])
	}

	// An item selector narrows to one id.
	plan, err = BuildPlan(src, tgt, CopyOptions{Items: []string{"github"}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].ID != "github" {
		t.Errorf("item filter rows = %+v, want only github", plan.Items)
	}

	// --exclude produces an explicit skipped row, never a silent omission.
	plan, err = BuildPlan(src, tgt, CopyOptions{Exclude: []string{"github"}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	found := false
	for _, it := range plan.Items {
		if it.ID == "github" {
			found = true
			if it.Action != ActionSkipped || it.Reason != "excluded by --exclude" {
				t.Errorf("excluded row = %+v", it)
			}
		}
	}
	if !found {
		t.Error("--exclude must report the excluded item, not drop it")
	}

	// A requested id the source does not have is reported as NOT FOUND.
	plan, err = BuildPlan(src, tgt, CopyOptions{Items: []string{"engram", "nope"}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.NotFound) != 1 || plan.NotFound[0] != "nope" {
		t.Errorf("NotFound = %v, want [nope]", plan.NotFound)
	}
}

// TestBuildPlanReportsUnsupportedPluginWithReason: kinds v1 cannot copy are
// reported per item with LPSM-COPY-003, and selecting such a kind produces a
// kind-level note instead of a silent empty plan.
func TestBuildPlanReportsUnsupportedPluginWithReason(t *testing.T) {
	src := testSource()
	src.Other = []SourceOther{{Kind: KindPlugin, Name: "toolx"}}
	tgt := testTarget()

	plan, err := BuildPlan(src, tgt, CopyOptions{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var row *CopyItem
	for i := range plan.Items {
		if plan.Items[i].Kind == KindPlugin {
			row = &plan.Items[i]
		}
	}
	if row == nil {
		t.Fatal("plugin row missing: an unsupported kind must never be silently dropped")
	}
	if row.Action != ActionUnsupported || !strings.Contains(row.Reason, "LPSM-COPY-003") {
		t.Errorf("plugin row = %+v, want unsupported with LPSM-COPY-003", *row)
	}
	if plan.Found[KindPlugin] != 1 {
		t.Errorf("Found[plugin] = %d, want 1", plan.Found[KindPlugin])
	}

	plan, err = BuildPlan(src, tgt, CopyOptions{Kinds: []string{KindPlugin}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.UnsupportedNotes) == 0 || !strings.Contains(plan.UnsupportedNotes[0], "LPSM-COPY-003") {
		t.Errorf("selecting plugins must report LPSM-COPY-003, got %v", plan.UnsupportedNotes)
	}
}

// TestBuildPlanUnsupportedTargetShapes covers the two support-check refusals
// (ARCH/16 §6.3): a target with no entry layout, and a target that cannot
// express a forwarded variable the source carries.
func TestBuildPlanUnsupportedTargetShapes(t *testing.T) {
	src := testSource()
	tgt := testTarget()
	tgt.HasEntrySpec = false

	plan, err := BuildPlan(src, tgt, CopyOptions{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := plan.CountKind(ActionUnsupported); got[KindMCP] != 2 {
		t.Fatalf("unsupported mcp = %d, want 2: %+v", got[KindMCP], plan.Items)
	}
	for _, it := range plan.Items {
		if !strings.Contains(it.Reason, "LPSM-COPY-002") {
			t.Errorf("row %s reason %q lacks LPSM-COPY-002", it.ID, it.Reason)
		}
	}

	// Environment forwarding: a reference-carrying entry against a target
	// with no documented behaviour is unsupported, with the names kept.
	src2 := &Source{
		From: "opencode", Display: "OpenCode", EntryShape: string(host.ShapeLocalArray),
		MCP: []SourceMCP{{Name: "cred", Command: "npx", Env: map[string]string{"API_KEY": "${API_KEY}"}}},
	}
	tgt2 := testTarget()
	tgt2.CanForwardEnv = false
	plan, err = BuildPlan(src2, tgt2, CopyOptions{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Action != ActionUnsupported {
		t.Fatalf("row = %+v, want unsupported", plan.Items)
	}
	if !strings.Contains(plan.Items[0].Reason, "LPSM-COPY-002") ||
		!strings.Contains(plan.Items[0].Reason, "API_KEY") {
		t.Errorf("reason must carry the code and the name: %q", plan.Items[0].Reason)
	}
	if len(plan.Items[0].Needs) != 1 || plan.Items[0].Needs[0] != "API_KEY" {
		t.Errorf("Needs = %v, want [API_KEY] even when unsupported", plan.Items[0].Needs)
	}
}

func TestBuildPlanSkillActions(t *testing.T) {
	src := &Source{
		From: "codex", Display: "Codex",
		Skills: []SourceSkill{
			{Name: "fresh", SourceDir: "/src/fresh", ContentDigest: "sha256:aa"},
			{Name: "same", SourceDir: "/src/same", ContentDigest: "sha256:bb"},
			{Name: "divergent", SourceDir: "/src/div", ContentDigest: "sha256:cc"},
			{Name: "gone", SourceDir: "", ContentDigest: "sha256:dd"},
		},
	}
	tgt := testTarget()
	tgt.ExistingSkills = map[string]TargetSkill{
		"same":      {Dir: "/tgt/same", Digest: "sha256:bb", Managed: true},
		"divergent": {Dir: "/tgt/div", Digest: "sha256:xx", Managed: true},
	}

	plan, err := BuildPlan(src, tgt, CopyOptions{From: "codex", To: "opencode"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	byID := map[string]CopyItem{}
	for _, it := range plan.Items {
		byID[it.ID] = it
	}
	if byID["fresh"].Action != ActionDirect {
		t.Errorf("fresh = %q, want direct", byID["fresh"].Action)
	}
	if byID["same"].Action != ActionSkipped || plan.Unchanged != 1 {
		t.Errorf("same = %q (unchanged %d), want skipped/unchanged", byID["same"].Action, plan.Unchanged)
	}
	if byID["divergent"].Action != ActionConflict {
		t.Errorf("divergent = %q, want conflict", byID["divergent"].Action)
	}
	if byID["gone"].Action != ActionUnsupported ||
		!strings.Contains(byID["gone"].Reason, "LPSM-COPY-002") {
		t.Errorf("gone = %+v, want unsupported LPSM-COPY-002", byID["gone"])
	}
	if len(byID["fresh"].Verify) == 0 || byID["fresh"].Verify[0] != VerifyL1SkillDir {
		t.Errorf("skill verify steps = %v", byID["fresh"].Verify)
	}

	// A target with no skills directory makes every skill unsupported.
	tgtNoDir := testTarget()
	tgtNoDir.SkillDir = ""
	plan, err = BuildPlan(src, tgtNoDir, CopyOptions{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := plan.CountKind(ActionUnsupported); got[KindSkill] != 4 {
		t.Errorf("unsupported skills = %d, want 4", got[KindSkill])
	}
}

// TestBuildPlanSkillUseSourceRefusesUnmanagedTrees: copy never deletes a
// directory LiteSPM did not create, even under an explicit use-source.
func TestBuildPlanSkillUseSourceRefusesUnmanagedTrees(t *testing.T) {
	src := &Source{
		From: "codex",
		Skills: []SourceSkill{
			{Name: "managed", SourceDir: "/src/managed", ContentDigest: "sha256:aa"},
			{Name: "handmade", SourceDir: "/src/handmade", ContentDigest: "sha256:bb"},
		},
	}
	tgt := testTarget()
	tgt.ExistingSkills = map[string]TargetSkill{
		"managed":  {Dir: "/tgt/managed", Digest: "sha256:old", Managed: true},
		"handmade": {Dir: "/tgt/handmade", Digest: "sha256:old", Managed: false},
	}

	plan, err := BuildPlan(src, tgt, CopyOptions{Conflict: StrategyUseSource})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	byID := map[string]CopyItem{}
	for _, it := range plan.Items {
		byID[it.ID] = it
	}
	if byID["managed"].Action != ActionDirect {
		t.Errorf("managed use-source = %q, want direct (installer update path)", byID["managed"].Action)
	}
	if byID["handmade"].Action != ActionSkipped ||
		!strings.Contains(byID["handmade"].Reason, "not created by LiteSPM") {
		t.Errorf("handmade use-source = %+v, want an explicit refusal", byID["handmade"])
	}
}

// TestBuildPlanUseSourceMarksOverwrites: only conflicts resolved to
// use-source become overwrite rows; the apply phase reads them back through
// Overwrites().
func TestBuildPlanUseSourceMarksOverwrites(t *testing.T) {
	src := testSource()
	tgt := testTarget()
	tgt.ExistingMCP["github"] = host.HostServerEntry{Name: "github", Command: "different"}

	plan, err := BuildPlan(src, tgt, CopyOptions{Conflict: StrategyUseSource})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	overwrites := plan.Overwrites()
	if !overwrites[ConflictKey(KindMCP, "github")] {
		t.Errorf("Overwrites = %v, want github marked", overwrites)
	}
	writable := plan.Writable()
	found := false
	for _, it := range writable {
		if it.ID == "github" {
			found = true
		}
	}
	if !found {
		t.Error("a use-source conflict must end up writable")
	}

	// keep-target: nothing writable from that conflict.
	plan, err = BuildPlan(src, tgt, CopyOptions{Conflict: StrategyKeepTarget})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Overwrites()) != 0 {
		t.Errorf("keep-target must mark no overwrites, got %v", plan.Overwrites())
	}
}

func TestBuildPlanRejectsInvalidStrategyAndNilInputs(t *testing.T) {
	if _, err := BuildPlan(testSource(), testTarget(), CopyOptions{Conflict: "sideways"}); err == nil {
		t.Error("an invalid --conflict value was accepted")
	}
	if _, err := BuildPlan(nil, testTarget(), CopyOptions{}); err == nil {
		t.Error("nil source accepted")
	}
	if _, err := BuildPlan(testSource(), nil, CopyOptions{}); err == nil {
		t.Error("nil target accepted")
	}
}

func TestAllNeedsIsTheSortedUnion(t *testing.T) {
	plan := &CopyPlan{Items: []CopyItem{
		{Kind: KindMCP, ID: "a", Needs: []string{"B_TOKEN"}},
		{Kind: KindMCP, ID: "b", Needs: []string{"A_TOKEN", "B_TOKEN"}},
	}}
	got := plan.AllNeeds()
	if len(got) != 2 || got[0] != "A_TOKEN" || got[1] != "B_TOKEN" {
		t.Errorf("AllNeeds = %v, want [A_TOKEN B_TOKEN]", got)
	}
}
