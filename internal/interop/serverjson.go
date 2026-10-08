package interop

// serverjson.go — MCP registry `server.json` import (ARCH/32 §5 row 6).
//
// The parse shape and its semantic mapping are reused from
// internal/source/mcp_registry.go — the same MCPRegistryServerSchema the
// catalog source adapter decodes (the 2025-12-11 live schema and its
// legacy spelling), the same single-object-or-array probe, the same meaning
// for packages[] (artifact) and remotes[] (endpoint). What import adds on
// top of the adapter is strictness and honesty: the adapter SKIPS
// nameless or versionless rows while ingesting a feed (thousands of rows,
// one bad one must not sink the run); an import file is the user's own
// input, so required fields are enforced (004), path traversal in names is
// refused (003), and a package that publishes no version is REPORTED
// instead of skipped.
//
// The live registry publishes package coordinates (identifier,
// registryType, transport object), environment variable NAMES, at most a
// runtimeHint (~1% of entries) and package-argument declarations — never a
// launch line. Nothing in this file turns those into a command: `command`
// is only ever reported from a document that itself declares one (the
// legacy spelling), arguments are reported as counts, and environment and
// header VALUES are never decoded at all.
//
// Namespace-verification assertions (ARCH/31 #19) are preserved, not
// flattened: the publisher (the party the registry verified) and every
// registry-qualified package coordinate (registryType + identifier, e.g.
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
// Anything outside it (license, version, $schema, websiteUrl, icons, the
// registry's own _meta extras, …) becomes a reported warning, never a
// silent drop — the 2025-12-11 schema declares its members additive (no
// additionalProperties: false), so an unrecognised member is reported and
// tolerated, never a refusal.
var serverKnownKeys = map[string]bool{
	"name": true, "title": true, "description": true, "repository": true,
	"homepage": true, "categories": true, "keywords": true, "publisher": true,
	"packages": true, "remotes": true, "status": true,
}

