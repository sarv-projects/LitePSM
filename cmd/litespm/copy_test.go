package main

// copy_test.go — the ARCH/38 §5.8 acceptance tests for `litespm copy`, plus
// the flag parser's contract.
//
// Every test sandboxes the whole machine `copy` can see: agent config paths
// (HOME & friends), LiteSPM's data root (LITESPM_* overrides — the platform
// default resolves through the OS user database, which a temp HOME cannot
// redirect), and the skill-directory env overrides. Nothing here touches the
// developer's real configs.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/deployment"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/porting"
	"github.com/sarv-projects/litespm/internal/skills"
	"github.com/sarv-projects/litespm/internal/state"
)

// pinCopyEnv sandboxes every path `litespm copy` resolves.
func pinCopyEnv(t *testing.T) (home, dataRoot string) {
	t.Helper()
	home = pinHostScanHome(t)
	dataRoot = t.TempDir()
	t.Setenv("LITESPM_DATA_ROOT", dataRoot)
	t.Setenv("LITESPM_CONFIG_ROOT", t.TempDir())
	t.Setenv("LITESPM_RUNTIME_ROOT", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	// Skill dirs resolve through these; pin them so the ambient machine can
	// never redirect a write.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home, dataRoot
}

func writeCopyConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// captureCopyStderr collects refusal messages (they go to the real stderr,
// like every other command's).
func captureCopyStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	data, _ := io.ReadAll(r)
	_ = r.Close()
	return string(data)
}

// TestPortingTranslatesOpenCodeCommandArrayToCodexArgs is acceptance sketch 1:
// the motivating real transcript, byte-pinned. OpenCode's combined argv array
// must land in Codex as command + args, rendered by the target's own writer.
func TestPortingTranslatesOpenCodeCommandArrayToCodexArgs(t *testing.T) {
	home, _ := pinCopyEnv(t)
	writeCopyConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"engram":{"type":"local","command":["engram","mcp","--tools=agent,graph"]}}}`)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "opencode", "--to", "codex", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}

	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("target config not written: %v", err)
	}
	want := "[mcp_servers.engram]\ncommand = \"engram\"\nargs = [\"mcp\", \"--tools=agent,graph\"]\n"
	if string(got) != want {
		t.Fatalf("target config is not the byte-pinned render:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(out.String(), "TRANSLATED") {
		t.Errorf("plan did not report the translation:\n%s", out.String())
	}
}

// TestPortingPlanCountsAndNoWrites is acceptance sketch 2: the plan phase
// prints counts (direct/translated/skipped/conflicts) and leaves both configs
// byte-identical — and never even opens the state database.
func TestPortingPlanCountsAndNoWrites(t *testing.T) {
	home, dataRoot := pinCopyEnv(t)
	// Source: Claude Code (object shape, same as Codex → direct rows).
	writeCopyConfig(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{`+
			`"fresh":{"command":"npx","args":["-y","fresh"]},`+
			`"github":{"command":"gh"},`+
			`"same":{"command":"node","args":["same.js"]}}}`)
	// Target: one divergent entry (conflict) and one identical entry.
	targetPath := filepath.Join(home, ".codex", "config.toml")
	writeCopyConfig(t, targetPath, "[mcp_servers.github]\ncommand = \"other-cmd\"\n\n[mcp_servers.same]\ncommand = \"node\"\nargs = [\"same.js\"]\n")

	beforeSrc := fileDigest(t, filepath.Join(home, ".claude.json"))
	beforeTgt := fileDigest(t, targetPath)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "claude-code", "--to", "codex"}); code != 0 {
		t.Fatalf("plan exit = %d, want 0\n%s", code, out.String())
	}
	printed := out.String()
	for _, want := range []string{
		"(no changes have been made)",
		"Found     3 MCP servers",
		"DIRECT    1 MCP",
		"SKIPPED   1 MCP already identical",
		"CONFLICTS mcp github",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("plan output missing %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "NOT FOUND") {
		t.Errorf("plan reported a missing item nobody asked for:\n%s", printed)
	}

	if got := fileDigest(t, filepath.Join(home, ".claude.json")); got != beforeSrc {
		t.Error("the plan phase modified the source config")
	}
	if got := fileDigest(t, targetPath); got != beforeTgt {
		t.Error("the plan phase modified the target config")
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "state.db")); !os.IsNotExist(err) {
		t.Error("the plan phase opened the state database; it must not")
	}
}

