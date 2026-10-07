package interop

// plan.go — the import IR, the plan diff, its preview rendering, and the
// approval decision.
//
// The plan is the only thing `litespm import` ever prints before a write,
// and it always prints: what would be written, where, which entries are
// added/changed/unchanged, which existing manifest entries are preserved,
// the source-faithful foreign record the format carries (reported, not
// flattened), and the Skipped/lossy list. Nothing in this file touches the
// filesystem — the caller writes an approved plan.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/manifest"
)

// Format tokens accepted by `litespm import <format>`. Each token is the
// foreign file's own name (or its well-known stem), so the command reads
// like the file it imports.
const (
	FormatSkillsLock        = "skills-lock"
	FormatServerJSON        = "server.json"
	FormatClaudeMarketplace = "claude-marketplace"
	FormatCodexMarketplace  = "codex-marketplace"
	FormatPlugin            = "plugin"
)

// AllFormats lists the supported tokens in help order.
var AllFormats = []string{
	FormatSkillsLock,
	FormatServerJSON,
	FormatClaudeMarketplace,
	FormatCodexMarketplace,
	FormatPlugin,
}

// NormalizeFormat lowercases a CLI format token and proves it is one this
// command ships; anything else is ErrCodeFormat (usage).
func NormalizeFormat(tok string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(tok))
	for _, f := range AllFormats {
		if t == f {
			return t, nil
		}
	}
	return "", errorf(ErrCodeFormat, "unknown format %q (supported: %s)",
		tok, strings.Join(AllFormats, ", "))
}

// DefaultFileName is the file each format resolves to when the CLI path
// argument names a directory.
func DefaultFileName(format string) string {
	switch format {
	case FormatSkillsLock:
		return "skills-lock.json"
	case FormatServerJSON:
		return "server.json"
	case FormatClaudeMarketplace:
		return ".claude-plugin/marketplace.json"
	case FormatCodexMarketplace:
		return ".agents/plugins/marketplace.json"
	case FormatPlugin:
		return "plugin.json"
	}
	return ""
}

// ForeignRecord is the source-faithful metadata preserved from the foreign
// file. It is REPORTED (preview + --json) and never written to the
// manifest: litespm.yml carries id and constraint only. Server.json
// publisher/namespace-verification assertions live here as distinct fields
// — packages keep their registry-qualified coordinates — so an import never
// flattens "who verified which namespace" into a bare name. Nothing here is
// ever executed or fetched, and env values never enter this struct: only
// names are recorded.
type ForeignRecord struct {
	Source          string           `json:"source,omitempty"`
	SourceType      string           `json:"sourceType,omitempty"`
	SkillPath       string           `json:"skillPath,omitempty"`
	Ref             string           `json:"ref,omitempty"`
	Version         string           `json:"version,omitempty"`
	Publisher       string           `json:"publisher,omitempty"`
	PublisherURL    string           `json:"publisherUrl,omitempty"`
	Packages        []ForeignPackage `json:"packages,omitempty"`
	Remotes         []ForeignRemote  `json:"remotes,omitempty"`
	Transport       string           `json:"transport,omitempty"`
	ArtifactType    string           `json:"artifactType,omitempty"`
	ArtifactLocator string           `json:"artifactLocator,omitempty"`
	Digest          string           `json:"digest,omitempty"`
	Status          string           `json:"status,omitempty"`
	Components      []string         `json:"components,omitempty"`
	EnvNames        []string         `json:"envNames,omitempty"`
	Notes           []string         `json:"notes,omitempty"`
}