// ParseServerJSON normalizes a server.json document (single object or an
// array of them, the registry's two server.json wire shapes).
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
		// The registry API's list response ({"servers":[{"server":…}],
		// "metadata":…}) is not a server.json document: it is a paginated
		// collection whose entries are *versions* of the same servers (the
		// live response holds several versions per name), so importing it
		// as one file would force LiteSPM to pick a version the document
		// set did not let it pick. Refuse it with the shape named instead
		// of failing on an incidental field.
		if source.IsRegistryListEnvelope(data) {
			return nil, errorf(ErrCodeSchema,
				"this document is a registry API list response (a \"servers\" array), not a server.json document: save one server document — or an array of the server objects the response lists — and import that")
		}
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
			if pkg.Digest != "" || pkg.FileSha256 != "" {
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

	rec := ForeignRecord{Source: srv.Repository.URL, SourceType: srv.Repository.Source, Status: srv.Status}
	// repository members the record has no field for are reported, not
	// silently dropped (the URL and hosting service are imported above).
	if id := strings.TrimSpace(srv.Repository.ID); id != "" {
		rec.Notes = append(rec.Notes, fmt.Sprintf("repository id %s (reported only, not written)", id))
	}
	if sub := strings.TrimSpace(srv.Repository.Subfolder); sub != "" {
		rec.Notes = append(rec.Notes, fmt.Sprintf("repository subfolder %s (reported only, not written)", sub))
	}
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
		// The live schema requires `identifier`; the legacy spelling is
		// `name`. Either satisfies the coordinate requirement — a package
		// with neither declares nothing to import.
		coord := pkg.Coordinate()
		coordField := "identifier"
		if pkg.Identifier == "" {
			coordField = "name"
		}
		if coord == "" {
			return nil, errorf(ErrCodeSchema, "%s.identifier is required (the package coordinate; legacy documents use \"name\")", pwhere)
		}
		if err := checkSafeField(pwhere+"."+coordField, coord); err != nil {
			return nil, err
		}
		if strings.TrimSpace(pkg.RegistryType) == "" {
			return nil, errorf(ErrCodeSchema, "%s.registryType is required (npm, pypi, cargo, oci, nuget or mcpb)", pwhere)
		}
		if pkg.Digest != "" && !shaPref.MatchString(pkg.Digest) {
			return nil, errorf(ErrCodeSchema, "%s.digest %q is not \"sha256:\" followed by 64 lowercase hex characters",
				pwhere, pkg.Digest)
		}
		if pkg.FileSha256 != "" && !hex64.MatchString(pkg.FileSha256) {
			return nil, errorf(ErrCodeSchema, "%s.fileSha256 %q is not 64 lowercase hex characters",
				pwhere, pkg.FileSha256)
		}
		// Env NAMES come from both spellings: the legacy `env` map and the
		// live `environmentVariables[]`. Values are never decoded by either
		// path — a live entry may declare none at all, and the ones it
		// declares may be secrets.
		envNames := sortedKeys(pkg.Env)
		if len(pkg.EnvironmentVariables) > 0 {
			seen := map[string]bool{}
			for _, n := range envNames {
				seen[n] = true
			}
			for _, kv := range pkg.EnvironmentVariables {
				name := strings.TrimSpace(kv.Name)
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				envNames = append(envNames, name)
			}
			sort.Strings(envNames)
		}
		fp := ForeignPackage{
			RegistryType: pkg.RegistryType,
			Name:         coord,
			Version:      strings.TrimSpace(pkg.Version),
			Digest:       pkg.Digest,
			FileSha256:   pkg.FileSha256,
			RuntimeHint:  strings.TrimSpace(pkg.RuntimeHint),
			Transport:    pkg.Transport.TransportType(),
			EnvNames:     envNames,
			// True only when the document itself declared a command
			// (legacy spelling). The live schema has no command field, so
			// no live package can ever set this — and nothing here derives
			// one from registryType, identifier or runtimeHint.
			HasCommand: pkg.Command != "" || len(pkg.Args) > 0,
		}
		if fp.Transport != "" && !knownTransport(fp.Transport) {
			*warnings = append(*warnings, fmt.Sprintf("%s.transport %q is not stdio/http/sse; recorded as declared", pwhere, fp.Transport))
		}
		if pkg.Transport.URL != "" {
			*warnings = append(*warnings, fmt.Sprintf("%s.transport.url %q is reported, not imported", pwhere, pkg.Transport.URL))
		}
		// Arguments are reported as counts: with no executable declared
		// there is nothing for them to attach to, and rendering them would
		// read like a launch line LiteSPM invented.
		if len(pkg.PackageArguments) > 0 {
			*warnings = append(*warnings, fmt.Sprintf("%s declares %d packageArguments but names no executable; the count is reported, never a launch line", pwhere, len(pkg.PackageArguments)))
		}
		if len(pkg.RuntimeArguments) > 0 {
			*warnings = append(*warnings, fmt.Sprintf("%s declares %d runtimeArguments but names no executable; the count is reported, never a launch line", pwhere, len(pkg.RuntimeArguments)))
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
		// The live schema requires `type`; the legacy spelling is
		// `transport`. Either satisfies the requirement — neither is a
		// licence to guess the endpoint's transport.
		rtransport := rem.TransportType()
		if rtransport == "" {
			return nil, errorf(ErrCodeSchema, "%s.type is required (streamable-http or sse; legacy documents use \"transport\")", rwhere)
		}
		if !knownTransport(rtransport) && rtransport != "http" {
			*warnings = append(*warnings, fmt.Sprintf("%s.transport %q is not stdio/http/sse; recorded as declared", rwhere, rtransport))
		}
		// Header NAMES would be record material; header VALUES may be
		// credentials and are never decoded by the schema structs at all.
		if len(rem.Headers) > 0 {
			names := make([]string, 0, len(rem.Headers))
			for _, h := range rem.Headers {
				if n := strings.TrimSpace(h.Name); n != "" {
					names = append(names, n)
				}
			}
			*warnings = append(*warnings, fmt.Sprintf("%s declares header names [%s]; header values are never imported (they may carry credentials)", rwhere, strings.Join(names, ", ")))
		}
		rec.Remotes = append(rec.Remotes, ForeignRemote{
			URL: rem.URL, Transport: rtransport, AuthType: rem.AuthType,
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
