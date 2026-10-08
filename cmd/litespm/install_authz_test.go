package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// authorizedTestContext stands in for a context built after a consumed
// approval, for tests that call a gated installer directly.
func authorizedTestContext() context.Context {
	return withInstallAuthorization(context.Background(), installAuthorization{
		Origin: approvalActorHumanCLI, ApprovalID: "appr_unit_test", PlanID: "plan_unit_test",
	})
}

// approvePlanAsHuman records an approval exactly as `litespm approve` does.
func approvePlanAsHuman(t *testing.T, db *state.DB, plan *domain.InstallPlan) string {
	t.Helper()
	id, err := recordHumanApproval(context.Background(), db, plan)
	if err != nil {
		t.Fatalf("recordHumanApproval: %v", err)
	}
	return id
}

// isolatedSkillHome points HOME at a temp dir with exactly one detectable agent
// (cursor) so a real skill install is hermetic.
func isolatedSkillHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	for _, a := range skills.AgentTargets() {
		if a.EnvVar != "" {
			t.Setenv(a.EnvVar, "")
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func skillHarness(t *testing.T) (*harness, string, string) {
	t.Helper()
	home := isolatedSkillHome(t)
	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "authz fixture")
	const listingID = "skill:test:demo-skill"
	h := newHarnessWith(t, []*domain.Listing{{
		SchemaVersion:  2,
		ID:             listingID,
		Kind:           domain.KindSkill,
		Name:           "demo-skill",
		Summary:        "authz fixture",
		Source:         domain.SourceReference{SourceID: "test", UpstreamID: "demo-skill", URL: skillDir},
		Versions:       []domain.VersionSummary{{Version: "1.0.0"}},
		Installability: domain.InstallabilityMetadataVerified,
	}})
	return h, listingID, home
}

func preparePlan(t *testing.T, h *harness, listingID string) domain.InstallPlan {
	t.Helper()
	return preparePlanScoped(t, h, listingID, "user")
}

func preparePlanScoped(t *testing.T, h *harness, listingID, scope string) domain.InstallPlan {
	t.Helper()
	var plan domain.InstallPlan
	if err := h.client.Call(context.Background(), "resolver.prepare_plan", map[string]any{
		"id": listingID, "version": "1.0.0", "scope": scope,
	}, &plan); err != nil {
		t.Fatalf("prepare_plan: %v", err)
	}
	return plan
}

func skillWritten(home string) bool {
	_, err := os.Stat(filepath.Join(home, ".cursor", "skills", "demo-skill", "SKILL.md"))
	return err == nil
}

// The agent path must not authorize itself: install.execute without a plan, or
// with a plan but no approval, is refused and writes nothing.
func TestInstallExecute_AgentPathWithoutApprovalIsRefused(t *testing.T) {
	h, listingID, home := skillHarness(t)
	ctx := context.Background()
	plan := preparePlan(t, h, listingID)

	cases := map[string]map[string]any{
		"no plan, listing only":  {"listingId": listingID},
		"plan without approval":  {"planId": plan.PlanID},
		"plan, empty token":      {"planId": plan.PlanID, "approvalToken": ""},
		"plan, unknown approval": {"planId": plan.PlanID, "approvalToken": "appr_does_not_exist"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			var raw json.RawMessage
			err := h.client.Call(ctx, "install.execute", params, &raw)
			if code := rpcCode(t, err); code != ipc.CodeUnauthorized {
				t.Fatalf("code=%d, want %d (err=%v)", code, ipc.CodeUnauthorized, err)
			}
		})
	}
	if skillWritten(home) {
		t.Fatal("a refused install wrote the skill")
	}
	recs, _ := h.db.ListInstalls(ctx, domain.ScopeUser, "")
	if len(recs) != 0 {
		t.Fatalf("a refused install recorded %d installs", len(recs))
	}
}

// An approval row that was not recorded by a human at a terminal (for example
// one an agent managed to insert) does not open the gate.
func TestInstallExecute_NonHumanApprovalRefused(t *testing.T) {
	h, listingID, home := skillHarness(t)
	plan := preparePlan(t, h, listingID)
	exp := time.Now().Add(time.Hour)
	if err := h.db.RecordApproval(context.Background(), "appr_agent_made", approvalSubjectInstallPlan, plan.PlanHash,
		"agent", "mcp-elicitation", "user", &exp); err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	err := h.client.Call(context.Background(), "install.execute", map[string]any{"planId": plan.PlanID, "approvalToken": "appr_agent_made"}, &raw)
	if code := rpcCode(t, err); code != ipc.CodeUnauthorized {
		t.Fatalf("code=%d, want unauthorized (err=%v)", code, err)
	}
	if skillWritten(home) {
		t.Fatal("non-human approval allowed a write")
	}
}

