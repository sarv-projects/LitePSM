package main

// list_test.go — the ARCH/38 §4 acceptance tests for `litespm list` and
// `litespm inventory`: the counting rule, the filters, the honest state
// vocabulary and the stable inventory schema.
//
// Every test sandboxes the whole machine these commands read (agent config
// paths and LiteSPM's roots), then seeds real state through the real install
// paths — an MCP server registered in two agents, a skill copied into an
// agent's skills tree, an entry LiteSPM never wrote, and an install with no
// recorded deployment. Drift is produced by editing the config afterwards,
// so a "drifted" row is evidence of a genuine read-back, not a fixture value.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// The listings this seed installs, so assertions name real ids.
const (
	seedMCPListing     = "mcp:example:demo-mcp"
	seedSkillListing   = "skill:example:demo-skill"
	seedPluginListing  = "plugin:example:demo-plugin"
	seedObservedRecord = "observed:claude-code:mine"
)

// listSeed describes the machine the seeded tests assert against.
type listSeed struct {
	home       string
	dataRoot   string
	projectDir string
	// skillAgents are the agents the seeded skill was deployed into: two of
	// them, so one capability is (correctly) counted as two deployments.
	skillAgents []string
	claudeCfg   string
	codexCfg    string
}

// projectAgents returns up to n agents that expose a project-scope skills
// directory under project.
func projectAgents(t *testing.T, project, home string, n int) []string {
	t.Helper()
	var out []string
	for _, a := range skills.AgentTargets() {
		if len(out) == n {
			break
		}
		if _, ok := skills.AgentSkillDir(a.ID, "project", project, home); ok {
			out = append(out, a.ID)
		}
	}
	if len(out) < n {
		t.Skipf("only %d agents expose a project-scope skills directory", len(out))
	}
	return out
}

// writeTestConfig writes an agent config, creating its directory.
func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedListMachine builds a two-agent machine with one managed MCP server
// deployed to both, one observed (foreign) entry, one skill deployed into two
// agents, and one install with no deployment at all — then makes the
// claude-code copy of the MCP entry drift by editing the config behind
// LiteSPM's back.
func seedListMachine(t *testing.T) listSeed {
	t.Helper()
	// pinCopyEnv (copy_test.go) sandboxes exactly these paths: agent configs
	// through HOME & friends, LiteSPM's roots through the LITESPM_* overrides
	// (the platform default resolves through the OS user database, which a
	// temp HOME cannot redirect), and the skill-directory overrides.
	home, dataRoot := pinCopyEnv(t)

	seed := listSeed{home: home, dataRoot: dataRoot}
	seed.claudeCfg = filepath.Join(home, ".claude.json")
	seed.codexCfg = filepath.Join(home, ".codex", "config.toml")

	// Agent 1: the LiteSPM bridge plus `mine`, an entry LiteSPM never wrote —
	// that is the observed row.
	writeTestConfig(t, seed.claudeCfg, `{"mcpServers":{`+
		`"litespm":{"command":"/usr/local/bin/litespm","args":["bridge","stdio","--host","claude-code"]},`+
		`"mine":{"command":"npx"}}}`)
	// Agent 2: the bridge only.
	writeTestConfig(t, seed.codexCfg,
		"[mcp_servers.litespm]\ncommand = \"/usr/local/bin/litespm\"\nargs = [\"bridge\", \"stdio\", \"--host\", \"codex\"]\n")

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		t.Fatalf("ResolvePlatformPaths: %v", err)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	// One capability, two deployments: the same MCP server registered in both
	// agents. This is the row the counting rule is about.
	ctx := context.Background()
	if _, err := installMCPFromListing(ctx, db, dataRoot, mcpListing(seedMCPListing, "demo-mcp"),
		"1.0.0", domain.ScopeUser, []string{"claude-code", "codex"}, false, stdioRuntime(), nil); err != nil {
		t.Fatalf("seed MCP install: %v", err)
	}

	// One skill, deployed into TWO agents' project-scope skills trees: one
	// capability, two deployments.
	project := t.TempDir()
	seed.projectDir = project
	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, skillDir, "demo-skill", "list fixture")
	agents := projectAgents(t, project, home, 2)
	seed.skillAgents = agents
	if _, err := installSkillFromListing(authorizedTestContext(), db, dataRoot, project, home,
		skillListingFor(seedSkillListing, "demo-skill", skillDir), "1.0.0",
		domain.ScopeProject, agents); err != nil {
		t.Fatalf("seed skill install: %v", err)
	}

	// A kind that writes no agent entry: installed, but with no deployment.
	now := time.Now().UTC()
	if err := db.SaveInstall(ctx, &domain.InstallRecord{
		InstallID:   "inst_user_plugin_example_demo_plugin_1_0_0",
		ListingID:   seedPluginListing,
		Kind:        domain.KindPlugin,
		Version:     "1.0.0",
		TreeDigest:  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Scope:       domain.ScopeUser,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed plugin install: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	// Drift: edit the managed entry in the claude-code config. The recorded
	// post-image no longer matches what is on disk, so the row must come out
	// as drifted — read back from the real config, never assumed.
	data, err := os.ReadFile(seed.claudeCfg)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(data), `"-y"`, `"-z"`, 1)
	if drifted == string(data) {
		t.Fatal("the seeded claude-code config does not contain the managed entry's argument to edit")
	}
	if err := os.WriteFile(seed.claudeCfg, []byte(drifted), 0o600); err != nil {
		t.Fatal(err)
	}
	return seed
}

