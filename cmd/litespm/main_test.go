package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/doctor"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/install"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/state"
	"github.com/sarv-projects/litespm/internal/update"
)

const testListingID = "mcp:builtin:mcp-registry:demo-tool"

// pipeListener hands one pre-connected in-memory pipe to an ipc.Server so the
// tests drive the real server code path without opening sockets.
type pipeListener struct {
	conns chan net.Conn
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn, 1)}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.conns) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

type harness struct {
	paths      *config.PlatformPaths
	db         *state.DB
	engine     *install.Engine
	supervisor *provider.Supervisor
	client     *ipc.Client
	catClient  *catalog.Client
}

// newHarness builds the daemon's real handler set over an in-memory pipe.
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil)
}

// newHarnessWith is newHarness plus extra catalog listings, so tests can drive
// install.execute for kinds other than the seeded MCP listing.
func newHarnessWith(t *testing.T, extra []*domain.Listing) *harness {
	t.Helper()
	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}

	engine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		t.Fatalf("install.NewEngine failed: %v", err)
	}

	catClient := catalog.NewClient("https://registry.invalid", filepath.Join(tempDir, "catalog"), nil)
	seed := []*domain.Listing{{
		SchemaVersion: 2,
		ID:            testListingID,
		Kind:          domain.KindMCP,
		Name:          "demo-tool",
		Summary:       "demo listing for plan tests",
		// metadata_verified so the plan/install error paths under test are
		// reached; discovery_only rows are refused earlier (see
		// TestInstallExecuteRefusesDiscoveryOnly).
		Installability: domain.InstallabilityMetadataVerified,
		Versions: []domain.VersionSummary{
			{Version: "1.0.0", ImmutableRef: "git:1111111111111111111111111111111111111111"},
			{Version: "2.0.0", ImmutableRef: "git:2222222222222222222222222222222222222222"},
		},
	}}
	seed = append(seed, extra...)
	catClient.IndexListings(seed)

	store, err := secrets.NewMemorySecretStore()
	if err != nil {
		t.Fatalf("NewMemorySecretStore failed: %v", err)
	}
	supervisor := provider.NewSupervisor()

	server := ipc.NewServer("test-daemon", ProtocolVersion)
	// nil config → the secure compiled policy defaults, exactly as a daemon
	// with unreadable configuration would run.
	registerCoreHandlers(server, db, catClient, engine, paths, store, supervisor, policyDefaultsFrom(nil))

	listener := newPipeListener()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn
	client := ipc.NewClientFromConn(clientConn)

	h := &harness{paths: paths, db: db, engine: engine, supervisor: supervisor, client: client, catClient: catClient}
	t.Cleanup(func() {
		supervisor.StopAll(context.Background())
		_ = client.Close()
		_ = listener.Close()
		_ = server.Stop()
		<-serveDone
		_ = db.Close()
	})
	return h
}

func rpcCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an RPC error, got nil")
	}
	var rpcErr *ipc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *ipc.RPCError, got %T: %v", err, err)
	}
	return rpcErr.Code
}

func TestParseInstallFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantErr  bool
		wantHelp bool
		check    func(t *testing.T, f installFlags)
	}{
		{name: "listing only", args: []string{"mcp:builtin:mcp-registry:pg"},
			check: func(t *testing.T, f installFlags) {
				if f.listingID != "mcp:builtin:mcp-registry:pg" || f.scope != domain.ScopeUser {
					t.Errorf("unexpected flags: %+v", f)
				}
			}},
		{name: "all flags", args: []string{"--version", "1.2.3", "--scope", "project", "-w", "ws_1", "some:listing:id"},
			check: func(t *testing.T, f installFlags) {
				if f.version != "1.2.3" || f.scope != domain.ScopeProject || f.workspaceID != "ws_1" || f.listingID != "some:listing:id" {
					t.Errorf("unexpected flags: %+v", f)
				}
			}},
		{name: "help", args: []string{"--help"}, wantHelp: true},
		{name: "short help", args: []string{"-h"}, wantHelp: true},
		{name: "missing listing", args: nil, wantErr: true},
		{name: "only flags", args: []string{"--scope", "user"}, wantErr: true},
		{name: "unknown flag", args: []string{"listing", "--nope"}, wantErr: true},
		{name: "invalid scope", args: []string{"--scope", "global", "listing"}, wantErr: true},
		{name: "missing scope value", args: []string{"listing", "--scope"}, wantErr: true},
		{name: "missing version value", args: []string{"listing", "--version"}, wantErr: true},
		{name: "extra positional", args: []string{"one", "two"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, err := parseInstallFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got flags %+v", flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if flags.showHelp != tc.wantHelp {
				t.Errorf("showHelp=%t, want %t", flags.showHelp, tc.wantHelp)
			}
			if tc.check != nil {
				tc.check(t, flags)
			}
		})
	}
}

