package main

// import_test.go — the `litespm import` CLI contract: the plan preview is
// required before any write, approval gates the write, exit codes follow
// ARCH/20 (0 ok · 1 refused/failed · 2 usage), and untrusted input is
// refused with its named LPSM-IMPORT code on stderr.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/interop"
	"github.com/sarv-projects/litespm/internal/manifest"
)

// importFixture points at the shared interop fixtures.
func importFixture(name string) string {
	return filepath.Join("..", "..", "internal", "interop", "testdata", name)
}

// captureImportStderr collects what fn writes to the real stderr.
func captureImportStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	data, _ := io.ReadAll(r)
	_ = r.Close()
	return string(data)
}

// tempProject returns a fresh project directory and fails if a manifest
// already sits above it: apply tests must target their own directory, so a
// stray litespm.toml higher in the tree would be a hazard, not a surprise.
func tempProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if path, err := manifest.FindUp(dir); err == nil {
		t.Fatalf("found an unexpected manifest above the temp dir: %s", path)
	}
	return dir
}

func TestImportPreviewWritesNothing(t *testing.T) {
	dir := tempProject(t)
	var out bytes.Buffer
	code := runImportTo(&out, []string{"skills-lock", importFixture("skills-lock.json"), "--project", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"Import plan — skills-lock",
		"(no changes have been made)",
		"ADD 3 · CHANGE 0 · UNCHANGED 0",
		"Skipped / lossy:",
		"Apply:  litespm import skills-lock",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("preview missing %q:\n%s", want, text)
		}
	}
	// The preview phase must not have created anything.
	if _, err := os.Stat(filepath.Join(dir, "litespm.toml")); !os.IsNotExist(err) {
		t.Errorf("preview wrote a manifest (err=%v)", err)
	}
}

