package main

// install_remote_e2e_test.go — B1 Phase 3 acceptance: a remote (URL) MCP
// listing goes through the WHOLE install path over the real IPC server, on a
// machine with one JSON host and one TOML host configured, and comes back out
// byte-identical.
//
//	plan (resolver.prepare_plan) → human approval (approve) → install.execute
//	→ the config holds EXACTLY the golden URL entry (the same bytes
//	internal/host/remote_entry_test.go pins) → install/registration/ledger rows
//	→ remove, then restore to the pre-install bytes.
//
// The catalog side is fixture-driven: a listing whose published version record
// carries RuntimeDescriptor{Type:"streamable-http", Endpoint:…}. No network,
// no sync, no probe of the endpoint — a config write is not a connection.
//
// Three hosts are registered so the target-set rule is observable end to end:
// claude-code (JSON) and codex (TOML) can express streamable-http, gemini-cli
// cannot (Tier D: no verified URL entry object), so it must be dropped from
// the plan AND left untouched on disk.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/host"
	"github.com/sarv-projects/litespm/internal/policy"
)

const (
	e2eRemoteListingID = "mcp:example:remote-demo"
	e2eRemoteEntryName = "remote-demo"
)

// e2eRemoteListing is an installable MCP row whose published version record
// carries an endpoint instead of a launch line.
func e2eRemoteListing() *domain.Listing {
	return mcpListing(e2eRemoteListingID, e2eRemoteEntryName)
}

func e2eRemoteRecord() *domain.VersionRecord {
	return &domain.VersionRecord{
		ListingID:  e2eRemoteListingID,
		Version:    "1.0.0",
		Components: []domain.Component{{Runtime: remoteRuntime()}},
	}
}

