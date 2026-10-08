package catalogbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// DatasetPublisher is the publisher object carried by every dataset row.
type DatasetPublisher struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// DatasetRow mirrors one flat row of the catalog dataset written by
// scripts/build_full_catalog.py (web/data/catalog.json).
//
// The dataset is richer than the domain model in ways the domain deliberately
// refuses to model: web display hints (compatibleHosts, installHint), an
// upstream `verified` flag, and stars (null everywhere by policy -- see the
// note at the top of the generator). Fields that have no domain counterpart
// are not decoded here and are never copied into a listing, so a dataset row
// cannot smuggle an unrepresentable claim past conversion.
type DatasetRow struct {
	ID               string           `json:"id"`
	Kind             string           `json:"kind"`
	Name             string           `json:"name"`
	Slug             string           `json:"slug"`
	Summary          string           `json:"summary"`
	Category         string           `json:"category"`
	Publisher        DatasetPublisher `json:"publisher"`
	Source           string           `json:"source"`           // producer source id when supplied
	SourceSnapshotID string           `json:"sourceSnapshotId"` // only present when the producer has a real source snapshot id
	Transport        string           `json:"transport"`        // mcp only: stdio | sse | streamable-http
	Version          string           `json:"version"`          // every row
	Command          string           `json:"command"`          // mcp only: stdio launch command
	Args             []string         `json:"args"`             // mcp only
	URL              string           `json:"url"`              // mcp only: remote endpoint the publisher declared
	SkillSource      string           `json:"skillSource"`      // skill only
	// Installability is the provenance class of the row (domain.Installability
	// values). Empty means discovery_only: heuristic ingestion never proved a
	// version or launch line.
	Installability string `json:"installability"`
}

// ParseDataset decodes and validates dataset bytes fail-closed. Every row must
// carry a domain-valid listing id whose kind segment agrees with the row, a
// non-empty name and -- for MCP rows that claim more than discovery_only -- the
// version, transport and a launch line a future install would use (command+args
// for stdio, or the publisher's remote endpoint); ids must be
// unique. The generator normalizes
// ids to the same grammar (canonical_id in scripts/build_full_catalog.py), so
// a violation means the producer and the domain have drifted and the release
// must not be built.
func ParseDataset(raw []byte) ([]DatasetRow, error) {
	var rows []DatasetRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode dataset: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("dataset contains no rows")
	}

	var problems []string
	seen := make(map[string]int, len(rows))
	for i, r := range rows {
		fail := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("row %d (%q): %s", i, r.ID, fmt.Sprintf(format, args...)))
		}

		if _, err := domain.ParseListingID(r.ID); err != nil {
			fail("id does not match the domain grammar %s", domain.RegexListingID.String())
		} else if parts := strings.SplitN(r.ID, ":", 3); parts[0] != r.Kind {
			fail("id kind %q disagrees with row kind %q", parts[0], r.Kind)
		}
		if prev, dup := seen[r.ID]; dup {
			fail("duplicate id (first seen at row %d)", prev)
		} else {
			seen[r.ID] = i
		}
		if r.Name == "" {
			fail("missing name")
		}
		if r.Source != "" {
			if _, err := domain.ParseSourceID(r.Source); err != nil {
				fail("source is not a valid domain SourceID: %v", err)
			}
		}
		if !domain.Installability(r.Installability).Valid() {
			fail("unknown installability %q", r.Installability)
		}
		// Version and launch line are required only of rows that claim more
		// than discovery_only: a heuristic row has none to give, and the
		// generator no longer invents them. The launch line is EITHER a
		// stdio command OR the publisher's remote endpoint -- an MCP row
		// above discovery_only with neither fails, and a discovery_only row
		// carrying either one fails the same way (both must be proven from
		// a manifest, never asserted by heuristic ingestion).
		if domain.Installability(r.Installability).Effective() != domain.InstallabilityDiscoveryOnly {
			if r.Kind == "mcp" {
				if r.Version == "" {
					fail("mcp row claims %s but has no version", r.Installability)
				}
				if r.Transport == "" {
					fail("mcp row claims %s but has no transport", r.Installability)
				}
				if r.Command == "" && r.URL == "" {
					fail("mcp row claims %s but has no launch line (neither a command nor an endpoint)", r.Installability)
				}
			}
		} else if r.Command != "" {
			fail("discovery_only row carries a launch command; commands must be proven from a manifest")
		} else if r.URL != "" {
			fail("discovery_only row carries a remote endpoint; endpoints must be proven from a manifest")
		}
	}
	if len(problems) > 0 {
		shown := problems
		ellipsis := ""
		if len(shown) > 10 {
			shown = shown[:10]
			ellipsis = fmt.Sprintf("; ... and %d more", len(problems)-10)
		}
		return nil, fmt.Errorf("dataset validation failed (%d problem(s)): %s%s",
			len(problems), strings.Join(shown, "; "), ellipsis)
	}
	return rows, nil
}

// LoadDataset reads a dataset file and validates it via ParseDataset.
func LoadDataset(path string) ([]DatasetRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %s: %w", path, err)
	}
	return ParseDataset(raw)
}

