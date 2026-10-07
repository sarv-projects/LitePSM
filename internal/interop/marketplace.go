package interop

// marketplace.go — Claude / Codex marketplace manifest import (ARCH/32
// §5 row 4).
//
// The manifest shapes and their flexible-type normalization are reused
// from internal/source (claude_marketplace.go, codex_marketplace.go,
// flex.go): ClaudeMarketplaceManifest / CodexMarketplaceManifest decode
// through the same FlexString / FlexSource helpers the catalog adapters
// use, and the listing ids are derived with the same slug rules against
// the same adapter source ids (builtin:claude-plugins, git:openai-plugins)
// — so an imported id is the id the catalog would later publish for the
// same plugin.
//
// Two deliberate differences from the adapters' Ingest:
//
//   - `command` sources are REJECTED with LPSM-IMPORT-005 instead of
//     skipped (ARCH/32 §5 row 4): ingest drops one row from a feed of
//     thousands, an import file is exactly what the user asked to bring in,
//     so hiding the refusal would be a silent drop. Nothing executes the
//     command — the file is refused unread-as-data.
//   - nameless entries are schema errors (004) instead of skips.
//
// slugifyName below mirrors internal/source/flex.go's unexported helper
// byte-for-byte so imported ids and adapter-ingested ids cannot drift.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sarv-projects/litespm/internal/source"
)

// slugifyName is the listing-name slug of internal/source/flex.go,
// duplicated because the helper is unexported and the id derivation must
// stay byte-identical to what the adapters produce.
func slugifyName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-.")
}

// claudeRootKeys / codexRootKeys: the documented top-level fields of the
// two marketplace manifests. Unknown keys become reported warnings.
var (
	claudeRootKeys = map[string]bool{
		"schemaVersion": true, "name": true, "description": true,
		"publisher": true, "owner": true, "metadata": true,
		"renames": true, "plugins": true,
	}
	codexRootKeys = map[string]bool{
		"name": true, "interface": true, "plugins": true,
	}
)

// Default SourceIDs of the two marketplace adapters (internal/source/
// claude_marketplace.go and codex_marketplace.go, applied when their
// constructors receive an empty source).
const (
	claudeMarketplaceSourceID = "builtin:claude-plugins"
	codexMarketplaceSourceID  = "git:openai-plugins"
)

// ParseClaudeMarketplace normalizes `.claude-plugin/marketplace.json`.
func ParseClaudeMarketplace(data []byte) (*Doc, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	root, err := decodeRoot(data)
	if err != nil {
		return nil, err
	}
	doc := &Doc{Format: FormatClaudeMarketplace}
	checkKnownKeys(root, claudeRootKeys, "(top level)", &doc.Warnings)

	var m source.ClaudeMarketplaceManifest
	if err := decodeStrict(data, &m); err != nil {
		return nil, err
	}
	if err := requireMarketplaceName(root, "(top level)", m.Name); err != nil {
		return nil, err
	}
	plugins, err := requirePluginsArray(root)
	if err != nil {
		return nil, err
	}
	_ = len(plugins) // the typed decode above already validated the elements

	// The adapter types expose no SourceID accessor, so the default
	// SourceIDs NewClaudeMarketplaceAdapter("")/NewCodexMarketplaceAdapter("")
	// construct are mirrored here — cited from internal/source, not
	// re-invented — and the slug rules below use the adapters' own
	// slugifyName, so imported ids match adapter-ingested ids exactly.
	sourceID := claudeMarketplaceSourceID
	seen := map[string]bool{}
	for i, p := range m.Plugins {
		entry, err := claudeEntry(i, p, m.Metadata.Version, sourceID, seen)
		if err != nil {
			return nil, err
		}
		doc.Entries = append(doc.Entries, *entry)
	}
	if len(doc.Entries) == 0 {
		doc.Warnings = append(doc.Warnings, "the marketplace declares no plugins; there is nothing to import")
	}

	doc.Lossy = append(doc.Lossy,
		"marketplace descriptions, categories, renames and per-plugin behavior flags are not written to the manifest",
		"skill and lspServer component lists are reported in the record above; components are discovered at install time, not by import",
		"import records plugin ids only — plugin install and trust semantics are not imported (ARCH/32 §5)")
	return doc, nil
}