// listJSON runs the command with --json and decodes the document.
func listJSON(t *testing.T, args []string, asInventory bool) (inventoryReport, string) {
	t.Helper()
	run := append(append([]string{}, args...), "--json")
	var out, errOut bytes.Buffer
	if code := runListTo(&out, &errOut, run, asInventory); code != 0 {
		t.Fatalf("exit %d for %v\nstdout:\n%s\nstderr:\n%s", code, run, out.String(), errOut.String())
	}
	var report inventoryReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode inventory for %v: %v\n%s", run, err, out.String())
	}
	return report, errOut.String()
}

// TestListCountsUniqueCapabilitiesVersusDeployments is the §4 counting rule,
// over real seeded installs: one MCP capability in two agents, one skill, one
// observed entry and one unplaced install.
func TestListCountsUniqueCapabilitiesVersusDeployments(t *testing.T) {
	seedListMachine(t)

	var out, errOut bytes.Buffer
	if code := runListTo(&out, &errOut, nil, false); code != 0 {
		t.Fatalf("list exit = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	text := out.String()

	// 4 unique capabilities (mcp + skill + plugin + the observed entry),
	// 5 deployments (the mcp in two agents, the skill in two agents, the
	// observed entry), 1 install with no recorded deployment.
	if !strings.Contains(text, "4 unique capabilities · 5 deployments") {
		t.Errorf("counting rule is missing or wrong:\n%s", text)
	}
	if !strings.Contains(text, "1 with no recorded deployment") {
		t.Errorf("the unplaced install is not reported:\n%s", text)
	}
	// The vocabulary must appear, and no fabricated health may.
	for _, word := range []string{"managed", "observed", "drifted", "NO RECORDED AGENT"} {
		if !strings.Contains(text, word) {
			t.Errorf("human output is missing %q:\n%s", word, text)
		}
	}
	for _, health := range []string{"Ready", "running", "healthy"} {
		if strings.Contains(text, health) {
			t.Errorf("list claims health (%q) it never measured:\n%s", health, text)
		}
	}
	if strings.TrimSpace(errOut.String()) != "" {
		t.Errorf("a clean machine produced warnings:\n%s", errOut.String())
	}
}

// TestListFiltersSelectRowsHonestly drives every §4 filter through the real
// command and checks the rows each one admits.
func TestListFiltersSelectRowsHonestly(t *testing.T) {
	seed := seedListMachine(t)

	byState := func(rep inventoryReport, want string) int {
		n := 0
		for _, rec := range rep.Records {
			if rec.State == want {
				n++
			}
		}
		return n
	}

	t.Run("no filter sees everything", func(t *testing.T) {
		rep, _ := listJSON(t, nil, false)
		if len(rep.Records) != 6 {
			t.Errorf("got %d records, want 6:\n%+v", len(rep.Records), rep.Records)
		}
		if rep.Counts.UniqueCapabilities != 4 || rep.Counts.Deployments != 5 || rep.Counts.Unplaced != 1 {
			t.Errorf("unexpected counts: %+v", rep.Counts)
		}
		if len(rep.Warnings) != 0 {
			t.Errorf("unexpected warnings: %v", rep.Warnings)
		}
	})

	t.Run("--agent narrows to one agent", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--agent", "claude-code"}, false)
		if len(rep.Records) == 0 {
			t.Fatal("no records for the agent that holds two")
		}
		for _, rec := range rep.Records {
			if rec.Agent != "claude-code" {
				t.Errorf("filter leaked a row for %q: %+v", rec.Agent, rec)
			}
		}
		if rep.Filters.Agent != "claude-code" {
			t.Errorf("the document must echo the filter, got %+v", rep.Filters)
		}
	})

	t.Run("--observed reports only what LiteSPM found", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--observed"}, false)
		if len(rep.Records) != 1 {
			t.Fatalf("got %d observed rows, want 1: %+v", len(rep.Records), rep.Records)
		}
		rec := rep.Records[0]
		if rec.State != stateObserved || rec.ID != seedObservedRecord || rec.Origin != originHostConfig {
			t.Errorf("unexpected observed row: %+v", rec)
		}
		if rec.InstallID != "" {
			t.Errorf("an observed entry has no install behind it: %+v", rec)
		}
	})

	t.Run("--managed excludes everything LiteSPM does not control", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--managed"}, false)
		if len(rep.Records) != 5 {
			t.Errorf("got %d managed-side rows, want 5: %+v", len(rep.Records), rep.Records)
		}
		if byState(rep, stateObserved) != 0 {
			t.Errorf("--managed admitted an observed row: %+v", rep.Records)
		}
	})

	t.Run("--drifted reports the edit with both digests", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--drifted"}, false)
		if len(rep.Records) != 1 {
			t.Fatalf("got %d drifted rows, want 1: %+v", len(rep.Records), rep.Records)
		}
		rec := rep.Records[0]
		if rec.State != stateDrifted || rec.Agent != "claude-code" || rec.ID != seedMCPListing {
			t.Errorf("unexpected drifted row: %+v", rec)
		}
		if rec.Digest == "" || rec.CurrentDigest == "" || rec.Digest == rec.CurrentDigest {
			t.Errorf("drift must show the recorded and the current digest, and they must differ: %+v", rec)
		}
		if rec.Detail == "" {
			t.Error("a drifted row must say what happened in plain language")
		}
	})

	t.Run("the untouched agent stays managed", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--agent", "codex", "--kind", "mcp"}, false)
		if len(rep.Records) != 1 {
			t.Fatalf("got %d rows, want 1: %+v", len(rep.Records), rep.Records)
		}
		rec := rep.Records[0]
		if rec.State != stateManaged {
			t.Errorf("expected managed, got %+v", rec)
		}
		if rec.Digest == "" || rec.Digest != rec.CurrentDigest {
			t.Errorf("an untouched entry must compare equal to itself: %+v", rec)
		}
	})

	t.Run("--kind selects one kind", func(t *testing.T) {
		for kind, want := range map[string]int{"mcp": 3, "skills": 2, "plugins": 1} {
			rep, _ := listJSON(t, []string{kind}, false)
			if len(rep.Records) != want {
				t.Errorf("`list %s` got %d records, want %d: %+v", kind, len(rep.Records), want, rep.Records)
			}
			wantKind := map[string]string{"mcp": "mcp", "skills": "skill", "plugins": "plugin"}[kind]
			for _, rec := range rep.Records {
				if rec.Kind != wantKind {
					t.Errorf("`list %s` admitted a %q row: %+v", kind, rec.Kind, rec)
				}
			}
		}
	})

	t.Run("scope filters split user from project", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--global"}, false)
		if len(rep.Records) != 4 {
			t.Errorf("--global got %d rows, want 4: %+v", len(rep.Records), rep.Records)
		}
		for _, rec := range rep.Records {
			if rec.Scope != "user" {
				t.Errorf("--global admitted a %q row: %+v", rec.Scope, rec)
			}
		}
		rep, _ = listJSON(t, []string{"--project", seed.projectDir}, false)
		if len(rep.Records) != 2 {
			t.Fatalf("--project got %d rows, want 2 (the skill in both agents): %+v", len(rep.Records), rep.Records)
		}
		for _, rec := range rep.Records {
			if rec.Scope != "project" || rec.Kind != "skill" {
				t.Errorf("unexpected project row: %+v", rec)
			}
			if !slices.Contains(seed.skillAgents, rec.Agent) {
				t.Errorf("project row is for %q, which is not one of the seeded agents %v", rec.Agent, seed.skillAgents)
			}
			if rec.State != stateManaged || rec.Digest == "" || rec.Digest != rec.CurrentDigest {
				t.Errorf("the seeded skill must read back as managed with its digest: %+v", rec)
			}
			if rec.ConfigPath == "" {
				t.Errorf("the skill's directory must be reported: %+v", rec)
			}
		}
	})

	t.Run("an agent with nothing still exits 0", func(t *testing.T) {
		rep, _ := listJSON(t, []string{"--agent", "nobody"}, false)
		if len(rep.Records) != 0 || rep.Counts.UniqueCapabilities != 0 {
			t.Errorf("expected an empty document, got %+v", rep.Counts)
		}
		var out, errOut bytes.Buffer
		if code := runListTo(&out, &errOut, []string{"--agent", "nobody"}, false); code != 0 {
			t.Fatalf("exit = %d, want 0 (nothing to do is success)\n%s", code, errOut.String())
		}
	})
}

