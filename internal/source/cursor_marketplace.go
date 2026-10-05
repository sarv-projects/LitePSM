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

// CursorMarketplaceManifest models .cursor-plugin/marketplace.json as used by
// the Cursor plugin marketplace (cursor/plugins).
//
// Entries are minimal: name, a same-repo subpath `source` string, and a
// description. Per-plugin detail (skills, rules, mcp.json) lives in each
// plugin's own `.cursor-plugin/plugin.json` and is not fetched at discovery.
type CursorMarketplaceManifest struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Owner       FlexString          `json:"owner,omitempty"`
	Publisher   FlexString          `json:"publisher,omitempty"`
	Metadata    CursorMetadata      `json:"metadata,omitempty"`
	Plugins     []CursorPluginEntry `json:"plugins"`
}

// CursorMetadata carries marketplace-level description/version.
type CursorMetadata struct {
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// CursorPluginEntry models one plugin in a Cursor marketplace manifest.
type CursorPluginEntry struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName,omitempty"`
	Description string     `json:"description,omitempty"`
	Version     string     `json:"version,omitempty"`
	Author      FlexString `json:"author,omitempty"`
	Source      FlexSource `json:"source"`
}

// CursorMarketplaceAdapter ingests Cursor plugin marketplace manifests.
type CursorMarketplaceAdapter struct {
	sourceID domain.SourceID
}

// NewCursorMarketplaceAdapter creates a new Cursor marketplace adapter.
func NewCursorMarketplaceAdapter(sourceID domain.SourceID) *CursorMarketplaceAdapter {
	if sourceID == "" {
		sourceID = domain.SourceID("git:cursor-plugins")
	}
	return &CursorMarketplaceAdapter{sourceID: sourceID}
}

// Ingest parses a raw Cursor marketplace manifest into Listings and VersionRecords.
func (a *CursorMarketplaceAdapter) Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error) {
	var manifest CursorMarketplaceManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse Cursor marketplace manifest: %w", err)
	}

	var listings []*domain.Listing
	var versions []*domain.VersionRecord
	now := time.Now().UTC()
	hasher := sha256.New()

	fallbackAuthor := firstNonEmpty(
		manifest.Owner.Value,
		manifest.Publisher.Value,
		PublisherForSource(a.sourceID),
		"Community",
	)

	for _, p := range manifest.Plugins {
		if p.Source.IsCommand() {
			continue
		}
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		cleanName := slugifyName(p.Name)
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanName)

		ver := firstNonEmpty(p.Version, manifest.Metadata.Version, "0.0.0-unknown")
		author := firstNonEmpty(p.Author.Value, fallbackAuthor)
		repoURL := firstNonEmpty(p.Source.RepositoryURL(), "")

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
			Title:         firstNonEmpty(p.DisplayName, p.Name),
			Summary:       p.Description,
			Categories:    []string{"developer-tools"},
			Keywords:      []string{"cursor", "plugin", cleanName},
			PublisherClaim: domain.PublisherClaim{
				Name: author,
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
			RequirementsSummary:  []string{},
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
