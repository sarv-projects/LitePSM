package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/sarv-projects/litepsm/internal/agent"
	"github.com/sarv-projects/litepsm/internal/domain"
)

// ACPAgentAdapter ingests the Agent Client Protocol registry into normalized
// agent Listings. It performs no downloads or execution; it only normalizes
// discovery metadata and distribution locators.
type ACPAgentAdapter struct {
	sourceID domain.SourceID
	rawFeed  []byte
}

// NewACPAgentAdapter creates an ACP registry adapter with the given index bytes.
func NewACPAgentAdapter(sourceID domain.SourceID, rawFeed []byte) (*ACPAgentAdapter, error) {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:acp-registry")
	}
	return &ACPAgentAdapter{sourceID: sourceID, rawFeed: rawFeed}, nil
}

// SourceID returns the registered SourceID.
func (a *ACPAgentAdapter) SourceID() domain.SourceID { return a.sourceID }

// Ingest parses the ACP registry index into agent Listings and VersionRecords.
func (a *ACPAgentAdapter) Ingest(ctx context.Context, snapshotID string) (*IngestResult, error) {
	reg, err := agent.ParseRegistry(a.rawFeed)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	hasher := sha256.New()
	var listings []*domain.Listing
	var versions []*domain.VersionRecord

	for _, ag := range reg.Agents {
		if ag.ID == "" {
			continue
		}

		version := ag.Version
		if version == "" {
			version = "latest"
		}

		listingID := domain.NewListingID(domain.KindAgent, a.sourceID, ag.ID)

		publisher := "Unknown"
		if len(ag.Authors) > 0 && ag.Authors[0] != "" {
			publisher = ag.Authors[0]
		}

		artifacts := acpArtifacts(ag)
		requirements := acpRequirements(ag)

		components := []domain.Component{{
			ID:                 domain.NewComponentID(listingID, version, domain.ComponentAgent, ag.ID).String(),
			Kind:               domain.ComponentAgent,
			Name:               ag.Name,
			DeclaredEffects:    []domain.EffectDeclaration{},
			SupportedByLitePSM: domain.SupportYes,
		}}

		versionRecord := &domain.VersionRecord{
			ListingID:           listingID.String(),
			Version:             version,
			SourceSnapshotID:    snapshotID,
			Artifacts:           artifacts,
			Components:          components,
			Dependencies:        []domain.DependencyConstraint{},
			Requirements:        requirements,
			PermissionsDeclared: []domain.PermissionDeclaration{},
			CompatibilityClaims: []domain.CompatibilityFact{},
			TestEvidence:        []domain.TestEvidenceRecord{},
			FetchedAt:           now,
			RawMetadataRef:      ag.Repository,
		}

		status := domain.ListingStatusActive
		if agent.IsDeprecated(ag.ID) {
			status = domain.ListingStatusDeprecated
		}

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            listingID.String(),
			Kind:          domain.KindAgent,
			Name:          ag.Name,
			Title:         ag.Name,
			Summary:       ag.Description,
			Categories:    []string{"agents"},
			Keywords:      []string{"agent", "acp"},
			PublisherClaim: domain.PublisherClaim{
				Name: publisher,
				URL:  ag.Repository,
			},
			Source: domain.SourceReference{
				SourceID:   a.sourceID.String(),
				UpstreamID: ag.ID,
				URL:        ag.Repository,
			},
			Versions: []domain.VersionSummary{{
				Version:     version,
				PublishedAt: &now,
			}},
			ComponentsSummary: []domain.ComponentSummary{{
				Kind: domain.ComponentAgent,
				Name: ag.Name,
			}},
			RequirementsSummary:  acpRequirementStrings(requirements),
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary:  domain.VerificationSummary{Level: "unverified"},
			Provenance: domain.ProvenanceRecord{
				SourceSnapshotID: snapshotID,
				IngestedAt:       now,
			},
			Status:         status,
			RawMetadataRef: ag.Repository,
		}

		hasher.Write([]byte(listingID.String()))
		listings = append(listings, listing)
		versions = append(versions, versionRecord)
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

func acpArtifacts(ag agent.Agent) []domain.ArtifactRef {
	var out []domain.ArtifactRef
	d := ag.Distribution

	if d.Npx != nil && d.Npx.Package != "" {
		out = append(out, domain.ArtifactRef{
			ArtifactID:  ag.ID + "_npx",
			Type:        domain.ArtifactNPM,
			Locator:     d.Npx.Package,
			FetchPolicy: domain.FetchImmutable,
			MediaType:   "application/vnd.npm.package",
		})
	}
	if d.Uvx != nil && d.Uvx.Package != "" {
		out = append(out, domain.ArtifactRef{
			ArtifactID:  ag.ID + "_uvx",
			Type:        domain.ArtifactPyPI,
			Locator:     d.Uvx.Package,
			FetchPolicy: domain.FetchImmutable,
		})
	}
	if len(d.Binary) > 0 {
		targets := make([]string, 0, len(d.Binary))
		for t := range d.Binary {
			targets = append(targets, string(t))
		}
		sort.Strings(targets)
		for _, t := range targets {
			bt := d.Binary[agent.Target(t)]
			out = append(out, domain.ArtifactRef{
				ArtifactID:  fmt.Sprintf("%s_binary_%s", ag.ID, t),
				Type:        domain.ArtifactArchive,
				Locator:     bt.Archive,
				Digest:      bt.SHA256,
				Subpath:     t,
				FetchPolicy: domain.FetchImmutable,
			})
		}
	}
	return out
}

func acpRequirements(ag agent.Agent) []domain.Requirement {
	var out []domain.Requirement
	d := ag.Distribution
	if d.Npx != nil {
		out = append(out, domain.Requirement{Type: "runtime", Name: "node"})
	}
	if d.Uvx != nil {
		out = append(out, domain.Requirement{Type: "runtime", Name: "uv"})
	}
	if len(d.Binary) > 0 && d.Npx == nil && d.Uvx == nil {
		out = append(out, domain.Requirement{Type: "runtime", Name: "binary"})
	}
	return out
}

func acpRequirementStrings(reqs []domain.Requirement) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, fmt.Sprintf("%s:%s", r.Type, r.Name))
	}
	return out
}
