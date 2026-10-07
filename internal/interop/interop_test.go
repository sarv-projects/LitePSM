package interop

// interop_test.go — the `litespm import` parser, plan and approval tests
// (ARCH/32 §5). The acceptance criteria of TODO row LPSM-K005 live here:
// fixtures parse to the right IR, the preview is required before any write,
// oversized and path-traversal inputs are refused with named codes, and the
// plugin.json schema is pinned rather than guessed.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/manifest"
)

// fixture reads one testdata file.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// wantCode fails unless err carries exactly the named LPSM-IMPORT code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	if got := CodeOf(err); got != code {
		t.Fatalf("expected error %s, got %q (err: %v)", code, got, err)
	}
}

// ---- format tokens ----------------------------------------------------------

func TestNormalizeFormat(t *testing.T) {
	for _, tok := range []string{"skills-lock", "server.json", "claude-marketplace", "codex-marketplace", "plugin"} {
		got, err := NormalizeFormat(tok)
		if err != nil || got != tok {
			t.Errorf("NormalizeFormat(%q) = %q, %v", tok, got, err)
		}
	}
	if got, err := NormalizeFormat(" Skills-Lock "); err != nil || got != "skills-lock" {
		t.Errorf("NormalizeFormat should fold case/space, got %q, %v", got, err)
	}
	_, err := NormalizeFormat("apm.yml")
	wantCode(t, err, ErrCodeFormat)
}

// ---- skills-lock ------------------------------------------------------------

