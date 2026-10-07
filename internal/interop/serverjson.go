package interop

// serverjson.go — MCP registry `server.json` import (ARCH/32 §5 row 6).
//
// The parse shape and its semantic mapping are reused from
// internal/source/mcp_registry.go — the same MCPRegistryServerSchema the
// catalog source adapter decodes, the same single-object-or-array probe,
// the same meaning for packages[] (artifact) and remotes[] (endpoint).
// What import adds on top of the adapter is strictness and honesty: the
// adapter SKIPS nameless or versionless rows while ingesting a feed
// (thousands of rows, one bad one must not sink the run); an import file
// is the user's own input, so required fields are enforced (004), path
// traversal in names is refused (003), and a package that publishes no
// version is REPORTED instead of skipped.
//
// Namespace-verification assertions (ARCH/31 #19) are preserved, not
// flattened: the publisher (the party the registry verified) and every
// registry-qualified package coordinate (registryType + name, e.g.
// `@modelcontextprotocol/server-postgres`) stay distinct fields of the
// foreign record — the listing id carries the server name only, and the
// assertions ride along in the plan output instead of being collapsed
// into a bare name. Environment VALUES and launch commands are never read
// into the record: names only, and nothing here executes or fetches.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/source"
)

// serverKnownKeys are the fields of internal/source's server.json schema.
// Anything outside it (license, version, $schema, the registry's own
// identifier/verification extras, …) becomes a reported warning, never a
// silent drop.
var serverKnownKeys = map[string]bool{
	"name": true, "title": true, "description": true, "repository": true,
	"homepage": true, "categories": true, "keywords": true, "publisher": true,
	"packages": true, "remotes": true, "status": true,
}

// ParseServerJSON normalizes a server.json document (single object or an
// array of them, the registry's two wire shapes).
func ParseServerJSON(data []byte) (*Doc, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	doc := &Doc{Format: FormatServerJSON}

	var raws []json.RawMessage
	switch firstNonWS(data) {
	case '[':
		if err := decodeStrict(data, &raws); err != nil {
			return nil, err
		}
	case '{':
		raws = []json.RawMessage{data}
	default:
		return nil, errorf(ErrCodeSchema,
			"server.json must be a JSON object or an array of objects")
	}
	if len(raws) == 0 {
		doc.Warnings = append(doc.Warnings, "the document declares no servers; there is nothing to import")
		return doc, nil
	}

	seen := map[string]bool{}
	var hasDigest bool
	for i, raw := range raws {
		entry, err := parseServerEntry(i, raw, seen)
		if err != nil {
			return nil, err
		}
		doc.Entries = append(doc.Entries, *entry)
		for _, pkg := range entry.Foreign.Packages {
			if pkg.Digest != "" {
				hasDigest = true
			}
		}
	}

	doc.Lossy = append(doc.Lossy,
		"server.json packages[]/remotes[] map to artifact and transport metadata — reported in the record above; the manifest writes id and constraint only (ARCH/32 §5 row 6)",
		"publisher namespace-verification assertions (publisher plus registry-qualified package coordinates) are preserved in the record above, not flattened into the id",
		"launch commands and environment VALUES are never imported or executed; only environment variable names are recorded",
		"title, description, license, categories, keywords and homepage metadata are not imported (unknown fields are additionally reported as warnings)")
	if hasDigest {
		doc.Lossy = append(doc.Lossy,
			"package digests have no litespm.yml field; reported in the record above, never written")
	}
	return doc, nil
}

