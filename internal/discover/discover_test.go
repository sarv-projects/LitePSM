package discover

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

// buildStrictMCPServer writes an MCP server that behaves like a real one: it
// refuses to answer anything until it has received `notifications/initialized`.
// A client that skips that notification connects, initializes, and then hangs —
// which is precisely the bug this fixture exists to catch.
func buildStrictMCPServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	program := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type req struct {
	ID     json.RawMessage
	Method string
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	initialized := false
	for in.Scan() {
		var r req
		if json.Unmarshal(in.Bytes(), &r) != nil {
			continue
		}
		switch r.Method {
		case "initialize":
			initialized = false
			write(out, r.ID, map[string]any{
				"protocolVersion": "2026-07-28",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "strict", "version": "1.0.0"},
			})
		case "notifications/initialized":
			initialized = true
		case "tools/list":
			if !initialized {
				// A real server would simply not answer; answering with an error
				// makes the test failure legible instead of a timeout.
				writeErr(out, r.ID, -32002, "server not initialized")
				continue
			}
			write(out, r.ID, map[string]any{"tools": []any{
				map[string]any{
					"name":        "echo",
					"description": "Echo a message",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
				},
			}})
		case "tools/call":
			if !initialized {
				writeErr(out, r.ID, -32002, "server not initialized")
				continue
			}
			write(out, r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "echoed: ok canary=[" + os.Getenv("LITESPM_DISCOVER_CANARY") + "] spec=[" + os.Getenv("LITESPM_DISCOVER_SPEC") + "]"}}})
		default:
			if r.ID != nil {
				write(out, r.ID, map[string]any{})
			}
		}
	}
}

func write(out *bufio.Writer, id json.RawMessage, result any) {
	if id == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintln(out, string(b))
	out.Flush()
}

