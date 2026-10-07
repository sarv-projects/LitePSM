package source

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// ClaudeMarketplaceManifest represents a .claude-plugin/marketplace.json file.
//
// Real-world manifests vary: `publisher`/`owner`/`author` may each be a string
// or an object, `category` may be a string or a list, and `source` may be a
// local-path string or a fetch descriptor object. The flexible types in
// flex.go normalize all observed shapes.
type ClaudeMarketplaceManifest struct {
	SchemaVersion int                 `json:"schemaVersion,omitempty"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Publisher     FlexString          `json:"publisher,omitempty"`
	Owner         FlexString          `json:"owner,omitempty"`
	Metadata      ClaudeMetadata      `json:"metadata,omitempty"`
	Renames       map[string]string   `json:"renames,omitempty"`
	Plugins       []ClaudePluginEntry `json:"plugins"`
}

// ClaudeMetadata carries marketplace-level version/description.
type ClaudeMetadata struct {
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// ClaudePluginEntry models an individual plugin entry in a Claude marketplace manifest.
type ClaudePluginEntry struct {
	Name        string                     `json:"name"`
	DisplayName string                     `json:"displayName,omitempty"`
	Description string                     `json:"description,omitempty"`
	Version     string                     `json:"version,omitempty"`
	Author      FlexString                 `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Repository  string                     `json:"repository,omitempty"`
	Category    FlexString                 `json:"category,omitempty"`
	Categories  FlexStrings                `json:"categories,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	Source      FlexSource                 `json:"source"`
	Strict      *bool                      `json:"strict,omitempty"`
	Skills      []string                   `json:"skills,omitempty"`
	LSPServers  map[string]json.RawMessage `json:"lspServers,omitempty"`
}

// ClaudeMarketplaceAdapter ingests Claude Code marketplace manifests.
type ClaudeMarketplaceAdapter struct {
	sourceID domain.SourceID
}

// NewClaudeMarketplaceAdapter creates a new Claude marketplace adapter.
func NewClaudeMarketplaceAdapter(sourceID domain.SourceID) *ClaudeMarketplaceAdapter {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:claude-plugins")
	}
	return &ClaudeMarketplaceAdapter{sourceID: sourceID}
}

// Ingest parses a raw marketplace manifest into normalized Listings and VersionRecords.
func (a *ClaudeMarketplaceAdapter) Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error) {
	var manifest ClaudeMarketplaceManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse Claude marketplace manifest: %w", err)
	}

	var listings []*domain.Listing
	var versions []*domain.VersionRecord
	now := time.Now().UTC()

	fallbackAuthor := firstNonEmpty(
		manifest.Owner.Value,
		manifest.Publisher.Value,
		PublisherForSource(a.sourceID),
		"Community",
	)

	for _, p := range manifest.Plugins {
		// Strict Security Invariant: Reject "command" source type in v1.
		// A command source executes a shell script during fetch.
		if p.Source.IsCommand() {
			continue
		}
		if strings.TrimSpace(p.Name) == "" {
			continue
		}

		ver := firstNonEmpty(p.Version, manifest.Metadata.Version, "0.0.0-unknown")

		title := firstNonEmpty(p.DisplayName, p.Name)
		cleanName := slugifyName(p.Name)
		if cleanName == "" {
			cleanName = slugifyName(title)
		}
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanName)

		author := firstNonEmpty(p.Author.Value, fallbackAuthor)

		var categories []string
		categories = append(categories, p.Categories.Values...)
		if p.Category.Value != "" {
			categories = append(categories, p.Category.Value)
		}
		if len(categories) == 0 {
			categories = []string{"developer-tools"}
		}

		var keywords []string
		keywords = append(keywords, p.Tags...)
		keywords = append(keywords, p.Keywords...)

		repoURL := firstNonEmpty(p.Source.RepositoryURL(), p.Repository, p.Homepage)

		// Component mapping: skill bundles expose one skill component per
		// declared skill; LSP maps expose one lsp component per server;
		// opaque bundles expose a single asset component. A plugin bundle is
		// never mislabelled as a bare skill.
		var components []domain.Component
		var compSummaries []domain.ComponentSummary
		for _, sp := range p.Skills {
			name := skillBaseName(sp)
			if name == "" {
				continue
			}
			components = append(components, domain.Component{
				ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentSkill, name)),
				Kind:               domain.ComponentSkill,
				Name:               name,
				Path:               sp,
				SupportedByLiteSPM: domain.SupportYes,
			})
			compSummaries = append(compSummaries, domain.ComponentSummary{
				Kind: domain.ComponentSkill,
				Name: name,
			})
		}
		for srv := range p.LSPServers {
			name := slugifyName(srv)
			if name == "" {
				continue
			}
			components = append(components, domain.Component{
				ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentLSP, name)),
				Kind:               domain.ComponentLSP,
				Name:               name,
				SupportedByLiteSPM: domain.SupportUnknown,
			})
			compSummaries = append(compSummaries, domain.ComponentSummary{
				Kind: domain.ComponentLSP,
				Name: name,
			})
		}
		if len(components) == 0 {
			components = append(components, domain.Component{
				ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentAsset, cleanName)),
				Kind:               domain.ComponentAsset,
				Name:               p.Name,
				SupportedByLiteSPM: domain.SupportUnknown,
			})
			compSummaries = append(compSummaries, domain.ComponentSummary{
				Kind: domain.ComponentAsset,
				Name: p.Name,
			})
		}

		verRecord := &domain.VersionRecord{
			ListingID:        string(listingID),
			Version:          ver,
			SourceSnapshotID: snapshotID,
			Components:       components,
			FetchedAt:        now,
		}
		versions = append(versions, verRecord)

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            string(listingID),
			Kind:          domain.KindPlugin,
			Name:          p.Name,
			Title:         title,
			Summary:       p.Description,
			Categories:    categories,
			Keywords:      keywords,
			PublisherClaim: domain.PublisherClaim{
				Name: author,
				URL:  firstNonEmpty(p.Homepage, repoURL),
			},
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: p.Name,
				URL:        repoURL,
			},
			Versions: []domain.VersionSummary{
				{
					Version:     ver,
					PublishedAt: &now,
				},
			},
			ComponentsSummary:    compSummaries,
			RequirementsSummary:  []string{},
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
			Provenance: domain.ProvenanceRecord{
				SourceSnapshotID: snapshotID,
				IngestedAt:       now,
			},
			Status:         domain.ListingStatusActive,
			Installability: domain.InstallabilityMetadataVerified,
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
