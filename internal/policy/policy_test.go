package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

func setupTestPolicyDB(t *testing.T) (*state.DB, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "policy_test.db")

	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	return db, tempDir
}

func TestPolicy_Tier1_InvariantDeny(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)

	// Invariant 1: Command marketplace sources
	input1 := PolicyInput{
		Actor:     "user",
		TargetRef: "cmd:curl-sh-pipe",
		Operation: "install",
		Effects: []EffectDeclaration{
			{Effect: EffectProcessSpawn, Provenance: domain.ProvenancePublisherDeclared},
		},
	}
	dec1 := engine.Evaluate(ctx, input1)
	if dec1.Decision != DecisionDeny || len(dec1.ReasonCodes) == 0 || dec1.ReasonCodes[0] != "INVARIANT_DENY_COMMAND_MARKETPLACE" {
		t.Errorf("expected INVARIANT_DENY_COMMAND_MARKETPLACE, got %+v", dec1)
	}

	// Invariant 2: SSRF to loopback / AWS metadata
	input2 := PolicyInput{
		Actor:     "agent",
		TargetRef: "tool-test",
		Operation: "invoke",
		Effects: []EffectDeclaration{
			{Effect: EffectNetworkOutbound, Provenance: domain.ProvenancePublisherDeclared, Target: "http://169.254.169.254/latest/meta-data"},
		},
	}
	dec2 := engine.Evaluate(ctx, input2)
	if dec2.Decision != DecisionDeny || len(dec2.ReasonCodes) == 0 || dec2.ReasonCodes[0] != "INVARIANT_DENY_SSRF_LOOPBACK" {
		t.Errorf("expected INVARIANT_DENY_SSRF_LOOPBACK, got %+v", dec2)
	}
}

func TestPolicy_Tier2_UserDeny(t *testing.T) {
	ctx := context.Background()
	denyRules := []DenyRule{
		{RuleID: "block_tool_x", TargetRef: "pkg:tool-x"},
		{RuleID: "block_deletion", Effect: EffectFilesystemDelete},
	}
	engine := NewEngine(nil, denyRules)

	input := PolicyInput{
		Actor:     "user",
		TargetRef: "pkg:tool-x",
		Operation: "install",
	}
	dec := engine.Evaluate(ctx, input)
	if dec.Decision != DecisionDeny || dec.MatchedRuleIDs[0] != "block_tool_x" {
		t.Errorf("expected block_tool_x deny, got %+v", dec)
	}

	inputDel := PolicyInput{
		Actor:     "user",
		TargetRef: "pkg:tool-y",
		Operation: "invoke",
		Effects: []EffectDeclaration{
			{Effect: EffectFilesystemDelete, Provenance: domain.ProvenanceCurated},
		},
	}
	decDel := engine.Evaluate(ctx, inputDel)
	if decDel.Decision != DecisionDeny || decDel.MatchedRuleIDs[0] != "block_deletion" {
		t.Errorf("expected block_deletion deny, got %+v", decDel)
	}
}

func TestPolicy_Tier3_GrantAndDriftDetection(t *testing.T) {
	ctx := context.Background()
	db, _ := setupTestPolicyDB(t)
	defer db.Close()

	engine := NewEngine(db, nil)

	// Setup mock install, provider, and capability in SQLite to satisfy foreign key constraints
	installID := "inst_postgres_test"
	provID := "prov_postgres_1"
	capID := "cap_postgres_query"
	correctFP := "sha256:1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"
	correctTree := "sha256:aaaabbbbccccddddeeeeffff1111222233334444555566667777888899990000"

	compID := "comp_default"
	_ = db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   "postgres",
		Version:     "1.0.0",
		TreeDigest:  correctTree,
		Scope:       domain.ScopeUser,
		Status:      domain.InstallActive,
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
	})

	_ = db.SaveInstallComponent(ctx, &domain.InstallComponentRecord{
		InstallID:     installID,
		Kind:          domain.ComponentMCPProvider,
		ComponentName: compID,
		Path:          "/test",
	})

	_ = db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    provID,
		InstallID:     installID,
		ComponentName: installID, // maps to component_id in query
		Transport:     "local-stdio",
		CreatedAt:     time.Now(),
	})

	_ = db.SaveCapability(ctx, &domain.CapabilityRecord{
		CapabilityID:      capID,
		ProviderID:        provID,
		Name:              "query",
		SchemaFingerprint: correctFP,
		DiscoveredAt:      time.Now(),
	})

	// Insert active capability grant
	_, err := db.Raw().ExecContext(ctx, `
	INSERT INTO capability_grants (grant_id, capability_id, schema_fingerprint, cas_tree_digest, status, granted_by, granted_at)
	VALUES ('grant_1', ?, ?, ?, 'active', 'user', CURRENT_TIMESTAMP);`, capID, correctFP, correctTree)
	if err != nil {
		t.Fatalf("failed to insert test grant: %v", err)
	}

	// 1. Matching Identity Binding -> ALLOW
	inputValid := PolicyInput{
		Actor:             "agent",
		TargetRef:         capID,
		CapabilityID:      capID,
		SchemaFingerprint: correctFP,
		CASTreeDigest:     correctTree,
		Operation:         "invoke",
		Effects: []EffectDeclaration{
			{Effect: EffectExternalRead, Provenance: domain.ProvenanceCurated},
		},
	}
	decValid := engine.Evaluate(ctx, inputValid)
	if decValid.Decision != DecisionAllow {
		t.Errorf("expected ALLOW for valid grant, got %+v", decValid)
	}

	// 2. Schema Drift -> DENY
	inputSchemaDrift := inputValid
	inputSchemaDrift.SchemaFingerprint = "sha256:different_schema_fingerprint_0000000000000000000000000000000"
	decSchemaDrift := engine.Evaluate(ctx, inputSchemaDrift)
	if decSchemaDrift.Decision != DecisionDeny || decSchemaDrift.ReasonCodes[0] != "LPSM-PROVIDER-SCHEMA-DRIFT" {
		t.Errorf("expected LPSM-PROVIDER-SCHEMA-DRIFT, got %+v", decSchemaDrift)
	}

	// 3. CAS Code Drift -> DENY
	inputCodeDrift := inputValid
	inputCodeDrift.CASTreeDigest = "sha256:tampered_tree_digest_0000000000000000000000000000000000000000"
	decCodeDrift := engine.Evaluate(ctx, inputCodeDrift)
	if decCodeDrift.Decision != DecisionDeny || decCodeDrift.ReasonCodes[0] != "LPSM-PROVIDER-CODE-DRIFT" {
		t.Errorf("expected LPSM-PROVIDER-CODE-DRIFT, got %+v", decCodeDrift)
	}
}