// ParseCodexMarketplace normalizes `.agents/plugins/marketplace.json`.
func ParseCodexMarketplace(data []byte) (*Doc, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	root, err := decodeRoot(data)
	if err != nil {
		return nil, err
	}
	doc := &Doc{Format: FormatCodexMarketplace}
	checkKnownKeys(root, codexRootKeys, "(top level)", &doc.Warnings)

	var m source.CodexMarketplaceManifest
	if err := decodeStrict(data, &m); err != nil {
		return nil, err
	}
	if err := requireMarketplaceName(root, "(top level)", m.Name); err != nil {
		return nil, err
	}
	if _, err := requirePluginsArray(root); err != nil {
		return nil, err
	}

	sourceID := codexMarketplaceSourceID
	seen := map[string]bool{}
	for i, p := range m.Plugins {
		entry, err := codexEntry(i, p, sourceID, seen)
		if err != nil {
			return nil, err
		}
		doc.Entries = append(doc.Entries, *entry)
	}
	if len(doc.Entries) == 0 {
		doc.Warnings = append(doc.Warnings, "the marketplace declares no plugins; there is nothing to import")
	}

	doc.Lossy = append(doc.Lossy,
		"descriptions, categories, display names and interface metadata are not written to the manifest",
		"declared policy (installation/authentication) is reported as a record note; import records no trust or install semantics (ARCH/32 §5)")
	return doc, nil
}

// requireMarketplaceName enforces the manifest's own required name field.
func requireMarketplaceName(root map[string]json.RawMessage, where, name string) error {
	raw, err := requireKey(root, "name")
	if err != nil {
		return err
	}
	var s string
	if err := decodeStrict(raw, &s); err != nil {
		return errorf(ErrCodeSchema, "%s.name: %s", where, errDetail(err))
	}
	if strings.TrimSpace(s) == "" {
		return errorf(ErrCodeSchema, "%s.name is required and must not be empty", where)
	}
	return nil
}

// requirePluginsArray enforces that the plugins key exists and is a real
// array (null is not an empty list).
func requirePluginsArray(root map[string]json.RawMessage) ([]json.RawMessage, error) {
	raw, err := requireKey(root, "plugins")
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, errorf(ErrCodeSchema, "plugins must be a JSON array")
	}
	var list []json.RawMessage
	if err := decodeStrict(raw, &list); err != nil {
		return nil, errorf(ErrCodeSchema, "plugins: %s", errDetail(err))
	}
	return list, nil
}

// claudeEntry validates and normalizes one Claude marketplace plugin.
func claudeEntry(i int, p source.ClaudePluginEntry, manifestVersion string,
	sourceID string, seen map[string]bool) (*Entry, error) {
	where := pluginWhere(i, p.Name)
	var localWarnings []string
	warnings := &localWarnings
	// Rejected on import, before anything looks at the rest of the entry:
	// a command source would have to be executed to be fetched.
	if p.Source.IsCommand() {
		return nil, errorf(ErrCodeCommandSource,
			"%s has a `command` source; command sources are rejected on import (ARCH/32 §5 row 4) and never executed", where)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errorf(ErrCodeSchema, "%s.name is required and must not be empty", where)
	}
	if err := checkSafeField(where+".name", p.Name); err != nil {
		return nil, err
	}
	slug := slugifyName(p.Name)
	if slug == "" {
		return nil, errorf(ErrCodeSchema, "%s.name %q cannot be normalized into a LiteSPM id", where, p.Name)
	}
	if seen[slug] {
		return nil, errorf(ErrCodeSchema, "duplicate plugin name %q (id %q already imported)", p.Name, slug)
	}
	seen[slug] = true
	if err := checkSourceLocator(where, p.Source); err != nil {
		return nil, err
	}

	id, err := listingID("plugin", string(sourceID), slug)
	if err != nil {
		return nil, err
	}

	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = strings.TrimSpace(manifestVersion)
	}
	constraint, warn := "", ""
	if version != "" {
		constraint = version
	} else {
		constraint, warn = constraintFromRef(p.Source.Ref, where)
		if warn != "" {
			*warnings = append(*warnings, warn)
		}
	}
	if constraint == "" {
		*warnings = append(*warnings, where+" declares no version or ref; imported unconstrained")
	}

	rec := ForeignRecord{
		Source:     firstNonEmptyStr(p.Source.RepositoryURL(), p.Source.Raw),
		SourceType: p.Source.Kind,
		Ref:        p.Source.Ref,
		Version:    version,
		Components: append([]string{}, p.Skills...),
	}
	for _, sp := range p.Skills {
		if err := checkSafeField(where+".skills", sp); err != nil {
			return nil, err
		}
	}
	if len(p.LSPServers) > 0 {
		rec.Notes = append(rec.Notes, fmt.Sprintf("%d lspServers declared (reported only; components are discovered at install)", len(p.LSPServers)))
	}
	if p.Strict != nil {
		rec.Notes = append(rec.Notes, fmt.Sprintf("strict=%t declared (behavior flag; not imported)", *p.Strict))
	}
	return &Entry{ID: id, Kind: "plugin", Constraint: constraint, Foreign: rec, Warnings: localWarnings}, nil
}