func TestNewPlanID(t *testing.T) {
	pattern := regexp.MustCompile(`^plan_[0-9A-Za-z]{26}$`)
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		id, err := newPlanID()
		if err != nil {
			t.Fatalf("newPlanID failed: %v", err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("plan id %q does not match the schema pattern", id)
		}
		if seen[id] {
			t.Fatalf("duplicate plan id %q", id)
		}
		seen[id] = true
	}
}

func TestPreparePlan_PersistsVerifiedPlan(t *testing.T) {
	// The plan declares the install scope's target hosts, which are read from
	// the machine's own agent config files; pin every variable the adapters
	// consult to an empty temp home so the assertions below describe the plan
	// and never this machine's setup.
	pinHostScanHome(t)
	h := newHarness(t)
	ctx := context.Background()

	var plan domain.InstallPlan
	err := h.client.Call(ctx, "resolver.prepare_plan", map[string]any{
		"id":      testListingID,
		"version": "1.0.0",
	}, &plan)
	if err != nil {
		t.Fatalf("resolver.prepare_plan failed: %v", err)
	}

	if !regexp.MustCompile(`^plan_[0-9A-Za-z]{26}$`).MatchString(plan.PlanID) {
		t.Errorf("plan id %q violates the schema pattern", plan.PlanID)
	}
	if plan.PlanHash == "" || !strings.HasPrefix(plan.PlanHash, "sha256:") {
		t.Errorf("plan was not sealed with a planHash: %q", plan.PlanHash)
	}
	if plan.Resolved.Version != "1.0.0" {
		t.Errorf("expected resolver selection 1.0.0, got %q", plan.Resolved.Version)
	}
	if len(plan.Resolved.ImmutableRefs) != 1 || plan.Resolved.ImmutableRefs[0] != "git:1111111111111111111111111111111111111111" {
		t.Errorf("expected the selected version's immutable ref, got %+v", plan.Resolved.ImmutableRefs)
	}
	if plan.Resolved.Artifacts == nil || len(plan.Resolved.Artifacts) != 0 {
		t.Errorf("catalog index carries no artifact pointers; expected an empty (non-nil) artifacts list, got %+v", plan.Resolved.Artifacts)
	}
	// This harness has no registered bridge host (pinned empty home) and no
	// published version record (no transport to declare), so the derived
	// effects are exactly the install itself.
	if len(plan.Effects) != 1 || plan.Effects[0] != "package.install" {
		t.Errorf("unexpected effects: %+v", plan.Effects)
	}
	if len(plan.HostChanges) != 0 {
		t.Errorf("no target host, but the plan declares host changes: %+v", plan.HostChanges)
	}
	if plan.RequestedAccess != nil {
		t.Errorf("nothing declared, but the plan requests access: %+v", plan.RequestedAccess)
	}
	// Nothing observed this machine's runtimes, so the plan may not claim any.
	if len(plan.Preconditions.RuntimesFound) != 0 || len(plan.Preconditions.FilesystemPaths) != 0 {
		t.Errorf("preconditions must stay empty when nothing was observed: %+v", plan.Preconditions)
	}
	if plan.Approval.Decision != "" || plan.Approval.ApprovalID != "" {
		t.Errorf("prepare_plan must not pre-record an approval decision: %+v", plan.Approval)
	}
	if plan.CreatedAt.IsZero() || plan.ExpiresAt.Before(plan.CreatedAt) {
		t.Errorf("invalid plan timestamps: %v -> %v", plan.CreatedAt, plan.ExpiresAt)
	}

	// The returned hash must match a recomputation over the returned document.
	recomputed, err := domain.ComputePlanHash(&plan)
	if err != nil {
		t.Fatalf("ComputePlanHash failed: %v", err)
	}
	if recomputed != plan.PlanHash {
		t.Errorf("returned planHash %q does not match recomputation %q", plan.PlanHash, recomputed)
	}

	// And the plan must be reloadable from the journal for install.execute.
	stored, err := h.db.GetPlan(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	if stored.PlanHash != plan.PlanHash || stored.Resolved.Version != plan.Resolved.Version {
		t.Errorf("stored plan differs from returned plan: %+v", stored)
	}
}

func TestPreparePlan_ErrorCodes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		params   any
		wantCode int
		contains string
	}{
		{"missing id", map[string]any{"version": "1.0.0"}, ipc.CodeInvalidParams, "id is required"},
		{"wrong param type", map[string]any{"id": 123}, ipc.CodeInvalidParams, ""},
		{"unknown listing", map[string]any{"id": "mcp:builtin:mcp-registry:nope"}, ipc.CodeInvalidParams, "not found"},
		{"bad scope", map[string]any{"id": testListingID, "scope": "global"}, ipc.CodeInvalidParams, "invalid scope"},
		{"unresolvable version", map[string]any{"id": testListingID, "version": "9.9.9"}, ipc.CodeInternalError, "LPSM-RESOLVE-CONFLICT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			err := h.client.Call(ctx, "resolver.prepare_plan", tc.params, &raw)
			if code := rpcCode(t, err); code != tc.wantCode {
				t.Errorf("code=%d, want %d (err=%v)", code, tc.wantCode, err)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %q does not contain %q", err, tc.contains)
			}
		})
	}
}