// DatasetSnapshotID derives a deterministic identity for a dataset input's
// bytes. It is a build-input fingerprint, not an upstream SourceSnapshot ID;
// row provenance must use DatasetRow.SourceSnapshotID when supplied.
func DatasetSnapshotID(dataset []byte) string {
	sum := sha256.Sum256(dataset)
	return "snap-" + hex.EncodeToString(sum[:8])
}

// ConvertDataset converts validated dataset rows into the listings and version
// records CompileRelease consumes.
//
// Every claim written here is one the dataset can support:
//   - sourceId comes from the producer's explicit source field when present;
//   - sourceSnapshotId is copied only from a per-row upstream snapshot id. The
//     dataset-wide input fingerprint is not an upstream snapshot and is not
//     attached to individual rows;
//   - verification level is "unverified" -- the dataset carries no audit
//     evidence, so no stronger level may be asserted;
//   - MCP components are SupportUnknown because installing a catalog-sourced
//     MCP server is not wired yet (STATUS.md; ARCH/13/17), while skill
//     components are SupportYes because the shipped skills lifecycle installs
//     and loads them today;
//   - popularity, host-compatibility, and star figures are not invented.
func ConvertDataset(rows []DatasetRow, releaseID, _datasetInputID string, ingestedAt time.Time) ([]*domain.Listing, []*domain.VersionRecord, error) {
	listings := make([]*domain.Listing, 0, len(rows))
	versions := make([]*domain.VersionRecord, 0, len(rows))

	for _, row := range rows {
		listing, version, err := convertRow(row, releaseID, ingestedAt)
		if err != nil {
			return nil, nil, err
		}
		listings = append(listings, listing)
		versions = append(versions, version)
	}
	return listings, versions, nil
}

func convertRow(row DatasetRow, releaseID string, ingestedAt time.Time) (*domain.Listing, *domain.VersionRecord, error) {
	lid, err := domain.ParseListingID(row.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("convert %q: %w", row.ID, err)
	}

	idParts := strings.Split(row.ID, ":")
	rawUpstream := idParts[2]
	encodedSource := ""
	if len(idParts) >= 4 {
		encodedSource = idParts[1] + ":" + idParts[2]
		rawUpstream = strings.Join(idParts[3:], ":")
	}
	upstream, err := url.PathUnescape(rawUpstream)
	if err != nil {
		return nil, nil, fmt.Errorf("convert %q: upstream id is not decodable: %w", row.ID, err)
	}
	sourceID := row.Source
	if sourceID == "" {
		// Older data may not carry the producer's source field. Preserve an
		// ID-encoded SourceID only when the listing ID actually contains the
		// full domain SourceID form; otherwise leave it unknown rather than
		// treating a publisher namespace as a fetch source.
		if encodedSource != "" {
			if _, parseErr := domain.ParseSourceID(encodedSource); parseErr == nil {
				sourceID = encodedSource
			}
		}
	}

	sourceURL := row.Publisher.URL
	if sourceURL == "" {
		sourceURL = row.SkillSource
	}

	categories := []string{}
	if row.Category != "" {
		categories = append(categories, row.Category)
	}

	summaries, components, err := datasetComponents(row, lid)
	if err != nil {
		return nil, nil, err
	}

	installability := domain.Installability(row.Installability)
	if installability == "" {
		// Dataset rows without an explicit provenance class are heuristic
		// ingestion (awesome-lists): discovery-only by default. Only vendor
		// manifests opt into metadata_verified by setting the field.
		installability = domain.InstallabilityDiscoveryOnly
	}

	versionForRecord := row.Version
	versionSummaries := []domain.VersionSummary{}
	if versionForRecord != "" {
		versionSummaries = []domain.VersionSummary{{Version: versionForRecord}}
	}

	listing := &domain.Listing{
		SchemaVersion: 1,
		ID:            row.ID,
		Kind:          domain.ListingKind(row.Kind),
		Name:          row.Name,
		Summary:       row.Summary,
		Categories:    categories,
		Keywords:      []string{},
		PublisherClaim: domain.PublisherClaim{
			Name: row.Publisher.Name,
			URL:  sourceURL,
		},
		Source: domain.SourceReference{
			SourceID:   sourceID,
			UpstreamID: upstream,
			URL:        sourceURL,
		},
		Versions:             versionSummaries,
		ComponentsSummary:    summaries,
		RequirementsSummary:  []string{},
		CompatibilitySummary: []domain.CompatibilityFact{},
		VerificationSummary: domain.VerificationSummary{
			Level: "unverified",
		},
		Provenance: domain.ProvenanceRecord{
			SourceSnapshotID: row.SourceSnapshotID,
			IngestedAt:       ingestedAt,
			CatalogReleaseID: releaseID,
		},
		Status:         domain.ListingStatusActive,
		Installability: installability,
	}

	version := &domain.VersionRecord{
		ListingID:        row.ID,
		Version:          versionForRecord,
		SourceSnapshotID: row.SourceSnapshotID,
		Artifacts:        []domain.ArtifactRef{},
		Components:       components,
		Dependencies:     []domain.DependencyConstraint{},
		Requirements:     []domain.Requirement{},
		// The dataset declares no permissions, and inventing a declaration
		// would fabricate the exact signal the policy engine acts on.
		PermissionsDeclared: []domain.PermissionDeclaration{},
		CompatibilityClaims: []domain.CompatibilityFact{},
		TestEvidence:        []domain.TestEvidenceRecord{},
		FetchedAt:           ingestedAt,
	}

	return listing, version, nil
}

