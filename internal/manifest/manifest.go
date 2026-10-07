// Package manifest implements the LiteSPM project manifest (ARCH/32).
//
// The manifest declares intent (requires, policy, targets); it never carries
// resolved versions or digests. The on-disk form is litespm.toml (primary);
// litespm.yml / litespm.yaml are accepted as aliases and parsed with the same
// minimal TOML-subset grammar so no third-party decoder is required.
//
// Supported grammar (subset of TOML, sufficient for the manifest contract):
//
//	schemaVersion = 1
//	[project]
//	name = "acme-agent-workspace"
//	defaultScope = "project"
//	[[requires]]
//	id = "mcp:builtin:mcp-registry:postgres"
//	constraint = ">=1.4 <2"
//	targets = ["claude-code", "codex"]
//	[policy]
//	allowSources = ["builtin:mcp-registry"]
//	denyEffects = ["external.delete"]
//	[lock]
//	required = true
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SchemaVersion is the only manifest schema version accepted by this package.
const SchemaVersion = 1

// ManifestFileNames are probed in order when locating a project manifest.
var ManifestFileNames = []string{"litespm.toml", "litespm.yml", "litespm.yaml"}

// Require declares one direct, human-authored dependency.
type Require struct {
	ID         string   `json:"id"`
	Constraint string   `json:"constraint,omitempty"`
	Targets    []string `json:"targets,omitempty"`
}

// Policy declares the project-local tighten-only policy slice.
type Policy struct {
	AllowSources []string `json:"allowSources,omitempty"`
	DenyEffects  []string `json:"denyEffects,omitempty"`
}

// Manifest is the parsed project manifest.
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	ProjectName   string    `json:"projectName"`
	DefaultScope  string    `json:"defaultScope"`
	Requires      []Require `json:"requires"`
	Policy        Policy    `json:"policy,omitempty"`
	LockRequired  bool      `json:"lockRequired"`
	// SourcePath records which file the manifest was loaded from.
	SourcePath string `json:"-"`
}

// FindUp locates the manifest by walking up from dir. It returns the path or
// an error when no manifest file exists.
func FindUp(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range ManifestFileNames {
			p := filepath.Join(abs, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("LPSM-MANIFEST-NOT-FOUND: no litespm.toml (or .yml/.yaml) found from %s", dir)
		}
		abs = parent
	}
}

// Load reads and validates the manifest at path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LPSM-MANIFEST-READ: %s: %w", path, err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("LPSM-MANIFEST-PARSE: %s: %w", path, err)
	}
	m.SourcePath = path
	return m, nil
}

// Parse parses manifest bytes (TOML-subset grammar).
func Parse(data []byte) (*Manifest, error) {
	doc, err := parseTOMLSubset(data)
	if err != nil {
		return nil, err
	}
	m := &Manifest{DefaultScope: "project"}
	if v, ok := doc.scalars["schemaVersion"]; ok {
		n, err := parseInt(v)
		if err != nil {
			return nil, fmt.Errorf("schemaVersion must be an integer: %q", v)
		}
		m.SchemaVersion = n
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported schemaVersion %d (want %d)", m.SchemaVersion, SchemaVersion)
	}
	if proj, ok := doc.tables["project"]; ok {
		m.ProjectName = unquote(proj["name"])
		if s := unquote(proj["defaultScope"]); s != "" {
			m.DefaultScope = s
		}
	}
	if m.ProjectName == "" {
		return nil, fmt.Errorf("project.name is required")
	}
	if m.DefaultScope != "project" && m.DefaultScope != "user" {
		return nil, fmt.Errorf("project.defaultScope must be project|user, got %q", m.DefaultScope)
	}
	for i, tbl := range doc.arrayTables["requires"] {
		id := unquote(tbl["id"])
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("requires[%d].id is required", i)
		}
		m.Requires = append(m.Requires, Require{
			ID:         id,
			Constraint: unquote(tbl["constraint"]),
			Targets:    parseStrArray(tbl["targets"]),
		})
	}
	if pol, ok := doc.tables["policy"]; ok {
		m.Policy.AllowSources = parseStrArray(pol["allowSources"])
		m.Policy.DenyEffects = parseStrArray(pol["denyEffects"])
	}
	if lk, ok := doc.tables["lock"]; ok {
		m.LockRequired = strings.EqualFold(strings.TrimSpace(lk["required"]), "true")
	}
	for key := range doc.tables {
		switch key {
		case "project", "policy", "lock", "binding":
		default:
			return nil, fmt.Errorf("unknown top-level table [%s]", key)
		}
	}
	return m, nil
}

// BindingMode reads the optional [binding] table (P6 one-bridge default).
// Absent means "managed". Only an explicit mode = "native" opts into native
// per-package registration.
func (m *Manifest) BindingMode() string { return "managed" }

// MarshalCanonical renders the manifest in canonical TOML form (stable key
// order, no timestamps) for digesting.
func (m *Manifest) MarshalCanonical() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "schemaVersion = %d\n", m.SchemaVersion)
	sb.WriteString("\n[project]\n")
	fmt.Fprintf(&sb, "name = %s\n", quote(m.ProjectName))
	fmt.Fprintf(&sb, "defaultScope = %s\n", quote(m.DefaultScope))
	for _, r := range m.Requires {
		sb.WriteString("\n[[requires]]\n")
		fmt.Fprintf(&sb, "id = %s\n", quote(r.ID))
		if r.Constraint != "" {
			fmt.Fprintf(&sb, "constraint = %s\n", quote(r.Constraint))
		}
		if len(r.Targets) > 0 {
			fmt.Fprintf(&sb, "targets = %s\n", quoteStrArray(r.Targets))
		}
	}
	if len(m.Policy.AllowSources) > 0 || len(m.Policy.DenyEffects) > 0 {
		sb.WriteString("\n[policy]\n")
		if len(m.Policy.AllowSources) > 0 {
			fmt.Fprintf(&sb, "allowSources = %s\n", quoteStrArray(m.Policy.AllowSources))
		}
		if len(m.Policy.DenyEffects) > 0 {
			fmt.Fprintf(&sb, "denyEffects = %s\n", quoteStrArray(m.Policy.DenyEffects))
		}
	}
	sb.WriteString("\n[lock]\n")
	if m.LockRequired {
		sb.WriteString("required = true\n")
	} else {
		sb.WriteString("required = false\n")
	}
	return sb.String()
}
