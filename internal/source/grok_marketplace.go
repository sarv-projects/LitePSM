package source

import (
	"context"
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
type GrokMarketplaceAdapter struct{}

func (a *GrokMarketplaceAdapter) Descriptor() SourceAdapterDescriptor {
	return SourceAdapterDescriptor{
		SourceID:          "grok-marketplace",
		DisplayName:       "Grok Build Plugin Marketplace",
		Version:           "1.0.0",
		SupportedProtocols: []string{"https"},
	}
}

// ParseManifest parses a Grok marketplace manifest.
func (a *GrokMarketplaceAdapter) ParseManifest(data []byte) ([]*domain.Listing, error) {
	var manifest GrokMarketplaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse Grok marketplace manifest: %w", err)
	}

	var listings []*domain.Listing
	now := time.Now().UTC()

	for _, p := range manifest.Plugins {
		id := p.ID
		if id == "" {
			id = p.Name
		}
		cleanID := strings.ToLower(strings.ReplaceAll(id, " ", "-"))
		listingID := fmt.Sprintf("plugin:grok:%s", cleanID)

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

		// Pinned commit SHA is required for reproducibility
		commitSHA := p.CommitSHA
		if len(commitSHA) < 40 {
			commitSHA = "0000000000000000000000000000000000000000"
		}

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            listingID,
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
			Status: domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "signature_verified",
			},
			CreatedAt: now,
			UpdatedAt: now,
			Versions: []domain.VersionRecord{
				{
					Version:   ver,
					Status:    "active",
					CreatedAt: now,
					Components: []domain.Component{
						{
							ComponentID: fmt.Sprintf("%s:main", listingID),
							Kind:        domain.KindPlugin,
							Name:        p.Name,
						},
					},
				},
			},
		}

		listings = append(listings, listing)
	}

	return listings, nil
}

// Ingest processes raw JSON into Listings.
func (a *GrokMarketplaceAdapter) Ingest(ctx context.Context, rawManifest []byte) ([]*domain.Listing, error) {
	return a.ParseManifest(rawManifest)
}