// codexEntry validates and normalizes one Codex marketplace plugin.
func codexEntry(i int, p source.CodexPluginEntry,
	sourceID string, seen map[string]bool) (*Entry, error) {
	where := pluginWhere(i, p.Name)
	var localWarnings []string
	warnings := &localWarnings
	if p.Source.IsCommand() {
		return nil, errorf(ErrCodeCommandSource,
			"%s has a `command` source; command sources are rejected on import (ARCH/32 §5 row 4) and never executed", where)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errorf(ErrCodeSchema, "%s.name is required and must not be empty", where)
	}
	if err := checkSafeField(where+".name", p.Name); err != nil {
		return nil, err
	}
	slug := slugifyName(p.Name)
	if slug == "" {
		return nil, errorf(ErrCodeSchema, "%s.name %q cannot be normalized into a LiteSPM id", where, p.Name)
	}
	if seen[slug] {
		return nil, errorf(ErrCodeSchema, "duplicate plugin name %q (id %q already imported)", p.Name, slug)
	}
	seen[slug] = true
	if err := checkSourceLocator(where, p.Source); err != nil {
		return nil, err
	}

	id, err := listingID("plugin", string(sourceID), slug)
	if err != nil {
		return nil, err
	}

	version := strings.TrimSpace(p.Version)
	constraint, warn := version, ""
	if constraint == "" {
		constraint, warn = constraintFromRef(p.Source.Ref, where)
		if warn != "" {
			*warnings = append(*warnings, warn)
		}
	}
	if constraint == "" {
		*warnings = append(*warnings, where+" declares no version or ref; imported unconstrained")
	}

	rec := ForeignRecord{
		Source:     firstNonEmptyStr(p.Source.RepositoryURL(), p.Source.Raw),
		SourceType: p.Source.Kind,
		Ref:        p.Source.Ref,
		Version:    version,
	}
	if p.Policy.Authentication != "" {
		rec.Notes = append(rec.Notes, fmt.Sprintf("declared authentication: %s (reported only; no trust semantics imported)", p.Policy.Authentication))
	}
	if p.Policy.Installation != "" {
		rec.Notes = append(rec.Notes, fmt.Sprintf("declared installation policy: %s (reported only)", p.Policy.Installation))
	}
	return &Entry{ID: id, Kind: "plugin", Constraint: constraint, Foreign: rec, Warnings: localWarnings}, nil
}

// pluginWhere names a marketplace entry for error messages.
func pluginWhere(i int, name string) string {
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("plugins[%d]", i)
	}
	return fmt.Sprintf("plugins[%d] (%q)", i, name)
}

// checkSourceLocator applies the path rules to a FlexSource: string-form
// (local) sources and local-kind subpaths are paths inside a repository, so
// traversal and absolute forms are refused; URL-form locators are not
// paths and are left as declared.
func checkSourceLocator(where string, s source.FlexSource) error {
	if s.Kind == "local" {
		if s.Raw != "" {
			if err := checkSafeField(where+".source", s.Raw); err != nil {
				return err
			}
		}
		if s.Subpath != "" {
			if err := checkSafeField(where+".source.path", s.Subpath); err != nil {
				return err
			}
		}
	}
	return nil
}

// firstNonEmptyStr returns the first non-blank string.
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
