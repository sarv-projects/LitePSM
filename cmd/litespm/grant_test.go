package main

// grant_test.go — acceptance for the invoke-authorization loop:
//
//	policy "ask" → litespm grant (interactive) → active CapabilityGrant →
//	discover.Invoke Tier-3 allow (with schema-drift re-verification)
//
// The daemon-side half is the wire that was missing: provider.invoke ran
// discover.Invoke with no policy option, so EVERY effectful invocation failed
// with "no policy engine configured" and no approval flow could ever fix it.
// These tests pin both halves — the engine is attached, and a grant made by
// issueCapabilityGrant opens the gate.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/discover"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

// seedGrantFixture installs the FK chain (install → component → provider →
// capability) a grant needs, using a command that does not exist so the test
// never spawns anything: the policy gate runs before the dial.
func seedGrantFixture(t *testing.T, db *state.DB) (capabilityID, fingerprint string) {
	t.Helper()
	ctx := context.Background()
	const installID = "inst_user_mcp_grant_1_0_0"
	const component = "demo-mcp"
	now := time.Now().UTC()

	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID: installID, ListingID: "mcp:example:demo-mcp", Kind: domain.KindMCP,
		Version: "1.0.0", Scope: domain.ScopeUser, Status: domain.InstallActive,
		InstalledAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID: installID, Kind: domain.ComponentMCPProvider,
		ComponentName: component, Path: "",
	}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	providerID := discover.ProviderIDFor(installID, component)
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID: providerID, InstallID: installID, ComponentName: component,
		Transport: "stdio", Command: "definitely-not-a-real-binary",
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	fingerprint = "sha256:" + strings.Repeat("a", 64)
	capabilityID = installID + "/" + component + "/echo"
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{
		CapabilityID: capabilityID, ProviderID: providerID, Name: "echo",
		InputSchemaJSON: `{"type":"object"}`, SchemaFingerprint: fingerprint,
		DiscoveredAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return capabilityID, fingerprint
}

// The daemon's provider.invoke must evaluate policy (ask without a grant)
// instead of the old blanket "no policy engine configured" deny, and a grant
// must open the gate up to the dial, where a missing binary — not policy —
// is the error.
func TestProviderInvokeGateOpensWithAGrant(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	capabilityID, fingerprint := seedGrantFixture(t, h.db)

	call := func() string {
		var raw map[string]any
		err := h.client.Call(ctx, "provider.invoke", map[string]any{
			"capabilityId": capabilityID,
			"arguments":    map[string]any{},
		}, &raw)
		if err == nil {
			return ""
		}
		return err.Error()
	}

	// No grant: the policy engine answers "ask" — the approval-required path.
	first := call()
	if first == "" {
		t.Fatal("ungranted invocation must be refused")
	}
	if strings.Contains(first, "no policy engine configured") {
		t.Fatalf("policy engine is not attached to provider.invoke: %s", first)
	}
	if !strings.Contains(first, "capability grant") {
		t.Fatalf("ungranted invocation must ask for a grant, got: %s", first)
	}

	// Record the grant the way `litespm grant` does.
	g, err := issueCapabilityGrant(ctx, h.db, capabilityID)
	if err != nil {
		t.Fatalf("issueCapabilityGrant: %v", err)
	}
	if g.SchemaFingerprint != fingerprint {
		t.Errorf("grant bound to %q, want the capability fingerprint %q", g.SchemaFingerprint, fingerprint)
	}

	// The gate is open: policy no longer refuses. The failure must come from
	// the dial (the seeded command does not exist) — anything policy-shaped
	// means the grant did not take.
	second := call()
	if strings.Contains(second, "capability grant") || strings.Contains(second, "refused by policy") {
		t.Fatalf("granted invocation still refused by policy: %s", second)
	}
	if second == "" {
		t.Fatal("expected the dial to fail for the fake binary (the gate passing is what is under test)")
	}
}