// TestListHumanOutputNamesTheAgentItReads groups rows by agent, which is what
// makes the roster view and the per-agent filter agree.
func TestListHumanOutputNamesTheAgentItReads(t *testing.T) {
	seed := seedListMachine(t)
	var out, errOut bytes.Buffer
	if code := runListTo(&out, &errOut, []string{"agents"}, false); code != 0 {
		t.Fatalf("list agents exit = %d\n%s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"AGENT", "CONFIG FILE", "BRIDGE", "DEPLOYMENTS", "claude-code", "codex", "registered"} {
		if !strings.Contains(text, want) {
			t.Errorf("roster is missing %q:\n%s", want, text)
		}
	}
	// The skill's agents hold deployments, so they must be listed too — even
	// though no agent config of theirs was detected.
	for _, agent := range seed.skillAgents {
		if !strings.Contains(text, agent) {
			t.Errorf("roster is missing the agent %q:\n%s", agent, text)
		}
	}
	// The bridge really is registered in both seeded configs, so those two
	// rows must say so — while an agent with no config of its own must not
	// borrow that claim.
	for _, hostID := range []string{"claude-code", "codex"} {
		row := rosterRowFor(text, hostID)
		if row == "" {
			t.Errorf("roster has no row for %s:\n%s", hostID, text)
			continue
		}
		if !strings.Contains(row, "registered") {
			t.Errorf("%s row does not report its registered bridge: %q", hostID, row)
		}
	}
}

// rosterRowFor returns the roster line whose first column is hostID.
func rosterRowFor(text, hostID string) string {
	for _, line := range strings.Split(text, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == hostID {
			return line
		}
	}
	return ""
}

// TestInventoryJSONShape is the schema contract automation depends on: a
// stable envelope, a record per deployment, and empty arrays that are arrays
// rather than null.
func TestInventoryJSONShape(t *testing.T) {
	requiredRecordKeys := map[string]string{
		"kind": "string", "id": "string", "name": "string", "agent": "string",
		"scope": "string", "state": "string", "placement": "string", "origin": "string",
		"installId": "string", "configPath": "string", "locator": "string",
		"entryStyle": "string", "digest": "string", "currentDigest": "string",
		"installedAt": "string", "updatedAt": "string", "detail": "string",
	}
	requiredCountKeys := []string{
		"uniqueCapabilities", "deployments", "unplaced", "managed", "observed",
		"adopted", "drifted", "broken", "unknown",
	}
	allowedStates := map[string]bool{
		stateManaged: true, stateObserved: true, stateAdopted: true,
		stateDrifted: true, stateBroken: true, stateUnknown: true,
	}
	allowedPlacements := map[string]bool{placementDeployed: true, placementUnplaced: true}
	allowedOrigins := map[string]bool{
		originCatalog: true, originAdopted: true, originHostConfig: true, originSkillsLedger: true,
	}

	assertShape := func(t *testing.T, raw string, wantRecords int) map[string]any {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("inventory is not valid JSON: %v\n%s", err, raw)
		}
		if doc["schema"] != inventorySchema {
			t.Errorf("schema = %v, want %q", doc["schema"], inventorySchema)
		}
		for _, key := range []string{"schema", "capturedAt", "filters", "counts", "records", "warnings"} {
			if _, ok := doc[key]; !ok {
				t.Errorf("document is missing the %q member", key)
			}
		}
		// The capture timestamp must be a real RFC3339 instant, not a label.
		if captured, ok := doc["capturedAt"].(string); !ok || captured == "" {
			t.Errorf("capturedAt = %v, want a timestamp", doc["capturedAt"])
		} else if _, err := time.Parse(time.RFC3339, captured); err != nil {
			t.Errorf("capturedAt %q is not RFC3339: %v", captured, err)
		}
		// Empty collections must be [] — automation does not want null.
		for _, key := range []string{"records", "warnings"} {
			if raw, ok := doc[key].([]any); !ok {
				t.Errorf("%s is %T, want an array", key, doc[key])
			} else if key == "records" && len(raw) != wantRecords {
				t.Errorf("%s has %d entries, want %d", key, len(raw), wantRecords)
			}
		}
		counts, ok := doc["counts"].(map[string]any)
		if !ok {
			t.Fatalf("counts is %T, want an object", doc["counts"])
		}
		for _, key := range requiredCountKeys {
			if _, ok := counts[key].(float64); !ok {
				t.Errorf("counts.%s is %T, want a number", key, counts[key])
			}
		}
		records, _ := doc["records"].([]any)
		for i, entry := range records {
			rec, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("record %d is %T, want an object", i, entry)
			}
			for key, wantType := range requiredRecordKeys {
				value, present := rec[key]
				if !present {
					t.Errorf("record %d is missing %q", i, key)
					continue
				}
				if _, isString := value.(string); !isString && wantType == "string" {
					t.Errorf("record %d: %s is %T, want a string", i, key, value)
				}
			}
			if !allowedStates[rec["state"].(string)] {
				t.Errorf("record %d: state %q is outside the vocabulary", i, rec["state"])
			}
			if !allowedPlacements[rec["placement"].(string)] {
				t.Errorf("record %d: placement %q is not recorded|unplaced", i, rec["placement"])
			}
			if !allowedOrigins[rec["origin"].(string)] {
				t.Errorf("record %d: origin %q is outside the vocabulary", i, rec["origin"])
			}
		}
		return doc
	}

	t.Run("an empty machine degrades to zero, never to an error", func(t *testing.T) {
		pinCopyEnv(t)
		var out, errOut bytes.Buffer
		if code := runListTo(&out, &errOut, []string{"--json"}, true); code != 0 {
			t.Fatalf("inventory exit = %d\n%s", code, errOut.String())
		}
		doc := assertShape(t, out.String(), 0)
		counts := doc["counts"].(map[string]any)
		for _, key := range requiredCountKeys {
			if counts[key].(float64) != 0 {
				t.Errorf("counts.%s = %v on an empty machine, want 0", key, counts[key])
			}
		}
	})

	t.Run("one record per deployment over seeded state", func(t *testing.T) {
		seedListMachine(t)
		rep, warnings := listJSON(t, nil, true)
		if warnings != "" {
			t.Errorf("unexpected warnings on stderr:\n%s", warnings)
		}
		raw, err := marshalInventory(&rep)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		assertShape(t, string(raw), len(rep.Records))
		if len(rep.Records) != 6 {
			t.Fatalf("got %d records, want 6: %+v", len(rep.Records), rep.Records)
		}

		// The counts must be derivable from the records themselves.
		unique := map[string]bool{}
		deployed := 0
		states := map[string]int{}
		for _, rec := range rep.Records {
			unique[rec.ID] = true
			states[rec.State]++
			if rec.Placement == placementDeployed {
				deployed++
			}
		}
		if rep.Counts.UniqueCapabilities != len(unique) {
			t.Errorf("uniqueCapabilities = %d, but the records hold %d distinct ids",
				rep.Counts.UniqueCapabilities, len(unique))
		}
		if rep.Counts.Deployments != deployed {
			t.Errorf("deployments = %d, but %d records have a recorded placement",
				rep.Counts.Deployments, deployed)
		}
		stateTotal := 0
		for _, n := range states {
			stateTotal += n
		}
		if stateTotal != len(rep.Records) {
			t.Errorf("the state counters count %d rows for %d records", stateTotal, len(rep.Records))
		}

		// State rows must be represented: the MCP install produced two
		// deployment records, the skill two (one per agent), the foreign
		// entry one, and the plugin install one unplaced record.
		byID := map[string][]inventoryRecord{}
		for _, rec := range rep.Records {
			byID[rec.ID] = append(byID[rec.ID], rec)
		}
		if got := len(byID[seedMCPListing]); got != 2 {
			t.Errorf("%s has %d records, want 2 (one per agent): %+v", seedMCPListing, got, byID[seedMCPListing])
		}
		if got := len(byID[seedSkillListing]); got != 2 {
			t.Errorf("%s has %d records, want 2 (one per agent): %+v", seedSkillListing, got, byID[seedSkillListing])
		}
		if got := len(byID[seedPluginListing]); got != 1 {
			t.Errorf("%s has %d records, want 1: %+v", seedPluginListing, got, byID[seedPluginListing])
		}
		plugin := byID[seedPluginListing][0]
		if plugin.Placement != placementUnplaced || plugin.Agent != "" {
			t.Errorf("the plugin install must be unplaced, not a phantom deployment: %+v", plugin)
		}
		if got := len(byID[seedObservedRecord]); got != 1 {
			t.Errorf("%s has %d records, want 1: %+v", seedObservedRecord, got, byID[seedObservedRecord])
		}

		// The binding vocabulary (internal/binding): a per-package entry in
		// an agent config is native; a skill directory, a foreign entry and a
		// placement-less install report no style rather than a borrowed one.
		for _, rec := range byID[seedMCPListing] {
			if rec.EntryStyle != "native" {
				t.Errorf("the MCP entry in %s has entryStyle %q, want native: %+v", rec.Agent, rec.EntryStyle, rec)
			}
		}
		for _, id := range []string{seedSkillListing, seedPluginListing, seedObservedRecord} {
			for _, rec := range byID[id] {
				if rec.EntryStyle != "" {
					t.Errorf("%s must not claim a host-config binding (%q): %+v", id, rec.EntryStyle, rec)
				}
			}
		}
	})

	t.Run("list --json and inventory --json are the same document", func(t *testing.T) {
		seedListMachine(t)
		fromList, _ := listJSON(t, nil, false)
		fromInventory, _ := listJSON(t, nil, true)
		// capturedAt is observation wall-clock: two invocations that
		// straddle a second boundary differ by construction. The contract
		// under test is that everything else is byte-identical, so pin
		// the timestamp before comparing.
		fromList.CapturedAt = "PINNED"
		fromInventory.CapturedAt = "PINNED"
		a, err := marshalInventory(&fromList)
		if err != nil {
			t.Fatal(err)
		}
		b, err := marshalInventory(&fromInventory)
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("the two commands disagree:\n--- list ---\n%s\n--- inventory ---\n%s", a, b)
		}
	})
}