// An approval is bound to the plan it was granted for.
func TestInstallExecute_ApprovalBoundToPlan(t *testing.T) {
	h, listingID, home := skillHarness(t)
	// planB must be execution-distinct from planA: ComputePlanHash is
	// deliberately invariant to planId/createdAt, so two content-identical
	// plans are the same approval subject. A different target scope gives the
	// second plan a different hash — which is exactly what an approval for
	// planA must not authorize.
	planA := preparePlan(t, h, listingID)
	planB := preparePlanScoped(t, h, listingID, "project")
	if planA.PlanHash == planB.PlanHash {
		t.Fatal("test setup: distinct plans must hash differently")
	}
	approvalForA := approvePlanAsHuman(t, h.db, &planA)

	var raw json.RawMessage
	err := h.client.Call(context.Background(), "install.execute", map[string]any{"planId": planB.PlanID, "approvalToken": approvalForA}, &raw)
	if code := rpcCode(t, err); code != ipc.CodeUnauthorized || !strings.Contains(err.Error(), "SUBJECT-MISMATCH") {
		t.Fatalf("want unauthorized subject mismatch, got code=%d err=%v", code, err)
	}
	if skillWritten(home) {
		t.Fatal("approval for another plan allowed a write")
	}
	// The mismatch did not burn the approval: it still works for plan A.
	var result map[string]any
	if err := h.client.Call(context.Background(), "install.execute", map[string]any{"planId": planA.PlanID, "approvalToken": approvalForA}, &result); err != nil {
		t.Fatalf("approval must still authorize its own plan: %v", err)
	}
	if !skillWritten(home) {
		t.Fatal("authorized install did not write the skill")
	}
}

// An approval authorizes exactly one install (replay prevention).
func TestInstallExecute_ApprovalIsSingleUse(t *testing.T) {
	h, listingID, _ := skillHarness(t)
	plan := preparePlan(t, h, listingID)
	id := approvePlanAsHuman(t, h.db, &plan)
	params := map[string]any{"planId": plan.PlanID, "approvalToken": id}

	var result map[string]any
	if err := h.client.Call(context.Background(), "install.execute", params, &result); err != nil {
		t.Fatalf("first install: %v", err)
	}
	var raw json.RawMessage
	err := h.client.Call(context.Background(), "install.execute", params, &raw)
	if code := rpcCode(t, err); code != ipc.CodeUnauthorized || !strings.Contains(err.Error(), "APPROVAL-CONSUMED") {
		t.Fatalf("replay must be refused as consumed, got code=%d err=%v", code, err)
	}
}

// A failure before the effect refunds the approval so the human need not
// approve again.
func TestInstallExecute_FailureRefundsApproval(t *testing.T) {
	h, listingID, home := skillHarness(t)
	plan := preparePlan(t, h, listingID)
	id := approvePlanAsHuman(t, h.db, &plan)
	params := map[string]any{"planId": plan.PlanID, "approvalToken": id}

	// Make the install fail at "no agent detected": remove the agent dir.
	if err := os.RemoveAll(filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if err := h.client.Call(context.Background(), "install.execute", params, &raw); err == nil {
		t.Fatal("expected the install to fail with no agent directories")
	}
	rec, err := h.db.GetApproval(context.Background(), id)
	if err != nil || rec.Status != "active" {
		t.Fatalf("approval must be refunded after a failed install, got %+v err=%v", rec, err)
	}

	// Fixing the cause and retrying with the same approval succeeds.
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := h.client.Call(context.Background(), "install.execute", params, &result); err != nil {
		t.Fatalf("retry with the refunded approval: %v", err)
	}
	if !skillWritten(home) {
		t.Fatal("retry did not write the skill")
	}
}

// installSkillFromListing itself refuses a context that carries no consumed
// approval: the old "this request is the authorization" auto-approval is gone.
func TestInstallSkillFromListing_RequiresAuthorization(t *testing.T) {
	db := openTestState(t)
	project, home := t.TempDir(), t.TempDir()
	agent, _ := projectAgent(t, project, home)
	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "x")
	listing := skillListingFor("skill:example:demo-skill", "demo-skill", skillDir)

	_, err := installSkillFromListing(context.Background(), db, t.TempDir(), project, home,
		listing, "1.0.0", domain.ScopeProject, []string{agent})
	if err == nil || domain.ErrorCode(err) != "LPSM-POLICY-UNAUTHORIZED" {
		t.Fatalf("expected LPSM-POLICY-UNAUTHORIZED, got %v", err)
	}
}

