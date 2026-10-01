package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustParse parses JSON or JSONC. Several hosts keep comments in their config,
// so assertions on their merged output must tolerate them.
func mustParse(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(s)), &m); err != nil {
		t.Fatalf("not valid JSON: %v\ninput:\n%s", err, s)
	}
	return m
}

// jsonc_offsets_preserved_strip_comments pins the invariant the whole surgical
// writer depends on: comment stripping replaces comments with equal-length
// whitespace, so byte offsets found in the stripped text address the same bytes
// in the original. If this ever breaks, mergeJSONEntrySurgical would corrupt
// user config files.
func TestStripJSONCommentsPreservesOffsets(t *testing.T) {
	cases := []string{
		"{\n  // a comment\n  \"a\": 1\n}\n",
		"{/* block */\"a\":1}",
		"{\n \"url\": \"https://example.com//path\", // trailing\n \"b\": 2\n}",
		"{\n \"esc\": \"a\\\\\\\"b\",\n \"c\": \"d\"\n}",
		"{}",
		"",
	}
	for _, in := range cases {
		out := stripJSONComments(in)
		if len(out) != len(in) {
			t.Fatalf("length changed for %q: got %d want %d", in, len(out), len(in))
		}
	}
}

func TestStripJSONCommentsKeepsSlashesInStrings(t *testing.T) {
	in := `{"url":"https://a.example//b","re":"/* not a comment */"}`
	got := stripJSONComments(in)
	if got != in {
		t.Fatalf("string contents were altered:\n got %s\nwant %s", got, in)
	}
}

func TestStripJSONCommentsRemovesRealComments(t *testing.T) {
	in := "{\n // gone\n \"a\": 1 /* also gone */\n}"
	out := mustParse(t, stripJSONComments(in))
	if len(out) != 1 {
		t.Fatalf("expected only key a, got %v", out)
	}
}

func TestMergeJSONEntrySurgicalPreservesForeignContent(t *testing.T) {
	orig := `{
  // Zed settings — keep my notes
  "theme": "One Dark",
  "context_servers": {
    // my own server
    "my-server": {
      "command": "uvx",
      "args": ["thing"]
    }
  },
  "buffer_font_size": 14
}
`
	entry := map[string]any{"command": "/usr/bin/litepsm", "args": []string{"bridge", "stdio", "--host", "zed"}}
	out, err := mergeJSONEntrySurgical(orig, []string{"context_servers"}, entry)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	for _, want := range []string{
		"// Zed settings — keep my notes",
		"// my own server",
		"\"theme\": \"One Dark\"",
		"\"buffer_font_size\": 14",
		"\"my-server\"",
		"\"litepsm\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("merged output lost %q:\n%s", want, out)
		}
	}
	// The result must still be valid JSON with both servers present.
	parsed := mustParse(t, out)
	cs := parsed["context_servers"].(map[string]any)
	if len(cs) != 2 {
		t.Fatalf("expected 2 servers, got %d: %v", len(cs), cs)
	}
}

func TestMergeJSONEntrySurgicalCreatesMissingKeyPath(t *testing.T) {
	orig := "{\n  \"theme\": \"dark\"\n}\n"
	entry := map[string]any{"command": "/bin/litepsm", "args": []string{"bridge"}}
	out, err := mergeJSONEntrySurgical(orig, []string{"amp", "mcpServers"}, entry)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	parsed := mustParse(t, out)
	amp := parsed["amp"].(map[string]any)
	servers := amp["mcpServers"].(map[string]any)
	if _, ok := servers["litepsm"]; !ok {
		t.Fatalf("entry not created: %s", out)
	}
	if parsed["theme"] != "dark" {
		t.Fatalf("unrelated key lost: %s", out)
	}
}

func TestMergeJSONEntrySurgicalCreatesDeepMissingPath(t *testing.T) {
	orig := `{}`
	entry := map[string]any{"command": "/bin/litepsm"}
	out, err := mergeJSONEntrySurgical(orig, []string{"a", "b", "c"}, entry)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	parsed := mustParse(t, out)
	leaf := parsed["a"].(map[string]any)["b"].(map[string]any)["c"].(map[string]any)
	if _, ok := leaf["litepsm"]; !ok {
		t.Fatalf("deep entry not created: %s", out)
	}
}

func TestMergeJSONEntrySurgicalReplacesExistingEntry(t *testing.T) {
	orig := `{
  "mcpServers": {
    "litepsm": {
      "command": "/old/path/litepsm",
      "args": ["bridge", "stdio", "--host", "cursor"]
    },
    "other": {"command": "x"}
  }
}
`
	entry := map[string]any{"command": "/new/path/litepsm", "args": []string{"bridge"}}
	out, err := mergeJSONEntrySurgical(orig, []string{"mcpServers"}, entry)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if strings.Contains(out, "/old/path/litepsm") {
		t.Fatalf("stale binary path survived:\n%s", out)
	}
	parsed := mustParse(t, out)
	servers := parsed["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d: %v", len(servers), servers)
	}
	if servers["other"].(map[string]any)["command"] != "x" {
		t.Fatalf("foreign server damaged: %v", servers["other"])
	}
	if servers["litepsm"].(map[string]any)["command"] != "/new/path/litepsm" {
		t.Fatalf("replacement not applied: %v", servers["litepsm"])
	}
}