func TestInstallExecute_ErrorMapping(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	t.Run("unknown plan is not-found", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "install.execute", map[string]any{"planId": "plan_MISSING0000000000000000"}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("bad params", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "install.execute", map[string]any{"planId": 42}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("valid plan for a kind without an artifact locator fails explicitly", func(t *testing.T) {
		var plan domain.InstallPlan
		if err := h.client.Call(ctx, "resolver.prepare_plan", map[string]any{"id": testListingID, "version": "1.0.0"}, &plan); err != nil {
			t.Fatalf("prepare_plan failed: %v", err)
		}

		approvalID := approvePlanAsHuman(t, h.db, &plan)
		var raw json.RawMessage
		err := h.client.Call(ctx, "install.execute", map[string]any{"planId": plan.PlanID, "approvalToken": approvalID}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInternalError {
			t.Fatalf("code=%d, want %d (err=%v)", code, ipc.CodeInternalError, err)
		}
		if !strings.Contains(err.Error(), "LPSM-ARTIFACT-UNAVAILABLE") {
			t.Errorf("unexpected failure reason: %v", err)
		}

		// The refused request must not leave a journal entry behind.
		var ops int
		if qErr := h.db.Raw().QueryRow("SELECT COUNT(*) FROM operations").Scan(&ops); qErr != nil {
			t.Fatalf("count query failed: %v", qErr)
		}
		if ops != 0 {
			t.Errorf("refused install created %d journal entries", ops)
		}
	})
}

func TestInstallRemove_HonestResults(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	t.Run("not found", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "install.remove", map[string]any{"installId": "inst_missing_0001"}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
		if strings.Contains(err.Error(), "removed") {
			t.Errorf("not-found must not read as a removal: %v", err)
		}
	})

	t.Run("missing installId", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "install.remove", map[string]any{}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("existing install", func(t *testing.T) {
		now := time.Now().UTC()
		rec := &domain.InstallRecord{
			InstallID:   "inst_user_demo_aaaa",
			ListingID:   testListingID,
			Version:     "1.0.0",
			TreeDigest:  "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Scope:       domain.ScopeUser,
			Status:      domain.InstallActive,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		if err := h.db.SaveInstall(ctx, rec); err != nil {
			t.Fatalf("SaveInstall failed: %v", err)
		}

		var resp struct {
			Removed   bool   `json:"removed"`
			InstallID string `json:"installId"`
		}
		if err := h.client.Call(ctx, "install.remove", map[string]any{"installId": rec.InstallID}, &resp); err != nil {
			t.Fatalf("install.remove failed: %v", err)
		}
		if !resp.Removed || resp.InstallID != rec.InstallID {
			t.Errorf("unexpected response: %+v", resp)
		}
		if _, err := h.db.GetInstall(ctx, rec.InstallID); err == nil {
			t.Error("install row still exists after removal")
		}
	})
}

// The six handlers registered without a full wiring pass must report their
// real behavior: real data where a caller exists, and an accurate method-not-
// found reason where the subsystem genuinely does not exist yet.
func TestRegisteredHandlers_RealBehavior(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	t.Run("skills.list", func(t *testing.T) {
		var resp struct {
			Skills           []map[string]any `json:"skills"`
			ProgressiveIndex string           `json:"progressiveIndex"`
		}
		if err := h.client.Call(ctx, "skills.list", nil, &resp); err != nil {
			t.Fatalf("skills.list failed: %v", err)
		}
		if resp.Skills == nil {
			t.Error("skills must be an empty list, not null")
		}
		if len(resp.Skills) != 0 {
			t.Errorf("fresh state must report no skills, got %d", len(resp.Skills))
		}
	})

	t.Run("host.detect_config", func(t *testing.T) {
		var raw json.RawMessage
		if err := h.client.Call(ctx, "host.detect_config", nil, &raw); err != nil {
			t.Fatalf("host.detect_config failed: %v", err)
		}
		if len(raw) == 0 {
			t.Error("expected a detection payload")
		}
	})

	t.Run("doctor.run_checks", func(t *testing.T) {
		var report map[string]any
		if err := h.client.Call(ctx, "doctor.run_checks", nil, &report); err != nil {
			t.Fatalf("doctor.run_checks failed: %v", err)
		}
		if _, ok := report["overallStatus"]; !ok {
			t.Errorf("expected a real report, got %v", report)
		}
		if _, ok := report["checks"]; !ok {
			t.Errorf("expected the check list, got %v", report)
		}
	})

	t.Run("system.status", func(t *testing.T) {
		var resp struct {
			Version         string `json:"version"`
			ProtocolVersion string `json:"protocolVersion"`
			PID             int    `json:"pid"`
			Status          string `json:"status"`
		}
		if err := h.client.Call(ctx, "system.status", nil, &resp); err != nil {
			t.Fatalf("system.status failed: %v", err)
		}
		if resp.Version != Version || resp.ProtocolVersion != ProtocolVersion {
			t.Errorf("unexpected identity: %+v", resp)
		}
		if resp.PID <= 0 {
			t.Errorf("expected a real pid, got %d", resp.PID)
		}
	})

	t.Run("provider.probe unknown provider", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "provider.probe", map[string]any{"providerId": "prov_missing"}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("provider.probe missing id", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "provider.probe", map[string]any{}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	// provider.invoke, capabilities.search and capabilities.describe are WIRED:
	// they answer over discovered capability rows (internal/discover). A request
	// for a capability that was never discovered must therefore be an
	// InvalidParams complaint, NOT a "not implemented" stub — a stub here would
	// mean the install→discover→invoke chain silently does not work.
	t.Run("provider.invoke unknown capability is wired", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "provider.invoke", map[string]any{
			"capabilityId": "inst_user_mcp_demo_missing_1_0_0/demo/nope",
			"arguments":    map[string]any{},
		}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d — provider.invoke is implemented and must not "+
				"answer MethodNotFound (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("capabilities.search returns real rows", func(t *testing.T) {
		var out struct {
			Capabilities []map[string]any `json:"capabilities"`
			Total        int              `json:"totalDiscovered"`
		}
		if err := h.client.Call(ctx, "capabilities.search", map[string]any{"query": "anything"}, &out); err != nil {
			t.Fatalf("capabilities.search: %v", err)
		}
		if out.Capabilities == nil {
			t.Error("capabilities must be an empty list, not null, when nothing is discovered")
		}
	})

	t.Run("capabilities.describe unknown capability is wired", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "capabilities.describe", map[string]any{
			"capabilityId": "inst_user_mcp_demo_missing_1_0_0/demo/nope",
		}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	unimplemented := []struct {
		method   string
		mustSay  string
		otherSay string
	}{
		// The asynchronous invocation registry is ARCH/34, still DESIGNED.
		// provider.invoke is synchronous by contract (ARCH/06 §4), so these two
		// stay honest stubs rather than inventing a row that outlives its process.
		{"invocation.get", "invocation registry", "ARCH/34"},
		{"invocation.cancel", "invocation registry", "ARCH/34"},
	}
	for _, tc := range unimplemented {
		t.Run(tc.method, func(t *testing.T) {
			var raw json.RawMessage
			err := h.client.Call(ctx, tc.method, map[string]any{"invocationId": "inv_x"}, &raw)
			if code := rpcCode(t, err); code != ipc.CodeMethodNotFound {
				t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeMethodNotFound, err)
			}
			if !strings.Contains(err.Error(), tc.mustSay) {
				t.Errorf("reason %q must mention %q", err, tc.mustSay)
			}
			if tc.otherSay != "" && !strings.Contains(err.Error(), tc.otherSay) {
				t.Errorf("reason %q must point at %s", err, tc.otherSay)
			}
		})
	}
}

func TestRunStartupRecovery_ReportsTruthfully(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	defer db.Close()

	// Interrupted before placement: staging only, must roll back.
	const stagingOp = "op_crash_staging"
	if err := db.CreateOperation(ctx, stagingOp, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, stagingOp, "staging"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	stagingDir := filepath.Join(paths.StagingPath(), stagingOp)
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Interrupted after the metadata commit: install row exists, must finalize.
	const commitOp = "op_crash_commit"
	const crashTree = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := db.CreateOperation(ctx, commitOp, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, commitOp, "commit_intent"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}
	if err := db.RecordOperationTree(ctx, commitOp, crashTree, true); err != nil {
		t.Fatalf("RecordOperationTree failed: %v", err)
	}
	now := time.Now().UTC()
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID:   "inst_user_crash_aaaa",
		ListingID:   testListingID,
		Version:     "1.0.0",
		TreeDigest:  crashTree,
		Scope:       domain.ScopeUser,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveInstall failed: %v", err)
	}

	summary, err := runStartupRecovery(ctx, db, paths.StagingPath(), func(string) string { return "" })
	if err != nil {
		t.Fatalf("runStartupRecovery failed: %v", err)
	}
	if summary.Examined != 2 || summary.RolledBack != 1 || summary.Committed != 1 || summary.Failed != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Error("staging directory of the interrupted operation must be removed")
	}
	if nonTerminal, err := db.GetNonTerminalOperations(ctx); err != nil || len(nonTerminal) != 0 {
		t.Errorf("journal must be fully reconciled, %d open (err=%v)", len(nonTerminal), err)
	}

	// A second sweep has nothing left to do.
	summary, err = runStartupRecovery(ctx, db, paths.StagingPath(), nil)
	if err != nil {
		t.Fatalf("second recovery failed: %v", err)
	}
	if summary.Examined != 0 {
		t.Errorf("second sweep must be a no-op, got %+v", summary)
	}
}

// TestReleaseDownloadTarget pins the update-command wiring: the updater must
// download the per-asset binary URL (not the release HTML page) and must fail
// closed when the binary or its checksum is missing.
func TestReleaseDownloadTarget(t *testing.T) {
	const bin = "litespm-linux-amd64"

	t.Run("selects asset url and checksum", func(t *testing.T) {
		info := &update.ReleaseInfo{
			Version:         "1.2.3",
			DownloadURLs:    map[string]string{bin: "https://example.test/" + bin},
			ChecksumsSHA256: map[string]string{bin: "abc123"},
		}
		url, sum, err := releaseDownloadTarget(info, bin)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://example.test/"+bin || sum != "abc123" {
			t.Fatalf("wrong target: url=%q sum=%q", url, sum)
		}
	})

	t.Run("missing binary fails closed", func(t *testing.T) {
		info := &update.ReleaseInfo{
			Version:         "1.2.3",
			DownloadURLs:    map[string]string{},
			ChecksumsSHA256: map[string]string{bin: "abc123"},
		}
		if _, _, err := releaseDownloadTarget(info, bin); err == nil {
			t.Fatal("expected an error for a missing binary asset")
		}
	})

	t.Run("missing checksum fails closed", func(t *testing.T) {
		info := &update.ReleaseInfo{
			Version:         "1.2.3",
			DownloadURLs:    map[string]string{bin: "https://example.test/" + bin},
			ChecksumsSHA256: map[string]string{},
		}
		if _, _, err := releaseDownloadTarget(info, bin); err == nil {
			t.Fatal("expected an error for a missing checksum")
		}
	})

	t.Run("nil release info fails closed", func(t *testing.T) {
		if _, _, err := releaseDownloadTarget(nil, bin); err == nil {
			t.Fatal("expected an error for nil release info")
		}
	})
}

func TestReadBounded(t *testing.T) {
	t.Run("within limit", func(t *testing.T) {
		data, err := readBounded(strings.NewReader("hello"), 16)
		if err != nil || string(data) != "hello" {
			t.Fatalf("data=%q err=%v", data, err)
		}
	})
	t.Run("over limit refused", func(t *testing.T) {
		if _, err := readBounded(strings.NewReader("0123456789"), 4); err == nil {
			t.Fatal("expected an oversize error")
		}
	})
	t.Run("invalid limit", func(t *testing.T) {
		if _, err := readBounded(strings.NewReader("x"), 0); err == nil {
			t.Fatal("expected an invalid-limit error")
		}
	})
}

// TestHostApplySetup_RealBehavior drives the host.apply_setup handler through
// the real IPC server. Error paths must report accurately; the success path is
// exercised against a sandboxed HOME so no real host config is touched.
func TestHostApplySetup_RealBehavior(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	t.Run("unknown host is invalid params", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "host.apply_setup", map[string]any{"hostId": "no-such-host"}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
		if !strings.Contains(err.Error(), "unknown host adapter") {
			t.Errorf("error %q must name the unknown host", err)
		}
	})

	t.Run("missing hostId is invalid params", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "host.apply_setup", map[string]any{}, &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("non-object params are invalid params", func(t *testing.T) {
		var raw json.RawMessage
		err := h.client.Call(ctx, "host.apply_setup", "not-an-object", &raw)
		if code := rpcCode(t, err); code != ipc.CodeInvalidParams {
			t.Errorf("code=%d, want %d (err=%v)", code, ipc.CodeInvalidParams, err)
		}
	})

	t.Run("applies to a sandboxed home", func(t *testing.T) {
		home := t.TempDir()
		// Full home sandbox: the adapter resolves through $HOME on Unix but
		// prefers $USERPROFILE on Windows, and through $XDG_CONFIG_HOME (or
		// $OPENCODE_CONFIG_DIR) for the config directory on every OS — the
		// runner images export real values for several of these, so pinning
		// HOME alone let the test read (and write) the runner's actual home.
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		t.Setenv("OPENCODE_CONFIG_DIR", "")
		t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
		t.Setenv("CLINE_DATA_DIR", "")

		var resp struct {
			HostID     string `json:"hostId"`
			ConfigPath string `json:"configPath"`
			Success    bool   `json:"success"`
		}
		if err := h.client.Call(ctx, "host.apply_setup", map[string]any{
			"hostId":     "opencode",
			"binaryPath": "/opt/litespm",
		}, &resp); err != nil {
			t.Fatalf("host.apply_setup failed: %v", err)
		}
		if !resp.Success || resp.HostID != "opencode" {
			t.Fatalf("unexpected response: %+v", resp)
		}
		want := filepath.Join(home, ".config", "opencode", "opencode.json")
		if resp.ConfigPath != want {
			t.Errorf("config path = %q, want %q", resp.ConfigPath, want)
		}
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("config was not written: %v", err)
		}
		if !strings.Contains(string(data), `"litespm"`) {
			t.Errorf("written config does not contain the litespm entry: %s", data)
		}
	})
}

// TestStartupRecoveryHaltsOnFailedOperation proves the daemon's startup sweep
// does not silently swallow a per-operation recovery failure. When an
// interrupted operation cannot be reconciled the sweep must return an error so
// runDaemonServe halts instead of serving a journal that still contains
// non-terminal entries.
func TestStartupRecoveryHaltsOnFailedOperation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	defer db.Close()

	const opID = "op_recovery_fail"
	if err := db.CreateOperation(ctx, opID, nil, "install", ""); err != nil {
		t.Fatalf("CreateOperation failed: %v", err)
	}
	if err := db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
		t.Fatalf("AdvanceOperationState failed: %v", err)
	}

	// Point the staging root at a regular file so os.RemoveAll of the
	// per-operation staging directory fails with ENOTDIR. This is a hermetic,
	// permission-independent way to force recoverOperation to fail.
	notADir := filepath.Join(tempDir, "staging-is-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to create staging blocker file: %v", err)
	}

	summary, err := runStartupRecovery(ctx, db, notADir, nil)
	if err == nil {
		t.Fatalf("startup recovery swallowed a failed operation (summary %+v)", summary)
	}
	if !strings.Contains(err.Error(), "journal recovery could not complete") {
		t.Errorf("error must name the recovery failure: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected exactly 1 failed operation, got %+v", summary)
	}

	// The failed operation must remain non-terminal; the daemon halts rather
	// than pretending it was reconciled.
	open, err := db.GetNonTerminalOperations(ctx)
	if err != nil {
		t.Fatalf("GetNonTerminalOperations failed: %v", err)
	}
	if len(open) != 1 || open[0].OperationID != opID {
		t.Fatalf("expected the failed operation to remain non-terminal, got %+v", open)
	}
}

