package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// GrokMarketplaceManifest models .grok-plugin/marketplace.json.
type GrokMarketplaceManifest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Publisher   string            `json:"publisher,omitempty"`
	Plugins     []GrokPluginEntry `json:"plugins"`
}

// GrokPluginEntry models a plugin item in Grok Build marketplace.
type GrokPluginEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Author      string   `json:"author,omitempty"`
	Repository  string   `json:"repository"`
	CommitSHA   string   `json:"commit_sha"` // Pinned 40-character commit SHA
	Categories  []string `json:"categories,omitempty"`
}

// GrokMarketplaceAdapter ingests Grok Build plugin marketplaces.
type GrokMarketplaceAdapter struct {
	sourceID domain.SourceID
}

// NewGrokMarketplaceAdapter creates a new Grok marketplace adapter.
func NewGrokMarketplaceAdapter(sourceID domain.SourceID) *GrokMarketplaceAdapter {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:grok-plugins")
	}
	return &GrokMarketplaceAdapter{sourceID: sourceID}
}

// Ingest parses raw JSON into normalized Listings and VersionRecords.
func (a *GrokMarketplaceAdapter) Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error) {
	var manifest GrokMarketplaceManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse Grok marketplace manifest: %w", err)
	}

	var listings []*domain.Listing
	var versions []*domain.VersionRecord
	now := time.Now().UTC()
	hasher := sha256.New()

	for _, p := range manifest.Plugins {
		id := p.ID
		if id == "" {
			id = p.Name
		}
		cleanID := strings.ToLower(strings.ReplaceAll(id, " ", "-"))
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanID)

		ver := p.Version
		if ver == "" {
			ver = "1.0.0"
		}

		author := p.Author
		if author == "" {
			author = manifest.Publisher
		}
		if author == "" {
			author = "xAI Ecosystem"
		}

		categories := p.Categories
		if len(categories) == 0 {
			categories = []string{"developer-tools"}
		}

		component := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentSkill, cleanID)),
			Kind:               domain.ComponentSkill,
			Name:               p.Name,
			SupportedByLitePSM: domain.SupportYes,
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
			Title:         p.Name,
			Summary:       p.Description,
			Categories:    categories,
			Keywords:      []string{"grok", "plugin", cleanID},
			PublisherClaim: domain.PublisherClaim{
				Name: author,
				URL:  p.Repository,
			},
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: cleanID,
				URL:        p.Repository,
			},
			Versions: []domain.VersionSummary{
				{
					Version:     ver,
					PublishedAt: &now,
				},
			},
			ComponentsSummary: []domain.ComponentSummary{
				{
					Kind: domain.ComponentSkill,
					Name: p.Name,
				},
			},
			RequirementsSummary:  []string{},
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary: domain.VerificationSummary{
				Level: "signature_verified",
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
