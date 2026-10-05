package test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/bridge"
	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/doctor"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/install"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/provider"
	"github.com/sarv-projects/litespm/internal/resolver"
	"github.com/sarv-projects/litespm/internal/secrets"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// pipeListener hands a single pre-connected pipe to the accepting server so
// the conformance suite can run a real ipc.Server without opening sockets.
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

type conformanceListingProvider struct {
	listings map[string]*domain.Listing
	versions map[string][]resolver.ListingVersionMetadata
}

func (c *conformanceListingProvider) GetListing(ctx context.Context, listingID string) (*domain.Listing, error) {
	if l, ok := c.listings[listingID]; ok {
		return l, nil
	}
	return nil, domain.ErrNotFound("listing", listingID)
}

func (c *conformanceListingProvider) GetListingVersions(ctx context.Context, listingID string) ([]resolver.ListingVersionMetadata, error) {
	if v, ok := c.versions[listingID]; ok {
		return v, nil
	}
	return nil, domain.ErrNotFound("listing_versions", listingID)
}

func createSyntheticArchive(t *testing.T, listingID string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	skillContent := fmt.Sprintf(`---
name: %s
description: Test skill package %s
triggers:
  - test
tools_used:
  - test_tool
---
# %s Skill Instructions
Progressive disclosure body.
`, listingID, listingID, listingID)

	f, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}
	if _, err := f.Write([]byte(skillContent)); err != nil {
		t.Fatalf("failed to write zip content: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestE2E_FullLifecycleConformance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(tempDir, "config"),
		DataRoot:    filepath.Join(tempDir, "data"),
		RuntimeRoot: filepath.Join(tempDir, "runtime"),
	}

	// 1. Ensure Directory Structure
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	// 2. Open SQLite WAL State Database
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open failed: %v", err)
	}
	defer db.Close()

	// 3. Catalog Sync & Indexing
	catClient := catalog.NewClient(config.DefaultRegistryURL, paths.DataRoot, nil)
	listings := []*domain.Listing{
		{
			SchemaVersion:  1,
			ID:             "mcp:github:modelcontextprotocol:servers:postgres",
			Kind:           domain.KindMCP,
			Name:           "postgres",
			Title:          "PostgreSQL MCP Server",
			Summary:        "Read and inspect schema for Postgres",
			Categories:     []string{"database"},
			PublisherClaim: domain.PublisherClaim{Name: "Model Context Protocol"},
			Status:         domain.ListingStatusActive,
		},
		{
			SchemaVersion:  1,
			ID:             "skill:builtin:agentskills:git-release",
			Kind:           domain.KindSkill,
			Name:           "git-release",
			Title:          "Git Release Assistant",
			Summary:        "Automated semver and changelog generation",
			Categories:     []string{"devops"},
			PublisherClaim: domain.PublisherClaim{Name: "AgentSkills"},
			Status:         domain.ListingStatusActive,
		},
	}
	catClient.IndexListings(listings)

	searchResults := catClient.Search("postgres", catalog.SearchOptions{})
	if len(searchResults) == 0 {
		t.Fatalf("expected search result for postgres, got 0")
	}

	// 4. SemVer Dependency Resolution
	providerMock := &conformanceListingProvider{
		listings: map[string]*domain.Listing{
			listings[0].ID: listings[0],
			listings[1].ID: listings[1],
		},
		versions: map[string][]resolver.ListingVersionMetadata{
			listings[0].ID: {{Version: "1.0.0"}},
			listings[1].ID: {{Version: "1.4.0"}},
		},
	}
	resEngine := resolver.NewResolver(providerMock)
	resResult, err := resEngine.Resolve(ctx, listings[0].ID, ">= 1.0.0")
	if err != nil {
		t.Fatalf("resolver.Resolve failed: %v", err)
	}
	if resResult == nil || len(resResult.TopologicalOrder) == 0 {
		t.Fatalf("expected non-empty resolved topological order")
	}

	// 5. CAS Installation & Transactional Commit
	installEngine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		t.Fatalf("install.NewEngine failed: %v", err)
	}

	artifactData := createSyntheticArchive(t, listings[0].ID)
	rec, err := installEngine.Execute(ctx, install.InstallOptions{
		ListingID: listings[0].ID,
		Version:   "1.0.0",
		Scope:     domain.ScopeUser,
		ArchiveSource: func(ctx context.Context, lid, ver string) (io.ReadCloser, string, error) {
			return io.NopCloser(bytes.NewReader(artifactData)), "zip", nil
		},
	})
	if err != nil {
		t.Fatalf("installEngine.Execute failed: %v", err)
	}
	if rec.Status != domain.InstallActive {
		t.Errorf("expected status 'active', got: %s", rec.Status)
	}
	if rec.TreeDigest == "" {
		t.Errorf("expected non-empty CAS tree digest")
	}

	// 6. Progressive Skills Loader Verification
	treePath, err := installEngine.TreePath(rec.TreeDigest)
	if err != nil {
		t.Fatalf("failed to resolve tree path: %v", err)
	}
	skillPkg, err := skills.LoadSkillFromDirectory(treePath)
	if err != nil {
		t.Fatalf("skills.LoadSkillFromDirectory failed: %v", err)
	}
	if skillPkg.Name != listings[0].ID {
		t.Errorf("expected skill name %s, got %s", listings[0].ID, skillPkg.Name)
	}

	// 7. Child Process Supervisor Lifecycle
	sup := provider.NewSupervisor()
	spec := provider.LaunchSpec{
		Executable: "go",
		Args:       []string{"version"},
		TimeoutSec: 5,
	}
	handle, err := sup.StartProvider(ctx, "test-conformance-proc", spec)
	if err != nil {
		t.Fatalf("supervisor.StartProvider failed: %v", err)
	}
	if handle.PID <= 0 {
		t.Errorf("invalid managed PID: %d", handle.PID)
	}
	_ = sup.StopProvider(ctx, "test-conformance-proc")

	// 8. Stdio MCP Bridge Shim Tool Calling
	// Daemon-backed: a real ipc.Server serves tools.list from the install
	// written in step 5, and the shim must relay exactly that data. The same
	// tool with no daemon connection must fail closed instead of fabricating
	// an inventory.
	server := ipc.NewServer("conformance-daemon", "2026-07-28")
	server.RegisterHandler("tools.list", func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
		installs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
		if err != nil {
			return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: err.Error()}
		}
		items := make([]bridge.CapabilityItem, 0, len(installs))
		for _, inst := range installs {
			item := bridge.CapabilityItem{
				ID:      inst.ListingID,
				Name:    inst.ListingID,
				Summary: fmt.Sprintf("Installed capability (v%s)", inst.Version),
			}
			if lid, err := domain.ParseListingID(inst.ListingID); err == nil {
				item.Kind = string(lid.Kind())
			}
			items = append(items, item)
		}
		return map[string]any{"installs": items, "count": len(items)}, nil
	})

	listener := newPipeListener()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn
	daemonClient := ipc.NewClientFromConn(clientConn)

	shim := bridge.NewShim("cline", daemonClient, nil, nil)
	idRaw := json.RawMessage(`100`)
	listCallParams, _ := json.Marshal(map[string]any{
		"name":      "list_installed",
		"arguments": map[string]any{},
	})
	callResp := shim.HandleRequest(ctx, &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  listCallParams,
	})
	if callResp == nil || callResp.Error != nil {
		t.Fatalf("bridge HandleRequest failed: %+v", callResp)
	}

	var toolResult bridge.MCPToolResult
	if err := json.Unmarshal(callResp.Result, &toolResult); err != nil {
		t.Fatal(err)
	}
	if toolResult.IsError {
		t.Fatalf("daemon-backed list_installed returned an error: %s", toolResult.Content[0].Text)
	}
	if len(toolResult.Content) == 0 || !strings.Contains(toolResult.Content[0].Text, "LiteSPM Capabilities") {
		t.Errorf("unexpected bridge tool result: %+v", toolResult)
	}
	// The panel must carry the capability installed in step 5 — real data from
	// the state database, not a fixture.
	if !strings.Contains(toolResult.Content[0].Text, listings[0].ID) {
		t.Errorf("panel does not list the installed capability %s: %s", listings[0].ID, toolResult.Content[0].Text)
	}

	_ = daemonClient.Close()
	_ = listener.Close()
	_ = server.Stop()
	<-serveDone

	// Standalone shim (no daemon connection) must fail closed for the same call.
	standaloneShim := bridge.NewShim("cline", nil, nil, nil)
	standaloneResp := standaloneShim.HandleRequest(ctx, &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  listCallParams,
	})
	if standaloneResp == nil || standaloneResp.Error != nil {
		t.Fatalf("standalone bridge HandleRequest failed: %+v", standaloneResp)
	}
	var standaloneResult bridge.MCPToolResult
	if err := json.Unmarshal(standaloneResp.Result, &standaloneResult); err != nil {
		t.Fatal(err)
	}
	if !standaloneResult.IsError {
		t.Fatalf("standalone list_installed must fail closed, got: %+v", standaloneResult)
	}
	if !strings.Contains(standaloneResult.Content[0].Text, "not connected to daemon") {
		t.Errorf("standalone error must name the missing daemon connection: %s", standaloneResult.Content[0].Text)
	}
	if strings.Contains(standaloneResult.Content[0].Text, "LiteSPM Capabilities") {
		t.Errorf("standalone shim fabricated an inventory: %s", standaloneResult.Content[0].Text)
	}

	// 9. Diagnostic Doctor & Automated Repair
	secretStore, _ := secrets.NewMemorySecretStore()
	docEngine := doctor.NewEngine(paths, db, secretStore)
	report := docEngine.RunChecks(ctx)
	if report == nil || report.FailCount > 0 {
		t.Errorf("doctor report indicated failures: %+v", report)
	}

	repairPlan := doctor.BuildRepairPlan(report, paths)
	if err := doctor.ApplyRepairPlan(ctx, repairPlan, paths, db); err != nil {
		t.Fatalf("doctor.ApplyRepairPlan failed: %v", err)
	}

	t.Log("✓ End-to-End Conformance Test Completed Successfully!")
}
