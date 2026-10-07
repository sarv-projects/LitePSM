package main

import (
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
// approval, for tests that call installSkillFromListing directly.
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
	var plan domain.InstallPlan
	if err := h.client.Call(context.Background(), "resolver.prepare_plan", map[string]any{
		"id": listingID, "version": "1.0.0", "scope": "user",
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
	planA := preparePlan(t, h, listingID)
	planB := preparePlan(t, h, listingID)
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
	grant, err := authorizeHumanInstall(ctx, h.db, h.catClient, listing, "1.0.0", domain.ScopeUser)
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
