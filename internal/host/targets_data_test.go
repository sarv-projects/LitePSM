package host

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

// bespokeAdapterIDs are the hand-written adapters that predate the data-driven
// target table. A verified target must never claim one of these ids, or the
// registry would silently shadow a tested adapter with a table row.
var bespokeAdapterIDs = []string{
	"codex", "claude-code", "opencode", "cline", "pi-agent", "pi", "grok", "grok-build",
}

func TestBridgeTargetTableHasUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, tgt := range verifiedBridgeTargets {
		if seen[tgt.ID] {
			t.Fatalf("duplicate target id %q", tgt.ID)
		}
		seen[tgt.ID] = true
	}
}

// TestBridgeTargetRowsAreComplete enforces the honesty contract: every shipped
// row must name the documentation it was verified against. A row without a
// DocsURL is a guess, and a guess in this table writes to somebody's config file.
func TestBridgeTargetRowsAreComplete(t *testing.T) {
	for _, tgt := range verifiedBridgeTargets {
		t.Run(tgt.ID, func(t *testing.T) {
			if strings.TrimSpace(tgt.Name) == "" {
				t.Error("missing display name")
			}
			if !strings.HasPrefix(tgt.DocsURL, "https://") {
				t.Errorf("DocsURL must be an https source, got %q", tgt.DocsURL)
			}
			if len(tgt.UserKey) == 0 && len(tgt.ProjectKey) == 0 {
				t.Error("no MCP key path declared")
			}
			if tgt.UserPath == nil && tgt.ProjectPath == nil {
				t.Error("no config path declared")
			}
			switch tgt.Format {
			case FormatJSON, FormatTOML:
			default:
				t.Errorf("unknown format %q", tgt.Format)
			}
			switch tgt.Shape {
			case ShapeObject, ShapeLocalArray, ShapeCommandString, ShapeStdioTyped:
			default:
				t.Errorf("unknown entry shape %q", tgt.Shape)
			}
			if tgt.Shape == ShapeLocalArray && tgt.Format != FormatJSON {
				t.Errorf("local-array shape is only defined for JSON targets")
			}
			if tgt.Shape == ShapeCommandString && tgt.Format != FormatJSON {
				t.Errorf("command-string shape is only defined for JSON targets")
			}
			if len(tgt.DetectPaths) == 0 && len(tgt.DetectBinaries) == 0 {
				t.Error("no install-detection signal declared")
			}
		})
	}
}

// TestBridgeTargetTableDoesNotShadowBespokeAdapters guards the boundary between
// the two adapter generations.
func TestBridgeTargetTableDoesNotShadowBespokeAdapters(t *testing.T) {
	for _, tgt := range verifiedBridgeTargets {
		for _, id := range bespokeAdapterIDs {
			if tgt.ID == id {
				t.Errorf("target %q collides with a hand-written adapter", tgt.ID)
			}
		}
	}
}

// TestBridgeTargetTableHasNoUndeclaredSharedConfigFiles is the guard against the
// Windsurf class of bug: two hosts silently resolving to one file, so setting up
// either one rewrites the other's config. Any duplicate path must be declared
// explicitly in SharedConfigWith on both sides, which forces a human to
// acknowledge it.
func TestBridgeTargetTableHasNoUndeclaredSharedConfigFiles(t *testing.T) {
	home := filepath.Join("/tmp", "litespm-integrity-home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	type owner struct {
		target BridgeTarget
		path   string
	}
	byPath := map[string][]owner{}
	for _, tgt := range verifiedBridgeTargets {
		if tgt.UserPath == nil {
			continue
		}
		byPath[tgt.UserPath(home)] = append(byPath[tgt.UserPath(home)], owner{tgt, "user"})
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		owners := byPath[p]
		if len(owners) < 2 {
			continue
		}
		ids := make([]string, 0, len(owners))
		for _, o := range owners {
			ids = append(ids, o.target.ID)
		}
		for _, o := range owners {
			for _, other := range ids {
				if other == o.target.ID {
					continue
				}
				if !declaresShare(o.target, other) {
					t.Errorf("targets %v both resolve to %s but %q does not declare the shared file in SharedConfigWith",
						ids, p, o.target.ID)
				}
			}
		}
	}
}

