package interop

// serverjson_live_test.go — `litespm import server.json` pinned against
// VERBATIM live registry documents (testdata/registry-live-*.json; see
// testdata/registry-live-provenance.md for capture provenance).
//
// Two properties are asserted against the real bytes:
//
//  1. the 2025-12-11 schema's documents PARSE — repository object,
//     identifier coordinates, transport objects, `type` remotes;
//  2. nothing derived from `registryType`, `identifier` or `runtimeHint`
//     ever reaches the IR or the plan as a launch line, and no environment
//     or header VALUE is reachable at all.

import (
	"bytes"
	"strings"
	"testing"
)

// livePlan builds a plan over a parsed doc for the negative assertions.
func livePlan(t *testing.T, data []byte) (string, string) {
	t.Helper()
	doc, err := ParseServerJSON(data)
	if err != nil {
		t.Fatalf("parse live document: %v", err)
	}
	plan := BuildPlan(doc, "/tmp/litespm.toml", nil)
	var buf bytes.Buffer
	plan.Render(&buf)
	payload, err := plan.JSON(false)
	if err != nil {
		t.Fatalf("plan JSON: %v", err)
	}
	return buf.String(), string(payload)
}

// assertNoLaunchLine fails if any rendering of the IR/plan carries a command
// field, a "command=" rendering, or the runtimeHint promoted into one.
func assertNoLaunchLine(t *testing.T, parts ...string) {
	t.Helper()
	for _, s := range parts {
		for _, banned := range []string{
			`"command"`, "command=", "command-present", `"hasCommand": true`,
			`"args"`, "npx -y", "npx@",
		} {
			if strings.Contains(s, banned) {
				t.Errorf("output carries a launch-line ingredient %q:\n%s", banned, s)
			}
		}
	}
}