// TestDoctorExitCode pins the doctor exit-code contract from ARCH/20 §2: a
// clean or warn-only report exits 0; a FAIL in a classified category returns
// that category's documented code (catalog 10, resolve 20, approval 30, install
// 40, provider 50, host 60, state 70); when several categories fail the most
// severe (highest) code wins; and an uncategorized FAIL keeps the historical
// STATE_ERROR fallback.
func TestDoctorExitCode(t *testing.T) {
	report := func(checks ...doctor.CheckResult) *doctor.DoctorReport {
		r := &doctor.DoctorReport{}
		for _, c := range checks {
			switch c.Status {
			case doctor.StatusFail:
				r.FailCount++
			case doctor.StatusWarn:
				r.WarnCount++
			default:
				r.PassedCount++
			}
			r.Checks = append(r.Checks, c)
		}
		return r
	}
	fail := func(category doctor.Category) doctor.CheckResult {
		return doctor.CheckResult{Status: doctor.StatusFail, Category: category}
	}

	cases := []struct {
		name string
		rep  *doctor.DoctorReport
		want int
	}{
		{"nil report", nil, 0},
		{"clean", report(doctor.CheckResult{Status: doctor.StatusPass}), 0},
		{"warning only", report(doctor.CheckResult{Status: doctor.StatusWarn, Category: doctor.CategoryState}), 0},
		{"state failure", report(fail(doctor.CategoryState)), 70},
		{"catalog failure", report(fail(doctor.CategoryCatalog)), 10},
		{"resolve failure", report(fail(doctor.CategoryResolve)), 20},
		{"approval failure", report(fail(doctor.CategoryApproval)), 30},
		{"install failure", report(fail(doctor.CategoryInstall)), 40},
		{"provider failure", report(fail(doctor.CategoryProvider)), 50},
		{"host failure", report(fail(doctor.CategoryHost)), 60},
		{"uncategorized failure", report(fail("")), 70},
		{"worst failure wins", report(fail(doctor.CategoryCatalog), fail(doctor.CategoryHost)), 60},
		{"warn does not mask fail", report(
			doctor.CheckResult{Status: doctor.StatusWarn, Category: doctor.CategoryState},
			fail(doctor.CategoryInstall),
		), 40},
		{"fail count without inspectable checks", &doctor.DoctorReport{FailCount: 2}, 70},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := doctorExitCode(tc.rep); got != tc.want {
				t.Fatalf("doctorExitCode(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestInstallExecuteRefusesDiscoveryOnly pins Phase 0.1/T3 at the daemon seam:
// install.execute refuses a discovery_only row with LPSM-NOT-INSTALLABLE.
func TestInstallExecuteRefusesDiscoveryOnly(t *testing.T) {
	const id = "mcp:awesome:heuristic-row"
	h := newHarnessWith(t, []*domain.Listing{{
		SchemaVersion: 1,
		ID:            id,
		Kind:          domain.KindMCP,
		Name:          "heuristic-row",
		Summary:       "discovery-only fixture",
	}})
	var raw json.RawMessage
	err := h.client.Call(context.Background(), "install.execute", map[string]any{"listingId": id}, &raw)
	if err == nil || !strings.Contains(err.Error(), "LPSM-NOT-INSTALLABLE") {
		t.Fatalf("err = %v, want LPSM-NOT-INSTALLABLE", err)
	}
}