func writeErr(out *bufio.Writer, id json.RawMessage, code int, msg string) {
	if id == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	fmt.Fprintln(out, string(b))
	out.Flush()
}
`
	if err := os.WriteFile(src, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module strictsrv\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Hermetic: the binary lives under this test's TempDir (never a fixed
	// shared path) and carries the platform executable suffix.
	server := filepath.Join(dir, "strict-mcp-server"+exeSuffix())
	build := exec.Command("go", "build", "-o", server, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the strict MCP server: %v\n%s", err, out)
	}
	return server
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func openState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedInstall(t *testing.T, db *state.DB, installID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.SaveInstall(context.Background(), &domain.InstallRecord{
		InstallID: installID, ListingID: "mcp:demo:strict", Version: "1.0.0",
		Scope: domain.ScopeUser, Status: domain.InstallActive, InstalledAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveInstall: %v", err)
	}
	if err := db.SaveInstallComponent(context.Background(), &domain.InstallComponentRecord{
		InstallID: installID, Kind: domain.ComponentMCPProvider, ComponentName: "strict", Path: "",
	}); err != nil {
		t.Fatalf("SaveInstallComponent: %v", err)
	}
}

func TestDiscoverPersistsProviderAndCapabilityRows(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	ctx := context.Background()
	installID := "inst_user_mcp_demo_strict_1_0_0"
	seedInstall(t, db, installID)

	found, err := Discover(ctx, db, ProviderSpec{
		InstallID: installID, ComponentName: "strict", Command: server, Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found.Capabilities) != 1 || found.Capabilities[0].Name != "echo" {
		t.Fatalf("unexpected capabilities: %+v", found.Capabilities)
	}

	wantID := string(domain.NewCapabilityID(domain.InstallID(installID), "strict", "echo"))
	if found.Capabilities[0].CapabilityID != wantID {
		t.Errorf("capability id %q, want %q", found.Capabilities[0].CapabilityID, wantID)
	}
	if found.Capabilities[0].SchemaFingerprint == "" {
		t.Error("no schema fingerprint recorded")
	}

	// The rows must be readable through the same accessors the daemon uses.
	caps, err := db.ListCapabilities(ctx, "")
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capability row, got %d", len(caps))
	}
	provider, err := db.GetProvider(ctx, found.ProviderID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if provider.Command != server {
		t.Errorf("provider command did not round-trip: %q", provider.Command)
	}
}

func TestInvokeCallsTheDiscoveredTool(t *testing.T) {
	server := buildStrictMCPServer(t)
	db := openState(t)
	ctx := context.Background()
	installID := "inst_user_mcp_demo_strict_1_0_0"
	seedInstall(t, db, installID)

	found, err := Discover(ctx, db, ProviderSpec{
		InstallID: installID, ComponentName: "strict", Command: server, Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	result, err := Invoke(ctx, db, found.Capabilities[0].CapabilityID, json.RawMessage(`{"message":"hi"}`), readOnlyOpt())
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.IsError {
		t.Errorf("tool reported an error: %+v", result)
	}
	if !strings.Contains(result.Output, "echoed") {
		t.Errorf("unexpected tool output: %q", result.Output)
	}
}

func TestInvokeRefusesAnUnknownCapability(t *testing.T) {
	db := openState(t)
	if _, err := Invoke(context.Background(), db, "not-a-capability-id", nil, readOnlyOpt()); err == nil {
		t.Fatal("Invoke accepted a capability id that was never discovered")
	}
}

func TestInvokeRefusesAServerWithNoCommandRecorded(t *testing.T) {
	db := openState(t)
	ctx := context.Background()
	installID := "inst_user_mcp_demo_broken_1_0_0"
	seedInstall(t, db, installID)

	// A provider row with an empty launch spec is what a half-written or
	// hand-edited state file looks like; invoking must fail, not spawn nothing.
	now := time.Now().UTC()
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID: ProviderIDFor(installID, "broken"), InstallID: installID,
		ComponentName: "broken", Transport: "stdio", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := db.SaveCapability(ctx, &domain.CapabilityRecord{
		CapabilityID: string(domain.NewCapabilityID(domain.InstallID(installID), "broken", "echo")),
		ProviderID:   ProviderIDFor(installID, "broken"), Name: "echo",
		InputSchemaJSON: `{"type":"object"}`, SchemaFingerprint: "sha256:x", DiscoveredAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCapability: %v", err)
	}
	_, err := Invoke(ctx, db, string(domain.NewCapabilityID(domain.InstallID(installID), "broken", "echo")), nil, readOnlyOpt())
	if err == nil {
		t.Fatal("Invoke ran a provider with no command")
	}
	if !strings.Contains(err.Error(), "no command recorded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscoverRefusesANonStdioTransportRatherThanGuessing(t *testing.T) {
	db := openState(t)
	_, err := Discover(context.Background(), db, ProviderSpec{
		InstallID: "inst_x", ComponentName: "remote", Command: "curl", Transport: "sse",
	})
	if err == nil {
		t.Fatal("Discover pretended it could dial a remote transport")
	}
	if !strings.Contains(err.Error(), "not supported yet") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestStrictServerBuildIsHermeticAndRunnable runs on every OS (no Skip): the
// fixture binary must live under this test's TempDir (never a fixed /tmp path)
// and carry the platform executable suffix, or Windows cannot spawn it.
func TestStrictServerBuildIsHermeticAndRunnable(t *testing.T) {
	server := buildStrictMCPServer(t)
	if filepath.Base(filepath.Dir(server)) == filepath.Base(os.TempDir()) {
		t.Errorf("strict server %q sits directly in the shared temp dir", server)
	}
	if !strings.HasPrefix(filepath.Base(server), "strict-mcp-server") {
		t.Errorf("unexpected fixture name %q", server)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(server, ".exe") {
		t.Errorf("windows server binary must end in .exe, got %q", server)
	}
	if runtime.GOOS != "windows" && strings.HasSuffix(server, ".exe") {
		t.Errorf("unix server binary must not end in .exe, got %q", server)
	}
	if _, err := os.Stat(server); err != nil {
		t.Fatalf("built server missing: %v", err)
	}
}