// TestServerJSONLiveSubset parses the captured live server documents.
func TestServerJSONLiveSubset(t *testing.T) {
	doc, err := ParseServerJSON(fixture(t, "registry-live-servers.json"))
	if err != nil {
		t.Fatalf("parse live subset: %v", err)
	}
	if len(doc.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(doc.Entries))
	}
	byID := map[string]int{}
	for i, e := range doc.Entries {
		byID[e.ID] = i
	}

	// repository is an OBJECT on the live schema: URL and source both land.
	bev := doc.Entries[byID["mcp:builtin:mcp-registry:ai.agent-bev%2Fbev-door"]]
	if bev.Foreign.Source != "https://github.com/agent-bev/bev-door" || bev.Foreign.SourceType != "github" {
		t.Errorf("repository object not mapped: %+v", bev.Foreign)
	}
	if bev.Constraint != "0.2.5" {
		t.Errorf("constraint = %q, want 0.2.5", bev.Constraint)
	}
	if len(bev.Foreign.Packages) != 1 {
		t.Fatalf("packages = %+v", bev.Foreign.Packages)
	}
	p := bev.Foreign.Packages[0]
	if p.Name != "@agent-bev/mcp-server" || p.RegistryType != "npm" || p.Transport != "stdio" {
		t.Errorf("coordinate/transport not preserved: %+v", p)
	}
	if p.HasCommand {
		t.Error("a live package must never set hasCommand")
	}
	if len(bev.Foreign.Remotes) != 1 || bev.Foreign.Remotes[0].Transport != "streamable-http" {
		t.Errorf("remote `type` not mapped: %+v", bev.Foreign.Remotes)
	}

	// A remote-only document: unconstrained, endpoint recorded.
	ac := doc.Entries[byID["mcp:builtin:mcp-registry:ac.inference.sh%2Fmcp"]]
	if ac.Constraint != "" {
		t.Errorf("remote-only constraint = %q, want empty", ac.Constraint)
	}
	if !containsSub(ac.Warnings, "remote endpoint only") {
		t.Errorf("remote-only warning missing: %v", ac.Warnings)
	}
	if len(ac.Foreign.Remotes) != 1 || ac.Foreign.Remotes[0].URL != "https://api.inference.sh/mcp" {
		t.Errorf("remote URL not preserved: %+v", ac.Foreign.Remotes)
	}

	// sse remote + repository object.
	adramp := doc.Entries[byID["mcp:builtin:mcp-registry:ai.adramp%2Fgoogle-ads"]]
	if len(adramp.Foreign.Remotes) != 1 || adramp.Foreign.Remotes[0].Transport != "sse" {
		t.Errorf("sse remote not preserved: %+v", adramp.Foreign.Remotes)
	}

	// environmentVariables[] → NAMES only.
	adtest := doc.Entries[byID["mcp:builtin:mcp-registry:ai.adtest%2Fadtest-mcp"]]
	want := []string{"ADTEST_API_BASE", "ADTEST_API_KEY", "ADTEST_POLL_TIMEOUT_MS"}
	got := adtest.Foreign.Packages[0].EnvNames
	if len(got) != len(want) {
		t.Fatalf("env names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("env names = %v, want %v", got, want)
		}
	}
	if adtest.Foreign.Packages[0].HasCommand {
		t.Error("a live package must never set hasCommand")
	}

	// runtimeHint is RECORDED as a hint, never promoted.
	affiliate := doc.Entries[byID["mcp:builtin:mcp-registry:ai.agenticaffiliate%2Faffiliate-networks-mcp"]]
	ap := affiliate.Foreign.Packages[0]
	if ap.RuntimeHint != "npx" {
		t.Errorf("runtimeHint not recorded: %+v", ap)
	}
	if ap.HasCommand {
		t.Error("runtimeHint must not flip hasCommand")
	}
	if !strings.Contains(affiliate.Foreign.String(), "runtimeHint=npx") {
		t.Errorf("runtimeHint not rendered as a hint: %s", affiliate.Foreign.String())
	}

	// Header names reported, values never decoded (the live doc declares
	// none, and the record must say the values are not imported).
	agentdm := doc.Entries[byID["mcp:builtin:mcp-registry:ai.agentdm%2Fagentdm"]]
	if !containsSub(agentdm.Warnings, "header values are never imported") {
		t.Errorf("header-value rule not stated: %v", agentdm.Warnings)
	}
	if !containsSub(agentdm.Warnings, "Authorization") {
		t.Errorf("header name not reported: %v", agentdm.Warnings)
	}

	// versionless package: coordinate reported, not pinned, and the record
	// says so instead of inventing a version.
	apithr := doc.Entries[byID["mcp:builtin:mcp-registry:ai.apithreshold%2Fapithreshold"]]
	if apithr.Constraint != "" {
		t.Errorf("versionless package pinned to %q", apithr.Constraint)
	}
	if !containsSub(apithr.Warnings, "publishes no version") {
		t.Errorf("versionless warning missing: %v", apithr.Warnings)
	}
	if !containsSub(apithr.Warnings, "packageArguments but names no executable") {
		t.Errorf("packageArguments not reported as a count: %v", apithr.Warnings)
	}
	// arguments must not be rendered as a command line anywhere
	if strings.Contains(apithr.Foreign.String(), "serve") {
		t.Errorf("package argument value rendered into the record: %s", apithr.Foreign.String())
	}

	// Unknown-to-the-consumer live members are warnings, not refusals
	// ($schema, version, websiteUrl, icons, _meta, license).
	if !containsSub(adtest.Warnings, "unknown field") {
		t.Errorf("live unknown fields must be reported: %v", adtest.Warnings)
	}

	// Global: no entry ever claims a command, and no env/header value is
	// anywhere in the IR.
	for _, e := range doc.Entries {
		for _, pkg := range e.Foreign.Packages {
			if pkg.HasCommand {
				t.Errorf("%s: hasCommand set on a live document", e.ID)
			}
		}
	}
	plan := BuildPlan(doc, "/tmp/litespm.toml", nil)
	var buf bytes.Buffer
	plan.Render(&buf)
	payload, err := plan.JSON(false)
	if err != nil {
		t.Fatal(err)
	}
	assertNoLaunchLine(t, buf.String(), string(payload))
}

// TestServerJSONLiveSingleObject parses the single-object wire shape.
func TestServerJSONLiveSingleObject(t *testing.T) {
	doc, err := ParseServerJSON(fixture(t, "registry-live-single.json"))
	if err != nil {
		t.Fatalf("parse live single: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.ID != "mcp:builtin:mcp-registry:ai.agent-bev%2Fbev-door" || e.Constraint != "0.2.5" {
		t.Errorf("entry = %+v", e)
	}
	if e.Foreign.Source != "https://github.com/agent-bev/bev-door" {
		t.Errorf("source = %q", e.Foreign.Source)
	}
}

// TestServerJSONListResponseRefusedAccurately: the registry API's ?limit=100
// response is a paginated collection of versioned documents (100 entries, 50
// distinct names) — not a server.json document. Import must name that shape
// rather than fail on an incidental field.
func TestServerJSONListResponseRefusedAccurately(t *testing.T) {
	_, err := ParseServerJSON(fixture(t, "registry-live-list-100.json"))
	wantCode(t, err, ErrCodeSchema)
	for _, want := range []string{"list response", "servers", "not a server.json document"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must say %q, got: %v", want, err)
		}
	}
}