// TestListUsageExitCodes pins the exit-code contract: 0 for help and success,
// 2 for usage, and no silent acceptance of a filter that cannot work.
func TestListUsageExitCodes(t *testing.T) {
	pinCopyEnv(t)
	cases := []struct {
		name      string
		args      []string
		want      int
		inErr     string
		inOut     string
		inventory bool
	}{
		{name: "help", args: []string{"--help"}, want: 0, inOut: "Usage: litespm list"},
		{name: "short help", args: []string{"-h"}, want: 0, inOut: "Usage: litespm list"},
		{name: "unknown flag", args: []string{"--bogus"}, want: 2, inErr: "unknown flag"},
		{name: "unknown kind", args: []string{"widgets"}, want: 2, inErr: "unknown filter"},
		{name: "second positional", args: []string{"mcp", "skills"}, want: 2, inErr: "unexpected argument"},
		{name: "missing flag value", args: []string{"--agent"}, want: 2, inErr: "requires a value"},
		{name: "two scopes", args: []string{"--project", ".", "--global"}, want: 2, inErr: "pass only one"},
		{name: "project that is not a directory", args: []string{"--project", "/no/such/project/dir"}, want: 2, inErr: "no such directory"},
		{name: "inventory refuses the roster view", args: []string{"agents"}, want: 2, inErr: "litespm list agents", inventory: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := runListTo(&out, &errOut, tc.args, tc.inventory); code != tc.want {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.want, out.String(), errOut.String())
			}
			if tc.inOut != "" && !strings.Contains(out.String(), tc.inOut) {
				t.Errorf("stdout is missing %q:\n%s", tc.inOut, out.String())
			}
			if tc.inErr != "" && !strings.Contains(errOut.String(), tc.inErr) {
				t.Errorf("stderr is missing %q:\n%s", tc.inErr, errOut.String())
			}
		})
	}
}

// TestListDegradesHonestlyOnAnEmptyMachine: a machine with no LiteSPM state
// and no agent configs is still a success, with zero counts and no crash.
func TestListDegradesHonestlyOnAnEmptyMachine(t *testing.T) {
	pinCopyEnv(t)

	var human, errOut bytes.Buffer
	if code := runListTo(&human, &errOut, nil, false); code != 0 {
		t.Fatalf("list exit = %d, want 0\n%s", code, errOut.String())
	}
	if !strings.Contains(human.String(), "0 unique capabilities · 0 deployments") {
		t.Errorf("empty machine must report zero counts honestly:\n%s", human.String())
	}
	for _, health := range []string{"Ready", "running", "healthy"} {
		if strings.Contains(human.String(), health) {
			t.Errorf("empty machine claims health (%q):\n%s", health, human.String())
		}
	}

	var roster, rosterErr bytes.Buffer
	if code := runListTo(&roster, &rosterErr, []string{"agents"}, false); code != 0 {
		t.Fatalf("list agents exit = %d, want 0\n%s", code, rosterErr.String())
	}
	if !strings.Contains(roster.String(), "No agent host was detected") {
		t.Errorf("an empty roster must say so:\n%s", roster.String())
	}
}
