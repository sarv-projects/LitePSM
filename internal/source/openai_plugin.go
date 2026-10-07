package source

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// OpenAIPluginManifest models the portable OpenAI plugin.json format.
type OpenAIPluginManifest struct {
	SchemaVersion string         `json:"schema_version,omitempty"`
	NameForHuman  string         `json:"name_for_human"`
	NameForModel  string         `json:"name_for_model"`
	Description   string         `json:"description_for_human"`
	ModelDesc     string         `json:"description_for_model,omitempty"`
	Auth          map[string]any `json:"auth,omitempty"`
	API           map[string]any `json:"api,omitempty"`
	LogoURL       string         `json:"logo_url,omitempty"`
	ContactEmail  string         `json:"contact_email,omitempty"`
	LegalInfoURL  string         `json:"legal_info_url,omitempty"`
	Skills        []string       `json:"skills,omitempty"`
	MCPServers    []string       `json:"mcp_servers,omitempty"`
	Extensions    map[string]any `json:"extensions,omitempty"`
}

// OpenAIPluginAdapter ingests OpenAI portable plugin manifests.
type OpenAIPluginAdapter struct {
	sourceID domain.SourceID
}

// NewOpenAIPluginAdapter creates a new OpenAI plugin adapter.
func NewOpenAIPluginAdapter(sourceID domain.SourceID) *OpenAIPluginAdapter {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:openai-plugins")
	}
	return &OpenAIPluginAdapter{sourceID: sourceID}
}

// Ingest processes a raw OpenAI plugin.json manifest into normalized Listings and VersionRecords.
func (a *OpenAIPluginAdapter) Ingest(ctx context.Context, snapshotID string, rawManifest []byte) (*IngestResult, error) {
	var manifest OpenAIPluginManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI plugin manifest: %w", err)
	}

	name := manifest.NameForModel
	if name == "" {
		name = manifest.NameForHuman
	}
	cleanName := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	listingID := domain.NewListingID(domain.KindPlugin, a.sourceID, cleanName)
	now := time.Now().UTC()
	ver := "1.0.0"

	var components []domain.Component
	var compSummaries []domain.ComponentSummary

	mainComp := domain.Component{
		ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentSkill, cleanName)),
		Kind:               domain.ComponentSkill,
		Name:               cleanName,
		SupportedByLiteSPM: domain.SupportYes,
	}
	components = append(components, mainComp)
	compSummaries = append(compSummaries, domain.ComponentSummary{
		Kind: domain.ComponentSkill,
		Name: cleanName,
	})

	for _, skill := range manifest.Skills {
		comp := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentSkill, skill)),
			Kind:               domain.ComponentSkill,
			Name:               skill,
			SupportedByLiteSPM: domain.SupportYes,
		}
		components = append(components, comp)
		compSummaries = append(compSummaries, domain.ComponentSummary{
			Kind: domain.ComponentSkill,
			Name: skill,
		})
	}

	for _, mcp := range manifest.MCPServers {
		comp := domain.Component{
			ID:                 string(domain.NewComponentID(listingID, ver, domain.ComponentMCPProvider, mcp)),
			Kind:               domain.ComponentMCPProvider,
			Name:               mcp,
			SupportedByLiteSPM: domain.SupportYes,
		}
		components = append(components, comp)
		compSummaries = append(compSummaries, domain.ComponentSummary{
			Kind: domain.ComponentMCPProvider,
			Name: mcp,
		})
	}

	verRecord := &domain.VersionRecord{
		ListingID:        string(listingID),
		Version:          ver,
		SourceSnapshotID: snapshotID,
		Components:       components,
		FetchedAt:        now,
	}

	listing := &domain.Listing{
		SchemaVersion: 1,
		ID:            string(listingID),
		Kind:          domain.KindPlugin,
		Name:          cleanName,
		Title:         manifest.NameForHuman,
		Summary:       manifest.Description,
		Categories:    []string{"plugins", "developer-tools"},
		Keywords:      []string{"openai", "plugin", cleanName},
		PublisherClaim: domain.PublisherClaim{
			Name:    manifest.ContactEmail,
			URL:     manifest.LegalInfoURL,
			Contact: manifest.ContactEmail,
		},
		Source: domain.SourceReference{
			SourceID:   string(a.sourceID),
			UpstreamID: cleanName,
			URL:        manifest.LegalInfoURL,
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

	digest, err := domain.ComputeContentDigest([]*domain.Listing{listing}, []*domain.VersionRecord{verRecord})
	if err != nil {
		return nil, fmt.Errorf("snapshot content digest: %w", err)
	}

	return &IngestResult{
		SourceID:   a.sourceID,
		SnapshotID: snapshotID,
		Listings:   []*domain.Listing{listing},
		Versions:   []*domain.VersionRecord{verRecord},
		ItemCount:  1,
		Digest:     digest,
		IngestedAt: now,
	}, nil
}