func declaresShare(tgt BridgeTarget, other string) bool {
	for _, id := range tgt.SharedConfigWith {
		if id == other {
			return true
		}
	}
	return false
}

// TestBridgeTargetTableExcludesUnverifiedAgents pins the deliberate exclusions.
// Each of these has a documented reason for not shipping an adapter; if one ever
// becomes verifiable, this test is the reminder to remove the row on purpose
// rather than by accident.
func TestBridgeTargetTableExcludesUnverifiedAgents(t *testing.T) {
	excluded := []string{
		"adal", "autohand-code", "continue", "dexto", "eve", "goose", "hermes-agent",
		"lingma", "loaf", "mcpjam", "minimax-code", "mistral-vibe", "moxby",
		"promptscript", "reasonix", "replit", "sarvam-code", "terramind", "tinycloud",
		"trae", "trae-cn", "vtcode", "windsurf",
	}
	for _, id := range excluded {
		if _, ok := LookupBridgeTarget(id); ok {
			t.Errorf("target %q is registered but was classified as not verifiable", id)
		}
	}
}

func TestLookupBridgeTargetIsCaseSensitive(t *testing.T) {
	if _, ok := LookupBridgeTarget("Cursor"); ok {
		t.Error("lookup should not silently case-fold ids")
	}
	if _, ok := LookupBridgeTarget("cursor"); !ok {
		t.Error("expected cursor to be registered")
	}
}

func TestVerifiedBridgeTargetsReturnsACopy(t *testing.T) {
	first := VerifiedBridgeTargets()
	if len(first) == 0 {
		t.Fatal("expected at least one target")
	}
	first[0].Name = "mutated"
	second := VerifiedBridgeTargets()
	if second[0].Name == "mutated" {
		t.Error("VerifiedBridgeTargets must not expose the backing array")
	}
}

// TestGenericAdapterRoundTrip exercises the full plan/apply/verify path against
// a real temporary HOME, which is the only way to catch path-resolution and
// permission mistakes.
func TestGenericAdapterRoundTrip(t *testing.T) {
	home := useTempHome(t)
	adapter := adapterFor(t, "cursor")
	ctx := context.Background()

	// Pre-existing user content that must survive the install. The path comes
	// from the adapter, because on Windows the resolver may target %APPDATA%.
	configPath := configPathOf(t, adapter)
	original := "{\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"npx\"}\n  }\n}\n"
	writeConfig(t, configPath, original)

	plan, err := adapter.PlanSetup(ctx, "/opt/litespm/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if plan.OriginalContent != original {
		t.Error("plan did not capture the original content verbatim")
	}
	if plan.BackupPath == "" {
		t.Error("expected a backup to be taken before editing")
	}
	backup, err := os.ReadFile(plan.BackupPath)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if string(backup) != original {
		t.Error("backup is not byte-identical to the original")
	}

	result, err := adapter.ApplySetup(ctx, plan)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !result.Success {
		t.Error("apply reported failure")
	}

	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed := mustParse(t, string(written))
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["mine"]; !ok {
		t.Error("pre-existing server was destroyed")
	}
	entry, ok := servers["litespm"].(map[string]any)
	if !ok {
		t.Fatalf("bridge entry missing: %s", written)
	}
	if entry["command"] != "/opt/litespm/bin/litespm" {
		t.Errorf("unexpected command: %v", entry["command"])
	}
	args := entry["args"].([]any)
	if len(args) != 4 || args[3] != "cursor" {
		t.Errorf("unexpected args: %v", args)
	}

	verify, err := adapter.VerifySetup(ctx)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !verify.Registered || verify.Status != "ready" {
		t.Errorf("expected ready, got registered=%v status=%q", verify.Registered, verify.Status)
	}
}