// ForeignPackage is one server.json `packages[]` coordinate, kept whole:
// registry type, namespace-qualified package name, version, digest and
// transport are the registry's namespace-verification assertion material.
type ForeignPackage struct {
	RegistryType string   `json:"registryType"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Digest       string   `json:"digest,omitempty"`
	Transport    string   `json:"transport,omitempty"`
	EnvNames     []string `json:"envNames,omitempty"`
	HasCommand   bool     `json:"hasCommand,omitempty"`
}

// ForeignRemote is one server.json `remotes[]` endpoint.
type ForeignRemote struct {
	URL       string `json:"url"`
	Transport string `json:"transport"`
	AuthType  string `json:"authType,omitempty"`
}

// Empty reports whether the record carries nothing to show.
func (r ForeignRecord) Empty() bool {
	return r.Source == "" && r.SourceType == "" && r.SkillPath == "" && r.Ref == "" && r.Version == "" &&
		r.Publisher == "" && r.PublisherURL == "" && len(r.Packages) == 0 &&
		len(r.Remotes) == 0 && r.Transport == "" && r.ArtifactType == "" &&
		r.ArtifactLocator == "" && r.Digest == "" && r.Status == "" &&
		len(r.Components) == 0 && len(r.EnvNames) == 0 && len(r.Notes) == 0
}

// String renders the record as one deterministic `key=value` line. Field
// order is fixed; empty fields are omitted.
func (r ForeignRecord) String() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	add("source", r.Source)
	add("sourceType", r.SourceType)
	add("skillPath", r.SkillPath)
	add("ref", r.Ref)
	add("version", r.Version)
	add("publisher", r.Publisher)
	add("publisherUrl", r.PublisherURL)
	add("transport", r.Transport)
	add("artifactType", r.ArtifactType)
	add("artifactLocator", r.ArtifactLocator)
	add("digest", r.Digest)
	add("status", r.Status)
	for _, p := range r.Packages {
		s := fmt.Sprintf("package[registry=%s name=%s", p.RegistryType, p.Name)
		if p.Version != "" {
			s += " version=" + p.Version
		}
		if p.Digest != "" {
			s += " digest=" + p.Digest
		}
		if p.Transport != "" {
			s += " transport=" + p.Transport
		}
		if len(p.EnvNames) > 0 {
			s += " env=[" + strings.Join(p.EnvNames, ",") + "]"
		}
		if p.HasCommand {
			s += " command-present"
		}
		parts = append(parts, s+"]")
	}
	for _, rem := range r.Remotes {
		s := fmt.Sprintf("remote[url=%s transport=%s", rem.URL, rem.Transport)
		if rem.AuthType != "" {
			s += " auth=" + rem.AuthType
		}
		parts = append(parts, s+"]")
	}
	for _, c := range r.Components {
		parts = append(parts, "component="+c)
	}
	if len(r.EnvNames) > 0 {
		parts = append(parts, "envNames=["+strings.Join(r.EnvNames, ",")+"]")
	}
	for _, n := range r.Notes {
		parts = append(parts, "note="+fmt.Sprintf("%q", n))
	}
	return strings.Join(parts, " ")
}

// Entry is one normalized import record: what would become one manifest
// require, plus the foreign record that documents where it came from.
type Entry struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"` // skill | mcp | plugin
	Constraint string        `json:"constraint,omitempty"`
	Targets    []string      `json:"targets,omitempty"`
	Foreign    ForeignRecord `json:"foreign"`
	Warnings   []string      `json:"warnings,omitempty"`
}

// Doc is a parsed foreign file, normalized: format, entries, doc-level
// warnings (unknown fields, unsupported metadata) and the per-format
// Skipped/lossy statements.
type Doc struct {
	Format     string
	SourcePath string
	Entries    []Entry
	Warnings   []string
	Lossy      []string
}

// Action is what one entry does to the target manifest.
type Action string

const (
	ActionAdd       Action = "add"
	ActionChange    Action = "change"
	ActionUnchanged Action = "unchanged"
)

// PlannedEntry is one row of the plan.
type PlannedEntry struct {
	Action         Action        `json:"action"`
	ID             string        `json:"id"`
	Kind           string        `json:"kind"`
	Constraint     string        `json:"constraint,omitempty"`
	FromConstraint string        `json:"fromConstraint,omitempty"`
	Targets        []string      `json:"targets,omitempty"`
	Foreign        ForeignRecord `json:"foreign"`
	Warnings       []string      `json:"warnings,omitempty"`
}

// Target states: what would happen to the target manifest file.
const (
	TargetCreate  = "create"  // no manifest yet; write a new one
	TargetExtend  = "extend"  // existing manifest, adds only: append blocks, comments preserved
	TargetRewrite = "rewrite" // existing manifest must change: canonical rewrite, comments lost
)

// Plan is the previewable diff: one row per imported entry, plus what the
// write would do to the target file.
type Plan struct {
	Format            string
	SourcePath        string
	TargetPath        string
	TargetState       string
	Entries           []PlannedEntry
	PreservedExisting int // existing manifest requires not touched by this import
	Lossy             []string
	Warnings          []string
}

