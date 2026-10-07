package interop

// plugin.go — Agent Plugins 1.0.0 `plugin.json` import (ARCH/32 §5 row 3).
//
// The schema is PINNED to the published specification, not guessed: the
// canonical identifier is `https://agent-plugins.org/schemas/1.0.0/
// plugin.schema.json`, fetched 2026-10-07 from the specification host and
// stored verbatim at testdata/plugin-1.0.0.schema.json (provenance is
// asserted by TestPluginSchemaPin, which compares this file's closed key
// set and required list against the pinned schema itself).
//
// The schema is closed (additionalProperties: false) with two required
// fields ($schema, name). Import enforces the schema STRICTLY: an unknown
// top-level field, a wrong type, or an unsupported $schema value is a
// named refusal (004). The specification itself treats unknown fields and
// non-object extensions as non-fatal for a client LOADING a plugin; an
// importer that ignored them would silently drop data the user believed
// was being imported, so this command refuses instead.
//
// The name pattern (^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$,
// maxLength 64) uses negative lookahead, which Go's RE2 engine cannot
// compile, so it is enforced structurally below: charset, first/last
// character, no "--", no "..", length bound. Path traversal in the name is
// additionally refused by checkSafeField before the pattern runs.
//
// Import brings the manifest record only: no trust, no install semantics,
// and no reading of skills/ or mcp.json — those components belong to
// `litespm install` (ARCH/32 §5 row 3 says so plainly).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PluginSchema100 is the canonical $schema value of Agent Plugins 1.0.0.
// A plugin.json whose $schema differs is refused (004): this importer
// supports exactly one pinned version and does not guess at others.
const PluginSchema100 = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"

// pluginKeys is the closed top-level field set of the pinned schema:
// {"$schema","name","version","description","author","homepage",
// "repository","license","keywords","extensions"}.
var pluginKeys = map[string]bool{
	"$schema": true, "name": true, "version": true, "description": true,
	"author": true, "homepage": true, "repository": true, "license": true,
	"keywords": true, "extensions": true,
}

// ParsePlugin normalizes an Agent Plugins 1.0.0 plugin.json.
func ParsePlugin(data []byte) (*Doc, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	root, err := decodeRoot(data)
	if err != nil {
		return nil, err
	}
	doc := &Doc{Format: FormatPlugin}

	// Closed schema: an unknown top-level field is a refusal, not a
	// warning — import must not drop data silently (see file header).
	for _, key := range sortedRootKeys(root) {
		if !pluginKeys[key] {
			return nil, errorf(ErrCodeSchema,
				"unknown top-level field %q (Agent Plugins 1.0.0 defines a closed schema; import refuses to silently drop it)", key)
		}
	}

	// $schema: required, and it must be the one pinned version supported.
	rawSchema, err := requireKey(root, "$schema")
	if err != nil {
		return nil, err
	}
	var schema string
	if err := decodeStrict(rawSchema, &schema); err != nil {
		return nil, errorf(ErrCodeSchema, "$schema: %s", errDetail(err))
	}
	if schema != PluginSchema100 {
		return nil, errorf(ErrCodeSchema,
			"unsupported $schema %q (this import supports Agent Plugins 1.0.0: %s)", schema, PluginSchema100)
	}

	// name: required, non-empty, traversal-checked, then the spec pattern.
	rawName, err := requireKey(root, "name")
	if err != nil {
		return nil, err
	}
	var name string
	if err := decodeStrict(rawName, &name); err != nil {
		return nil, errorf(ErrCodeSchema, "name: %s", errDetail(err))
	}
	if strings.TrimSpace(name) == "" {
		return nil, errorf(ErrCodeSchema, "name is required and must not be empty")
	}
	if err := checkSafeField("name", name); err != nil {
		return nil, err
	}
	if err := validatePluginName(name); err != nil {
		return nil, err
	}

	// Typed decode of the remaining (optional) fields: wrong types are 004.
	var manifest pluginManifest
	if err := decodeStrict(data, &manifest); err != nil {
		return nil, err
	}
	if err := validateAuthor(root); err != nil {
		return nil, err
	}
	extensions, err := validateExtensions(root, &doc.Warnings)
	if err != nil {
		return nil, err
	}

	// id: derive the source from the repository locator when one is
	// declared; an absent repository lands in the `import` namespace rather
	// than pretending to a registry LiteSPM knows.
	sourceID, err := sourceIDFor("import", "agent-plugin")
	if err != nil {
		return nil, errorf(ErrCodeSchema, "cannot derive the fallback source id: %v", err)
	}
	if strings.TrimSpace(manifest.Repository) != "" {
		if sid, err := sourceIDFor("git", repositorySlug(manifest.Repository)); err == nil {
			sourceID = sid
		}
	}
	id, err := listingID("plugin", string(sourceID), name)
	if err != nil {
		return nil, err
	}

	rec := ForeignRecord{
		Source:  manifest.Repository,
		Version: manifest.Version,
	}
	if manifest.License != "" {
		rec.Notes = append(rec.Notes, fmt.Sprintf("license %s declared (reported only; the manifest has no license field)", manifest.License))
	}
	for _, ns := range extensions {
		rec.Notes = append(rec.Notes, fmt.Sprintf("extensions namespace %s declared (client-specific; not imported)", ns))
	}

	entry := &Entry{ID: id, Kind: "plugin", Constraint: manifest.Version, Foreign: rec}
	if manifest.Version == "" {
		entry.Warnings = append(entry.Warnings, "plugin.json declares no version; imported unconstrained")
	}

	doc.Entries = append(doc.Entries, *entry)
	doc.Lossy = append(doc.Lossy,
		"plugin import records the manifest only: it brings no trust and no install semantics — skills/ and mcp.json components are discovered by `litespm install`, not by import (ARCH/32 §5 row 3)",
		"description, author, homepage and keywords are validated but have no manifest field; license and extension namespaces appear as record notes")
	return doc, nil
}