func TestPolicy_Tier4_AskAndTier5_Benign(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)

	// Benign read-only -> ALLOW
	inputRead := PolicyInput{
		Actor:     "agent",
		TargetRef: "catalog",
		Operation: "read",
		Effects: []EffectDeclaration{
			{Effect: EffectFilesystemRead, Provenance: domain.ProvenanceCurated},
		},
	}
	decRead := engine.Evaluate(ctx, inputRead)
	if decRead.Decision != DecisionAllow {
		t.Errorf("expected benign read allow, got %+v", decRead)
	}

	// Dangerous effect without grant -> ASK
	inputWrite := PolicyInput{
		Actor:     "agent",
		TargetRef: "file-writer",
		Operation: "invoke",
		Effects: []EffectDeclaration{
			{Effect: EffectFilesystemWrite, Provenance: domain.ProvenancePublisherDeclared},
		},
	}
	decWrite := engine.Evaluate(ctx, inputWrite)
	if decWrite.Decision != DecisionAsk {
		t.Errorf("expected ASK for dangerous action, got %+v", decWrite)
	}
}

func TestPolicy_SSRF_StructuralChecks(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)

	denied := []string{
		"http://10.0.0.5/x", "10.255.255.255", "http://172.16.0.1", "172.31.255.254:8080",
		"https://192.168.1.1/admin", "http://169.254.169.254/latest/meta-data", "100.64.0.1",
		"100.127.255.255", "127.0.0.1", "http://localhost:3000", "foo.localhost", "0.0.0.0",
		"0.1.2.3", "[::1]:80", "::1", "http://[::1]/", "fc00::1", "fd12:3456::1", "fe80::1",
		"[fe80::1%25eth0]", "::ffff:10.0.0.1", "2130706433", "0x7f.0.0.1", "017700000001", "127.1",
		"printer.local", "metadata.google.internal",
	}
	for _, target := range denied {
		dec := engine.Evaluate(ctx, PolicyInput{
			Actor: "user", Operation: "invoke",
			Effects: []EffectDeclaration{{Effect: EffectNetworkOutbound, Provenance: domain.ProvenancePublisherDeclared, Target: target}},
		})
		if dec.Decision != DecisionDeny || dec.ReasonCodes[0] != "INVARIANT_DENY_SSRF_LOOPBACK" {
			t.Errorf("target %q: expected SSRF deny, got %+v", target, dec)
		}
	}

	// Public targets (including names that merely contain private-looking
	// substrings) are not an invariant violation; they fall to the ask tier.
	allowedToAsk := []string{
		"https://example.com", "https://example10.com/x", "api.192-168-1-1.example.org",
		"8.8.8.8", "172.32.0.1", "172.15.255.255", "100.128.0.1", "100.63.255.255", "2606:4700::1111",
		"https://notlocalhost.example.com",
	}
	for _, target := range allowedToAsk {
		dec := engine.Evaluate(ctx, PolicyInput{
			Actor: "user", Operation: "invoke",
			Effects: []EffectDeclaration{{Effect: EffectNetworkOutbound, Target: target}},
		})
		if dec.Decision != DecisionAsk {
			t.Errorf("target %q: expected ask, got %+v", target, dec)
		}
	}
}

