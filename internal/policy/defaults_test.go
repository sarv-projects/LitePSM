package policy

// Tests for the operator-configured engine defaults (config.PolicyConfig →
// NewEngineWithDefaults): the default gate level and signature enforcement.
// Every test asserts both that the configured behavior takes effect and that
// the fail-closed paths (invariants, deny rules, drift) are untouched by it.

import (
	"context"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

func TestSecureDefaultsIsCompiledBaseline(t *testing.T) {
	d := SecureDefaults()
	if d.DefaultLevel != LevelAskOnce {
		t.Errorf("secure default level = %q, want %q", d.DefaultLevel, LevelAskOnce)
	}
	if !d.EnforceSignatures {
		t.Error("secure default must enforce signatures")
	}
}

func TestNormalizeDefaultsRejectsUnknownLevel(t *testing.T) {
	got := normalizeDefaults(Defaults{DefaultLevel: "trust-everything", EnforceSignatures: true})
	if got.DefaultLevel != LevelAskOnce {
		t.Errorf("unrecognized level normalized to %q, want the documented default %q", got.DefaultLevel, LevelAskOnce)
	}
	for _, level := range []string{LevelAskOnce, LevelAlwaysAsk, LevelTrustCurated, ""} {
		want := level
		if want == "" {
			want = LevelAskOnce
		}
		if got := normalizeDefaults(Defaults{DefaultLevel: level}); got.DefaultLevel != want {
			t.Errorf("level %q normalized to %q, want %q", level, got.DefaultLevel, want)
		}
	}
}

// seedGrant installs the install/provider/capability/grant rows a Tier-3
// evaluation needs (mirrors TestPolicy_Tier3_GrantAndDriftDetection).
func seedGrant(t *testing.T, ctx context.Context, db *state.DB, capID, fp, tree string) {
	t.Helper()
	installID := "inst_" + capID
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID: installID, ListingID: capID, Version: "1.0.0", TreeDigest: tree,
		Scope: domain.ScopeUser, Status: domain.InstallActive,
		InstalledAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveInstall: %v", err)
	}
	if err := db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID: installID, Kind: domain.ComponentMCPProvider, ComponentName: "comp_default", Path: "/test",
	}); err != nil {
		t.Fatalf("SaveInstallComponent: %v", err)
	}
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID: "prov_" + capID, InstallID: installID, ComponentName: installID,
		Transport: "local-stdio", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{
		CapabilityID: capID, ProviderID: "prov_" + capID, Name: "query",
		SchemaFingerprint: fp, DiscoveredAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCapability: %v", err)
	}
	if _, err := db.Raw().ExecContext(ctx, `
	INSERT INTO capability_grants (grant_id, capability_id, schema_fingerprint, cas_tree_digest, status, granted_by, granted_at)
	VALUES (?, ?, ?, ?, 'active', 'user', CURRENT_TIMESTAMP);`, "grant_"+capID, capID, fp, tree); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
}

func grantInput(capID, fp, tree string) PolicyInput {
	return PolicyInput{
		Actor:             "agent",
		TargetRef:         capID,
		CapabilityID:      capID,
		SchemaFingerprint: fp,
		CASTreeDigest:     tree,
		Operation:         "invoke",
		Effects: []EffectDeclaration{
			{Effect: EffectExternalRead, Provenance: domain.ProvenanceCurated},
		},
	}
}

func TestDefaults_AlwaysAsk_RequiresApprovalDespiteGrant(t *testing.T) {
	ctx := context.Background()
	db, _ := setupTestPolicyDB(t)
	defer db.Close()

	const fp = "sha256:1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"
	const tree = "sha256:aaaabbbbccccddddeeeeffff1111222233334444555566667777888899990000"
	const capID = "cap_always_ask"
	seedGrant(t, ctx, db, capID, fp, tree)

	eng := NewEngineWithDefaults(db, nil, Defaults{
		DefaultLevel: LevelAlwaysAsk, EnforceSignatures: true,
	})

	// A matching grant normally auto-allows; always_ask re-opens the gate.
	dec := eng.Evaluate(ctx, grantInput(capID, fp, tree))
	if dec.Decision != DecisionAsk {
		t.Fatalf("always_ask with a valid grant: decision = %q, want ask (%+v)", dec.Decision, dec)
	}
	if len(dec.ReasonCodes) == 0 || dec.ReasonCodes[0] != "ALWAYS_ASK_LEVEL" {
		t.Fatalf("expected ALWAYS_ASK_LEVEL, got %+v", dec)
	}

	// Integrity drift still denies first: always_ask only ever *adds* asks.
	drifted := grantInput(capID, fp, tree)
	drifted.SchemaFingerprint = "sha256:drifted_0000000000000000000000000000000000000000000000000000000"
	if dec := eng.Evaluate(ctx, drifted); dec.Decision != DecisionDeny {
		t.Fatalf("always_ask must not weaken drift invalidation: %+v", dec)
	}
}

