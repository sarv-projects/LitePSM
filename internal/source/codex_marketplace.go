package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// CodexMarketplaceManifest models .agents/plugins/marketplace.json as used by
// the OpenAI Codex plugin marketplaces (e.g. openai/plugins).
//
// Entries carry no description or version; they declare a `policy`
// (installation availability + authentication point) and a `category`.
type CodexMarketplaceManifest struct {
	Name      string             `json:"name"`
	Interface CodexInterface     `json:"interface,omitempty"`
	Plugins   []CodexPluginEntry `json:"plugins"`
}

// CodexInterface carries a display name for the marketplace or plugin.
type CodexInterface struct {
	DisplayName string `json:"displayName,omitempty"`
}

// CodexPluginEntry models one plugin in a Codex marketplace manifest.
type CodexPluginEntry struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"displayName,omitempty"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version,omitempty"`
	Source      FlexSource     `json:"source"`
	Policy      CodexPolicy    `json:"policy,omitempty"`
	Category    FlexString     `json:"category,omitempty"`
	Interface   CodexInterface `json:"interface,omitempty"`
}

// CodexPolicy declares installation availability and authentication.
type CodexPolicy struct {
	Installation   string   `json:"installation,omitempty"`
	Authentication string   `json:"authentication,omitempty"`
	Products       []string `json:"products,omitempty"`
}

// CodexMarketplaceAdapter ingests Codex plugin marketplace manifests.
type CodexMarketplaceAdapter struct {
	sourceID domain.SourceID
}

// NewCodexMarketplaceAdapter creates a new Codex marketplace adapter.
func NewCodexMarketplaceAdapter(sourceID domain.SourceID) *CodexMarketplaceAdapter {
	if sourceID == "" {
		sourceID = domain.SourceID("git:openai-plugins")
	}
	return &CodexMarketplaceAdapter{sourceID: sourceID}
}

// Ingest parses a raw Codex marketplace manifest into Listings and VersionRecords.
func (a *CodexMarketplaceAdapter) Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error) {
	var manifest CodexMarketplaceManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse Codex marketplace manifest: %w", err)
	}

	var listings []*domain.Listing
	var versions []*domain.VersionRecord
	now := time.Now().UTC()
	hasher := sha256.New()

	publisher := firstNonEmpty(PublisherForSource(a.sourceID), "Community")

	for _, p := range manifest.Plugins {
		if p.Source.IsCommand() {
			continue
		}
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		cleanName := slugifyName(p.Name)
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanName)

		ver := firstNonEmpty(p.Version, "0.0.0-unknown")

		categories := []string{"developer-tools"}
		if p.Category.Value != "" {
			categories = []string{p.Category.Value}
		}

		var requirements []string
		if p.Policy.Authentication != "" {
			requirements = append(requirements, "authentication required: "+strings.ToLower(p.Policy.Authentication)+" (declared by manifest)")
		}

		repoURL := p.Source.RepositoryURL()

		component := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentAsset, cleanName)),
			Kind:               domain.ComponentAsset,
			Name:               p.Name,
			SupportedByLiteSPM: domain.SupportUnknown,
		}

		verRecord := &domain.VersionRecord{
			ListingID:        string(listingID),
			Version:          ver,
			SourceSnapshotID: snapshotID,
			Components:       []domain.Component{component},
			FetchedAt:        now,
		}
		versions = append(versions, verRecord)

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            string(listingID),
			Kind:          domain.KindPlugin,
			Name:          p.Name,
			Title:         firstNonEmpty(p.DisplayName, p.Interface.DisplayName, p.Name),
			Summary:       p.Description,
			Categories:    categories,
			Keywords:      []string{"codex", "plugin", cleanName},
			PublisherClaim: domain.PublisherClaim{
				Name: publisher,
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
			ComponentsSummary: []domain.ComponentSummary{
				{
					Kind: domain.ComponentAsset,
					Name: p.Name,
				},
			},
			RequirementsSummary:  requirements,
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
			Provenance: domain.ProvenanceRecord{
				SourceSnapshotID: snapshotID,
				IngestedAt:       now,
			},
			Status: domain.ListingStatusActive,
		}

		listings = append(listings, listing)
		hasher.Write([]byte(listing.ID))
	}

	digest := fmt.Sprintf("sha256:%s", hex.EncodeToString(hasher.Sum(nil)))

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