func TestPolicy_SSRF_ProvenanceCannotBypass(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)
	for _, prov := range []domain.EffectProvenance{
		domain.ProvenanceUserClassified, domain.ProvenanceCurated, domain.ProvenancePublisherDeclared,
	} {
		dec := engine.Evaluate(ctx, PolicyInput{
			Actor: "user", Operation: "invoke",
			Effects: []EffectDeclaration{{Effect: EffectNetworkOutbound, Provenance: prov, Target: "http://127.0.0.1:9000"}},
		})
		if dec.Decision != DecisionDeny {
			t.Errorf("provenance %q bypassed the SSRF invariant: %+v", prov, dec)
		}
	}
}

func TestPolicy_EmptyNetworkTargetDenied(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)
	for _, target := range []string{"", "   ", "http://", "://x"} {
		dec := engine.Evaluate(ctx, PolicyInput{
			Actor: "user", Operation: "invoke",
			Effects: []EffectDeclaration{{Effect: EffectNetworkOutbound, Provenance: domain.ProvenanceUserClassified, Target: target}},
		})
		if dec.Decision != DecisionDeny || dec.ReasonCodes[0] != "INVARIANT_DENY_NETWORK_TARGET_UNKNOWN" {
			t.Errorf("target %q: expected unknown-target deny, got %+v", target, dec)
		}
	}
}

func TestPolicy_UnknownEffectDenied(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, nil)
	dec := engine.Evaluate(ctx, PolicyInput{
		Actor: "user", Operation: "read",
		Effects: []EffectDeclaration{{Effect: CanonicalEffect("filesystem.teleport")}},
	})
	if dec.Decision != DecisionDeny || dec.ReasonCodes[0] != "INVARIANT_DENY_UNKNOWN_EFFECT" {
		t.Errorf("expected unknown-effect deny, got %+v", dec)
	}
	dec = engine.Evaluate(ctx, PolicyInput{
		Actor: "user", Operation: "read",
		Effects: []EffectDeclaration{{Effect: ""}},
	})
	if dec.Decision != DecisionDeny {
		t.Errorf("empty effect must be denied, got %+v", dec)
	}
}

func TestPolicy_DenyRuleHostIDAndConjunction(t *testing.T) {
	ctx := context.Background()
	engine := NewEngine(nil, []DenyRule{
		{RuleID: "no_codex_install", TargetRef: "pkg:x", HostID: "codex"},
		{RuleID: "no_delete_for_pkg_z", TargetRef: "pkg:z", Effect: EffectFilesystemDelete},
		{RuleID: "empty"},
	})

	mk := func(host, target string, effs ...CanonicalEffect) PolicyInput {
		in := PolicyInput{Actor: "user", HostID: host, TargetRef: target, Operation: "install"}
		for _, e := range effs {
			in.Effects = append(in.Effects, EffectDeclaration{Effect: e})
		}
		return in
	}

	if dec := engine.Evaluate(ctx, mk("codex", "pkg:x")); dec.Decision != DecisionDeny || dec.MatchedRuleIDs[0] != "no_codex_install" {
		t.Errorf("host-scoped rule must deny on its host, got %+v", dec)
	}
	if dec := engine.Evaluate(ctx, mk("opencode", "pkg:x")); dec.Decision == DecisionDeny {
		t.Errorf("host-scoped rule must not deny another host, got %+v", dec)
	}
	// Conjunction: the effect alone, or the target alone, does not match.
	if dec := engine.Evaluate(ctx, mk("", "pkg:z")); dec.Decision == DecisionDeny {
		t.Errorf("target-only input must not match a target+effect rule, got %+v", dec)
	}
	if dec := engine.Evaluate(ctx, mk("", "pkg:other", EffectFilesystemDelete)); dec.Decision == DecisionDeny && len(dec.MatchedRuleIDs) > 0 {
		t.Errorf("effect-only input must not match a target+effect rule, got %+v", dec)
	}
	if dec := engine.Evaluate(ctx, mk("", "pkg:z", EffectFilesystemDelete)); dec.Decision != DecisionDeny || dec.MatchedRuleIDs[0] != "no_delete_for_pkg_z" {
		t.Errorf("target+effect rule must match, got %+v", dec)
	}
	for _, id := range []string{"empty"} {
		if dec := engine.Evaluate(ctx, mk("", "pkg:q")); len(dec.MatchedRuleIDs) > 0 && dec.MatchedRuleIDs[0] == id {
			t.Errorf("a rule with no fields must match nothing, got %+v", dec)
		}
	}
}

func TestLoadDenyRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DenyRulesFile)

	rules, err := LoadDenyRules(path)
	if err != nil || rules != nil {
		t.Fatalf("missing file must be (nil, nil), got %v, %v", rules, err)
	}
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"ruleId":"r1","targetRef":"pkg:a","hostId":"codex"}]`)
	rules, err = LoadDenyRules(path)
	if err != nil || len(rules) != 1 || rules[0].HostID != "codex" {
		t.Fatalf("valid file: %v, %v", rules, err)
	}
	for _, bad := range []string{`not json`, `[{"ruleId":"r"}]`, `[{"ruleId":"r","effect":"bogus"}]`} {
		write(bad)
		if _, err := LoadDenyRules(path); err == nil {
			t.Errorf("malformed rules %q must be an error (fail closed)", bad)
		}
	}
}