// TestPortingNeverWritesLiteralEnvValues is acceptance sketch 3: the source
// carries a literal credential; after a full apply the literal is absent from
// the target config AND from every byte of plan output, while Needs names the
// variable so the user knows to bind it out of band.
func TestPortingNeverWritesLiteralEnvValues(t *testing.T) {
	home, _ := pinCopyEnv(t)
	const literal = "ghp_literal_secret_token_never_written"
	writeCopyConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"github":{"type":"local","command":["gh","mcp"],`+
			`"environment":{"GITHUB_TOKEN":"`+literal+`"}}}}`)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "opencode", "--to", "codex", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}
	printed := out.String()

	target, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("target config: %v", err)
	}
	if strings.Contains(string(target), literal) {
		t.Errorf("literal secret written into the target config:\n%s", target)
	}
	if strings.Contains(printed, literal) {
		t.Errorf("literal secret leaked into plan output:\n%s", printed)
	}
	if !strings.Contains(printed, "GITHUB_TOKEN") {
		t.Errorf("Needs must name the variable:\n%s", printed)
	}
	if !strings.Contains(printed, "value NOT copied") {
		t.Errorf("plan must state the value was not copied:\n%s", printed)
	}
}

// TestPortingApplyIsManagedAndUndoable is acceptance sketch 4: a copy lands
// as a full managed install — install row under a local-origin listing id,
// deployment mutation, host registration — and `install remove` reverses it.
func TestPortingApplyIsManagedAndUndoable(t *testing.T) {
	home, dataRoot := pinCopyEnv(t)
	writeCopyConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"demo":{"type":"local","command":["npx","-y","demo"]}}}`)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "opencode", "--to", "codex", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "mcp_adopted_opencode_demo") {
		t.Errorf("apply output must show the local-origin install id:\n%s", out.String())
	}

	ctx := context.Background()
	db, err := state.Open(filepath.Join(dataRoot, "state.db"))
	if err != nil {
		t.Fatalf("open copy-created state: %v", err)
	}
	defer func() { _ = db.Close() }()

	const listingID = "mcp:adopted:opencode:demo"
	recs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	var installID string
	for _, r := range recs {
		if r.ListingID == listingID {
			installID = r.InstallID
		}
	}
	if installID == "" {
		t.Fatalf("no install row for %s (rows: %+v)", listingID, recs)
	}

	muts, err := deployment.NewLedger(db.Raw()).MutationsForInstall(ctx, installID)
	if err != nil {
		t.Fatalf("MutationsForInstall: %v", err)
	}
	if len(muts) != 1 {
		t.Fatalf("deployment mutations = %d, want 1", len(muts))
	}
	if muts[0].StructureType != "toml-table" || muts[0].CapabilityID != listingID {
		t.Errorf("unexpected mutation: %+v", muts[0])
	}
	regs, err := db.ListHostRegistrationsForEntry(ctx, domain.ScopeUser, "demo")
	if err != nil || len(regs) != 1 || regs[0].HostID != "codex" {
		t.Errorf("host registrations = %+v (err %v), want one codex row", regs, err)
	}

	// The reversal: install remove strips the entry and deletes state.
	targetPath := filepath.Join(home, ".codex", "config.toml")
	if before, _ := os.ReadFile(targetPath); !strings.Contains(string(before), "[mcp_servers.demo]") {
		t.Fatalf("target config never held the entry:\n%s", before)
	}
	if err := removeInstalledPackage(ctx, db, dataRoot, installID); err != nil {
		t.Fatalf("removeInstalledPackage: %v", err)
	}
	if after, _ := os.ReadFile(targetPath); strings.Contains(string(after), "[mcp_servers.demo]") {
		t.Errorf("entry survived removal:\n%s", after)
	}
	if _, err := db.GetInstall(ctx, installID); err == nil {
		t.Error("install row survived removal")
	}
}