// pluginManifest is the typed view of the pinned schema's optional fields
// (required fields are validated separately, with their own messages).
type pluginManifest struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Homepage    string         `json:"homepage"`
	Repository  string         `json:"repository"`
	License     string         `json:"license"`
	Keywords    []string       `json:"keywords"`
	Author      pluginAuthor   `json:"author"`
	Extensions  map[string]any `json:"extensions"`
}

type pluginAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// sortedRootKeys returns the document's top-level keys in sorted order so
// an unknown-key refusal always names the same first key for the same
// input, no matter how the map iterates.
func sortedRootKeys(root map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

// validatePluginName enforces the pinned schema's name pattern
// structurally (RE2 cannot compile the schema's negative lookaheads):
// 1-64 chars of [a-z0-9.-], starting and ending alphanumeric, with no
// "--" and no ".." anywhere.
func validatePluginName(name string) error {
	if len(name) > 64 {
		return errorf(ErrCodeSchema, "name %q is longer than the 64-character schema limit", name)
	}
	if strings.Contains(name, "--") || strings.Contains(name, "..") {
		return errorf(ErrCodeSchema,
			"name %q violates the Agent Plugins 1.0.0 name pattern (no \"--\" or \"..\" sequences)", name)
	}
	for i, r := range name {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		switch {
		case alnum:
		case (r == '.' || r == '-') && i > 0 && i < len(name)-1:
		default:
			return errorf(ErrCodeSchema,
				"name %q violates the Agent Plugins 1.0.0 name pattern (lowercase letters, digits, '-' and '.', starting and ending alphanumeric)", name)
		}
	}
	return nil
}

// validateAuthor enforces the schema's author object: optional string
// fields, no additional properties.
func validateAuthor(root map[string]json.RawMessage) error {
	raw, ok := root["author"]
	if !ok {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := decodeStrict(raw, &obj); err != nil {
		return errorf(ErrCodeSchema, "author must be an object with optional name/email/url strings: %s", errDetail(err))
	}
	for _, key := range sortedRootKeys(obj) {
		if key != "name" && key != "email" && key != "url" {
			return errorf(ErrCodeSchema, "unknown field author.%q (the pinned schema allows only name, email, url)", key)
		}
	}
	for _, key := range []string{"name", "email", "url"} {
		if v, ok := obj[key]; ok {
			var s string
			if err := decodeStrict(v, &s); err != nil {
				return errorf(ErrCodeSchema, "author.%s must be a string: %s", key, errDetail(err))
			}
		}
	}
	return nil
}

// validateExtensions enforces the schema's extensions object: member
// values must be objects. The namespace names are returned for the
// record's notes, sorted for deterministic output.
func validateExtensions(root map[string]json.RawMessage, warnings *[]string) ([]string, error) {
	raw, ok := root["extensions"]
	if !ok {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := decodeStrict(raw, &obj); err != nil {
		return nil, errorf(ErrCodeSchema, "extensions must be an object keyed by reverse-domain namespace: %s", errDetail(err))
	}
	names := make([]string, 0, len(obj))
	for _, ns := range sortedRootKeys(obj) {
		var member map[string]json.RawMessage
		if err := decodeStrict(obj[ns], &member); err != nil {
			return nil, errorf(ErrCodeSchema, "extensions[%q] must be an object: %s", ns, errDetail(err))
		}
		names = append(names, ns)
	}
	sortStrings(names)
	return names, nil
}

// repositorySlug folds a repository locator into a SourceID slug: scheme
// and trailing .git are stripped, the rest (host + path) is slugged, so
// `https://github.com/acme/hello.git` becomes `github-com-acme-hello`.
func repositorySlug(repo string) string {
	s := strings.TrimSpace(repo)
	for _, scheme := range []string{"https://", "http://", "git://", "ssh://"} {
		s = strings.TrimPrefix(s, scheme)
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return s
}