// BuildPlan diffs the parsed doc against the target manifest (nil when the
// manifest does not exist yet). It is pure: no file access, no clock.
func BuildPlan(doc *Doc, targetPath string, existing *manifest.Manifest) *Plan {
	p := &Plan{
		Format:      doc.Format,
		SourcePath:  doc.SourcePath,
		TargetPath:  targetPath,
		TargetState: TargetCreate,
		Lossy:       append([]string{}, doc.Lossy...),
		Warnings:    append([]string{}, doc.Warnings...),
	}

	existingByID := map[string]manifest.Require{}
	if existing != nil {
		p.TargetState = TargetExtend
		for _, r := range existing.Requires {
			if _, dup := existingByID[r.ID]; !dup {
				existingByID[r.ID] = r
			}
		}
	}

	imported := map[string]bool{}
	changes := 0
	for _, e := range doc.Entries {
		imported[e.ID] = true
		row := PlannedEntry{
			Action:     ActionAdd,
			ID:         e.ID,
			Kind:       e.Kind,
			Constraint: e.Constraint,
			Targets:    e.Targets,
			Foreign:    e.Foreign,
			Warnings:   e.Warnings,
		}
		if old, ok := existingByID[e.ID]; ok {
			sameConstraint := old.Constraint == e.Constraint
			sameTargets := len(old.Targets) == len(e.Targets)
			if sameTargets {
				for i := range old.Targets {
					if old.Targets[i] != e.Targets[i] {
						sameTargets = false
						break
					}
				}
			}
			if sameConstraint && sameTargets {
				row.Action = ActionUnchanged
			} else {
				row.Action = ActionChange
				row.FromConstraint = old.Constraint
				changes++
			}
		}
		p.Entries = append(p.Entries, row)
	}

	if existing != nil {
		for _, r := range existing.Requires {
			if !imported[r.ID] {
				p.PreservedExisting++
			}
		}
		if changes > 0 {
			p.TargetState = TargetRewrite
			p.Warnings = append(p.Warnings,
				"existing manifest would be rewritten in canonical form: hand-written comments, formatting and unrecognized lines are not preserved")
		}
	}

	// The lock is deliberately out of scope: a litespm.lock entry must be
	// reproducible from the manifest plus the local catalog (ARCH/32
	// invariants), and foreign resolved data cannot be. Saying so beats
	// leaving a lock the next `litespm lock --check` would flag.
	p.Lossy = append(p.Lossy,
		"litespm.lock is not written by import; run `litespm lock` to resolve the new requires against the local catalog index")
	p.Lossy = append(p.Lossy,
		"import writes the manifest only — installing is a separate `litespm install` step (ARCH/32 §5)")
	return p
}

// counts returns (add, change, unchanged).
func (p *Plan) counts() (int, int, int) {
	var a, c, u int
	for _, e := range p.Entries {
		switch e.Action {
		case ActionAdd:
			a++
		case ActionChange:
			c++
		case ActionUnchanged:
			u++
		}
	}
	return a, c, u
}

// Counts returns (add, change, unchanged) for callers outside the package.
func (p *Plan) Counts() (int, int, int) { return p.counts() }

// Actionable is the number of rows a write would actually change.
func (p *Plan) Actionable() int {
	a, c, _ := p.counts()
	return a + c
}

// allWarnings flattens plan warnings and per-entry warnings (prefixed by
// id) in a deterministic order.
func (p *Plan) allWarnings() []string {
	out := append([]string{}, p.Warnings...)
	for _, e := range p.Entries {
		for _, w := range e.Warnings {
			out = append(out, e.ID+": "+w)
		}
	}
	return out
}

// targetStateText explains each target state in the preview.
func (p *Plan) targetStateText() string {
	switch p.TargetState {
	case TargetCreate:
		return "create (no manifest yet)"
	case TargetRewrite:
		return "rewrite in canonical form (comments/formatting are not preserved)"
	default:
		return "extend (existing entries and comments preserved)"
	}
}

