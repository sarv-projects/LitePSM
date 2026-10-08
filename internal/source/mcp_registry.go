package source

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// The structs in this file decode the Official MCP Registry's server.json
// documents against the authoritative 2025-12-11 schema
// (https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json,
// fetched and cross-checked against live documents from
// https://registry.modelcontextprotocol.io/v0/servers).
//
// Two spellings are decoded deliberately, because both describe the same
// fact and rejecting either would hard-fail documents that were valid for
// the schema they were published under:
//
//   - the live 2025-12-11 schema: `repository` is an object, the package
//     coordinate is `identifier`, `packages[].transport` is an object,
//     remotes carry `type`, packages carry `environmentVariables[]`,
//     `runtimeHint`, `packageArguments[]`, `runtimeArguments[]` and
//     `fileSha256`;
//   - the legacy spelling: `repository` a bare URL string, the coordinate
//     in `name`, `transport` a bare string, remotes carrying `transport`,
//     and `env`/`command`/`args`/`runtime` on the package.
//
// The live schema publishes package *coordinates* and, on roughly 1% of
// entries, a `runtimeHint` — never a launch line. Nothing here synthesises
// a command from `registryType`, `identifier` or `runtimeHint`: the legacy
// `command`/`args` fields are only ever read when the document itself
// declares them, and `environmentVariables[]` values (which may be
// secrets) are never decoded at all — names only, mirroring
// internal/interop's record rule.
type MCPRegistryServerSchema struct {
	Name        string                `json:"name"`
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description"`
	Repository  MCPRegistryRepository `json:"repository,omitempty"`
	Homepage    string                `json:"homepage,omitempty"`   // legacy spelling (live: websiteUrl, not consumed)
	Categories  []string              `json:"categories,omitempty"` // legacy
	Keywords    []string              `json:"keywords,omitempty"`   // legacy
	Publisher   *MCPRegistryPublisher `json:"publisher,omitempty"`  // legacy
	Packages    []MCPRegistryPackage  `json:"packages,omitempty"`
	Remotes     []MCPRegistryRemote   `json:"remotes,omitempty"`
	Status      string                `json:"status,omitempty"` // legacy
}

type MCPRegistryPublisher struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// MCPRegistryRepository is the `repository` field: the 2025-12-11 schema
// publishes an object (`url` + `source`, optionally `id`/`subfolder`);
// older documents published the bare URL string. Both spellings carry the
// same fact — where the source lives — so both decode.
type MCPRegistryRepository struct {
	URL       string `json:"url"`
	Source    string `json:"source"`
	ID        string `json:"id,omitempty"`
	Subfolder string `json:"subfolder,omitempty"`
}

// UnmarshalJSON accepts the object spelling (2025-12-11) and the legacy
// bare-string spelling. Anything else surfaces as the caller's typed
// decode error, naming the field.
func (r *MCPRegistryRepository) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*r = MCPRegistryRepository{URL: s}
		return nil
	}
	type plain MCPRegistryRepository
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*r = MCPRegistryRepository(p)
	return nil
}

// MCPRegistryTransport decodes a package's `transport`: the 2025-12-11
// schema publishes an object ({"type":"stdio", …}); older documents
// published a bare string ("stdio"). TransportType returns the declared
// type exactly as the document spelled it — never a guess.
type MCPRegistryTransport struct {
	Type    string                     `json:"type,omitempty"`
	URL     string                     `json:"url,omitempty"`
	Headers []MCPRegistryKeyValueInput `json:"headers,omitempty"`

	bare string // legacy bare-string spelling
}

// UnmarshalJSON accepts the object spelling (2025-12-11) and the legacy
// bare-string spelling.
func (t *MCPRegistryTransport) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = MCPRegistryTransport{bare: s}
		return nil
	}
	type plain MCPRegistryTransport
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*t = MCPRegistryTransport(p)
	return nil
}

// TransportType returns the declared transport type verbatim ("stdio",
// "streamable-http", "sse", or the legacy "http"/bare string).
func (t MCPRegistryTransport) TransportType() string {
	if b := strings.TrimSpace(t.bare); b != "" {
		return b
	}
	return strings.TrimSpace(t.Type)
}

// MCPRegistryKeyValueInput is one `environmentVariables[]` or transport
// `headers[]` entry of the 2025-12-11 schema. Only the NAME is decoded:
// the schema's `value` member may carry a secret, and neither this adapter
// nor internal/interop ever reads an environment or header value — the
// import record is names-only by design.
type MCPRegistryKeyValueInput struct {
	Name string `json:"name"`
}

// MCPRegistryArgument is one `packageArguments[]`/`runtimeArguments[]`
// entry. Deliberately no fields: LiteSPM records that a document declares
// arguments (and may name a named flag), never their values, because
// argument values with no executable to attach them to read like a command
// line LiteSPM would be inventing.
type MCPRegistryArgument struct{}