// One grant row per capability: re-approval after a schema change rebinds the
// fingerprint, and attribution (granted_by/granted_at) is immutable across
// re-saves so an authorization cannot be backdated or re-attributed.
func TestIssueCapabilityGrantBindsAndRebindsOneRow(t *testing.T) {
	db := openTestState(t)
	ctx := context.Background()
	capabilityID, _ := seedGrantFixture(t, db)

	g, err := issueCapabilityGrant(ctx, db, capabilityID)
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if g.Status != "active" || g.GrantedBy != approvalActorHumanCLI {
		t.Fatalf("unexpected grant: %+v", g)
	}
	firstGrantedAt := g.CreatedAt

	grants, err := db.ListCapabilityGrants(ctx, capabilityID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("want exactly one grant row, got %v (err=%v)", grants, err)
	}

	// Schema drift: the capability's fingerprint moves (the provider now
	// serves a different tool shape). A fresh grant must rebind the SAME row
	// under a different save attribution and a later timestamp.
	drifted := "sha256:" + strings.Repeat("b", 64)
	cr, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		t.Fatal(err)
	}
	cr.SchemaFingerprint = drifted
	if err := db.SaveCapability(ctx, cr); err != nil {
		t.Fatalf("simulate drift: %v", err)
	}

	g2, err := issueCapabilityGrant(ctx, db, capabilityID)
	if err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if g2.GrantID != g.GrantID {
		t.Fatalf("re-grant created a second row id %q, want to rebind %q", g2.GrantID, g.GrantID)
	}
	if g2.SchemaFingerprint != drifted {
		t.Errorf("grant not rebound: %q", g2.SchemaFingerprint)
	}

	grants, err = db.ListCapabilityGrants(ctx, capabilityID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("want one row after re-grant, got %v (err=%v)", grants, err)
	}
	if grants[0].SchemaFingerprint != drifted {
		t.Errorf("stored fingerprint = %q, want %q", grants[0].SchemaFingerprint, drifted)
	}
	// Attribution immutability: still the first save's actor and timestamp,
	// despite the second save passing the same values again.
	if grants[0].GrantedBy != approvalActorHumanCLI {
		t.Errorf("granted_by rewritten to %q — attribution must be immutable", grants[0].GrantedBy)
	}
	if !grants[0].CreatedAt.Equal(firstGrantedAt) {
		t.Errorf("granted_at rewritten: %s → %s — backdating/re-attribution must be impossible",
			firstGrantedAt, grants[0].CreatedAt)
	}
}

// A grant for an unknown capability, or for one without a fingerprint, is
// refused: a grant must bind to something verifiable.
func TestIssueCapabilityGrantRefusesUnbindableTargets(t *testing.T) {
	db := openTestState(t)
	ctx := context.Background()

	if _, err := issueCapabilityGrant(ctx, db, "inst_nope/tool/echo"); err == nil {
		t.Error("unknown capability must be refused")
	}

	capabilityID, _ := seedGrantFixture(t, db)
	cr, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		t.Fatal(err)
	}
	cr.SchemaFingerprint = ""
	if err := db.SaveCapability(ctx, cr); err != nil {
		t.Fatal(err)
	}
	if _, err := issueCapabilityGrant(ctx, db, capabilityID); err == nil ||
		!strings.Contains(err.Error(), "no schema fingerprint") {
		t.Errorf("fingerprint-less capability must be refused, got: %v", err)
	}
}

// Revocation is a status advance on the recorded row; revoking an
// unrecorded grant is an honest not-found, not a silent success.
func TestGrantRevokeStatus(t *testing.T) {
	db := openTestState(t)
	ctx := context.Background()
	capabilityID, _ := seedGrantFixture(t, db)

	if err := db.SetCapabilityGrantStatus(ctx, "grant_missing", "revoked"); err == nil {
		t.Error("revoking an unknown grant must fail")
	}

	g, err := issueCapabilityGrant(ctx, db, capabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetCapabilityGrantStatus(ctx, g.GrantID, "revoked"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	grants, err := db.ListCapabilityGrants(ctx, capabilityID)
	if err != nil || len(grants) != 1 || grants[0].Status != "revoked" {
		t.Fatalf("want one revoked row, got %v (err=%v)", grants, err)
	}
	if _, err := db.GetActiveGrant(ctx, capabilityID, grants[0].SchemaFingerprint); err == nil {
		t.Error("a revoked grant must not read back as active")
	}
}

// grant ids are deterministic per capability — the property that keeps
// re-approval from accumulating rows the engine's drift check would deny on.
func TestGrantIDForIsDeterministicPerCapability(t *testing.T) {
	a := grantIDFor("inst_x/demo/echo")
	b := grantIDFor("inst_x/demo/echo")
	c := grantIDFor("inst_x/demo/write")
	if a != b {
		t.Errorf("not deterministic: %s != %s", a, b)
	}
	if a == c {
		t.Error("different capabilities must not share a grant id")
	}
	if !strings.HasPrefix(a, "grant_") || len(a) != len("grant_")+24 {
		t.Errorf("unexpected id shape: %s", a)
	}
}