func TestSkillsLockHappyPath(t *testing.T) {
	doc, err := ParseSkillsLock(fixture(t, "skills-lock.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(doc.Entries))
	}
	byID := map[string]Entry{}
	for _, e := range doc.Entries {
		byID[e.ID] = e
	}

	front, ok := byID["skill:git:vercel-labs-agent-skills:frontend-design"]
	if !ok {
		t.Fatalf("missing frontend-design id, got ids: %v", keysOf(doc.Entries))
	}
	if front.Constraint != "main" {
		t.Errorf("frontend-design constraint = %q, want ref verbatim %q", front.Constraint, "main")
	}
	if front.Kind != "skill" {
		t.Errorf("kind = %q, want skill", front.Kind)
	}
	if front.Foreign.Source != "vercel-labs/agent-skills" || front.Foreign.SourceType != "github" {
		t.Errorf("foreign source not preserved: %+v", front.Foreign)
	}
	if front.Foreign.SkillPath != "skills/frontend-design/SKILL.md" {
		t.Errorf("skillPath not preserved: %q", front.Foreign.SkillPath)
	}
	const frontendHash = "e8a9f1c2d3b4a5968778695a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c"
	if front.Foreign.Digest != frontendHash {
		t.Errorf("digest not preserved: got %q, want %q", front.Foreign.Digest, frontendHash)
	}
	// A branch ref is a warning, not silence: it is recorded verbatim and
	// the plan says `litespm lock` may refuse it.
	if !containsSub(front.Warnings, "branch or tag name") {
		t.Errorf("expected a branch-ref warning, got %v", front.Warnings)
	}

	if _, ok := byID["skill:git:vercel-labs-skills:release-notes"]; !ok {
		t.Errorf("missing release-notes id, got ids: %v", keysOf(doc.Entries))
	}
	pdf, ok := byID["skill:npm:acme-skills:pdf-tools"]
	if !ok {
		t.Fatalf("missing pdf-tools id, got ids: %v", keysOf(doc.Entries))
	}
	if pdf.Constraint != "" {
		t.Errorf("pdf-tools should import unconstrained, got %q", pdf.Constraint)
	}
	if !containsSub(pdf.Warnings, "no version or ref") {
		t.Errorf("expected an unconstrained warning, got %v", pdf.Warnings)
	}

	// Lossy: the format's own absence is stated, hashes are reported not
	// written — and absent concepts (subagents, UI state) are not claimed.
	joined := strings.Join(doc.Lossy, "\n")
	if !strings.Contains(joined, "no effect or permission data") {
		t.Errorf("missing effect/permission lossy line:\n%s", joined)
	}
	if !strings.Contains(joined, "content hashes") {
		t.Errorf("missing content-hash lossy line:\n%s", joined)
	}
	if strings.Contains(joined, "subagent placement") || strings.Contains(joined, "UI state") {
		t.Errorf("lossy lines for absent fields must not appear:\n%s", joined)
	}
}

func TestSkillsLockGlobalV3(t *testing.T) {
	doc, err := ParseSkillsLock(fixture(t, "skills-lock-v3.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.ID != "skill:git:vercel-labs-agent-skills:web-design-guidelines" {
		t.Errorf("id = %q", e.ID)
	}
	if len(e.Foreign.Digest) != 40 {
		t.Errorf("git tree SHA not preserved: %q", e.Foreign.Digest)
	}
	if !containsSub(e.Foreign.Notes, "web-design-tools") {
		t.Errorf("pluginName not reported: %v", e.Foreign.Notes)
	}
	if !containsSub(doc.Warnings, "global skills lock (version 3)") {
		t.Errorf("missing global-lock scope warning: %v", doc.Warnings)
	}
	joined := strings.Join(doc.Lossy, "\n")
	if !strings.Contains(joined, "UI state") {
		t.Errorf("dismissed/lastSelectedAgents present but UI-state lossy line missing:\n%s", joined)
	}
}

func TestSkillsLockRejectsMaliciousAndMalformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		code string
	}{
		{"traversal in skill name", fixture(t, "skills-lock-traversal.json"), ErrCodeUnsafe},
		{"deep nesting", fixture(t, "skills-lock-deep.json"), ErrCodeSyntax},
		{"unsupported version", []byte(`{"version":2,"skills":{}}`), ErrCodeSchema},
		{"version wrong type", []byte(`{"version":"1","skills":{}}`), ErrCodeSchema},
		{"missing version", []byte(`{"skills":{}}`), ErrCodeSchema},
		{"missing skills", []byte(`{"version":1}`), ErrCodeSchema},
		{"null skills", []byte(`{"version":1,"skills":null}`), ErrCodeSchema},
		{"entry not an object", []byte(`{"version":1,"skills":{"a":"str"}}`), ErrCodeSchema},
		{"missing source", []byte(`{"version":1,"skills":{"a":{"sourceType":"github"}}}`), ErrCodeSchema},
		{"empty sourceType", []byte(`{"version":1,"skills":{"a":{"source":"x","sourceType":""}}}`), ErrCodeSchema},
		{"absolute source", []byte(`{"version":1,"skills":{"a":{"source":"/etc/passwd","sourceType":"local"}}}`), ErrCodeUnsafe},
		{"traversal skillPath", []byte(`{"version":1,"skills":{"a":{"source":"x","sourceType":"local","skillPath":"../SKILL.md"}}}`), ErrCodeUnsafe},
		{"bad computedHash", []byte(`{"version":1,"skills":{"a":{"source":"x","sourceType":"local","computedHash":"nothex"}}}`), ErrCodeSchema},
		{"timestamp wrong type", []byte(`{"version":1,"skills":{"a":{"source":"x","sourceType":"local","installedAt":123}}}`), ErrCodeSchema},
		{"not JSON", []byte(`{version:1}`), ErrCodeSyntax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSkillsLock(tc.data)
			wantCode(t, err, tc.code)
		})
	}
}