func TestDefaults_EnforceSignatures_GatesGrantReverification(t *testing.T) {
	ctx := context.Background()

	const fp = "sha256:1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"
	const tree = "sha256:aaaabbbbccccddddeeeeffff1111222233334444555566667777888899990000"
	const capID = "cap_enforce_sigs"

	drifted := grantInput(capID, fp, tree)
	drifted.SchemaFingerprint = "sha256:drifted_0000000000000000000000000000000000000000000000000000000"

	// On (the default): an unverifiable grant fails closed.
	dbOn, _ := setupTestPolicyDB(t)
	defer dbOn.Close()
	seedGrant(t, ctx, dbOn, capID, fp, tree)
	on := NewEngineWithDefaults(dbOn, nil, Defaults{DefaultLevel: LevelAskOnce, EnforceSignatures: true})
	if dec := on.Evaluate(ctx, drifted); dec.Decision != DecisionDeny ||
		len(dec.ReasonCodes) == 0 || dec.ReasonCodes[0] != "LPSM-PROVIDER-SCHEMA-DRIFT" {
		t.Fatalf("EnforceSignatures=true must deny drifted grant, got %+v", dec)
	}
	if dec := on.Evaluate(ctx, grantInput(capID, fp, tree)); dec.Decision != DecisionAllow {
		t.Fatalf("matching grant must still allow with EnforceSignatures=true, got %+v", dec)
	}

	// Off (explicit opt-out): the grant is honored without re-verification.
	dbOff, _ := setupTestPolicyDB(t)
	defer dbOff.Close()
	seedGrant(t, ctx, dbOff, capID, fp, tree)
	off := NewEngineWithDefaults(dbOff, nil, Defaults{DefaultLevel: LevelAskOnce, EnforceSignatures: false})
	if dec := off.Evaluate(ctx, drifted); dec.Decision != DecisionAllow {
		t.Fatalf("EnforceSignatures=false must honor the grant without re-verification, got %+v", dec)
	}
}

func TestDefaults_TrustCurated_RelaxesOnlyTheInstallAskGate(t *testing.T) {
	ctx := context.Background()
	engine := NewEngineWithDefaults(nil, []DenyRule{
		{RuleID: "block_pkg_x", TargetRef: "pkg:blocked"},
	}, Defaults{DefaultLevel: LevelTrustCurated, EnforceSignatures: true})

	// A plain package install is trusted: no ask.
	install := PolicyInput{
		Actor: "user", TargetRef: "pkg:curated", Operation: "install",
		Effects: []EffectDeclaration{{Effect: EffectPackageInstall, Provenance: domain.ProvenanceCurated}},
	}
	if dec := engine.Evaluate(ctx, install); dec.Decision != DecisionAllow ||
		len(dec.ReasonCodes) == 0 || dec.ReasonCodes[0] != "TRUST_CURATED_GATE" {
		t.Fatalf("trust_curated install: expected allow via TRUST_CURATED_GATE, got %+v", dec)
	}

	// Dangerous effects are not part of the trust: they still ask.
	dangerous := install
	dangerous.TargetRef = "pkg:writer"
	dangerous.Effects = []EffectDeclaration{
		{Effect: EffectPackageInstall, Provenance: domain.ProvenanceCurated},
		{Effect: EffectFilesystemWrite, Provenance: domain.ProvenanceCurated},
	}
	if dec := engine.Evaluate(ctx, dangerous); dec.Decision != DecisionAsk {
		t.Fatalf("trust_curated must still ask for dangerous effects, got %+v", dec)
	}

	// A user deny rule still denies.
	denied := install
	denied.TargetRef = "pkg:blocked"
	if dec := engine.Evaluate(ctx, denied); dec.Decision != DecisionDeny ||
		len(dec.MatchedRuleIDs) == 0 || dec.MatchedRuleIDs[0] != "block_pkg_x" {
		t.Fatalf("trust_curated must not bypass deny rules, got %+v", dec)
	}

	// Tier-1 invariants still deny.
	ssrf := PolicyInput{
		Actor: "user", TargetRef: "pkg:net", Operation: "install",
		Effects: []EffectDeclaration{{Effect: EffectNetworkOutbound, Target: "http://169.254.169.254/latest/meta-data"}},
	}
	if dec := engine.Evaluate(ctx, ssrf); dec.Decision != DecisionDeny {
		t.Fatalf("trust_curated must not bypass SSRF invariant, got %+v", dec)
	}
}

func TestDefaults_UnknownLevelBehavesAsAskOnce(t *testing.T) {
	ctx := context.Background()
	engine := NewEngineWithDefaults(nil, nil, Defaults{
		DefaultLevel: "trust_everything", EnforceSignatures: true,
	})
	install := PolicyInput{
		Actor: "user", TargetRef: "pkg:curated", Operation: "install",
		Effects: []EffectDeclaration{{Effect: EffectPackageInstall}},
	}
	// A typo must never unlock the permissive gate: it falls back to the
	// documented default, which asks.
	if dec := engine.Evaluate(ctx, install); dec.Decision != DecisionAsk {
		t.Fatalf("unrecognized level must fall back to ask_once, got %+v", dec)
	}
}