// e2eMachine pins every host-scan path at a fresh temp home and writes the
// three bridge registrations (two capable, one not) the plan will read. It
// returns the pre-install bytes of each config so the restore can be compared
// byte for byte.
func e2eMachine(t *testing.T) (home, claudePath, codexPath, geminiPath string, pre map[string]string) {
	t.Helper()
	home = pinHostScanHome(t)
	// Redirects a generic row's config out of $HOME; cleared so gemini-cli
	// resolves under the sandbox like every other adapter.
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("COPILOT_HOME", "")

	claudePath = filepath.Join(home, ".claude.json")
	codexPath = filepath.Join(home, ".codex", "config.toml")
	geminiPath = filepath.Join(home, ".gemini", "settings.json")

	pre = map[string]string{
		claudePath: `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},"mine":{"command":"npx"}}}`,
		codexPath:  "# my codex config\n[mcp_servers.litespm]\ncommand = \"/usr/local/bin/litespm\"\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n",
		geminiPath: `{"mcpServers":{"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","gemini-cli"]}}}`,
	}
	for path, content := range pre {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return home, claudePath, codexPath, geminiPath, pre
}

// e2ePlan is one resolver.prepare_plan call decoded so the capability drop
// list (which domain.InstallPlan does not carry) is visible too.
type e2ePlan struct {
	domain.InstallPlan
	DroppedHosts []hostCapabilityDrop `json:"droppedHosts"`
}

func prepareE2EPlan(t *testing.T, h *harness) e2ePlan {
	t.Helper()
	var raw json.RawMessage
	if err := h.client.Call(context.Background(), "resolver.prepare_plan", map[string]any{
		"id": e2eRemoteListingID, "version": "1.0.0", "scope": "user",
	}, &raw); err != nil {
		t.Fatalf("resolver.prepare_plan: %v", err)
	}
	var plan e2ePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	return plan
}

// tomlTableBody returns the body of one [table] block: everything after its
// header up to the next table header or EOF, trimmed. An empty result means
// the table is absent.
func tomlTableBody(content, header string) string {
	idx := strings.Index(content, header)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(header):]
	if next := strings.Index(rest, "\n["); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

// TestRemoteInstallEndToEndJSONAndTOMLHosts is the whole Phase 3 chain.
func TestRemoteInstallEndToEndJSONAndTOMLHosts(t *testing.T) {
	ctx := context.Background()
	_, claudePath, codexPath, geminiPath, pre := e2eMachine(t)
	h := newHarnessWithCatalog(t, []*domain.Listing{e2eRemoteListing()}, []*domain.VersionRecord{e2eRemoteRecord()})

	// --- 0. the machine really has the three fixture hosts registered ------
	registered := host.RegisteredBridgeHosts(ctx, domain.ScopeUser)
	for _, want := range []string{"claude-code", "codex", "gemini-cli"} {
		if !containsHost(registered, want) {
			t.Fatalf("fixture host %q is not registered; registered = %v", want, registered)
		}
	}

	// --- 1. plan: capable hosts only, drops reported, endpoint sealed -------
	plan := prepareE2EPlan(t, h)
	if got, want := hostChangeIDs(&plan.InstallPlan), []string{"claude-code", "codex"}; !equalStrings(got, want) {
		t.Fatalf("plan targets = %v, want exactly the capable hosts %v", got, want)
	}
	if len(plan.DroppedHosts) != 1 || plan.DroppedHosts[0].HostID != "gemini-cli" {
		t.Fatalf("droppedHosts = %+v, want gemini-cli reported", plan.DroppedHosts)
	}
	if !strings.Contains(plan.DroppedHosts[0].Reason, "LPSM-HOST-REMOTE-UNSUPPORTED") {
		t.Errorf("drop reason must carry the capability code: %s", plan.DroppedHosts[0].Reason)
	}
	wantEffects := []string{"package.install", string(policy.EffectHostConfig), string(policy.EffectNetworkOutbound)}
	if !equalStrings(plan.Effects, wantEffects) {
		t.Errorf("effects = %v, want %v", plan.Effects, wantEffects)
	}
	var sealed mcpInstallPlanValue
	if err := json.Unmarshal([]byte(plan.HostChanges[0].ValueJSON), &sealed); err != nil {
		t.Fatalf("ValueJSON: %v", err)
	}
	if sealed.Runtime.Endpoint != remoteEndpoint || sealed.Runtime.Command != "" {
		t.Errorf("plan did not seal the endpoint runtime: %+v", sealed.Runtime)
	}
	// The stored plan is what execute will read back.
	stored, err := h.db.GetPlan(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if stored.PlanHash != plan.PlanHash {
		t.Errorf("stored plan hash %q != returned %q", stored.PlanHash, plan.PlanHash)
	}

	// --- 2. a human approves it --------------------------------------------
	approvalID := approvePlanAsHuman(t, h.db, &plan.InstallPlan)

	// --- 3. execute: the response names the endpoint, never a blank command --
	var exec struct {
		InstallID string `json:"installId"`
		Status    string `json:"status"`
		Kind      string `json:"kind"`
		EntryName string `json:"entryName"`
		Command   string `json:"command"`
		Endpoint  string `json:"endpoint"`
		Transport string `json:"transport"`
		Hosts     []struct {
			HostID     string `json:"hostId"`
			ConfigPath string `json:"configPath"`
			EntryName  string `json:"entryName"`
		} `json:"hosts"`
	}
	if err := h.client.Call(ctx, "install.execute", map[string]any{
		"planId": plan.PlanID, "approvalToken": approvalID,
	}, &exec); err != nil {
		t.Fatalf("install.execute: %v", err)
	}
	if exec.Status != string(domain.InstallActive) || exec.Kind != string(domain.KindMCP) {
		t.Errorf("unexpected result: %+v", exec)
	}
	if exec.Endpoint != remoteEndpoint {
		t.Errorf("endpoint = %q, want %q (an empty endpoint makes command=%q read as success-with-nothing)",
			exec.Endpoint, remoteEndpoint, exec.Command)
	}
	if exec.Command != "" {
		t.Errorf("a remote entry reported a command: %q", exec.Command)
	}
	if exec.Transport != host.TransportStreamableHTTP {
		t.Errorf("transport = %q, want %q", exec.Transport, host.TransportStreamableHTTP)
	}
	if exec.EntryName != e2eRemoteEntryName {
		t.Errorf("entryName = %q, want %q", exec.EntryName, e2eRemoteEntryName)
	}
	if len(exec.Hosts) != 2 || !containsHost([]string{exec.Hosts[0].HostID, exec.Hosts[1].HostID}, "claude-code") ||
		!containsHost([]string{exec.Hosts[0].HostID, exec.Hosts[1].HostID}, "codex") {
		t.Fatalf("written hosts = %+v, want claude-code and codex", exec.Hosts)
	}

	// --- 4. the configs hold EXACTLY the golden entries ---------------------
	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	claudeJSON := string(claudeData)
	// The same bytes internal/host/remote_entry_test.go pins for claude-code.
	wantClaudeEntry := `"` + e2eRemoteEntryName + `": {"type":"http","url":"` + remoteEndpoint + `"}`
	if !strings.Contains(claudeJSON, wantClaudeEntry) {
		t.Errorf("claude-code config does not contain the golden entry %s:\n%s", wantClaudeEntry, claudeJSON)
	}
	t.Logf("claude-code config after install:\n%s", claudeJSON)
	doc := map[string]any{}
	if err := json.Unmarshal(claudeData, &doc); err != nil {
		t.Fatalf("claude-code config is not JSON: %v\n%s", err, claudeJSON)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entryVal, ok := servers[e2eRemoteEntryName].(map[string]any)
	if !ok {
		t.Fatalf("claude-code entry missing:\n%s", claudeJSON)
	}
	if want := map[string]any{"type": "http", "url": remoteEndpoint}; !equalAny(entryVal, want) {
		t.Errorf("claude-code entry = %#v, want exactly %#v", entryVal, want)
	}
	if _, ok := servers["litespm"]; !ok {
		t.Errorf("bridge entry lost:\n%s", claudeJSON)
	}
	if _, ok := servers["mine"]; !ok {
		t.Errorf("user's sibling entry lost:\n%s", claudeJSON)
	}

	codexData, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	codexTOML := string(codexData)
	// The same bytes the host package pins for codex, under this listing's name.
	wantCodexHeader := "[mcp_servers." + e2eRemoteEntryName + "]"
	if !strings.Contains(codexTOML, wantCodexHeader) {
		t.Fatalf("codex config has no %s table:\n%s", wantCodexHeader, codexTOML)
	}
	body := tomlTableBody(codexTOML, wantCodexHeader)
	if want := `url = "` + remoteEndpoint + `"`; body != want {
		t.Errorf("codex table body = %q, want exactly %q (a remote table carries the URL and nothing else)", body, want)
	}
	t.Logf("codex config after install:\n%s", codexTOML)
	if !strings.Contains(codexTOML, "[mcp_servers.litespm]") {
		t.Errorf("bridge table lost:\n%s", codexTOML)
	}

	// The incapable host was dropped, so its config must be byte-identical to
	// what the test wrote.
	if geminiData, err := os.ReadFile(geminiPath); err != nil {
		t.Fatal(err)
	} else if string(geminiData) != pre[geminiPath] {
		t.Errorf("gemini-cli (incapable) was written:\nwant: %s\ngot:  %s", pre[geminiPath], geminiData)
	}

	// --- 5. ledger / receipt rows ------------------------------------------
	rec, err := h.db.GetInstall(ctx, exec.InstallID)
	if err != nil {
		t.Fatalf("install record: %v", err)
	}
	if rec.Status != domain.InstallActive || rec.Kind != domain.KindMCP {
		t.Errorf("install record = %+v", rec)
	}
	if want := mcpEntryTreeDigest(remoteEndpoint, "", nil); rec.TreeDigest != want {
		t.Errorf("TreeDigest = %q, want the endpoint-bound digest %q", rec.TreeDigest, want)
	}
	regs, err := h.db.ListHostRegistrationsForEntry(ctx, domain.ScopeUser, e2eRemoteEntryName)
	if err != nil {
		t.Fatalf("host registrations: %v", err)
	}
	if len(regs) != 2 {
		t.Errorf("host registrations = %+v, want exactly the two written hosts", regs)
	}
	muts := mutationsOf(t, h.db, exec.InstallID)
	if len(muts) != 2 {
		t.Errorf("deployment ledger rows = %d, want 2 (one per written config)", len(muts))
	}

	// --- 6. restore: the machine comes back BYTE-IDENTICAL to its pre-install
	// state, and every receipt row it produced is gone.
	if _, err := restoreInstall(ctx, h.db, h.paths.DataRoot, exec.InstallID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, path := range []string{claudePath, codexPath} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != pre[path] {
			t.Errorf("restore of %s is not byte-identical\nwant: %q\ngot:  %q", path, pre[path], got)
		}
	}
	if _, err := h.db.GetInstall(ctx, exec.InstallID); err == nil {
		t.Error("install row survived restore")
	}
	if regs, _ := h.db.ListHostRegistrationsForEntry(ctx, domain.ScopeUser, e2eRemoteEntryName); len(regs) != 0 {
		t.Errorf("host registrations survived restore: %+v", regs)
	}
	if muts := mutationsOf(t, h.db, exec.InstallID); len(muts) != 0 {
		t.Errorf("deployment ledger rows survived restore: %+v", muts)
	}

	// --- 7. install again, then remove: the SURGICAL path. The entry goes,
	// the bridge and the user's own entries stay — remove is not a rollback,
	// so the file's formatting is the writer's, not the pre-install bytes
	// (that is what restore in step 6 is for).
	plan2 := prepareE2EPlan(t, h)
	approval2 := approvePlanAsHuman(t, h.db, &plan2.InstallPlan)
	var exec2 struct {
		InstallID string `json:"installId"`
		Endpoint  string `json:"endpoint"`
	}
	if err := h.client.Call(ctx, "install.execute", map[string]any{
		"planId": plan2.PlanID, "approvalToken": approval2,
	}, &exec2); err != nil {
		t.Fatalf("second install.execute: %v", err)
	}
	if exec2.Endpoint != remoteEndpoint {
		t.Fatalf("second install did not register the endpoint: %+v", exec2)
	}
	var removed struct {
		Removed bool `json:"removed"`
	}
	if err := h.client.Call(ctx, "install.remove", map[string]any{"installId": exec2.InstallID}, &removed); err != nil {
		t.Fatalf("install.remove: %v", err)
	}
	if !removed.Removed {
		t.Fatal("install.remove reported no removal")
	}
	afterRemoveClaude, _ := os.ReadFile(claudePath)
	if strings.Contains(string(afterRemoveClaude), remoteEndpoint) {
		t.Errorf("remote entry survived remove:\n%s", afterRemoveClaude)
	}
	if !strings.Contains(string(afterRemoveClaude), `"litespm"`) || !strings.Contains(string(afterRemoveClaude), `"mine"`) {
		t.Errorf("remove took the bridge or the user's entry with it:\n%s", afterRemoveClaude)
	}
	afterRemoveCodex, _ := os.ReadFile(codexPath)
	if tomlTableBody(string(afterRemoveCodex), wantCodexHeader) != "" {
		t.Errorf("remote table survived remove:\n%s", afterRemoveCodex)
	}
	if !strings.Contains(string(afterRemoveCodex), "[mcp_servers.litespm]") {
		t.Errorf("remove took the bridge table with it:\n%s", afterRemoveCodex)
	}
	if _, err := h.db.GetInstall(ctx, exec2.InstallID); err == nil {
		t.Error("install row survived remove")
	}
	if len(mutationsOf(t, h.db, exec2.InstallID)) != 0 {
		t.Error("deployment ledger rows survived remove")
	}
	if regs, _ := h.db.ListHostRegistrationsForEntry(ctx, domain.ScopeUser, e2eRemoteEntryName); len(regs) != 0 {
		t.Errorf("host registrations survived remove: %+v", regs)
	}
	// The incapable host was never a target at any point of the chain.
	if geminiData, _ := os.ReadFile(geminiPath); string(geminiData) != pre[geminiPath] {
		t.Errorf("gemini-cli (incapable) changed across the whole test:\n%s", geminiData)
	}
}

// The CLI path (authorizeHumanInstall) must hand the drop list back so
// runInstall can print it: the CLI's response to a mixed default target set is
// the same one the RPC reports.
func TestAuthorizeHumanInstallReportsRemoteCapabilityDrops(t *testing.T) {
	e2eMachine(t)
	h := newHarnessWithCatalog(t, []*domain.Listing{e2eRemoteListing()}, []*domain.VersionRecord{e2eRemoteRecord()})
	listing, err := h.catClient.GetListing(e2eRemoteListingID)
	if err != nil {
		t.Fatal(err)
	}
	grant, drops, err := authorizeHumanInstall(context.Background(), h.db, h.catClient, listing,
		"1.0.0", domain.ScopeUser, &mcpInstallBinding{
			Hosts:   []string{"claude-code", "gemini-cli", "codex"},
			Runtime: remoteRuntime(),
		})
	if err != nil {
		t.Fatalf("authorizeHumanInstall: %v", err)
	}
	if grant == nil || grant.Plan == nil {
		t.Fatal("no plan was recorded")
	}
	if got, want := hostChangeIDs(grant.Plan), []string{"claude-code", "codex"}; !equalStrings(got, want) {
		t.Errorf("plan targets = %v, want %v", got, want)
	}
	if len(drops) != 1 || drops[0].HostID != "gemini-cli" ||
		!strings.Contains(drops[0].Reason, "LPSM-HOST-REMOTE-UNSUPPORTED") {
		t.Errorf("drops = %+v, want gemini-cli with its capability reason", drops)
	}
}

func containsHost(hosts []string, want string) bool {
	for _, h := range hosts {
		if h == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalAny compares two decoded JSON values structurally.
func equalAny(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}