// TestPortingConflictStrategies is acceptance sketch 5: one test, five
// sub-cases, each on a fresh sandboxed machine.
func TestPortingConflictStrategies(t *testing.T) {
	const (
		sourceEntry = `{"mcp":{"shared":{"type":"local","command":["source-cmd"]}}}`
		targetEntry = "[mcp_servers.shared]\ncommand = \"target-cmd\"\n"
	)
	setup := func(t *testing.T) (home, targetPath string) {
		t.Helper()
		home, _ = pinCopyEnv(t)
		writeCopyConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), sourceEntry)
		targetPath = filepath.Join(home, ".codex", "config.toml")
		writeCopyConfig(t, targetPath, targetEntry)
		return home, targetPath
	}
	targetHas := func(t *testing.T, path, want string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("target config must contain %q, got:\n%s", want, data)
		}
	}

	t.Run("ask refuses in non-TTY without --yes", func(t *testing.T) {
		_, targetPath := setup(t)
		var out bytes.Buffer
		stderr := captureCopyStderr(t, func() {
			if code := runCopyTo(&out, []string{"--from", "opencode", "--to", "codex", "--apply"}); code != 1 {
				t.Errorf("exit = %d, want 1 (LPSM-COPY-004 refusal)", code)
			}
		})
		if !strings.Contains(stderr, "LPSM-COPY-004") {
			t.Errorf("stderr must carry LPSM-COPY-004, got:\n%s", stderr)
		}
		if !strings.Contains(out.String(), "CONFLICTS") {
			t.Errorf("the plan must print before the refusal:\n%s", out.String())
		}
		targetHas(t, targetPath, "target-cmd")
	})

	t.Run("keep-target leaves the target entry", func(t *testing.T) {
		_, targetPath := setup(t)
		var out bytes.Buffer
		code := runCopyTo(&out, []string{
			"--from", "opencode", "--to", "codex", "--conflict", "keep-target", "--apply", "--yes"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out.String())
		}
		targetHas(t, targetPath, "target-cmd")
		if !strings.Contains(out.String(), "conflict, target kept") {
			t.Errorf("SKIPPED line must classify the kept conflict:\n%s", out.String())
		}
	})

	t.Run("use-source overwrites with the source entry", func(t *testing.T) {
		_, targetPath := setup(t)
		var out bytes.Buffer
		code := runCopyTo(&out, []string{
			"--from", "opencode", "--to", "codex", "--conflict", "use-source", "--apply", "--yes"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out.String())
		}
		targetHas(t, targetPath, "source-cmd")
		data, _ := os.ReadFile(targetPath)
		if strings.Contains(string(data), "target-cmd") {
			t.Errorf("target entry was not replaced:\n%s", data)
		}
	})

	t.Run("compatible keeps the representable target", func(t *testing.T) {
		_, targetPath := setup(t)
		var out bytes.Buffer
		code := runCopyTo(&out, []string{
			"--from", "opencode", "--to", "codex", "--conflict", "compatible", "--apply", "--yes"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out.String())
		}
		targetHas(t, targetPath, "target-cmd")
		if !strings.Contains(out.String(), "compatible") {
			t.Errorf("plan must report the compatible resolution:\n%s", out.String())
		}
	})

	t.Run("skip never touches conflicts", func(t *testing.T) {
		_, targetPath := setup(t)
		var out bytes.Buffer
		code := runCopyTo(&out, []string{
			"--from", "opencode", "--to", "codex", "--conflict", "skip", "--apply", "--yes"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out.String())
		}
		targetHas(t, targetPath, "target-cmd")
		if !strings.Contains(out.String(), "--conflict skip") {
			t.Errorf("plan must report the skip resolution:\n%s", out.String())
		}
	})
}