type MCPRegistryPackage struct {
	RegistryType         string                     `json:"registryType"` // npm | pypi | cargo | oci | nuget | mcpb
	Identifier           string                     `json:"identifier,omitempty"`
	Name                 string                     `json:"name,omitempty"` // legacy spelling of the coordinate
	Version              string                     `json:"version"`
	Digest               string                     `json:"digest,omitempty"`     // legacy: sha256:<hex>
	FileSha256           string                     `json:"fileSha256,omitempty"` // live: bare 64 lowercase hex
	RegistryBaseURL      string                     `json:"registryBaseUrl,omitempty"`
	RuntimeHint          string                     `json:"runtimeHint,omitempty"`
	Transport            MCPRegistryTransport       `json:"transport,omitempty"`
	EnvironmentVariables []MCPRegistryKeyValueInput `json:"environmentVariables,omitempty"`
	PackageArguments     []MCPRegistryArgument      `json:"packageArguments,omitempty"`
	RuntimeArguments     []MCPRegistryArgument      `json:"runtimeArguments,omitempty"`

	// Legacy launch metadata. Read only when the document declares it;
	// never derived from registryType, identifier or runtimeHint.
	Runtime string            `json:"runtime,omitempty"` // node | python | docker
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Coordinate returns the registry-qualified package coordinate: the live
// schema's `identifier`, or the legacy `name` spelling when that is what
// the document published.
func (p MCPRegistryPackage) Coordinate() string {
	if c := strings.TrimSpace(p.Identifier); c != "" {
		return c
	}
	return strings.TrimSpace(p.Name)
}

type MCPRegistryRemote struct {
	URL       string                     `json:"url"`
	Type      string                     `json:"type,omitempty"`      // 2025-12-11 spelling
	Transport string                     `json:"transport,omitempty"` // legacy spelling
	AuthType  string                     `json:"authType,omitempty"`  // legacy
	Headers   []MCPRegistryKeyValueInput `json:"headers,omitempty"`   // 2025-12-11 (values never decoded)
}

// TransportType returns the declared remote transport verbatim: `type` on
// the live schema, `transport` on the legacy spelling.
func (r MCPRegistryRemote) TransportType() string {
	if t := strings.TrimSpace(r.Type); t != "" {
		return t
	}
	return strings.TrimSpace(r.Transport)
}

// mcpRegistryHex64 matches the 2025-12-11 schema's fileSha256 pattern
// (^[a-f0-9]{64}$) exactly as published.
var mcpRegistryHex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

// IsRegistryListEnvelope reports whether data is the registry API's list
// response ({"servers":[{"server":{…},"_meta":{…}},…],"metadata":{…}}),
// which is a collection of versioned server documents with pagination —
// not a server.json document or feed. Shared with internal/interop so both
// consumers name the shape the same way instead of failing on an
// incidental field.
func IsRegistryListEnvelope(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, hasServers := probe["servers"]
	_, hasName := probe["name"]
	return hasServers && !hasName
}

// MCPRegistryAdapter normalizes the Official MCP Registry feed into LiteSPM domain entities.
type MCPRegistryAdapter struct {
	sourceID domain.SourceID
	rawFeed  []byte
}

// NewMCPRegistryAdapter creates an MCP registry adapter with static or downloaded feed bytes.
func NewMCPRegistryAdapter(sourceID domain.SourceID, rawFeed []byte) (*MCPRegistryAdapter, error) {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:mcp-registry")
	}
	return &MCPRegistryAdapter{
		sourceID: sourceID,
		rawFeed:  rawFeed,
	}, nil
}

// SourceID returns the registered SourceID.
func (a *MCPRegistryAdapter) SourceID() domain.SourceID {
	return a.sourceID
}

