package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `schemaVersion = 1

[project]
name = "acme-agent-workspace"
defaultScope = "project"

[[requires]]
id = "mcp:builtin:mcp-registry:postgres"
constraint = ">=1.4 <2"
targets = ["claude-code", "codex"]

[[requires]]
id = "skill:builtin:agent-skills:review-pr"
constraint = "1.2.0"

[policy]
allowSources = ["builtin:mcp-registry", "git:anthropic-skills"]
denyEffects = ["external.delete"]

[lock]
required = true
`

func TestParseManifest(t *testing.T) {
	m, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ProjectName != "acme-agent-workspace" {
		t.Errorf("project name = %q", m.ProjectName)
	}
	if len(m.Requires) != 2 {
		t.Fatalf("requires = %d, want 2", len(m.Requires))
	}
	if m.Requires[0].ID != "mcp:builtin:mcp-registry:postgres" {
		t.Errorf("requires[0].id = %q", m.Requires[0].ID)
	}
	if len(m.Requires[0].Targets) != 2 {
		t.Errorf("requires[0].targets = %v", m.Requires[0].Targets)
	}
	if !m.LockRequired {
		t.Error("lock.required should be true")
	}
	if len(m.Policy.AllowSources) != 2 {
		t.Errorf("allowSources = %v", m.Policy.AllowSources)
	}
}

func TestParseRejectsUnknownTable(t *testing.T) {
	bad := "schemaVersion = 1\n[project]\nname = \"x\"\n[nope]\nfoo = \"bar\"\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("expected error for unknown table")
	}
}

func TestParseRejectsBadSchemaVersion(t *testing.T) {
	bad := "schemaVersion = 99\n[project]\nname = \"x\"\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("expected error for schemaVersion 99")
	}
}

func TestCanonicalRoundTrip(t *testing.T) {
	m, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m2, err := Parse([]byte(m.MarshalCanonical()))
	if err != nil {
		t.Fatalf("re-parse canonical: %v", err)
	}
	if m2.ProjectName != m.ProjectName || len(m2.Requires) != len(m.Requires) {
		t.Error("canonical round-trip changed the manifest")
	}
	if !strings.Contains(m.MarshalCanonical(), "schemaVersion = 1") {
		t.Error("canonical form missing schemaVersion")
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "litespm.toml"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindUp(sub)
	if err != nil {
		t.Fatalf("FindUp: %v", err)
	}
	if got != filepath.Join(root, "litespm.toml") {
		t.Errorf("FindUp = %q", got)
	}
	if _, err := FindUp(t.TempDir()); err == nil {
		t.Error("expected not-found error in empty dir")
	}
}
