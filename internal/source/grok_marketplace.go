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
//
// Observed shapes: `owner`/`publisher` may be strings or objects, plugin
// `source` may be a string or a descriptor object, `category` may be a string
// or a list, and plugins carry `keywords`/`domains` instead of versions.
type GrokMarketplaceManifest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Publisher   FlexString        `json:"publisher,omitempty"`
	Owner       FlexString        `json:"owner,omitempty"`
	Plugins     []GrokPluginEntry `json:"plugins"`
}

// GrokPluginEntry models a plugin item in a Grok Build marketplace.
type GrokPluginEntry struct {
	ID          string      `json:"id,omitempty"`
	Name        string      `json:"name"`
	DisplayName string      `json:"displayName,omitempty"`
	Description string      `json:"description,omitempty"`
	Version     string      `json:"version,omitempty"`
	Author      FlexString  `json:"author,omitempty"`
	Repository  string      `json:"repository,omitempty"`
	Homepage    string      `json:"homepage,omitempty"`
	Source      FlexSource  `json:"source"`
	CommitSHA   string      `json:"commit_sha,omitempty"`
	Category    FlexString  `json:"category,omitempty"`
	Categories  FlexStrings `json:"categories,omitempty"`
	Keywords    []string    `json:"keywords,omitempty"`
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

	fallbackAuthor := firstNonEmpty(
		manifest.Owner.Value,
		manifest.Publisher.Value,
		PublisherForSource(a.sourceID),
		"xAI Ecosystem",
	)

	for _, p := range manifest.Plugins {
		if p.Source.IsCommand() {
			continue
		}
		name := firstNonEmpty(p.Name, p.ID)
		if strings.TrimSpace(name) == "" {
			continue
		}
		cleanID := slugifyName(firstNonEmpty(p.ID, p.Name))
		listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanID)

		ver := firstNonEmpty(p.Version, "0.0.0-unknown")

		author := firstNonEmpty(p.Author.Value, fallbackAuthor)

		var categories []string
		categories = append(categories, p.Categories.Values...)
		if p.Category.Value != "" {
			categories = append(categories, p.Category.Value)
		}
		if len(categories) == 0 {
			categories = []string{"developer-tools"}
		}

		keywords := append([]string{"grok", "plugin", cleanID}, p.Keywords...)
		repoURL := firstNonEmpty(p.Source.RepositoryURL(), p.Repository, p.Homepage)

		component := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentAsset, cleanID)),
			Kind:               domain.ComponentAsset,
			Name:               name,
			SupportedByLitePSM: domain.SupportUnknown,
		}

		verRecord := &domain.VersionRecord{
			ListingID:        string(listingID),
			Version:          ver,
			SourceSnapshotID: snapshotID,
			Components:       []domain.Component{component},
			FetchedAt:        now,
		}
		if isFullCommitSHA(p.CommitSHA) {
			verRecord.ImmutableRef = "git:" + strings.ToLower(p.CommitSHA)
		} else if isFullCommitSHA(p.Source.SHA) {
			verRecord.ImmutableRef = "git:" + strings.ToLower(p.Source.SHA)
		}
		versions = append(versions, verRecord)

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            string(listingID),
			Kind:          domain.KindPlugin,
			Name:          name,
			Title:         firstNonEmpty(p.DisplayName, name),
			Summary:       p.Description,
			Categories:    categories,
			Keywords:      keywords,
			PublisherClaim: domain.PublisherClaim{
				Name: author,
				URL:  firstNonEmpty(p.Homepage, repoURL),
			},
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: name,
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
					Name: name,
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

// isFullCommitSHA reports whether s looks like a full 40-hex commit SHA.
func isFullCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range strings.ToLower(s) {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