// Ingest parses the feed into normalized Listings and VersionRecords.
func (a *MCPRegistryAdapter) Ingest(ctx context.Context, snapshotID string) (*IngestResult, error) {
	if len(a.rawFeed) == 0 {
		return nil, fmt.Errorf("raw feed is empty")
	}

	var rawServers []MCPRegistryServerSchema
	// Try parsing as array of servers first
	if err := json.Unmarshal(a.rawFeed, &rawServers); err != nil {
		// Try parsing as a single server object
		var singleServer MCPRegistryServerSchema
		if errSingle := json.Unmarshal(a.rawFeed, &singleServer); errSingle != nil {
			return nil, fmt.Errorf("failed to parse MCP registry feed as array or object: %w", err)
		}
		// Two shapes decode as an object but are not a server document;
		// naming them beats returning an empty ingest as if the feed had
		// simply contained nothing.
		if strings.TrimSpace(singleServer.Name) == "" {
			if IsRegistryListEnvelope(a.rawFeed) {
				return nil, fmt.Errorf(
					"feed is a registry API list response ({\"servers\": [...]}), not a server.json feed: pass an array of the server documents it lists (or a single server document)")
			}
			return nil, fmt.Errorf(
				"failed to parse MCP registry feed as array or object: single object declares no server name")
		}
		rawServers = []MCPRegistryServerSchema{singleServer}
	}

	now := time.Now().UTC()
	var listings []*domain.Listing
	var versions []*domain.VersionRecord

	for _, srv := range rawServers {
		if srv.Name == "" {
			continue
		}

		listingID := domain.NewListingID(domain.KindMCP, a.sourceID, srv.Name)

		pubClaim := domain.PublisherClaim{
			Name: "Unknown",
		}
		if srv.Publisher != nil && srv.Publisher.Name != "" {
			pubClaim.Name = srv.Publisher.Name
			pubClaim.URL = srv.Publisher.URL
			pubClaim.Contact = srv.Publisher.Email
		}

		categories := srv.Categories
		if len(categories) == 0 {
			categories = []string{"utilities"}
		}

		versionSummaries := []domain.VersionSummary{}
		var compSummaries []domain.ComponentSummary
		var reqSummaries []string

		// Default component summary
		compSummaries = append(compSummaries, domain.ComponentSummary{
			Kind: domain.ComponentMCPProvider,
			Name: "server",
		})

		// Process packages
		for i, pkg := range srv.Packages {
			coord := pkg.Coordinate()
			if coord == "" {
				// The live schema requires `identifier`; the legacy
				// spelling is `name`. A package with neither declares no
				// coordinate at all — an unparseable document, refused
				// with the field that is missing.
				return nil, fmt.Errorf("server %q packages[%d].identifier is required (the registry package coordinate; legacy documents use \"name\")", srv.Name, i)
			}
			if pkg.FileSha256 != "" && !mcpRegistryHex64.MatchString(pkg.FileSha256) {
				return nil, fmt.Errorf("server %q packages[%d].fileSha256 %q is not 64 lowercase hex characters", srv.Name, i, pkg.FileSha256)
			}
			verStr := strings.TrimSpace(pkg.Version)
			if verStr == "" {
				// A package that published no version contributes nothing
				// installable: no version summary, no artifact, no runtime.
				// This used to default to "1.0.0", which manufactured a
				// resolvable, installable row out of a guess — an artifact
				// URL and a digest key built from a version the upstream
				// never published. With every package like this the listing
				// has zero versions and mcpInstallability marks it
				// discovery_only, which is exactly what the catalog can
				// stand behind for it.
				continue
			}

			versionSummaries = append(versionSummaries, domain.VersionSummary{
				Version: verStr,
			})

			// Map artifact type
			artType := domain.ArtifactNPM
			switch pkg.RegistryType {
			case "pypi":
				artType = domain.ArtifactPyPI
			case "cargo":
				artType = domain.ArtifactCargo
			case "oci":
				artType = domain.ArtifactOCI
			case "nuget":
				artType = domain.ArtifactNuGet
			case "mcpb":
				artType = domain.ArtifactMCPB
			}

			artID := fmt.Sprintf("art_%s_%s", coord, verStr)
			locator := coord
			if pkg.RegistryType == "npm" {
				locator = fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz", coord, coord, verStr)
			}

			// The live schema publishes a bare package-file hash in
			// `fileSha256`; the legacy spelling is `digest` with the
			// sha256: prefix already attached. Both land in the artifact's
			// digest in the one vocabulary domain.ArtifactRef documents.
			digest := pkg.Digest
			if digest == "" && pkg.FileSha256 != "" {
				digest = "sha256:" + pkg.FileSha256
			}

			artifact := domain.ArtifactRef{
				ArtifactID:  artID,
				Type:        artType,
				Locator:     locator,
				Digest:      digest,
				FetchPolicy: domain.FetchImmutable,
			}

			// Command and Args are read only from the document's own
			// legacy `command`/`args` fields — never derived from
			// registryType, identifier or runtimeHint, none of which the
			// live schema claims start anything. `environmentVariables[]`
			// (live) is deliberately not mapped into Env: Env is a
			// name→value map, a live entry is a required input that often
			// declares no value at all, and inventing either a value or an
			// empty one would be a fabricated fact — interop records the
			// names instead.
			component := domain.Component{
				ID:   string(domain.NewComponentID(listingID, verStr, domain.ComponentMCPProvider, "server")),
				Kind: domain.ComponentMCPProvider,
				Name: "server",
				Runtime: &domain.RuntimeDescriptor{
					Type:    pkg.Transport.TransportType(),
					Command: pkg.Command,
					Args:    pkg.Args,
					Env:     pkg.Env,
				},
				SupportedByLiteSPM: domain.SupportYes,
			}

			var reqs []domain.Requirement
			if pkg.Runtime != "" {
				reqs = append(reqs, domain.Requirement{
					Type: "runtime",
					Name: pkg.Runtime,
				})
				reqSummaries = append(reqSummaries, pkg.Runtime)
			}

			verRecord := &domain.VersionRecord{
				ListingID:        string(listingID),
				Version:          verStr,
				SourceSnapshotID: snapshotID,
				Artifacts:        []domain.ArtifactRef{artifact},
				Components:       []domain.Component{component},
				Requirements:     reqs,
				FetchedAt:        now,
			}
			versions = append(versions, verRecord)
		}

		// A registry `remotes` entry publishes an endpoint, not a version —
		// the schema has no version field for it at all. Earlier revisions
		// stamped the sentinel "remote" here, which put a version string the
		// upstream never published into the row's summaries and version
		// records: fabricated metadata of the same class as the old "1.0.0"
		// default, and one mcpInstallability counted as a proven version.
		//
		// The endpoint itself is real metadata and stays, as a component on a
		// version-less record. It contributes no version summary, so a listing
		// whose packages published nothing ends up with zero versions and
		// resolves to discovery_only — searchable metadata with no install
		// path, which is all the catalog can stand behind for it. The record
		// carries no artifact either: there is no package coordinate to fetch
		// and no version to build a locator from.
		//
		// The component ID still needs a non-empty version segment
		// (ParseComponentID rejects an empty one), so it uses the literal
		// "discovery" — the same convention internal/catalogbuild/dataset.go
		// uses for a row with no proven version. Inventing a version number to
		// fill that slot is exactly what this file must not do.
		for _, rem := range srv.Remotes {
			component := domain.Component{
				ID:   string(domain.NewComponentID(listingID, versionlessComponentVersion, domain.ComponentMCPProvider, "server")),
				Kind: domain.ComponentMCPProvider,
				Name: "server",
				Runtime: &domain.RuntimeDescriptor{
					Type:     rem.TransportType(),
					Endpoint: rem.URL,
				},
				SupportedByLiteSPM: domain.SupportYes,
			}

			verRecord := &domain.VersionRecord{
				ListingID:        string(listingID),
				Version:          "",
				SourceSnapshotID: snapshotID,
				Components:       []domain.Component{component},
				FetchedAt:        now,
			}
			versions = append(versions, verRecord)
		}

		status := domain.ListingStatusActive
		if srv.Status == "deprecated" {
			status = domain.ListingStatusDeprecated
		}

		listing := &domain.Listing{
			SchemaVersion:  1,
			ID:             string(listingID),
			Kind:           domain.KindMCP,
			Name:           srv.Name,
			Title:          srv.Title,
			Summary:        srv.Description,
			Categories:     categories,
			Keywords:       srv.Keywords,
			PublisherClaim: pubClaim,
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: srv.Name,
				URL:        srv.Repository.URL,
			},
			Versions:             versionSummaries,
			ComponentsSummary:    compSummaries,
			RequirementsSummary:  reqSummaries,
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
			Provenance: domain.ProvenanceRecord{
				SourceSnapshotID: snapshotID,
				IngestedAt:       now,
			},
			Status: status,
			// Only a registry entry that published a package with a version
			// proved something installable. An endpoint with no published
			// version proves it exists, not what would be launched, so it
			// stays discovery_only.
			Installability: mcpInstallability(versionSummaries),
		}

		listings = append(listings, listing)
	}

	digest, err := domain.ComputeContentDigest(listings, versions)
	if err != nil {
		return nil, fmt.Errorf("snapshot content digest: %w", err)
	}

	return &IngestResult{
		SourceID:   a.sourceID,
		SnapshotID: snapshotID,
		Listings:   listings,
		Versions:   versions,
		ItemCount:  len(listings),
		Digest:     digest,
		IngestedAt: now,
	}, nil
}

// versionlessComponentVersion is the version segment used in a component ID for
// a component that belongs to no published version. The ID format requires a
// non-empty segment, and inventing a number there would be the same fabrication
// this package refuses everywhere else; internal/catalogbuild/dataset.go uses
// this literal for exactly the same reason.
const versionlessComponentVersion = "discovery"

// mcpInstallability maps "the registry entry published N proven versions" to
// the proven installability class. An empty version string is not a version —
// a row that published none is discovery_only, so no install path can ever
// meet a listing that claims metadata_verified while being unable to name the
// version it would install. Remote endpoints publish no version and therefore
// add no summary: a listing that only has one is discovery_only.
func mcpInstallability(versionSummaries []domain.VersionSummary) domain.Installability {
	proven := 0
	for _, summary := range versionSummaries {
		if strings.TrimSpace(summary.Version) != "" {
			proven++
		}
	}
	if proven == 0 {
		return domain.InstallabilityDiscoveryOnly
	}
	return domain.InstallabilityMetadataVerified
}