// TestPortingReportsUnsupportedPluginWithReason is acceptance sketch 6: a
// plugin is reported — per item and per selected kind — with LPSM-COPY-003
// and a reason, never silently dropped.
func TestPortingReportsUnsupportedPluginWithReason(t *testing.T) {
	// Planner level: a plugin component found on the source becomes a row.
	plan, err := porting.BuildPlan(
		&porting.Source{From: "codex", Display: "Codex",
			Other: []porting.SourceOther{{Kind: porting.KindPlugin, Name: "toolx"}}},
		&porting.TargetCaps{To: "opencode", Display: "OpenCode"},
		porting.CopyOptions{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var row *porting.CopyItem
	for i := range plan.Items {
		if plan.Items[i].Kind == porting.KindPlugin {
			row = &plan.Items[i]
		}
	}
	if row == nil {
		t.Fatal("plugin not reported: an unsupported kind must never be silently dropped")
	}
	if row.Action != porting.ActionUnsupported || !strings.Contains(row.Reason, "LPSM-COPY-003") {
		t.Errorf("plugin row = %+v, want unsupported with LPSM-COPY-003", *row)
	}

	// CLI level: selecting the kind reports it even when the source has none.
	pinCopyEnv(t)
	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"plugins", "--from", "codex", "--to", "opencode"}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	printed := out.String()
	if !strings.Contains(printed, "LPSM-COPY-003") || !strings.Contains(printed, "plugin") {
		t.Errorf("selecting plugins must report LPSM-COPY-003 with a reason:\n%s", printed)
	}
}

// TestPortingSkillCopyRoundTrip is acceptance sketch 7: a ledger-recorded
// skill on the source agent lands in the target agent's skill directory
// through the normal skills installer, fully ledgered and reversible.
func TestPortingSkillCopyRoundTrip(t *testing.T) {
	home, dataRoot := pinCopyEnv(t)
	ctx := context.Background()

	// Source: a real skill install into Codex's global skill directory.
	srcTree := filepath.Join(t.TempDir(), "demo-skill")
	writeTestSkill(t, srcTree, "demo-skill", "portable skill body")
	srcSkillDir, ok := skills.AgentSkillDir("codex", "global", "", home)
	if !ok {
		t.Fatal("codex has no documented global skills directory")
	}
	ledger, err := skills.OpenLedger(skills.LedgerPath(dataRoot))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := skills.NewInstaller(ledger, nil).Install(ctx, skills.InstallRequest{
		Op: skills.InstallOp{
			SkillName: "demo-skill",
			FromDir:   srcTree,
			ToDir:     filepath.Join(srcSkillDir, "demo-skill"),
			HostLabel: "codex",
		},
		Source: skills.SkillSource{Kind: "local", Display: srcTree, LocalDir: srcTree},
		Scope:  "global",
	}); err != nil {
		t.Fatalf("seed the source skill: %v", err)
	}

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "codex", "--to", "opencode", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}
	printed := out.String()
	if !strings.Contains(printed, "demo-skill") || !strings.Contains(printed, "DIRECT") {
		t.Errorf("plan must show the skill row:\n%s", printed)
	}

	// The skill landed in the target agent's own directory.
	targetSkillDir, ok := skills.AgentSkillDir("opencode", "global", "", home)
	if !ok {
		t.Fatal("opencode has no documented global skills directory")
	}
	targetDir := filepath.Join(targetSkillDir, "demo-skill")
	skillFile, err := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("target skill not written: %v", err)
	}
	if !strings.Contains(string(skillFile), "portable skill body") {
		t.Errorf("target SKILL.md lost the source content:\n%s", skillFile)
	}

	// Ledger and state record it as a managed, local-origin install.
	entries, err := ledger.All()
	if err != nil {
		t.Fatal(err)
	}
	foundLedger := false
	for _, e := range entries {
		if e.DestDir == targetDir {
			foundLedger = true
		}
	}
	if !foundLedger {
		t.Errorf("skills ledger has no entry for the copied destination: %+v", entries)
	}

	db, err := state.Open(filepath.Join(dataRoot, "state.db"))
	if err != nil {
		t.Fatalf("open copy-created state: %v", err)
	}
	defer func() { _ = db.Close() }()

	const listingID = "skill:adopted:codex:demo-skill"
	var installID string
	recs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	for _, r := range recs {
		if r.ListingID == listingID {
			installID = r.InstallID
		}
	}
	if installID == "" {
		t.Fatalf("no install row for %s (rows: %+v)", listingID, recs)
	}
	muts, err := deployment.NewLedger(db.Raw()).MutationsForInstall(ctx, installID)
	if err != nil {
		t.Fatalf("MutationsForInstall: %v", err)
	}
	if len(muts) != 1 || muts[0].StructureType != "skill-dir" || muts[0].FilePath != targetDir {
		t.Fatalf("deployment mutations = %+v, want one skill-dir row for %s", muts, targetDir)
	}

	// Reversal: `restore` takes the copied skill back out, keyed by its own
	// directory. (`install remove` for skills removes by NAME across the
	// ledger, which would also take the source agent's copy — a pre-existing
	// ledger-wide behavior outside this command; restore is the path that is
	// exact for a copy. See the final report's open items.)
	if _, err := restoreInstall(ctx, db, dataRoot, installID); err != nil {
		t.Fatalf("restoreInstall: %v", err)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Errorf("copied skill directory survived restore: %v", err)
	}
	if _, err := db.GetInstall(ctx, installID); err == nil {
		t.Error("install row survived restore")
	}
	entries, err = ledger.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.DestDir == targetDir {
			t.Errorf("target skills ledger row survived restore: %+v", e)
		}
	}
	// The SOURCE must be untouched: copy never modifies the agent it read,
	// and undoing a copy must not either.
	sourceDir := filepath.Join(srcSkillDir, "demo-skill")
	if _, err := os.Stat(filepath.Join(sourceDir, "SKILL.md")); err != nil {
		t.Errorf("the source skill was modified: %v", err)
	}
	sourceRowPresent := false
	for _, e := range entries {
		if e.DestDir == sourceDir {
			sourceRowPresent = true
		}
	}
	if !sourceRowPresent {
		t.Errorf("the source agent's ledger row was destroyed by undoing the copy: %+v", entries)
	}
}

func TestParseCopyFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantErr  bool
		wantHelp bool
		check    func(t *testing.T, f copyFlags)
	}{
		{
			name: "full",
			args: []string{"mcp,skills", "github", "--from", "opencode", "--to", "codex",
				"--exclude", "exa", "--conflict", "skip", "--apply", "--yes", "--project", "."},
			check: func(t *testing.T, f copyFlags) {
				if f.from != "opencode" || f.to != "codex" {
					t.Errorf("hosts = %q → %q", f.from, f.to)
				}
				if len(f.positionals) != 2 || f.positionals[0] != "mcp,skills" || f.positionals[1] != "github" {
					t.Errorf("positionals = %v", f.positionals)
				}
				if len(f.exclude) != 1 || f.exclude[0] != "exa" {
					t.Errorf("exclude = %v", f.exclude)
				}
				if f.conflict != "skip" || !f.apply || !f.yes || f.project != "." || f.global {
					t.Errorf("unexpected flags: %+v", f)
				}
			},
		},
		{name: "help", args: []string{"--help"}, wantHelp: true},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: true},
		{name: "missing value", args: []string{"--from"}, wantErr: true},
		{name: "dry run only", args: []string{"--from", "codex", "--to", "opencode", "--dry-run"},
			check: func(t *testing.T, f copyFlags) {
				if !f.dryRun || f.apply {
					t.Errorf("unexpected flags: %+v", f)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseCopyFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", f)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.showHelp != tc.wantHelp {
				t.Errorf("showHelp = %v, want %v", f.showHelp, tc.wantHelp)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}

// TestRunCopyUsageExitCodes pins the usage half of the exit-code contract
// (ARCH/20): 2 for every invalid invocation, 0 for help.
func TestRunCopyUsageExitCodes(t *testing.T) {
	pinCopyEnv(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"missing --from", []string{"--to", "codex"}, 2},
		{"missing --to", []string{"--from", "codex"}, 2},
		{"unknown source", []string{"--from", "no-such-host", "--to", "codex"}, 2},
		{"unknown target", []string{"--from", "codex", "--to", "no-such-host"}, 2},
		{"same host", []string{"--from", "codex", "--to", "codex"}, 2},
		{"invalid conflict", []string{"--from", "codex", "--to", "opencode", "--conflict", "force"}, 2},
		{"yes without apply", []string{"--from", "codex", "--to", "opencode", "--yes"}, 2},
		{"help", []string{"--help"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := runCopyTo(&out, tc.args); code != tc.want {
				t.Errorf("exit = %d, want %d\n%s", code, tc.want, out.String())
			}
		})
	}
}

func TestSplitPositionals(t *testing.T) {
	kinds, items := splitPositionals([]string{"mcp,skills", "github"})
	if len(kinds) != 2 || kinds[0] != porting.KindMCP || kinds[1] != porting.KindSkill {
		t.Errorf("kinds = %v", kinds)
	}
	if len(items) != 1 || items[0] != "github" {
		t.Errorf("items = %v", items)
	}
}

// --- remote (URL) MCP entries in copy — B1, PART 4 Phase 2 Step 2.6 -------

// TestPortingCopiesRemoteEntryBetweenCapableHosts: a remote entry read from
// one capable host is written through the TARGET's own spelling (Claude Code's
// mandatory type discriminator), as a DIRECT row — a URL entry has no argv
// shape to translate — and L1 verifies the read-back fingerprint.
func TestPortingCopiesRemoteEntryBetweenCapableHosts(t *testing.T) {
	home, _ := pinCopyEnv(t)
	writeCopyConfig(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"demo":{"url":"https://mcp.example.com/mcp"}}}`)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "cursor", "--to", "claude-code", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}

	got, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("target config not written: %v", err)
	}
	want := "{\n  \"mcpServers\": {\n  \"demo\": {\"type\":\"http\",\"url\":\"https://mcp.example.com/mcp\"}\n}\n}"
	if string(got) != want {
		t.Errorf("target is not the host's own remote spelling:\n got: %q\nwant: %q", got, want)
	}
	text := out.String()
	if !strings.Contains(text, "DIRECT") || strings.Contains(text, "TRANSLATED") {
		t.Errorf("a remote row must be DIRECT (no argv translation):\n%s", text)
	}
	if !strings.Contains(text, "L1 ok") {
		t.Errorf("read-back verification did not pass:\n%s", text)
	}
}

// TestPortingCopiesRemoteEntryJsonToTOML exercises the other direction: an
// OpenCode remote entry (type:"remote", shape local-array) lands in Codex's
// TOML as a bare url table, and the shape difference is NOT reported as a
// translation because there is no argv to translate.
func TestPortingCopiesRemoteEntryJsonToTOML(t *testing.T) {
	home, _ := pinCopyEnv(t)
	writeCopyConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"demo":{"type":"remote","url":"https://mcp.example.com/mcp"}}}`)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "opencode", "--to", "codex", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}

	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("target config not written: %v", err)
	}
	want := "[mcp_servers.demo]\nurl = \"https://mcp.example.com/mcp\"\n"
	if string(got) != want {
		t.Errorf("target is not the byte-pinned TOML render:\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(out.String(), "DIRECT") {
		t.Errorf("remote row must be DIRECT:\n%s", out.String())
	}
}