// parseServerEntry validates and normalizes one server.json object.
func parseServerEntry(idx int, raw json.RawMessage, seen map[string]bool) (*Entry, error) {
	if string(raw) == "null" {
		return nil, errorf(ErrCodeSchema, "servers[%d] must be a JSON object", idx)
	}
	var srv source.MCPRegistryServerSchema
	if err := decodeStrict(raw, &srv); err != nil {
		return nil, err
	}
	where := fmt.Sprintf("servers[%d]", idx)
	if strings.TrimSpace(srv.Name) == "" {
		return nil, errorf(ErrCodeSchema, "%s.name is required and must not be empty", where)
	}
	var localWarnings []string
	warnings := &localWarnings
	where = "server " + srv.Name
	if err := checkSafeField(where+".name", srv.Name); err != nil {
		return nil, err
	}
	if seen[srv.Name] {
		return nil, errorf(ErrCodeSchema, "duplicate server name %q in the input", srv.Name)
	}
	seen[srv.Name] = true

	// Unknown fields are reported with the server's name for context.
	var generic map[string]json.RawMessage
	if err := decodeStrict(raw, &generic); err != nil {
		return nil, err
	}
	checkKnownKeys(generic, serverKnownKeys, where, warnings)

	rec := ForeignRecord{Source: srv.Repository, Status: srv.Status}
	if srv.Publisher != nil {
		rec.Publisher = strings.TrimSpace(srv.Publisher.Name)
		rec.PublisherURL = strings.TrimSpace(srv.Publisher.URL)
		if email := strings.TrimSpace(srv.Publisher.Email); email != "" {
			rec.Notes = append(rec.Notes, fmt.Sprintf("publisher contact %s (reported only, not written)", email))
		}
	}
	if srv.Status == "deprecated" {
		rec.Notes = append(rec.Notes, "server status is deprecated upstream")
	}

	// constraint: the first package that actually published a version.
	// A package without one contributes its coordinate to the record and a
	// warning — reported, never silently skipped.
	var constraint string
	for i, pkg := range srv.Packages {
		pwhere := fmt.Sprintf("%s.packages[%d]", where, i)
		if strings.TrimSpace(pkg.Name) == "" {
			return nil, errorf(ErrCodeSchema, "%s.name is required", pwhere)
		}
		if err := checkSafeField(pwhere+".name", pkg.Name); err != nil {
			return nil, err
		}
		if strings.TrimSpace(pkg.RegistryType) == "" {
			return nil, errorf(ErrCodeSchema, "%s.registryType is required (npm, pypi, cargo, oci or mcpb)", pwhere)
		}
		if pkg.Digest != "" && !shaPref.MatchString(pkg.Digest) {
			return nil, errorf(ErrCodeSchema, "%s.digest %q is not \"sha256:\" followed by 64 lowercase hex characters",
				pwhere, pkg.Digest)
		}
		envNames := sortedKeys(pkg.Env)
		fp := ForeignPackage{
			RegistryType: pkg.RegistryType,
			Name:         pkg.Name,
			Version:      strings.TrimSpace(pkg.Version),
			Digest:       pkg.Digest,
			Transport:    pkg.Transport,
			EnvNames:     envNames,
			HasCommand:   pkg.Command != "" || len(pkg.Args) > 0,
		}
		if fp.Transport != "" && !knownTransport(fp.Transport) {
			*warnings = append(*warnings, fmt.Sprintf("%s.transport %q is not stdio/http/sse; recorded as declared", pwhere, fp.Transport))
		}
		rec.Packages = append(rec.Packages, fp)
		if fp.Version == "" {
			*warnings = append(*warnings, fmt.Sprintf("%s publishes no version; its coordinate is reported, not pinned", pwhere))
			continue
		}
		if constraint == "" {
			constraint = fp.Version
		} else if constraint != fp.Version {
			*warnings = append(*warnings, fmt.Sprintf(
				"%s publishes version %s while the constraint is already pinned to %s; both are reported in the record",
				pwhere, fp.Version, constraint))
		}
	}
	for i, rem := range srv.Remotes {
		rwhere := fmt.Sprintf("%s.remotes[%d]", where, i)
		if strings.TrimSpace(rem.URL) == "" {
			return nil, errorf(ErrCodeSchema, "%s.url is required", rwhere)
		}
		if strings.TrimSpace(rem.Transport) == "" {
			return nil, errorf(ErrCodeSchema, "%s.transport is required (http or sse)", rwhere)
		}
		if !knownTransport(rem.Transport) && rem.Transport != "http" {
			*warnings = append(*warnings, fmt.Sprintf("%s.transport %q is not stdio/http/sse; recorded as declared", rwhere, rem.Transport))
		}
		rec.Remotes = append(rec.Remotes, ForeignRemote{
			URL: rem.URL, Transport: rem.Transport, AuthType: rem.AuthType,
		})
	}

	// The record's transport line is what the foreign file declares,
	// normalized onto the lock vocabulary (ARCH/32 §3) only where the
	// mapping is a rename, never a guess.
	rec.Transport = ""
	if len(rec.Packages) > 0 {
		rec.Transport = rec.Packages[0].Transport
	} else if len(rec.Remotes) > 0 {
		rec.Transport = rec.Remotes[0].Transport
	}
	if rec.Transport == "http" {
		rec.Transport = "streamable-http"
		rec.Notes = append(rec.Notes, "transport http normalized to streamable-http (ARCH/32 §3)")
	}

	if constraint == "" && len(rec.Packages) > 0 {
		*warnings = append(*warnings, where+" publishes no package version; imported unconstrained (discovery-only upstream)")
	}
	if constraint == "" && len(rec.Packages) == 0 && len(rec.Remotes) > 0 {
		*warnings = append(*warnings, where+" declares a remote endpoint only; imported unconstrained (endpoints publish no version)")
	}

	id, err := listingID("mcp", "builtin:mcp-registry", srv.Name)
	if err != nil {
		return nil, err
	}
	return &Entry{ID: id, Kind: "mcp", Constraint: constraint, Foreign: rec, Warnings: localWarnings}, nil
}

// firstNonWS returns the first non-whitespace byte of a validated document.
func firstNonWS(data []byte) byte {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return b
		}
	}
	return 0
}

// knownTransport reports whether t is one of the transports the lock
// vocabulary defines (ARCH/32 §3).
func knownTransport(t string) bool {
	switch t {
	case "stdio", "http", "sse", "streamable-http":
		return true
	}
	return false
}

// sortedKeys returns an env map's NAMES in sorted order. The values are
// deliberately never touched: they may be secrets, and import has no place
// to write them even if they were not.
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