func TestSkillsLockReportsUnknownFieldsAndSubagents(t *testing.T) {
	data := []byte(`{"version":1,"futureTop":1,"skills":{"a":{"source":"x","sourceType":"local",
		"futureEntry":2,"subagents":["reviewer"]}}}`)
	doc, err := ParseSkillsLock(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !containsSub(doc.Warnings, "unknown field (top level).futureTop") {
		t.Errorf("unknown top-level field not reported: %v", doc.Warnings)
	}
	entryWarns := doc.Entries[0].Warnings
	if !containsSub(entryWarns, "unknown field skills.a.futureEntry") {
		t.Errorf("unknown entry field not reported on the entry: %v", entryWarns)
	}
	if !containsSub(entryWarns, "subagent placement") {
		t.Errorf("subagents not reported on the entry: %v", entryWarns)
	}
	if !containsSub(doc.Lossy, "placement (subagents)") {
		t.Errorf("subagent lossy line missing: %v", doc.Lossy)
	}
}

// ---- size and depth bounds --------------------------------------------------

func TestSizeBound(t *testing.T) {
	oversized := make([]byte, MaxImportBytes+1)
	wantCode(t, ValidateInput(oversized), ErrCodeSize)

	path := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(path, oversized, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFileBounded(path)
	wantCode(t, err, ErrCodeSize)

	// A well-formed file inside the bound reads back exactly.
	small := []byte(`{"version":1,"skills":{}}`)
	if err := os.WriteFile(path, small, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFileBounded(path)
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("ReadFileBounded = %q, %v", got, err)
	}
}

func TestDepthBoundIgnoresBracketsInStrings(t *testing.T) {
	// Containers inside string values are not nesting.
	if err := ValidateInput([]byte(`{"a":"[[[[[[[[[[[[[[[[[[[[[[[[[[[[[["}`)); err != nil {
		t.Fatalf("brackets in a string must not count as depth: %v", err)
	}
}

// ---- server.json ------------------------------------------------------------

func TestServerJSONSingleObject(t *testing.T) {
	doc, err := ParseServerJSON(fixture(t, "server-single.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.ID != "mcp:builtin:mcp-registry:postgres" {
		t.Errorf("id = %q", e.ID)
	}
	if e.Constraint != "0.6.2" {
		t.Errorf("constraint = %q, want 0.6.2", e.Constraint)
	}
	// Namespace-verification assertions preserved, not flattened.
	if e.Foreign.Publisher != "Model Context Protocol" {
		t.Errorf("publisher not preserved: %q", e.Foreign.Publisher)
	}
	if e.Foreign.PublisherURL != "https://modelcontextprotocol.io" {
		t.Errorf("publisher url not preserved: %q", e.Foreign.PublisherURL)
	}
	if len(e.Foreign.Packages) != 1 {
		t.Fatalf("packages not preserved: %+v", e.Foreign.Packages)
	}
	p := e.Foreign.Packages[0]
	if p.Name != "@modelcontextprotocol/server-postgres" || p.RegistryType != "npm" || p.Version != "0.6.2" {
		t.Errorf("registry-qualified coordinate flattened: %+v", p)
	}
	if len(p.EnvNames) != 1 || p.EnvNames[0] != "POSTGRES_CONNECTION_SECRET" {
		t.Errorf("env names not recorded: %v", p.EnvNames)
	}
	if !p.HasCommand {
		t.Error("command presence not reported")
	}
	if e.Foreign.Transport != "stdio" {
		t.Errorf("transport = %q", e.Foreign.Transport)
	}
	// Unknown-to-the-schema field (license) is reported on the entry, not dropped.
	if !containsSub(e.Warnings, "unknown field server postgres.license") {
		t.Errorf("license field silently dropped: %v", e.Warnings)
	}
	if !strings.Contains(strings.Join(doc.Lossy, "\n"), "namespace-verification") {
		t.Errorf("namespace-verification lossy/preserve line missing: %v", doc.Lossy)
	}
}

func TestServerJSONArray(t *testing.T) {
	doc, err := ParseServerJSON(fixture(t, "server-array.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(doc.Entries))
	}
	remote := doc.Entries[0]
	if remote.ID != "mcp:builtin:mcp-registry:remote-search" || remote.Constraint != "" {
		t.Errorf("remote entry = %+v", remote)
	}
	if len(remote.Foreign.Remotes) != 1 || remote.Foreign.Remotes[0].AuthType != "oauth2" {
		t.Errorf("remotes not preserved: %+v", remote.Foreign.Remotes)
	}
	if !containsSub(remote.Warnings, "remote endpoint only") {
		t.Errorf("remote-only warning missing: %v", remote.Warnings)
	}
	versionless := doc.Entries[1]
	if versionless.Constraint != "" {
		t.Errorf("versionless server must import unconstrained, got %q", versionless.Constraint)
	}
	// A versionless package is REPORTED (warnings), never silently skipped.
	if !containsSub(versionless.Warnings, "publishes no version") {
		t.Errorf("versionless package not reported: %v", versionless.Warnings)
	}
	if len(versionless.Foreign.Packages) != 1 || versionless.Foreign.Packages[0].Version != "" {
		t.Errorf("versionless coordinate not preserved: %+v", versionless.Foreign.Packages)
	}
}

func TestServerJSONRejectsMaliciousAndMalformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		code string
	}{
		{"traversal name", fixture(t, "server-traversal.json"), ErrCodeUnsafe},
		{"missing name", []byte(`{"packages":[]}`), ErrCodeSchema},
		{"duplicate name", []byte(`[{"name":"x"},{"name":"x"}]`), ErrCodeSchema},
		{"package missing registryType", []byte(`{"name":"x","packages":[{"name":"p"}]}`), ErrCodeSchema},
		{"bad package digest", []byte(`{"name":"x","packages":[{"registryType":"npm","name":"p","version":"1","digest":"deadbeef"}]}`), ErrCodeSchema},
		{"remote missing url", []byte(`{"name":"x","remotes":[{"transport":"sse"}]}`), ErrCodeSchema},
		{"remote missing transport", []byte(`{"name":"x","remotes":[{"url":"https://e.com"}]}`), ErrCodeSchema},
		{"top level not object/array", []byte(`"server.json"`), ErrCodeSchema},
		{"status wrong type", []byte(`{"name":"x","status":5}`), ErrCodeSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseServerJSON(tc.data)
			wantCode(t, err, tc.code)
		})
	}
}

func TestServerJSONNeverPrintsEnvValues(t *testing.T) {
	doc, err := ParseServerJSON(fixture(t, "server-single.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The value must not be reachable through the IR at all — not merely
	// absent from one rendering.
	if strings.Contains(strings.Join([]string{strings.Join(doc.Entries[0].Warnings, " "),
		strings.Join(doc.Entries[0].Foreign.Notes, " "), doc.Entries[0].Foreign.String()}, " "), "hunter2-never-printed") {
		t.Fatal("an environment VALUE leaked into the IR")
	}
	plan := BuildPlan(doc, "/tmp/litespm.toml", nil)
	var buf bytes.Buffer
	plan.Render(&buf)
	if strings.Contains(buf.String(), "hunter2-never-printed") {
		t.Fatal("an environment VALUE leaked into the plan preview")
	}
	payload, err := plan.JSON(false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("hunter2-never-printed")) {
		t.Fatal("an environment VALUE leaked into the JSON plan")
	}
}

// ---- marketplaces -----------------------------------------------------------

func TestClaudeMarketplace(t *testing.T) {
	doc, err := ParseClaudeMarketplace(fixture(t, "claude-marketplace.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(doc.Entries))
	}
	byID := map[string]Entry{}
	for _, e := range doc.Entries {
		byID[e.ID] = e
	}
	web, ok := byID["plugin:builtin:claude-plugins:web-toolkit"]
	if !ok {
		t.Fatalf("missing slugified id, got: %v", keysOf(doc.Entries))
	}
	if web.Constraint != "2.0.0" {
		t.Errorf("web-toolkit constraint = %q", web.Constraint)
	}
	if web.Foreign.Source != "https://github.com/acme/web-toolkit" || web.Foreign.SourceType != "github" {
		t.Errorf("source not preserved: %+v", web.Foreign)
	}
	if len(web.Foreign.Components) != 2 {
		t.Errorf("skill components not preserved: %v", web.Foreign.Components)
	}

	docs, ok := byID["plugin:builtin:claude-plugins:docs-helper"]
	if !ok {
		t.Fatalf("missing docs-helper, got: %v", keysOf(doc.Entries))
	}
	// No entry version: the marketplace-level metadata version applies,
	// exactly like the catalog adapter.
	if docs.Constraint != "1.2.0" {
		t.Errorf("docs-helper should inherit metadata version, got %q", docs.Constraint)
	}
	if docs.Foreign.Source != "./plugins/docs-helper" || docs.Foreign.SourceType != "local" {
		t.Errorf("local source not preserved: %+v", docs.Foreign)
	}
	if !containsSub(docs.Foreign.Notes, "lspServers") {
		t.Errorf("lspServers not reported: %v", docs.Foreign.Notes)
	}
	if len(doc.Lossy) == 0 {
		t.Error("marketplace lossy lines missing")
	}
}

func TestCodexMarketplace(t *testing.T) {
	doc, err := ParseCodexMarketplace(fixture(t, "codex-marketplace.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(doc.Entries))
	}
	pdf := doc.Entries[0]
	if pdf.ID != "plugin:git:openai-plugins:pdf-tools" || pdf.Constraint != "0.3.1" {
		t.Errorf("pdf-tools = %q %q", pdf.ID, pdf.Constraint)
	}
	if !containsSub(pdf.Foreign.Notes, "authentication: required") {
		t.Errorf("authentication policy not reported: %v", pdf.Foreign.Notes)
	}
	second := doc.Entries[1]
	if second.ID != "plugin:git:openai-plugins:no-version-helper" || second.Constraint != "" {
		t.Errorf("second = %q %q", second.ID, second.Constraint)
	}
	if !containsSub(second.Warnings, "no version or ref") {
		t.Errorf("unconstrained warning missing: %v", second.Warnings)
	}
}

func TestMarketplaceCommandSourcesAreRejected(t *testing.T) {
	for _, name := range []string{"claude-marketplace-command.json", "codex-marketplace-command.json"} {
		t.Run(name, func(t *testing.T) {
			var err error
			if strings.HasPrefix(name, "claude") {
				_, err = ParseClaudeMarketplace(fixture(t, name))
			} else {
				_, err = ParseCodexMarketplace(fixture(t, name))
			}
			wantCode(t, err, ErrCodeCommandSource)
			if !strings.Contains(err.Error(), "never executed") {
				t.Errorf("rejection must state nothing is executed: %v", err)
			}
		})
	}
}

func TestMarketplaceRejectsMalformed(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		claude bool
		code   string
	}{
		{"claude plugins wrong type", []byte(`{"name":"x","plugins":"nope"}`), true, ErrCodeSchema},
		{"claude plugins null", []byte(`{"name":"x","plugins":null}`), true, ErrCodeSchema},
		{"claude missing name", []byte(`{"plugins":[]}`), true, ErrCodeSchema},
		{"claude traversal name", []byte(`{"name":"x","plugins":[{"name":"../evil"}]}`), true, ErrCodeUnsafe},
		{"claude entry not object", []byte(`{"name":"x","plugins":[42]}`), true, ErrCodeSchema},
		{"codex plugins wrong type", []byte(`{"name":"x","plugins":{}}`), false, ErrCodeSchema},
		{"codex traversal name", []byte(`{"name":"x","plugins":[{"name":"../evil"}]}`), false, ErrCodeUnsafe},
		{"codex entry missing name", []byte(`{"name":"x","plugins":[{"source":"a/b"}]}`), false, ErrCodeSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.claude {
				_, err = ParseClaudeMarketplace(tc.data)
			} else {
				_, err = ParseCodexMarketplace(tc.data)
			}
			wantCode(t, err, tc.code)
		})
	}
}

// ---- plugin.json ------------------------------------------------------------

func TestPluginHappyPath(t *testing.T) {
	doc, err := ParsePlugin(fixture(t, "plugin.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.ID != "plugin:git:github-com-acme-hello-plugin:hello-plugin" {
		t.Errorf("id = %q", e.ID)
	}
	if e.Constraint != "1.2.0" || e.Foreign.Version != "1.2.0" {
		t.Errorf("version not mapped: %q / %+v", e.Constraint, e.Foreign)
	}
	if e.Foreign.Source != "https://github.com/acme/hello-plugin" {
		t.Errorf("repository not preserved: %q", e.Foreign.Source)
	}
	joined := strings.Join(doc.Lossy, "\n")
	if !strings.Contains(joined, "no trust and no install semantics") {
		t.Errorf("trust/install lossy line missing:\n%s", joined)
	}
	if !containsSub(e.Foreign.Notes, "license MIT") {
		t.Errorf("license note missing: %v", e.Foreign.Notes)
	}
	if !containsSub(e.Foreign.Notes, "com.example.client") {
		t.Errorf("extensions namespace note missing: %v", e.Foreign.Notes)
	}
}

func TestPluginRejectsMaliciousAndMalformed(t *testing.T) {
	cases := []struct {
		name string
		file string
		code string
	}{
		{"unsupported schema version", "plugin-bad-schema.json", ErrCodeSchema},
		{"missing name", "plugin-missing-name.json", ErrCodeSchema},
		{"unknown top-level field", "plugin-unknown-key.json", ErrCodeSchema},
		{"traversal name", "plugin-traversal.json", ErrCodeUnsafe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePlugin(fixture(t, tc.file))
			wantCode(t, err, tc.code)
		})
	}

	inline := []struct {
		name string
		data string
		code string
	}{
		{"missing $schema", `{"name":"x"}`, ErrCodeSchema},
		{"$schema wrong type", `{"$schema":1,"name":"x"}`, ErrCodeSchema},
		{"uppercase name", `{"$schema":"` + PluginSchema100 + `","name":"Hello"}`, ErrCodeSchema},
		{"double dash name", `{"$schema":"` + PluginSchema100 + `","name":"a--b"}`, ErrCodeSchema},
		{"author not object", `{"$schema":"` + PluginSchema100 + `","name":"x","author":"Acme"}`, ErrCodeSchema},
		{"author unknown field", `{"$schema":"` + PluginSchema100 + `","name":"x","author":{"hobby":"x"}}`, ErrCodeSchema},
		{"extensions member not object", `{"$schema":"` + PluginSchema100 + `","name":"x","extensions":{"a":1}}`, ErrCodeSchema},
		{"keywords not array", `{"$schema":"` + PluginSchema100 + `","name":"x","keywords":"demo"}`, ErrCodeSchema},
		{"not an object", `[]`, ErrCodeSchema},
	}
	for _, tc := range inline {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePlugin([]byte(tc.data))
			wantCode(t, err, tc.code)
		})
	}
}

// TestPluginSchemaPin proves the importer's closed key set, required list
// and $schema constant come from the pinned published schema file, not from
// a guess. Provenance: testdata/plugin-1.0.0.schema.json was fetched from
// https://agent-plugins.org/schemas/1.0.0/plugin.schema.json on 2026-10-07
// (the canonical $id the Agent Plugins 1.0.0 specification publishes).
func TestPluginSchemaPin(t *testing.T) {
	var schema struct {
		ID                   string                     `json:"$id"`
		Required             []string                   `json:"required"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(fixture(t, "plugin-1.0.0.schema.json"), &schema); err != nil {
		t.Fatalf("pinned schema does not parse: %v", err)
	}
	if schema.ID != PluginSchema100 {
		t.Errorf("pinned $id %q != PluginSchema100 %q", schema.ID, PluginSchema100)
	}
	if string(schema.AdditionalProperties) != "false" {
		t.Errorf("pinned schema additionalProperties = %s, want false", schema.AdditionalProperties)
	}
	if len(schema.Required) != 2 {
		t.Fatalf("pinned required = %v", schema.Required)
	}
	for _, req := range []string{"$schema", "name"} {
		found := false
		for _, r := range schema.Required {
			if r == req {
				found = true
			}
		}
		if !found {
			t.Errorf("pinned schema does not require %q", req)
		}
	}
	if len(schema.Properties) != len(pluginKeys) {
		t.Fatalf("pinned schema has %d properties, importer knows %d", len(schema.Properties), len(pluginKeys))
	}
	for key := range schema.Properties {
		if !pluginKeys[key] {
			t.Errorf("pinned schema property %q missing from the importer's closed key set", key)
		}
	}
	for key := range pluginKeys {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("importer accepts %q, which the pinned schema does not define", key)
		}
	}
}

// ---- plan: diff, preview, JSON, approval ------------------------------------

// skillsDoc parses the happy fixture for plan tests.
func skillsDoc(t *testing.T) *Doc {
	t.Helper()
	doc, err := ParseSkillsLock(fixture(t, "skills-lock.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc.SourcePath = "./skills-lock.json"
	return doc
}

func manifestFrom(t *testing.T, src string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(src))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	return m
}

func TestBuildPlanDiff(t *testing.T) {
	existing := manifestFrom(t, `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

[[requires]]
id = "skill:git:vercel-labs-agent-skills:frontend-design"
constraint = "0.9.0"

[[requires]]
id = "skill:npm:acme-skills:pdf-tools"

[[requires]]
id = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"
`)
	plan := BuildPlan(skillsDoc(t), "/x/litespm.toml", existing)

	add, change, unchanged := plan.Counts()
	if add != 1 || change != 1 || unchanged != 1 {
		t.Errorf("counts = %d/%d/%d, want 1/1/1", add, change, unchanged)
	}
	if plan.PreservedExisting != 1 {
		t.Errorf("preserved = %d, want 1 (postgres)", plan.PreservedExisting)
	}
	if plan.TargetState != TargetRewrite {
		t.Errorf("state = %q, want rewrite (a constraint changes)", plan.TargetState)
	}
	byAction := map[Action]PlannedEntry{}
	for _, e := range plan.Entries {
		byAction[e.Action] = e
	}
	if e := byAction[ActionChange]; e.FromConstraint != "0.9.0" || e.Constraint != "main" {
		t.Errorf("change row = %+v", e)
	}
	if _, ok := byAction[ActionAdd]; !ok {
		t.Error("no add row")
	}
	if !containsSub(plan.Warnings, "rewritten in canonical form") {
		t.Errorf("rewrite warning missing: %v", plan.Warnings)
	}

	// No manifest yet: everything is added, the target is a create.
	create := BuildPlan(skillsDoc(t), "/x/litespm.toml", nil)
	if create.TargetState != TargetCreate {
		t.Errorf("state = %q, want create", create.TargetState)
	}
	if a, c, u := create.Counts(); a != 3 || c != 0 || u != 0 {
		t.Errorf("create counts = %d/%d/%d, want 3/0/0", a, c, u)
	}
	if create.PreservedExisting != 0 {
		t.Errorf("preserved = %d on create", create.PreservedExisting)
	}

	// Everything already matches: extend (append), nothing to change.
	allMatch := manifestFrom(t, `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

[[requires]]
id = "skill:git:vercel-labs-agent-skills:frontend-design"
constraint = "main"

[[requires]]
id = "skill:git:vercel-labs-skills:release-notes"

[[requires]]
id = "skill:npm:acme-skills:pdf-tools"
`)
	ext := BuildPlan(skillsDoc(t), "/x/litespm.toml", allMatch)
	if ext.TargetState != TargetExtend {
		t.Errorf("state = %q, want extend", ext.TargetState)
	}
	if ext.Actionable() != 0 {
		t.Errorf("actionable = %d, want 0", ext.Actionable())
	}
	// Every plan states the lock is not written — the honesty line is not
	// conditional.
	for _, p := range []*Plan{plan, create, ext} {
		if !containsSub(p.Lossy, "litespm.lock is not written") {
			t.Errorf("lock-not-written lossy line missing: %v", p.Lossy)
		}
	}
}

func TestPlanRender(t *testing.T) {
	existing := manifestFrom(t, `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

[[requires]]
id = "skill:git:vercel-labs-agent-skills:frontend-design"
constraint = "0.9.0"

[[requires]]
id = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"
`)
	plan := BuildPlan(skillsDoc(t), "/x/litespm.toml", existing)
	var buf bytes.Buffer
	plan.Render(&buf)
	out := buf.String()

	for _, want := range []string{
		"Import plan — skills-lock",
		"(no changes have been made)",
		"Source    ./skills-lock.json (3 entries)",
		"Target    /x/litespm.toml",
		"rewrite in canonical form",
		"ADD 2 · CHANGE 1 · UNCHANGED 0 · existing preserved 1",
		`constraint="main" (was "0.9.0")`,
		"Foreign record (preserved from skills-lock;",
		"source=vercel-labs/agent-skills",
		"Skipped / lossy:",
		"no effect or permission data",
		"litespm.lock is not written by import",
		"Warnings:",
		"Apply:  litespm import skills-lock",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
}

func TestPlanJSON(t *testing.T) {
	plan := BuildPlan(skillsDoc(t), "/x/litespm.toml", nil)
	first, err := plan.JSON(false)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := plan.JSON(false)
	if !bytes.Equal(first, second) {
		t.Fatal("JSON plan is not deterministic")
	}
	var parsed struct {
		Format      string         `json:"format"`
		Target      string         `json:"target"`
		TargetState string         `json:"targetState"`
		Applied     bool           `json:"applied"`
		Entries     []PlannedEntry `json:"entries"`
		Lossy       []string       `json:"lossy"`
		Warnings    []string       `json:"warnings"`
	}
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatalf("plan JSON does not reparse: %v", err)
	}
	if parsed.Format != "skills-lock" || parsed.Applied || parsed.TargetState != TargetCreate {
		t.Errorf("parsed = %+v", parsed)
	}
	if len(parsed.Entries) != 3 || len(parsed.Lossy) == 0 {
		t.Errorf("entries=%d lossy=%d", len(parsed.Entries), len(parsed.Lossy))
	}
	applied, _ := plan.JSON(true)
	if !bytes.Contains(applied, []byte(`"applied": true`)) {
		t.Errorf("applied flag not reflected: %s", applied)
	}
}

func TestApprovalDecision(t *testing.T) {
	cases := []struct {
		name string
		a    Approval
		want Decision
		code string // expected code inside a refusal message ("" otherwise)
	}{
		{name: "default is preview", a: Approval{}, want: DecisionPreview},
		{name: "apply+yes approves", a: Approval{Apply: true, Yes: true}, want: DecisionApproved},
		{name: "apply+yes wins over json", a: Approval{Apply: true, Yes: true, JSONOut: true}, want: DecisionApproved},
		{name: "dry-run beats apply+yes", a: Approval{DryRun: true, Apply: true, Yes: true}, want: DecisionPreview},
		{name: "apply on a terminal prompts", a: Approval{Apply: true, Interactive: true}, want: DecisionPrompt},
		{name: "apply without approval refuses", a: Approval{Apply: true}, want: DecisionRefused, code: ErrCodeApproval},
		{name: "json cannot prompt", a: Approval{Apply: true, JSONOut: true}, want: DecisionRefused, code: ErrCodeApproval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := tc.a.Decide()
			if got != tc.want {
				t.Fatalf("Decide() = %v, want %v (msg %q)", got, tc.want, msg)
			}
			if tc.want == DecisionRefused {
				if !strings.Contains(msg, tc.code) {
					t.Errorf("refusal must carry %s: %q", tc.code, msg)
				}
			}
		})
	}
}

// ---- shared helpers ---------------------------------------------------------

func TestConstraintFromRef(t *testing.T) {
	if got, warn := constraintFromRef("0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a", "field"); got != "commit:0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a" || warn != "" {
		t.Errorf("commit ref = %q, %q", got, warn)
	}
	if got, warn := constraintFromRef("1.2.3", "field"); got != "1.2.3" || warn != "" {
		t.Errorf("semver ref = %q, %q", got, warn)
	}
	got, warn := constraintFromRef("main", "field")
	if got != "main" || !strings.Contains(warn, "branch or tag") {
		t.Errorf("branch ref = %q, %q", got, warn)
	}
	if got, warn := constraintFromRef("", "field"); got != "" || warn != "" {
		t.Errorf("empty ref = %q, %q", got, warn)
	}
}

func TestCheckSafeField(t *testing.T) {
	for _, bad := range []string{"..", "../x", "a/../../b", `/abs/path`, `C:\evil`, `\\unc\share`, "a\x00b", `sub\..\win`} {
		if err := checkSafeField("f", bad); err == nil || CodeOf(err) != ErrCodeUnsafe {
			t.Errorf("checkSafeField(%q) = %v, want %s", bad, err, ErrCodeUnsafe)
		}
	}
	for _, good := range []string{"skill", "skills/a/SKILL.md", "owner/repo", "./plugins/x", "a..b"} {
		if err := checkSafeField("f", good); err != nil {
			t.Errorf("checkSafeField(%q) = %v, want ok", good, err)
		}
	}
}

func TestSourceIDFor(t *testing.T) {
	cases := []struct{ ns, raw, want string }{
		{"github", "vercel-labs/agent-skills", "git:vercel-labs-agent-skills"},
		{"node_modules", "@acme/skills", "npm:acme-skills"},
		{"local", "../weird path", "local:weird-path"},
		{"huggingface", "org/repo", "import:org-repo"},
	}
	for _, tc := range cases {
		got, err := sourceIDFor(tc.ns, tc.raw)
		if err != nil {
			t.Errorf("sourceIDFor(%q,%q): %v", tc.ns, tc.raw, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("sourceIDFor(%q,%q) = %q, want %q", tc.ns, tc.raw, got, tc.want)
		}
	}
}

// containsSub reports whether any element of list contains substr.
func containsSub(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// keysOf lists entry ids for failure messages.
func keysOf(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}