// datasetComponents derives the per-kind component lists. MCP rows carry their
// launch line into a RuntimeDescriptor ONLY when the dataset proves one: a
// stdio command+args, or -- for a remote row -- the publisher's endpoint and
// its transport. Discovery-only rows (no proven launch line) produce a
// component with no runtime so no installer can invent a launch line. When a
// row carries both, stdio wins: a real command is a local install and the
// endpoint is unused (no published row carries both today -- the producer
// writes an endpoint only when command/args are null).
func datasetComponents(row DatasetRow, lid domain.ListingID) ([]domain.ComponentSummary, []domain.Component, error) {
	// Component IDs embed a version; discovery-only rows carry no proven
	// version, so the ID uses the literal "discovery" rather than inventing
	// one. The VersionRecord itself keeps the honest empty value.
	versionForID := row.Version
	if versionForID == "" {
		versionForID = "discovery"
	}
	switch domain.ListingKind(row.Kind) {
	case domain.KindMCP:
		if row.Command == "" {
			if row.URL == "" {
				// No proven launch line: honest absence, not an empty command.
				return []domain.ComponentSummary{{Kind: domain.ComponentMCPProvider, Name: "server"}},
					[]domain.Component{{
						ID:   string(domain.NewComponentID(lid, versionForID, domain.ComponentMCPProvider, "server")),
						Kind: domain.ComponentMCPProvider,
						Name: "server",
						// Runtime is nil: RuntimeForListing fails closed.
						Runtime:         nil,
						DeclaredEffects: []domain.EffectDeclaration{},
						// Not "yes": resolving and installing this launch line from
						// the catalog is not wired yet (STATUS.md install row).
						SupportedByLiteSPM: domain.SupportUnknown,
					}}, nil
			}
			// Remote (URL) row: the launch line is the endpoint itself -- no
			// process is spawned, the client connects. ParseDataset required
			// a transport alongside the URL, so the descriptor carries the
			// published transport verbatim ("streamable-http", "sse") and
			// the endpoint the egress guard re-checks at connect time.
			return []domain.ComponentSummary{{Kind: domain.ComponentMCPProvider, Name: "server"}},
				[]domain.Component{{
					ID:   string(domain.NewComponentID(lid, versionForID, domain.ComponentMCPProvider, "server")),
					Kind: domain.ComponentMCPProvider,
					Name: "server",
					Runtime: &domain.RuntimeDescriptor{
						Type:     row.Transport,
						Endpoint: row.URL,
					},
					DeclaredEffects: []domain.EffectDeclaration{},
					// Not "yes": registering a remote entry in a host config
					// is not wired yet (STATUS.md install row).
					SupportedByLiteSPM: domain.SupportUnknown,
				}}, nil
		}
		transport := row.Transport
		if transport == "" {
			transport = "stdio"
		}
		return []domain.ComponentSummary{{Kind: domain.ComponentMCPProvider, Name: "server"}},
			[]domain.Component{{
				ID:   string(domain.NewComponentID(lid, versionForID, domain.ComponentMCPProvider, "server")),
				Kind: domain.ComponentMCPProvider,
				Name: "server",
				Runtime: &domain.RuntimeDescriptor{
					Type:    transport,
					Command: row.Command,
					Args:    append([]string(nil), row.Args...),
				},
				DeclaredEffects: []domain.EffectDeclaration{},
				// Not "yes": resolving and installing this launch line from
				// the catalog is not wired yet (STATUS.md install row).
				SupportedByLiteSPM: domain.SupportUnknown,
			}}, nil

	case domain.KindSkill:
		componentName := row.Slug
		if componentName == "" {
			componentName = row.ID
		}
		return []domain.ComponentSummary{{Kind: domain.ComponentSkill, Name: row.Name}},
			[]domain.Component{{
				ID:   string(domain.NewComponentID(lid, versionForID, domain.ComponentSkill, componentName)),
				Kind: domain.ComponentSkill,
				Name: row.Name,
				// The shipped skills lifecycle installs, stores, and loads
				// SKILL.md packages today (STATUS.md skills row).
				DeclaredEffects:    []domain.EffectDeclaration{},
				SupportedByLiteSPM: domain.SupportYes,
			}}, nil

	case domain.KindPlugin:
		// The dataset does not enumerate a plugin's components; an empty list
		// records what is known instead of inventing members.
		return []domain.ComponentSummary{}, []domain.Component{}, nil

	default:
		return nil, nil, fmt.Errorf("convert %q: unsupported kind %q", row.ID, row.Kind)
	}
}