// TestGenericAdapterPreservesUserCommentsEndToEnd is the regression test for the
// data-loss bug this writer exists to prevent.
func TestGenericAdapterPreservesUserCommentsEndToEnd(t *testing.T) {
	home := useTempHome(t)
	adapter := adapterFor(t, "zed")
	ctx := context.Background()

	path := configPathOf(t, adapter)
	original := "{\n  // my theme, do not clobber\n  \"theme\": \"One Dark\",\n  \"context_servers\": {\n    // my server\n    \"mine\": { \"command\": \"uvx\" }\n  }\n}\n"
	writeConfig(t, path, original)

	plan, err := adapter.PlanSetup(ctx, "/bin/litespm", filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if _, err := adapter.ApplySetup(ctx, plan); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"// my theme, do not clobber", "// my server", `"theme": "One Dark"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("comment or setting %q was destroyed:\n%s", want, out)
		}
	}
	parsed := mustParse(t, string(out))
	cs, ok := parsed["context_servers"].(map[string]any)
	if !ok {
		t.Fatalf("context_servers missing:\n%s", out)
	}
	if _, ok := cs["litespm"]; !ok {
		t.Fatalf("bridge entry missing:\n%s", out)
	}
	verify, err := adapter.VerifySetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if verify.Status != "ready" {
		t.Errorf("verification of a commented config failed: %+v", verify)
	}
}

func TestGenericAdapterDetectsPreExistingComponentsReadOnly(t *testing.T) {
	useTempHome(t)
	adapter := adapterFor(t, "cursor")
	writeConfig(t, configPathOf(t, adapter),
		`{"mcpServers":{"a":{"command":"x"},"litespm":{"command":"y"}}}`)
	comps, err := adapter.DetectPreExistingComponents(context.Background())
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}
	if len(comps) != 1 {
		t.Fatalf("expected only the foreign server, got %+v", comps)
	}
	if comps[0].Name != "a" {
		t.Errorf("unexpected component %q", comps[0].Name)
	}
	if !comps[0].ReadOnly {
		t.Error("detected components must be read-only")
	}
}

// TestGenericAdapterHonoursEnvOverride proves the documented home overrides are
// wired, since they are the only escape hatch for tests and sandboxed installs.
func TestGenericAdapterHonoursEnvOverride(t *testing.T) {
	home := useTempHome(t)
	alt := filepath.Join(home, "alt-home")
	t.Setenv("KIMI_CODE_HOME", alt)

	tgt, _ := LookupBridgeTarget("kimi-code-cli")
	adapter := NewGenericAdapter(tgt)
	got, err := adapter.DetectConfig(context.Background(), domain.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(alt, "mcp.json"); got != want {
		t.Fatalf("env override ignored: got %q want %q", got, want)
	}
}

func TestGenericAdapterProjectOnlyHostUsesProjectFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	t.Chdir(dir)

	tgt, ok := LookupBridgeTarget("codestudio")
	if !ok {
		t.Fatal("codestudio target missing")
	}
	if tgt.UserPath != nil {
		t.Fatal("codestudio is documented as workspace-only and must have no user path")
	}
	adapter := NewGenericAdapter(tgt)
	got, err := adapter.DetectConfig(context.Background(), domain.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".codestudio", "mcp.json"); got != want {
		t.Fatalf("project path mismatch: got %q want %q", got, want)
	}
}

func TestGenericAdapterRejectsProjectScopeWhenUndocumented(t *testing.T) {
	tgt := BridgeTarget{ID: "x", Name: "X", Format: FormatJSON, UserKey: []string{"mcpServers"}, UserPath: func(h string) string { return "/tmp/x.json" }}
	adapter := NewGenericAdapter(tgt)
	if _, err := adapter.DetectConfig(context.Background(), domain.ScopeProject); err == nil {
		t.Error("expected an error for a host with no documented project path")
	}
}

func TestGenericAdapterDescriptorIsComplete(t *testing.T) {
	for _, tgt := range verifiedBridgeTargets {
		d := NewGenericAdapter(tgt).Descriptor()
		if d.HostID != tgt.ID {
			t.Errorf("%s: descriptor host id %q", tgt.ID, d.HostID)
		}
		if d.DisplayName != tgt.Name {
			t.Errorf("%s: descriptor name %q", tgt.ID, d.DisplayName)
		}
		if d.SlashCommandTrigger != "/marketplace" {
			t.Errorf("%s: unexpected slash trigger %q", tgt.ID, d.SlashCommandTrigger)
		}
		if d.ConfigFormat != string(tgt.Format) {
			t.Errorf("%s: descriptor format %q", tgt.ID, d.ConfigFormat)
		}
	}
}

func TestRenderManualSetupNamesTheRealKey(t *testing.T) {
	cases := map[string]string{
		"zed":  "context_servers",
		"kilo": `"mcp"`,
		"amp":  "amp",
	}
	for id, want := range cases {
		tgt, ok := LookupBridgeTarget(id)
		if !ok {
			t.Fatalf("%s target missing", id)
		}
		snippet := NewGenericAdapter(tgt).RenderManualSetup("/bin/litespm")
		if !strings.Contains(snippet, want) {
			t.Errorf("%s: manual snippet does not mention %s:\n%s", id, want, snippet)
		}
		if !strings.Contains(snippet, "/bin/litespm") {
			t.Errorf("%s: manual snippet missing binary path:\n%s", id, snippet)
		}
	}
}

// TestEveryTableTargetAppliesToARealisticFile runs the whole table through a
// representative pre-existing config for each format. It is the broadest check
// that no row can emit a broken edit.
//
// Comment-bearing seeds are only used for hosts whose format permits comments:
// a strict-JSON host is expected to refuse a commented file rather than guess.
func TestEveryTableTargetAppliesToARealisticFile(t *testing.T) {
	strictSeed := "{\n  \"existingKey\": true,\n  \"mcpServers\": {\n    \"theirs\": {\"command\": \"keep-me\"}\n  }\n}\n"
	commentSeed := "{\n  // user content\n  \"existingKey\": true,\n  \"mcpServers\": {\n    \"theirs\": {\"command\": \"keep-me\"}\n  }\n}\n"
	for _, tgt := range verifiedBridgeTargets {
		t.Run(tgt.ID, func(t *testing.T) {
			a := NewGenericAdapter(tgt)
			seed := strictSeed
			wantUserContent := false
			if tgt.TolerateComments {
				seed = commentSeed
				wantUserContent = true
			}
			if tgt.Format == FormatTOML {
				seed = "# user content\n[other]\nkey = 1\n"
				wantUserContent = true
			}
			out, err := a.renderConfig(seed, "/bin/litespm", false)
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}
			if wantUserContent && !strings.Contains(out, "user content") {
				t.Errorf("user content dropped:\n%s", out)
			}
			if tgt.Format == FormatTOML {
				if !strings.Contains(out, "[mcp_servers."+litespmServerName+"]") {
					t.Errorf("bridge table missing:\n%s", out)
				}
				if !strings.Contains(out, "key = 1") {
					t.Errorf("sibling table damaged:\n%s", out)
				}
				return
			}
			parsed := mustParse(t, out)
			if !inspectEntry(parsed, tgt.keyPathFor(false)) {
				t.Errorf("bridge entry not present at %v:\n%s", tgt.keyPathFor(false), out)
			}
			if parsed["existingKey"] != true {
				t.Errorf("unrelated key damaged:\n%s", out)
			}
			if !strings.Contains(out, "keep-me") {
				t.Errorf("foreign server damaged:\n%s", out)
			}
		})
	}
}

// TestStrictJSONHostsRefuseCommentedFiles pins the opposite expectation, so the
// two halves of the comment policy cannot drift into each other.
func TestStrictJSONHostsRefuseCommentedFiles(t *testing.T) {
	commented := "{\n  // user comment\n  \"mcpServers\": {}\n}\n"
	strict, ok := LookupBridgeTarget("cursor")
	if !ok {
		t.Fatal("cursor target missing")
	}
	if _, err := NewGenericAdapter(strict).renderConfig(commented, "/bin/litespm", false); err == nil {
		t.Error("expected a strict-JSON host to refuse a config containing comments")
	}
	tolerant, ok := LookupBridgeTarget("amp")
	if !ok {
		t.Fatal("amp target missing")
	}
	out, err := NewGenericAdapter(tolerant).renderConfig(commented, "/bin/litespm", false)
	if err != nil {
		t.Fatalf("a comment-tolerant host should accept this file: %v", err)
	}
	if !strings.Contains(out, "// user comment") {
		t.Errorf("comment dropped:\n%s", out)
	}
}

// --- remote (URL) capability matrix — B1 acceptance, PART 1 §1.4/§1.5 -----

// remoteCapableBespokeIDs are the hand-written adapter ids whose remote shape
// §1.3 verified, including the two documented alias ids (pi and grok) that
// resolve to the same adapters as pi-agent and grok-build.
var remoteCapableBespokeIDs = []string{"claude-code", "cline", "opencode", "codex", "pi-agent", "pi", "grok", "grok-build"}

// remoteCapableGenericIDs are the Tier A rows: the only two generic targets
// whose exact remote entry object was read from the row's DocsURL.
var remoteCapableGenericIDs = []string{"cursor", "zed"}

// TestRemoteCapabilityMatrixCounts is the anti-mass-enable guard: for every
// registered host RemoteEntrySpecFor must answer (spec or false), and the
// counts must stay at exactly 8 capable hosts / 42 refused targets. Enabling
// a Tier B/C/D row without evidence — or disabling a verified one — fails
// here rather than silently changing what installs write.
func TestRemoteCapabilityMatrixCounts(t *testing.T) {
	capableIDs := map[string]bool{}
	refusedIDs := map[string]bool{}
	for _, a := range ListAdapters() {
		id := a.Descriptor().HostID
		spec, ok := RemoteEntrySpecFor(id)
		if ok {
			capableIDs[id] = true
			if !spec.Supports(TransportStreamableHTTP) {
				t.Errorf("host %q is capable but does not declare %q", id, TransportStreamableHTTP)
			}
		} else {
			refusedIDs[id] = true
		}
	}

	// Exactly 8 capable HOSTS among the registered adapter ids.
	wantCapable := map[string]bool{
		"claude-code": true, "cline": true, "opencode": true, "codex": true,
		"pi-agent": true, "grok-build": true, "cursor": true, "zed": true,
	}
	if len(capableIDs) != 8 {
		t.Errorf("remote-capable hosts = %d, want 8 (capable ids: %v)", len(capableIDs), capableIDs)
	}
	for id := range wantCapable {
		if !capableIDs[id] {
			t.Errorf("host %q must be remote-capable but RemoteEntrySpecFor refused it", id)
		}
	}
	for id := range capableIDs {
		if !wantCapable[id] {
			t.Errorf("host %q is remote-capable but is not in the verified set — a spec was enabled without evidence", id)
		}
	}
	// Exactly 42 refused targets, and the two sets cover the registry.
	if len(refusedIDs) != 42 {
		t.Errorf("remote-refused hosts = %d, want 42 (refused ids: %v)", len(refusedIDs), refusedIDs)
	}
	if len(capableIDs)+len(refusedIDs) != len(ListAdapters()) {
		t.Errorf("matrix does not cover the registry: %d capable + %d refused vs %d adapters",
			len(capableIDs), len(refusedIDs), len(ListAdapters()))
	}
	// The alias ids answer too (they resolve to the same adapters).
	for _, alias := range []string{"pi", "grok"} {
		if _, ok := RemoteEntrySpecFor(alias); !ok {
			t.Errorf("alias id %q must resolve the same remote capability as its canonical adapter", alias)
		}
	}
	// Every bespoke adapter must be capable (§1.3 verified all six); the
	// refused set is generic-rows-only.
	for _, id := range remoteCapableBespokeIDs {
		if refusedIDs[id] {
			t.Errorf("bespoke adapter %q is refused; §1.3 verified its remote shape", id)
		}
		if _, ok := RemoteEntrySpecFor(id); !ok {
			t.Errorf("bespoke adapter %q answered refused to RemoteEntrySpecFor", id)
		}
	}
	// Generic rows: exactly cursor and zed carry a spec, the other 42 do not.
	genericCapable := map[string]bool{}
	for _, tgt := range verifiedBridgeTargets {
		if tgt.Remote != nil {
			genericCapable[tgt.ID] = true
		}
	}
	if len(genericCapable) != len(remoteCapableGenericIDs) {
		t.Errorf("generic rows with a Remote spec = %d, want %d (%v)",
			len(genericCapable), len(remoteCapableGenericIDs), genericCapable)
	}
	for _, id := range remoteCapableGenericIDs {
		if !genericCapable[id] {
			t.Errorf("Tier A row %q lost its Remote spec", id)
		}
	}
}

// TestRemoteSpecHonoursTheHonestyRule is Step 2.1's validation: a Remote spec
// may only exist where the row documents the evidence for it (DocsURL), may
// only declare registry vocabulary, and must include the default transport.
func TestRemoteSpecHonoursTheHonestyRule(t *testing.T) {
	vocab := map[string]bool{}
	for _, v := range remoteTransportVocab {
		vocab[v] = true
	}
	for _, tgt := range verifiedBridgeTargets {
		if tgt.Remote == nil {
			continue
		}
		t.Run(tgt.ID, func(t *testing.T) {
			if strings.TrimSpace(tgt.DocsURL) == "" {
				t.Error("Remote spec without a DocsURL: the entry object was never verified")
			}
			if len(tgt.Remote.Transports) == 0 {
				t.Error("Remote spec declares no transports")
			}
			seen := map[string]bool{}
			for _, tr := range tgt.Remote.Transports {
				if !vocab[tr] {
					t.Errorf("transport %q is outside the registry vocabulary %v", tr, remoteTransportVocab)
				}
				if seen[tr] {
					t.Errorf("transport %q declared twice", tr)
				}
				seen[tr] = true
			}
			if !seen[TransportStreamableHTTP] {
				t.Error("every capable host must express streamable-http (it is the registry default)")
			}
			if tgt.Remote.URLKey == "" {
				t.Error("Remote spec with no URL key")
			}
			// A host that discriminates by type must know BOTH spellings, or
			// the writer would guess the missing one (the typeless-url hazard).
			if tgt.Remote.TypeKey != "" && tgt.Remote.TypeValue == "" {
				t.Error("discriminator key without its streamable-http value")
			}
		})
	}
}

// TestTierBRowsCarryNoRemoteSpec pins the four PARTIAL-evidence rows as
// refused: a field name glimpsed in a doc is not an entry shape, and enabling
// one without pinning the whole object writes an entry the host ignores.
func TestTierBRowsCarryNoRemoteSpec(t *testing.T) {
	for _, id := range []string{"antigravity", "antigravity-cli", "qwen-code", "crush"} {
		t.Run(id, func(t *testing.T) {
			tgt, ok := LookupBridgeTarget(id)
			if !ok {
				t.Fatalf("Tier B row %q is not registered", id)
			}
			if tgt.Remote != nil {
				t.Errorf("Tier B row %q has a Remote spec; §1.4 requires re-verification first", id)
			}
			if _, ok := RemoteEntrySpecFor(id); ok {
				t.Errorf("RemoteEntrySpecFor(%q) reports capability for a Tier B row", id)
			}
		})
	}
	// Tier C rows (cannot carry a URL entry at all) stay refused too.
	for _, id := range []string{"amp", "mux"} {
		if _, ok := RemoteEntrySpecFor(id); ok {
			t.Errorf("Tier C row %q must stay refused", id)
		}
	}
}