// TestServerJSONLiveSchemaEdgeNeverLaunches is the demanded negative test
// against the schema members no live entry happened to publish at capture
// time (fileSha256, runtimeHint, package/runtime arguments, environment and
// header VALUES). The sentinel values in the fixture must be unreachable and
// no launch line may exist in any rendering.
func TestServerJSONLiveSchemaEdgeNeverLaunches(t *testing.T) {
	data := fixture(t, "server-live-schema-edge.json")
	doc, err := ParseServerJSON(data)
	if err != nil {
		t.Fatalf("parse edge fixture: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.Constraint != "9.9.9" {
		t.Errorf("constraint = %q, want 9.9.9", e.Constraint)
	}
	if len(e.Foreign.Packages) != 1 {
		t.Fatalf("packages = %+v", e.Foreign.Packages)
	}
	p := e.Foreign.Packages[0]
	if p.FileSha256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Errorf("fileSha256 not recorded: %+v", p)
	}
	if p.RuntimeHint != "npx" {
		t.Errorf("runtimeHint not recorded: %+v", p)
	}
	if p.HasCommand {
		t.Error("hasCommand must stay false: the document declares no command")
	}
	if p.Name != "com.litespm.schema-edge/fixture" || p.RegistryType != "mcpb" {
		t.Errorf("coordinate not preserved: %+v", p)
	}
	if len(p.EnvNames) != 2 || p.EnvNames[0] != "EDGE_API_KEY" || p.EnvNames[1] != "EDGE_MODEL" {
		t.Errorf("env names = %v", p.EnvNames)
	}
	// repository members with no record field are reported, not dropped.
	if !containsSub(e.Foreign.Notes, "repository id repo-id-12345") {
		t.Errorf("repository id not reported: %v", e.Foreign.Notes)
	}
	if !containsSub(e.Foreign.Notes, "repository subfolder packages/edge") {
		t.Errorf("repository subfolder not reported: %v", e.Foreign.Notes)
	}
	if !containsSub(e.Warnings, "declares 2 packageArguments but names no executable") {
		t.Errorf("packageArguments count missing: %v", e.Warnings)
	}
	if !containsSub(e.Warnings, "declares 1 runtimeArguments but names no executable") {
		t.Errorf("runtimeArguments count missing: %v", e.Warnings)
	}
	if !containsSub(e.Warnings, "header values are never imported") {
		t.Errorf("header-value rule missing: %v", e.Warnings)
	}
	if e.Foreign.Digest != "" {
		t.Errorf("fileSha256 must not be promoted into the record digest: %q", e.Foreign.Digest)
	}

	// The negative: values and launch lines are unreachable in every
	// rendering of the IR and the plan.
	plan := BuildPlan(doc, "/tmp/litespm.toml", nil)
	var buf bytes.Buffer
	plan.Render(&buf)
	payload, err := plan.JSON(false)
	if err != nil {
		t.Fatal(err)
	}
	renderings := []string{
		e.Foreign.String(),
		strings.Join(e.Warnings, "\n"),
		strings.Join(e.Foreign.Notes, "\n"),
		strings.Join(doc.Lossy, "\n"),
		buf.String(),
		string(payload),
	}
	for _, s := range renderings {
		for _, sentinel := range []string{
			"hunter2-never-printed",
			"SENTINEL_PACKAGE_ARG_NEVER_LAUNCHED",
			"SENTINEL_NAMED_ARG_NEVER_LAUNCHED",
			"SENTINEL_RUNTIME_ARG_NEVER_LAUNCHED",
		} {
			if strings.Contains(s, sentinel) {
				t.Errorf("value %q leaked into the IR/plan:\n%s", sentinel, s)
			}
		}
	}
	assertNoLaunchLine(t, renderings...)

	// The digest is reported as lossy (it has nowhere to be written), which
	// is a statement about the manifest, not a launch fact.
	if !containsSub(doc.Lossy, "package digests have no litespm.yml field") {
		t.Errorf("digest lossy line missing: %v", doc.Lossy)
	}
}

// TestServerJSONRejectsRegistryEdgeMalformations: fail-closed stays where
// the document itself is the problem.
func TestServerJSONRejectsRegistryEdgeMalformations(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"package with neither identifier nor name",
			`{"name":"x/y","packages":[{"registryType":"npm","version":"1.0.0"}]}`,
			"packages[0].identifier is required"},
		{"malformed fileSha256",
			`{"name":"x/y","packages":[{"registryType":"npm","identifier":"x-y","version":"1.0.0","fileSha256":"NOTHEX"}]}`,
			"fileSha256"},
		{"remote with neither type nor transport",
			`{"name":"x/y","remotes":[{"url":"https://e.example/mcp"}]}`,
			"remotes[0].type is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseServerJSON([]byte(tc.data))
			wantCode(t, err, ErrCodeSchema)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error must contain %q, got: %v", tc.want, err)
			}
		})
	}
}