// The human CLI path records a plan and a human-cli approval, consumes it, and
// authorizes the install.
func TestAuthorizeHumanInstall_RecordsHumanCLIApproval(t *testing.T) {
	h, listingID, _ := skillHarness(t)
	listing, err := h.catClient.GetListing(listingID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	grant, _, err := authorizeHumanInstall(ctx, h.db, h.catClient, listing, "1.0.0", domain.ScopeUser, nil)
	if err != nil {
		t.Fatalf("authorizeHumanInstall: %v", err)
	}
	if grant.Origin != approvalActorHumanCLI {
		t.Errorf("origin = %q, want %q", grant.Origin, approvalActorHumanCLI)
	}
	rec, err := h.db.GetApproval(ctx, grant.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Actor != approvalActorHumanCLI || rec.SubjectHash != grant.Plan.PlanHash || rec.Status != "consumed" {
		t.Errorf("approval row = %+v", rec)
	}
	if _, err := h.db.GetPlan(ctx, grant.Plan.PlanID); err != nil {
		t.Errorf("plan was not recorded: %v", err)
	}
	if _, ok := installAuthorizationFrom(grant.context(ctx)); !ok {
		t.Error("grant context carries no authorization")
	}
	grant.refund(ctx, h.db)
	if rec, _ := h.db.GetApproval(ctx, grant.ApprovalID); rec.Status != "active" {
		t.Errorf("refund did not reactivate the approval: %+v", rec)
	}
}

// The CLI policy engine loads the user's deny tier from the data root and
// fails closed on a malformed file; skills commands no longer run on a bare
// policy.NewEngine(nil, nil).
func TestOpenCLIPolicyEngine_LoadsRealTiers(t *testing.T) {
	dataRoot := t.TempDir()
	rules := `[{"ruleId":"no_demo","targetRef":"demo-skill"}]`
	if err := os.WriteFile(filepath.Join(dataRoot, "policy-deny.json"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	eng, closeFn, err := openCLIPolicyEngine(dataRoot)
	if err != nil {
		t.Fatalf("openCLIPolicyEngine: %v", err)
	}
	defer closeFn()

	checker := enginePolicyChecker(eng, func(context.Context, skills.PolicyRequest, string) bool { return true })
	verdict := checker(context.Background(), skills.PolicyRequest{
		Operation: "install", SkillName: "demo-skill", Scope: "project", DestDir: "/tmp/x",
		Effects: []string{"filesystem.write"},
	})
	if verdict.Allowed || verdict.Decision != "deny" {
		t.Fatalf("user deny rule must stop the write even when the asker would approve: %+v", verdict)
	}
	other := checker(context.Background(), skills.PolicyRequest{
		Operation: "install", SkillName: "other", Scope: "project", DestDir: "/tmp/x",
		Effects: []string{"filesystem.write"},
	})
	if !other.Allowed {
		t.Fatalf("unrelated skill should reach the ask tier and be approved: %+v", other)
	}

	if err := os.WriteFile(filepath.Join(dataRoot, "policy-deny.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openCLIPolicyEngine(dataRoot); err == nil {
		t.Fatal("a malformed deny-rules file must fail closed, not be ignored")
	}
}

// --- B1 Phase 3 Step 3.4: the approval prompt must show the runtime --------

// approvalPromptFixture builds an install plan the way buildInstallPlan does,
// with the runtime sealed into every host change's ValueJSON.
func approvalPromptFixture(expires time.Time, valueJSONs ...string) *domain.InstallPlan {
	hostIDs := []string{"codex"}
	if len(valueJSONs) == 2 {
		hostIDs = []string{"codex", "claude-code"}
	}
	paths := map[string]string{
		"codex":       "/home/user/.codex/config.toml",
		"claude-code": "/home/user/.claude.json",
	}
	entries := []string{"remote-demo"}
	plan := &domain.InstallPlan{
		PlanID: "plan_01234567890123456789012345",
		Request: domain.PlanRequest{
			ListingID:   "mcp:example:remote-demo",
			TargetScope: domain.ScopeUser,
		},
		Resolved:  domain.PlanResolved{Version: "1.0.0"},
		Effects:   []string{"package.install", "host.config.write", "network.outbound"},
		ExpiresAt: expires,
	}
	for i, vj := range valueJSONs {
		hostID := hostIDs[0]
		if len(hostIDs) == 2 {
			hostID = hostIDs[i]
		}
		entryKey := entries[0]
		plan.HostChanges = append(plan.HostChanges, domain.HostChange{
			HostID:     hostID,
			ConfigPath: paths[hostID],
			Action:     "register_command",
			EntryKey:   entryKey,
			ValueJSON:  vj,
		})
	}
	return plan
}

// A human approving an endpoint plan must SEE the endpoint. This is the exact
// stdout of `litespm approve <plan-id>` for a remote listing: the endpoint line
// is mandatory and carries the transport, because a plan that printed no
// runtime line would be approving the URL blind.
func TestApprovalPromptPrintsEndpointForRemotePlan(t *testing.T) {
	expires := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	plan := approvalPromptFixture(expires, mcpInstallPlanValueJSON(&mcpInstallBinding{
		Hosts:   []string{"codex"},
		Runtime: &domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "https://mcp.example.com/mcp"},
	}))

	var buf bytes.Buffer
	writeApprovalPrompt(&buf, plan)

	want := `  listing : mcp:example:remote-demo
  version : 1.0.0
  scope   : user
  effects : package.install, host.config.write, network.outbound
  target  : codex -> /home/user/.codex/config.toml (remote-demo)
  endpoint : "https://mcp.example.com/mcp" (streamable-http)
  options : force=false env-names=[]
  expires : 2026-10-08T12:00:00Z
`
	if got := buf.String(); got != want {
		t.Errorf("approval prompt is not the golden text\n got: %q\nwant: %q", got, want)
	}

	// With several targets the runtime line is printed exactly once, and it is
	// never skipped: an endpoint plan must never render with no runtime line.
	two := approvalPromptFixture(expires,
		mcpInstallPlanValueJSON(&mcpInstallBinding{
			Hosts:   []string{"codex"},
			Runtime: &domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "https://mcp.example.com/mcp"},
		}),
		mcpInstallPlanValueJSON(&mcpInstallBinding{
			Hosts:   []string{"claude-code"},
			Runtime: &domain.RuntimeDescriptor{Type: "streamable-http", Endpoint: "https://mcp.example.com/mcp"},
		}),
	)
	buf.Reset()
	writeApprovalPrompt(&buf, two)
	got := buf.String()
	if n := strings.Count(got, `endpoint : "https://mcp.example.com/mcp" (streamable-http)`); n != 1 {
		t.Errorf("endpoint line printed %d times, want exactly 1:\n%s", n, got)
	}
	if n := strings.Count(got, "  target  : "); n != 2 {
		t.Errorf("target lines = %d, want 2:\n%s", n, got)
	}
	if !strings.Contains(got, "  target  : claude-code -> /home/user/.claude.json (remote-demo)") {
		t.Errorf("second target missing:\n%s", got)
	}
}

// The stdio prompt is unchanged: the command line still prints, with the
// options that bind it.
func TestApprovalPromptStillPrintsCommandForStdioPlan(t *testing.T) {
	expires := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	plan := approvalPromptFixture(expires, mcpInstallPlanValueJSON(&mcpInstallBinding{
		Hosts:    []string{"claude-code"},
		Runtime:  &domain.RuntimeDescriptor{Type: "stdio", Command: "npx", Args: []string{"-y", "demo-mcp"}},
		EnvNames: []string{"API_TOKEN"},
		Force:    true,
	}))
	plan.Request.ListingID = "mcp:example:demo-mcp"
	plan.Effects = []string{"package.install", "host.config.write"}
	plan.HostChanges[0].HostID = "claude-code"
	plan.HostChanges[0].ConfigPath = "/home/user/.claude.json"
	plan.HostChanges[0].EntryKey = "demo-mcp"

	var buf bytes.Buffer
	writeApprovalPrompt(&buf, plan)

	want := `  listing : mcp:example:demo-mcp
  version : 1.0.0
  scope   : user
  effects : package.install, host.config.write
  target  : claude-code -> /home/user/.claude.json (demo-mcp)
  command : "npx" ["-y" "demo-mcp"]
  options : force=true env-names=["API_TOKEN"]
  expires : 2026-10-08T12:00:00Z
`
	if got := buf.String(); got != want {
		t.Errorf("stdio approval prompt is not the golden text\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(buf.String(), "endpoint :") {
		t.Errorf("a stdio plan printed an endpoint line:\n%s", buf.String())
	}
}

// A plan whose host changes carry no runtime (a skill install) prints no
// runtime line and no options line — the only legitimate skip, and the plan
// gate rejects such a shape for MCP before a prompt can exist.
func TestApprovalPromptSkipsRuntimeOnlyWhenNoneIsSealed(t *testing.T) {
	expires := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	plan := &domain.InstallPlan{
		PlanID:    "plan_01234567890123456789012345",
		Request:   domain.PlanRequest{ListingID: "skill:example:demo-skill", TargetScope: domain.ScopeProject},
		Resolved:  domain.PlanResolved{Version: "1.0.0"},
		Effects:   []string{"package.install", "filesystem.write"},
		ExpiresAt: expires,
	}
	var buf bytes.Buffer
	writeApprovalPrompt(&buf, plan)
	got := buf.String()
	if strings.Contains(got, "command :") || strings.Contains(got, "endpoint :") || strings.Contains(got, "options :") {
		t.Errorf("a runtime-less plan printed a runtime or options line:\n%s", got)
	}
	if !strings.Contains(got, "  expires : 2026-10-08T12:00:00Z") {
		t.Errorf("prompt lost its expiry line:\n%s", got)
	}
}