// Render writes the plan preview. It is printed unconditionally before any
// write, and it never contains an environment value: the record carries
// names only.
func (p *Plan) Render(w io.Writer) {
	add, change, unchanged := p.counts()
	mode := "  (no changes have been made)"
	fmt.Fprintf(w, "Import plan — %s%s\n\n", p.Format, mode)
	fmt.Fprintf(w, "Source    %s (%d entries)\n", p.SourcePath, len(p.Entries))
	fmt.Fprintf(w, "Target    %s (%s)\n", p.TargetPath, p.targetStateText())
	fmt.Fprintf(w, "ADD %d · CHANGE %d · UNCHANGED %d · existing preserved %d\n",
		add, change, unchanged, p.PreservedExisting)

	if len(p.Entries) == 0 {
		fmt.Fprintln(w, "\nRows:\n  (the file declares no importable entries)")
	} else {
		fmt.Fprintln(w, "\nRows:")
		for _, e := range p.Entries {
			line := fmt.Sprintf("  %-10s %s  constraint=%q", e.Action, e.ID, e.Constraint)
			if e.Action == ActionChange {
				line += fmt.Sprintf(" (was %q)", e.FromConstraint)
			}
			fmt.Fprintln(w, line)
		}
	}

	showRecord := false
	for _, e := range p.Entries {
		if !e.Foreign.Empty() {
			showRecord = true
			break
		}
	}
	if showRecord {
		fmt.Fprintf(w, "\nForeign record (preserved from %s; reported here — the manifest writes id and constraint only):\n", p.Format)
		for _, e := range p.Entries {
			if e.Foreign.Empty() {
				continue
			}
			fmt.Fprintf(w, "  %s  %s\n", e.ID, e.Foreign.String())
		}
	}

	if len(p.Lossy) > 0 {
		fmt.Fprintln(w, "\nSkipped / lossy:")
		for _, l := range p.Lossy {
			fmt.Fprintf(w, "  - %s\n", l)
		}
	}
	if wLines := p.allWarnings(); len(wLines) > 0 {
		fmt.Fprintln(w, "\nWarnings:")
		for _, wn := range wLines {
			fmt.Fprintf(w, "  - %s\n", wn)
		}
	}
	if p.Actionable() > 0 {
		fmt.Fprintf(w, "\nApply:  litespm import %s %s --apply  (add --yes for non-interactive approval)\n",
			p.Format, p.SourcePath)
	}
}

// PlanJSON is the machine-readable plan (`--json`). Field order is fixed
// and every collection is a slice, so the output is deterministic.
type PlanJSON struct {
	Format            string         `json:"format"`
	Source            string         `json:"source"`
	Target            string         `json:"target"`
	TargetState       string         `json:"targetState"`
	Entries           []PlannedEntry `json:"entries"`
	PreservedExisting int            `json:"preservedExisting"`
	Lossy             []string       `json:"lossy"`
	Warnings          []string       `json:"warnings"`
	Applied           bool           `json:"applied"`
}

// JSON renders the plan as deterministic, indented JSON.
func (p *Plan) JSON(applied bool) ([]byte, error) {
	out := PlanJSON{
		Format:            p.Format,
		Source:            p.SourcePath,
		Target:            p.TargetPath,
		TargetState:       p.TargetState,
		Entries:           p.Entries,
		PreservedExisting: p.PreservedExisting,
		Lossy:             p.Lossy,
		Warnings:          p.allWarnings(),
		Applied:           applied,
	}
	if out.Entries == nil {
		out.Entries = []PlannedEntry{}
	}
	if out.Lossy == nil {
		out.Lossy = []string{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return json.MarshalIndent(out, "", "  ")
}

// Decision is what the approval gate chose.
type Decision int

const (
	// DecisionPreview: print the plan and stop; nothing is requested.
	DecisionPreview Decision = iota
	// DecisionPrompt: an interactive terminal should ask y/N.
	DecisionPrompt
	// DecisionApproved: approval already given (--apply --yes).
	DecisionApproved
	// DecisionRefused: a write was requested but cannot be approved.
	DecisionRefused
)

// Approval is the state of the write gate (ARCH/15: no write without a
// plan and approval). DryRun always wins: it can never leave Preview.
type Approval struct {
	Apply       bool
	Yes         bool
	JSONOut     bool
	DryRun      bool
	Interactive bool
}

// Decide maps the flags onto a decision. Refused carries the named error
// message to print on stderr (exit 1); the caller performs the prompt.
func (a Approval) Decide() (Decision, string) {
	if a.DryRun || !a.Apply {
		return DecisionPreview, ""
	}
	if a.Yes {
		return DecisionApproved, ""
	}
	if a.JSONOut {
		return DecisionRefused, errorf(ErrCodeApproval,
			"--json output cannot prompt for approval; re-run with --apply --yes to approve the printed plan").Error()
	}
	if a.Interactive {
		return DecisionPrompt, ""
	}
	return DecisionRefused, errorf(ErrCodeApproval,
		"--apply needs an interactive terminal to approve, or --yes to approve the printed plan non-interactively").Error()
}

// sortStrings is a tiny helper used by parsers that collect map keys.
func sortStrings(s []string) {
	sort.Strings(s)
}