func TestImportApplyYesCreatesManifest(t *testing.T) {
	dir := tempProject(t)
	var out bytes.Buffer
	code := runImportTo(&out, []string{
		"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--apply", "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr was captured separately\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "✓ Wrote") {
		t.Errorf("missing write confirmation:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Next: run `litespm lock`") {
		t.Errorf("missing honest next step:\n%s", out.String())
	}
	m, err := manifest.Load(filepath.Join(dir, "litespm.toml"))
	if err != nil {
		t.Fatalf("written manifest does not load: %v", err)
	}
	if len(m.Requires) != 3 {
		t.Fatalf("requires = %d, want 3", len(m.Requires))
	}
	ids := map[string]bool{}
	for _, r := range m.Requires {
		ids[r.ID] = true
	}
	for _, want := range []string{
		"skill:git:vercel-labs-agent-skills:frontend-design",
		"skill:git:vercel-labs-skills:release-notes",
		"skill:npm:acme-skills:pdf-tools",
	} {
		if !ids[want] {
			t.Errorf("missing require %s", want)
		}
	}
}

func TestImportApplyExtendsPreservingComments(t *testing.T) {
	dir := tempProject(t)
	original := `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

# a hand-written note that must survive an append
[[requires]]
id = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"
`
	manifestPath := filepath.Join(dir, "litespm.toml")
	if err := os.WriteFile(manifestPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := runImportTo(&out, []string{
		"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--apply", "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// Adds-only imports extend the file byte-for-byte: everything that was
	// there — comment included — is a prefix of the result.
	if !strings.HasPrefix(string(after), original) {
		t.Errorf("original manifest was not preserved as a prefix:\n--- want prefix ---\n%s\n--- got ---\n%s", original, after)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("merged manifest does not load: %v", err)
	}
	if len(m.Requires) != 4 {
		t.Fatalf("requires = %d, want 4 (1 existing + 3 imported)", len(m.Requires))
	}
	if !strings.Contains(out.String(), "existing preserved 1") {
		t.Errorf("preserved count missing:\n%s", out.String())
	}
	// The lock is never written by import.
	if _, err := os.Stat(filepath.Join(dir, "litespm.lock")); !os.IsNotExist(err) {
		t.Error("import must not write litespm.lock")
	}
}

func TestImportApplyRewritesOnChange(t *testing.T) {
	dir := tempProject(t)
	original := `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

# this comment documents a constraint the import is about to change
[[requires]]
id = "skill:git:vercel-labs-agent-skills:frontend-design"
constraint = "0.9.0"
`
	manifestPath := filepath.Join(dir, "litespm.toml")
	if err := os.WriteFile(manifestPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := runImportTo(&out, []string{
		"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--apply", "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	// The preview warned that a change forces a canonical rewrite…
	if !strings.Contains(out.String(), "rewrite in canonical form") {
		t.Errorf("rewrite warning missing:\n%s", out.String())
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// …and the write did exactly what the preview said.
	if strings.Contains(string(after), "# this comment") {
		t.Error("rewrite kept a comment the preview said would not be preserved")
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("rewritten manifest does not load: %v", err)
	}
	found := false
	for _, r := range m.Requires {
		if r.ID == "skill:git:vercel-labs-agent-skills:frontend-design" {
			found = true
			if r.Constraint != "main" {
				t.Errorf("constraint = %q, want the imported ref %q", r.Constraint, "main")
			}
		}
	}
	if !found {
		t.Error("imported require missing after rewrite")
	}
}

func TestImportApplyRejectsStaleManifestSnapshot(t *testing.T) {
	dir := tempProject(t)
	manifestPath := filepath.Join(dir, "litespm.toml")
	original := `schemaVersion = 1

[project]
name = "demo"
defaultScope = "project"

[[requires]]
id = "skill:git:vercel-labs-agent-skills:frontend-design"
constraint = "0.9.0"
`
	if err := os.WriteFile(manifestPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readImportManifestSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(importFixture("skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := interop.Parse(interop.FormatSkillsLock, data)
	if err != nil {
		t.Fatal(err)
	}
	doc.SourcePath = importFixture("skills-lock.json")
	plan := interop.BuildPlan(doc, manifestPath, snapshot.manifest)

	concurrentEdit := strings.Replace(original, `name = "demo"`, `name = "edited by user"`, 1)
	if err := os.WriteFile(manifestPath, []byte(concurrentEdit), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var code int
	stderr := captureImportStderr(t, func() {
		code = writeImportPlan(&out, plan, dir, snapshot)
	})
	if code != 1 || !strings.Contains(stderr, "LPSM-IMPORT-008") {
		t.Fatalf("stale plan should be refused (code=%d stderr=%q)", code, stderr)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != concurrentEdit {
		t.Fatalf("stale import overwrote the concurrent edit:\n%s", after)
	}
}

func TestImportApplyRejectsManifestCreatedAfterPlan(t *testing.T) {
	dir := tempProject(t)
	manifestPath := filepath.Join(dir, "litespm.toml")
	snapshot, err := readImportManifestSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(importFixture("skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := interop.Parse(interop.FormatSkillsLock, data)
	if err != nil {
		t.Fatal(err)
	}
	doc.SourcePath = importFixture("skills-lock.json")
	plan := interop.BuildPlan(doc, manifestPath, nil)

	concurrentFile := "schemaVersion = 1\n\n[project]\nname = \"concurrent\"\ndefaultScope = \"project\"\n"
	if err := os.WriteFile(manifestPath, []byte(concurrentFile), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var code int
	stderr := captureImportStderr(t, func() {
		code = writeImportPlan(&out, plan, dir, snapshot)
	})
	if code != 1 || !strings.Contains(stderr, "LPSM-IMPORT-008") {
		t.Fatalf("new target should invalidate the create plan (code=%d stderr=%q)", code, stderr)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != concurrentFile {
		t.Fatalf("stale create plan overwrote the newly-created manifest:\n%s", after)
	}
}

func TestImportUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown format", []string{"toml", importFixture("skills-lock.json")}},
		{"missing positionals", []string{"skills-lock"}},
		{"extra positional", []string{"skills-lock", "a.json", "b.json"}},
		{"unknown flag", []string{"skills-lock", "a.json", "--force"}},
		{"missing flag value", []string{"skills-lock", "a.json", "--project"}},
		{"yes without apply", []string{"skills-lock", importFixture("skills-lock.json"), "--yes"}},
		{"bad project dir", []string{"skills-lock", importFixture("skills-lock.json"), "--project", filepath.Join(t.TempDir(), "nope")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			stderr := captureImportStderr(t, func() {
				if code := runImportTo(&out, tc.args); code != 2 {
					t.Errorf("exit = %d, want 2 (out: %s)", code, out.String())
				}
			})
			_ = stderr
		})
	}
	// The format refusal carries its named code.
	var out bytes.Buffer
	stderr := captureImportStderr(t, func() {
		_ = runImportTo(&out, []string{"toml", "x.json"})
	})
	if !strings.Contains(stderr, "LPSM-IMPORT-006") {
		t.Errorf("stderr missing LPSM-IMPORT-006: %q", stderr)
	}
}

func TestImportHelp(t *testing.T) {
	var out bytes.Buffer
	if code := runImportTo(&out, []string{"--help"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: litespm import <format> <path>") {
		t.Errorf("usage missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "no install") {
		t.Errorf("usage must state the no-install rule:\n%s", out.String())
	}
}

func TestImportNonInteractiveApplyIsRefusedWithoutYes(t *testing.T) {
	dir := tempProject(t)
	var out bytes.Buffer
	stderr := captureImportStderr(t, func() {
		if code := runImportTo(&out, []string{
			"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--apply"}); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "LPSM-IMPORT-007") {
		t.Errorf("stderr missing LPSM-IMPORT-007: %q", stderr)
	}
	// The plan was printed (stdout), the refusal was named (stderr), and
	// nothing was written.
	if !strings.Contains(out.String(), "Import plan") {
		t.Errorf("plan must print even when the write is refused:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "litespm.toml")); !os.IsNotExist(err) {
		t.Error("refused run wrote a manifest")
	}
}

func TestImportJSONPreviewAndApply(t *testing.T) {
	// Preview: machine-readable plan, applied=false, nothing written.
	dir := tempProject(t)
	var out bytes.Buffer
	if code := runImportTo(&out, []string{
		"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--json"}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	var preview struct {
		Format  string `json:"format"`
		Applied bool   `json:"applied"`
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, out.String())
	}
	if preview.Format != "skills-lock" || preview.Applied || len(preview.Entries) != 3 {
		t.Errorf("preview = %+v", preview)
	}
	if _, err := os.Stat(filepath.Join(dir, "litespm.toml")); !os.IsNotExist(err) {
		t.Error("JSON preview wrote a manifest")
	}

	// Apply: one JSON document, applied=true, manifest written.
	var applyOut bytes.Buffer
	if code := runImportTo(&applyOut, []string{
		"skills-lock", importFixture("skills-lock.json"), "--project", dir, "--json", "--apply", "--yes"}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, applyOut.String())
	}
	var applied struct {
		Applied bool `json:"applied"`
	}
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, applyOut.String())
	}
	if !applied.Applied {
		t.Error("applied not true after an approved write")
	}
	if _, err := manifest.Load(filepath.Join(dir, "litespm.toml")); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
}

func TestImportRejectsUntrustedInput(t *testing.T) {
	cases := []struct {
		name string
		file string
		code string
	}{
		{"path traversal", "skills-lock-traversal.json", "LPSM-IMPORT-003"},
		{"deep nesting", "skills-lock-deep.json", "LPSM-IMPORT-002"},
		{"command source", "claude-marketplace-command.json", "LPSM-IMPORT-005"},
		{"plugin schema drift", "plugin-bad-schema.json", "LPSM-IMPORT-004"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempProject(t)
			format := "skills-lock"
			switch tc.name {
			case "command source":
				format = "claude-marketplace"
			case "plugin schema drift":
				format = "plugin"
			}
			var out bytes.Buffer
			stderr := captureImportStderr(t, func() {
				if code := runImportTo(&out, []string{format, importFixture(tc.file), "--project", dir}); code != 1 {
					t.Errorf("exit = %d, want 1", code)
				}
			})
			if !strings.Contains(stderr, tc.code) {
				t.Errorf("stderr missing %s: %q", tc.code, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "litespm.toml")); !os.IsNotExist(err) {
				t.Error("rejected input wrote a manifest")
			}
		})
	}
}

func TestImportRejectsOversizedInput(t *testing.T) {
	dir := tempProject(t)
	huge := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(huge, make([]byte, (8<<20)+1), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	stderr := captureImportStderr(t, func() {
		if code := runImportTo(&out, []string{"skills-lock", huge, "--project", dir}); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "LPSM-IMPORT-001") {
		t.Errorf("stderr missing LPSM-IMPORT-001: %q", stderr)
	}
}

func TestImportMissingInputFile(t *testing.T) {
	var out bytes.Buffer
	stderr := captureImportStderr(t, func() {
		if code := runImportTo(&out, []string{"skills-lock", filepath.Join(t.TempDir(), "nope.json")}); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "nope.json") {
		t.Errorf("stderr should name the missing file: %q", stderr)
	}
}

func TestImportResolvesDirectoryInput(t *testing.T) {
	// A directory argument resolves to the format's well-known file name.
	var out bytes.Buffer
	code := runImportTo(&out, []string{
		"skills-lock", filepath.Join("..", "..", "internal", "interop", "testdata")})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "skills-lock.json") {
		t.Errorf("directory did not resolve to skills-lock.json:\n%s", out.String())
	}
}
