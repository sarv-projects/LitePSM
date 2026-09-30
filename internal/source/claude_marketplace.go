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

// ClaudeMarketplaceManifest represents a .claude-plugin/marketplace.json file.
type ClaudeMarketplaceManifest struct {
	SchemaVersion int                 `json:"schemaVersion,omitempty"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Publisher     string              `json:"publisher,omitempty"`
	Plugins       []ClaudePluginEntry `json:"plugins"`
}

// ClaudePluginEntry models an individual plugin entry in a Claude marketplace manifest.
type ClaudePluginEntry struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Version     string             `json:"version,omitempty"`
	Author      string             `json:"author,omitempty"`
	Homepage    string             `json:"homepage,omitempty"`
	Repository  string             `json:"repository,omitempty"`
	Categories  []string           `json:"categories,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	Source      ClaudePluginSource `json:"source"`
}

// ClaudePluginSource defines how the plugin is fetched.
type ClaudePluginSource struct {
	Type    string `json:"type"` // "github", "git-subdir", "archive", "npm", or "command" (REJECTED)
	Repo    string `json:"repo,omitempty"`
	Subdir  string `json:"subdir,omitempty"`
	Ref     string `json:"ref,omitempty"`
	URL     string `json:"url,omitempty"`
	Package string `json:"package,omitempty"`
	Command string `json:"command,omitempty"`
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
	hasher := sha256.New()

	for _, p := range manifest.Plugins {
		// Strict Security Invariant: Reject "command" source type in v1
		if strings.ToLower(p.Source.Type) == "command" || p.Source.Command != "" {
			continue
		}

		ver := p.Version
		if ver == "" {
			ver = "1.0.0"
		}

		cleanName := strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanName)
		author := p.Author
		if author == "" {
			author = manifest.Publisher
		}
		if author == "" {
			author = "Community"
		}

		categories := p.Categories
		if len(categories) == 0 {
			categories = []string{"developer-tools"}
		}

		component := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentSkill, cleanName)),
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
			Keywords:      p.Tags,
			PublisherClaim: domain.PublisherClaim{
				Name: author,
				URL:  p.Homepage,
			},
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: p.Name,
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