// TestPortingRemoteToIncapableTargetRefusesAndWritesNothing: gemini-cli has
// no verified remote spec, so the row is reported "not copyable" with the
// reason and the target config stays byte-identical — in plan mode AND in
// apply mode.
func TestPortingRemoteToIncapableTargetRefusesAndWritesNothing(t *testing.T) {
	home, _ := pinCopyEnv(t)
	t.Setenv("GEMINI_CLI_HOME", "")
	writeCopyConfig(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"demo":{"url":"https://mcp.example.com/mcp"}}}`)
	targetPath := filepath.Join(home, ".gemini", "settings.json")
	writeCopyConfig(t, targetPath,
		`{"mcpServers":{"mine":{"command":"npx"}}}`)

	// Plan mode: the refusal is printed, nothing is written.
	var plan bytes.Buffer
	if code := runCopyTo(&plan, []string{"--from", "cursor", "--to", "gemini-cli"}); code != 0 {
		t.Fatalf("plan exit = %d, want 0\n%s", code, plan.String())
	}
	if !strings.Contains(plan.String(), "not copyable") || !strings.Contains(plan.String(), "gemini-cli") {
		t.Errorf("plan must report the explicit not-copyable refusal:\n%s", plan.String())
	}
	if !strings.Contains(plan.String(), "LPSM-COPY-002") {
		t.Errorf("refusal must carry the typed code:\n%s", plan.String())
	}

	// Apply mode: nothing writable → nothing applied, target untouched.
	var applied bytes.Buffer
	if code := runCopyTo(&applied, []string{"--from", "cursor", "--to", "gemini-cli", "--apply", "--yes"}); code != 0 {
		t.Fatalf("apply exit = %d, want 0\n%s", code, applied.String())
	}
	if !strings.Contains(applied.String(), "Nothing to apply") {
		t.Errorf("apply must report nothing to write:\n%s", applied.String())
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(after) != `{"mcpServers":{"mine":{"command":"npx"}}}` {
		t.Errorf("target config changed:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err != nil {
		t.Errorf("source config was modified: %v", err)
	}
}

// TestPortingRemoteCopyRestoresByteIdenticalPreInstallState is the cmd-level
// half of Step 2.3: a remote entry written by copy carries a real install
// record and deployment mutation, and `restore` puts the pre-install bytes
// back exactly — the ledger-driven path `litespm restore` replays.
func TestPortingRemoteCopyRestoresByteIdenticalPreInstallState(t *testing.T) {
	home, dataRoot := pinCopyEnv(t)
	writeCopyConfig(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"demo":{"url":"https://mcp.example.com/mcp"}}}`)
	targetPath := filepath.Join(home, ".claude.json")
	original := "{\n  \"mcpServers\": {\n" +
		"    \"litespm\": {\"command\": \"litespm\", \"args\": [\"bridge\"]},\n" +
		"    \"mine\": {\"command\": \"npx\"}\n" +
		"  }\n}\n"
	writeCopyConfig(t, targetPath, original)

	var out bytes.Buffer
	if code := runCopyTo(&out, []string{"--from", "cursor", "--to", "claude-code", "--apply", "--yes"}); code != 0 {
		t.Fatalf("copy exit = %d, want 0\n%s", code, out.String())
	}
	installed := string(fileBytes(t, targetPath))
	if !strings.Contains(installed, "https://mcp.example.com/mcp") || !strings.Contains(installed, `"type":"http"`) {
		t.Fatalf("remote entry was not installed:\n%s", installed)
	}
	if !strings.Contains(installed, `"mine": {"command": "npx"}`) {
		t.Fatalf("sibling lost during install:\n%s", installed)
	}

	ctx := context.Background()
	db, err := state.Open(filepath.Join(dataRoot, "state.db"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	defer func() { _ = db.Close() }()

	listingID := porting.AdoptedListingID(porting.KindMCP, "cursor", "demo")
	var installID string
	recs, err := db.ListInstalls(ctx, domain.ScopeUser, "")
	if err != nil {
		t.Fatalf("ListInstalls: %v", err)
	}
	for _, r := range recs {
		if r.ListingID == listingID {
			installID = r.InstallID
		}
	}
	if installID == "" {
		t.Fatalf("no install row for %s (rows: %+v)", listingID, recs)
	}

	if _, err := restoreInstall(ctx, db, dataRoot, installID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored := string(fileBytes(t, targetPath))
	if restored != original {
		t.Errorf("restore is not byte-identical\nwant: %q\ngot:  %q", original, restored)
	}
	if _, err := db.GetInstall(ctx, installID); err == nil {
		t.Error("install row survived restore")
	}
}
