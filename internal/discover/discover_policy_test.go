package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/state"
)

func readOnlyOpt() Option {
	return WithEffects(policy.EffectDeclaration{
		Effect: policy.EffectFilesystemRead, Provenance: domain.ProvenanceUserClassified,
	})
}

// stubPolicy records every evaluation and answers with a fixed decision.
type stubPolicy struct {
	mu       sync.Mutex
	decision policy.PolicyDecision
	inputs   []policy.PolicyInput
}

func (s *stubPolicy) Evaluate(_ context.Context, in policy.PolicyInput) policy.PolicyDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = append(s.inputs, in)
	return s.decision
}

// discoverStrict discovers the strict server and returns its capability row.
func discoverStrict(t *testing.T, db *state.DB, spec ProviderSpec) domain.CapabilityRecord {
	t.Helper()
	seedInstall(t, db, spec.InstallID)
	found, err := Discover(context.Background(), db, spec)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return found.Capabilities[0]
}

// breakCommand rewrites the provider row so any spawn attempt fails with a
// "start" error: a policy denial that surfaces a policy error instead proves no
// process was started.
func breakCommand(t *testing.T, db *state.DB, installID string) {
	t.Helper()
	ctx := context.Background()
	id := ProviderIDFor(installID, "strict")
	p, err := db.GetProvider(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.Command = "/nonexistent/litespm-must-not-spawn"
	if err := db.SaveProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
}

func TestInvokeWithoutPolicyIsFailClosedForNonReadOnly(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{InstallID: installID, ComponentName: "strict", Command: server})
	breakCommand(t, db, installID)

	for name, opts := range map[string][]Option{
		"default effects": nil,
		"explicit write":  {WithEffects(policy.EffectDeclaration{Effect: policy.EffectFilesystemWrite, Provenance: domain.ProvenanceUserClassified})},
		"empty effects":   {WithEffects()},
		"mixed read+exec": {WithEffects(
			policy.EffectDeclaration{Effect: policy.EffectFilesystemRead},
			policy.EffectDeclaration{Effect: policy.EffectProcessSpawn})},
	} {
		_, err := Invoke(context.Background(), db, capRec.CapabilityID, nil, opts...)
		var lerr *domain.LPSMError
		if !errors.As(err, &lerr) || lerr.Code != domain.CodePolicyDenied {
			t.Errorf("%s: want policy denial, got %v", name, err)
		}
	}
}

func TestInvokeWithPolicyDenyAndAskNeverSpawn(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{InstallID: installID, ComponentName: "strict", Command: server})
	breakCommand(t, db, installID)

	for _, kind := range []policy.DecisionKind{policy.DecisionDeny, policy.DecisionAsk} {
		sp := &stubPolicy{decision: policy.PolicyDecision{Decision: kind, ReasonCodes: []string{"X"}, Detail: "nope"}}
		_, err := Invoke(context.Background(), db, capRec.CapabilityID, nil, WithPolicy(sp))
		var lerr *domain.LPSMError
		if !errors.As(err, &lerr) || lerr.Code != domain.CodePolicyDenied {
			t.Errorf("%s: want policy denial, got %v", kind, err)
		}
		if len(sp.inputs) != 1 {
			t.Errorf("%s: want exactly the pre-spawn evaluation, got %d", kind, len(sp.inputs))
		}
	}
}