func TestMergeJSONEntrySurgicalIsIdempotent(t *testing.T) {
	orig := "{\n  \"mcpServers\": {\n    \"other\": {\"command\": \"x\"}\n  }\n}\n"
	entry := map[string]any{"command": "/bin/litepsm", "args": []string{"bridge", "stdio", "--host", "kode"}}
	once, err := mergeJSONEntrySurgical(orig, []string{"mcpServers"}, entry)
	if err != nil {
		t.Fatalf("first merge failed: %v", err)
	}
	twice, err := mergeJSONEntrySurgical(once, []string{"mcpServers"}, entry)
	if err != nil {
		t.Fatalf("second merge failed: %v", err)
	}
	if once != twice {
		t.Fatalf("merge is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", once, twice)
	}
}

func TestMergeJSONEntrySurgicalOnEmptyFile(t *testing.T) {
	entry := map[string]any{"command": "/bin/litepsm", "args": []string{"bridge"}}
	out, err := mergeJSONEntrySurgical("", []string{"mcpServers"}, entry)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	parsed := mustParse(t, out)
	if _, ok := parsed["mcpServers"].(map[string]any)["litepsm"]; !ok {
		t.Fatalf("entry missing from empty-file merge: %s", out)
	}
}

// TestMergeJSONEntrySurgicalAgreesWithRemarshal proves the surgical result is
// semantically identical to the simple parse-and-re-serialize approach for
// strict JSON. That equivalence is the evidence that splicing did not corrupt
// or drop anything.
func TestMergeJSONEntrySurgicalAgreesWithRemarshal(t *testing.T) {
	orig := `{
  "z": 1,
  "a": {"nested": true},
  "mcpServers": {"keep": {"command": "k", "args": ["1"]}},
  "list": [1, 2, {"deep": "value"}],
  "nullish": null,
  "num": 3.5
}
`
	keyPath := []string{"mcpServers"}
	entry := map[string]any{"command": "/bin/litepsm", "args": []string{"bridge", "stdio", "--host", "fx"}}

	surgical, err := mergeJSONEntrySurgical(orig, keyPath, entry)
	if err != nil {
		t.Fatalf("surgical merge failed: %v", err)
	}
	remarshalRoot := mustParse(t, orig)
	if err := mergeJSONEntry(remarshalRoot, keyPath, entry); err != nil {
		t.Fatalf("remarshal merge failed: %v", err)
	}
	remarshalled, err := json.Marshal(remarshalRoot)
	if err != nil {
		t.Fatalf("remarshal failed: %v", err)
	}
	var a, b any
	if err := json.Unmarshal([]byte(surgical), &a); err != nil {
		t.Fatalf("surgical output invalid: %v", err)
	}
	if err := json.Unmarshal(remarshalled, &b); err != nil {
		t.Fatalf("remarshal output invalid: %v", err)
	}
	if !jsonEqual(a, b) {
		t.Fatalf("surgical and remarshal disagree:\nsurgical: %s\nremarshal: %s", surgical, remarshalled)
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func TestMergeTOMLEntryAppendsWhenAbsent(t *testing.T) {
	orig := "# my toml\n[other]\nkey = 1\n"
	out, err := mergeTOMLEntry(orig, "[mcp_servers.litepsm]", "[mcp_servers.litepsm]\ncommand = \"/bin/litepsm\"\n")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !strings.Contains(out, "# my toml") || !strings.Contains(out, "[other]") {
		t.Fatalf("existing content lost:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.litepsm]") {
		t.Fatalf("table not appended:\n%s", out)
	}
}

func TestMergeTOMLEntryReplacesExistingTable(t *testing.T) {
	orig := "[mcp_servers.litepsm]\ncommand = \"/old\"\nargs = [\"a\"]\n\n[mcp_servers.other]\ncommand = \"keep\"\n"
	out, err := mergeTOMLEntry(orig, "[mcp_servers.litepsm]", "[mcp_servers.litepsm]\ncommand = \"/new\"\n")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if strings.Contains(out, "/old") {
		t.Fatalf("stale command survived:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.other]") || !strings.Contains(out, "keep") {
		t.Fatalf("sibling table damaged:\n%s", out)
	}
	if strings.Count(out, "[mcp_servers.litepsm]") != 1 {
		t.Fatalf("duplicate table emitted:\n%s", out)
	}
}

func TestRenderConfigRefusesInvalidJSON(t *testing.T) {
	a := &GenericAdapter{Target: BridgeTarget{
		ID: "x", Name: "X", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, Shape: ShapeObject,
	}}
	if _, err := a.renderConfig("{ this is not json", "/bin/litepsm", false); err == nil {
		t.Fatal("expected an error for invalid input rather than a corrupt write")
	}
}

// TestRenderConfigRefusesToClobberScalar guards the confused-deputy case: if a
// key path collides with an unrelated scalar, we must fail rather than replace
// the user's value.
func TestRenderConfigRefusesToClobberScalar(t *testing.T) {
	a := &GenericAdapter{Target: BridgeTarget{
		ID: "x", Name: "X", Format: FormatJSON,
		UserKey: []string{"mcpServers"}, Shape: ShapeObject,
	}}
	orig := `{"mcpServers": "this should be an object but is a string"}`
	if _, err := a.renderConfig(orig, "/bin/litepsm", false); err == nil {
		t.Fatal("expected refusal to overwrite a non-object value")
	}
}

func TestStripJSONCommentsIsIdempotent(t *testing.T) {
	in := "{\n // c\n \"a\": 1\n}"
	once := stripJSONComments(in)
	twice := stripJSONComments(once)
	if once != twice {
		t.Fatalf("not idempotent:\n%q\n%q", once, twice)
	}
}

func TestLineIndentOfIgnoresMidExpressionOffsets(t *testing.T) {
	if got := lineIndentOf(`{"a": 1}`, 5); got != "" {
		t.Fatalf("expected empty indent mid-line, got %q", got)
	}
	if got := lineIndentOf("{\n  \"a\": 1\n}", 6); got != "" {
		t.Fatalf("expected empty indent mid-line, got %q", got)
	}
	if got := lineIndentOf("{\n  \"a\": 1\n}", 4); got != "  " {
		t.Fatalf("expected two-space indent before a line's first token, got %q", got)
	}
	if got := lineIndentOf("{\n  \"a\": 1\n}", 2); got != "" {
		t.Fatalf("expected no indent when already at a line start, got %q", got)
	}
}

func TestIsEmptyJSONObject(t *testing.T) {
	if !isEmptyJSONObject("{") || !isEmptyJSONObject("  {  ") {
		t.Fatal("expected empty-object detection to succeed")
	}
	if isEmptyJSONObject(`{"a":1`) {
		t.Fatal("non-empty object misdetected as empty")
	}
}

func TestUpsertObjectMemberOnNonObjectReturnsUnchanged(t *testing.T) {
	got, err := upsertObjectMember(`[1,2]`, "x", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[1,2]` {
		t.Fatalf("expected unchanged array, got %s", got)
	}
}

func TestRenderConfigWritesCommandStringShape(t *testing.T) {
	tgt, ok := LookupBridgeTarget("mux")
	if !ok {
		t.Fatal("mux target missing from the table")
	}
	if tgt.Shape != ShapeCommandString {
		t.Fatalf("mux should use the command-string shape, got %q", tgt.Shape)
	}
	a := &GenericAdapter{Target: tgt}
	out, err := a.renderConfig("{\n  \"servers\": {\n    \"mine\": \"run-me\"\n  }\n}\n", "/opt/my apps/litepsm", false)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	parsed := mustParse(t, out)
	servers := parsed["servers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %v", servers)
	}
	cmd, ok := servers["litepsm"].(string)
	if !ok {
		t.Fatalf("expected a string command, got %T", servers["litepsm"])
	}
	if !strings.Contains(cmd, `"/opt/my apps/litepsm"`) {
		t.Fatalf("binary path with a space was not quoted: %s", cmd)
	}
	if !strings.Contains(cmd, "bridge stdio --host mux") {
		t.Fatalf("bridge args missing: %s", cmd)
	}
}

func TestRenderConfigHandlesLocalArrayShape(t *testing.T) {
	tgt, _ := LookupBridgeTarget("kilo")
	a := &GenericAdapter{Target: tgt}
	out, err := a.renderConfig("{\n  \"mcp\": {\n    \"keep\": {\"type\": \"remote\", \"url\": \"https://x\"}\n  }\n}\n", "/bin/litepsm", false)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	parsed := mustParse(t, out)
	entry := parsed["mcp"].(map[string]any)["litepsm"].(map[string]any)
	if entry["type"] != "local" {
		t.Fatalf("expected type=local, got %v", entry)
	}
	cmd := entry["command"].([]any)
	if len(cmd) != 5 || cmd[0] != "/bin/litepsm" || cmd[1] != "bridge" {
		t.Fatalf("unexpected argv array: %v", cmd)
	}
}

func TestBackupIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cfg.json")
	content := "{\n  // keep\n  \"a\": 1\n}\n"
	if err := os.WriteFile(src, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := CreateAtomicBackup(src, filepath.Join(dir, "backups"), "testhost")
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("backup differs:\n got %q\nwant %q", got, content)
	}
}

func TestCreateAtomicBackupSkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	backup, err := CreateAtomicBackup(filepath.Join(dir, "absent.json"), filepath.Join(dir, "b"), "h")
	if err != nil {
		t.Fatalf("expected no error for a missing file, got %v", err)
	}
	if backup != "" {
		t.Fatalf("expected empty backup path, got %q", backup)
	}
}