func TestInvokeEvaluatesPolicyBeforeSpawnAndAgainstCurrentFingerprint(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{InstallID: installID, ComponentName: "strict", Command: server})

	sp := &stubPolicy{decision: policy.PolicyDecision{Decision: policy.DecisionAllow}}
	res, err := Invoke(context.Background(), db, capRec.CapabilityID, json.RawMessage(`{}`),
		WithPolicy(sp), WithActor("agent"), WithHostID("opencode"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(res.Output, "echoed") {
		t.Errorf("unexpected output %q", res.Output)
	}
	if len(sp.inputs) != 2 {
		t.Fatalf("want 2 evaluations (pre-spawn, post-probe), got %d", len(sp.inputs))
	}
	for _, in := range sp.inputs {
		if in.Operation != "invoke" || in.CapabilityID != capRec.CapabilityID || in.TargetRef != capRec.CapabilityID ||
			in.Actor != "agent" || in.HostID != "opencode" {
			t.Errorf("bad policy input: %+v", in)
		}
		if in.SchemaFingerprint != capRec.SchemaFingerprint {
			t.Errorf("fingerprint %q want %q", in.SchemaFingerprint, capRec.SchemaFingerprint)
		}
		if len(in.Effects) == 0 {
			t.Error("effects must be classified, never empty")
		}
	}
}

func TestInvokeWithRealEngineRequiresAMatchingGrant(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{InstallID: installID, ComponentName: "strict", Command: server})
	eng := policy.NewEngine(db, nil)
	ctx := context.Background()

	// No grant: refused.
	if _, err := Invoke(ctx, db, capRec.CapabilityID, nil, WithPolicy(eng)); err == nil {
		t.Fatal("invocation without a grant must be refused")
	}

	// Grant bound to a different fingerprint: refused as drift.
	if err := db.SaveCapabilityGrant(ctx, &domain.CapabilityGrant{
		GrantID: "g-stale", CapabilityID: capRec.CapabilityID, SchemaFingerprint: "sha256:stale",
		Status: "active", CreatedAt: time.Now().UTC(),
	}, "user"); err != nil {
		t.Fatal(err)
	}
	_, err := Invoke(ctx, db, capRec.CapabilityID, nil, WithPolicy(eng))
	var lerr *domain.LPSMError
	if !errors.As(err, &lerr) || lerr.Code != domain.CodeProviderSchemaDrift {
		t.Fatalf("want schema drift denial, got %v", err)
	}

	// Revoke the stale grant, add a matching one: allowed.
	if err := db.SaveCapabilityGrant(ctx, &domain.CapabilityGrant{
		GrantID: "g-stale", CapabilityID: capRec.CapabilityID, SchemaFingerprint: "sha256:stale",
		Status: "revoked", CreatedAt: time.Now().UTC(),
	}, "user"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCapabilityGrant(ctx, &domain.CapabilityGrant{
		GrantID: "g-good", CapabilityID: capRec.CapabilityID, SchemaFingerprint: capRec.SchemaFingerprint,
		Status: "active", CreatedAt: time.Now().UTC(),
	}, "user"); err != nil {
		t.Fatal(err)
	}
	res, err := Invoke(ctx, db, capRec.CapabilityID, nil, WithPolicy(eng))
	if err != nil {
		t.Fatalf("granted invocation failed: %v", err)
	}
	if !strings.Contains(res.Output, "echoed") {
		t.Errorf("unexpected output %q", res.Output)
	}
}

// The parent's environment must never reach the spawned server; only the
// stored spec env does.
func TestInvokeAndDiscoverDoNotLeakParentEnvironment(t *testing.T) {
	t.Setenv("LITESPM_DISCOVER_CANARY", "leaked-canary-value")
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{
		InstallID: installID, ComponentName: "strict", Command: server,
		Env: map[string]string{"LITESPM_DISCOVER_SPEC": "from-spec"},
	})
	res, err := Invoke(context.Background(), db, capRec.CapabilityID, nil, readOnlyOpt())
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if strings.Contains(res.Output, "leaked-canary-value") || !strings.Contains(res.Output, "canary=[]") {
		t.Errorf("parent canary reached the child: %q", res.Output)
	}
	if !strings.Contains(res.Output, "spec=[from-spec]") {
		t.Errorf("spec env did not reach the child: %q", res.Output)
	}
}

func TestResolveEnvOnlyForwardsNamedVariables(t *testing.T) {
	t.Setenv("LITESPM_DISCOVER_CANARY", "c")
	t.Setenv("LITESPM_NAMED", "named-value")
	got := resolveEnv(ProviderSpec{HostID: "opencode", Env: map[string]string{
		"WANTED":  "${env:LITESPM_NAMED}",
		"LITERAL": "plain",
	}})
	if _, leaked := got["LITESPM_DISCOVER_CANARY"]; leaked {
		t.Error("unnamed parent variable forwarded")
	}
	if got["LITERAL"] != "plain" {
		t.Errorf("literal lost: %v", got)
	}
}

func TestInvokeUsesSuppliedSupervisor(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	const installID = "inst_user_mcp_demo_strict_1_0_0"
	capRec := discoverStrict(t, db, ProviderSpec{InstallID: installID, ComponentName: "strict", Command: server})
	sup := provider.NewSupervisor()
	if _, err := Invoke(context.Background(), db, capRec.CapabilityID, nil, readOnlyOpt(), WithSupervisor(sup)); err != nil {
		t.Fatal(err)
	}
	// The call went through the supervisor: it tracked the child and the
	// cleanup left it stopped.
	snap, err := sup.SnapshotProvider(fmt.Sprintf("%s#%d", ProviderIDFor(installID, "strict"), dialSeq.Load()))
	if err != nil {
		t.Fatalf("supervisor did not track the invocation: %v", err)
	}
	if snap.Status != provider.StatusStopped && snap.Status != provider.StatusError {
		t.Errorf("child should be stopped after Invoke, status=%s", snap.Status)
	}
}
