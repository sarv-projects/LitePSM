package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// MCPRegistryServerSchema represents the upstream server.json schema from the MCP Registry.
type MCPRegistryServerSchema struct {
	Name        string                `json:"name"`
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description"`
	Repository  string                `json:"repository,omitempty"`
	Homepage    string                `json:"homepage,omitempty"`
	Categories  []string              `json:"categories,omitempty"`
	Keywords    []string              `json:"keywords,omitempty"`
	Publisher   *MCPRegistryPublisher `json:"publisher,omitempty"`
	Packages    []MCPRegistryPackage  `json:"packages,omitempty"`
	Remotes     []MCPRegistryRemote   `json:"remotes,omitempty"`
	Status      string                `json:"status,omitempty"`
}

type MCPRegistryPublisher struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

type MCPRegistryPackage struct {
	RegistryType string            `json:"registryType"` // npm | pypi | cargo | oci | mcpb
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Digest       string            `json:"digest,omitempty"`    // sha256:<hex>
	Runtime      string            `json:"runtime,omitempty"`   // node | python | docker
	Transport    string            `json:"transport,omitempty"` // stdio | http | sse
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

type MCPRegistryRemote struct {
	URL       string `json:"url"`
	Transport string `json:"transport"`          // http | sse
	AuthType  string `json:"authType,omitempty"` // oauth2 | api_key | none
}

// MCPRegistryAdapter normalizes the Official MCP Registry feed into LitePSM domain entities.
type MCPRegistryAdapter struct {
	sourceID domain.SourceID
	rawFeed  []byte
}

// NewMCPRegistryAdapter creates an MCP registry adapter with static or downloaded feed bytes.
func NewMCPRegistryAdapter(sourceID domain.SourceID, rawFeed []byte) (*MCPRegistryAdapter, error) {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:mcp-registry")
	}
	return &MCPRegistryAdapter{
		sourceID: sourceID,
		rawFeed:  rawFeed,
	}, nil
}

// SourceID returns the registered SourceID.
func (a *MCPRegistryAdapter) SourceID() domain.SourceID {
	return a.sourceID
}

// Ingest parses the feed into normalized Listings and VersionRecords.
func (a *MCPRegistryAdapter) Ingest(ctx context.Context, snapshotID string) (*IngestResult, error) {
	if len(a.rawFeed) == 0 {
		return nil, fmt.Errorf("raw feed is empty")
	}

	var rawServers []MCPRegistryServerSchema
	// Try parsing as array of servers first
	if err := json.Unmarshal(a.rawFeed, &rawServers); err != nil {
		// Try parsing as a single server object
		var singleServer MCPRegistryServerSchema
		if errSingle := json.Unmarshal(a.rawFeed, &singleServer); errSingle != nil {
			return nil, fmt.Errorf("failed to parse MCP registry feed as array or object: %w", err)
		}
		rawServers = []MCPRegistryServerSchema{singleServer}
	}

	now := time.Now().UTC()
	var listings []*domain.Listing
	var versions []*domain.VersionRecord

	hasher := sha256.New()

	for _, srv := range rawServers {
		if srv.Name == "" {
			continue
		}

		listingID := domain.NewListingID(domain.KindMCP, a.sourceID, srv.Name)

		pubClaim := domain.PublisherClaim{
			Name: "Unknown",
		}
		if srv.Publisher != nil && srv.Publisher.Name != "" {
			pubClaim.Name = srv.Publisher.Name
			pubClaim.URL = srv.Publisher.URL
			pubClaim.Contact = srv.Publisher.Email
		}

		categories := srv.Categories
		if len(categories) == 0 {
			categories = []string{"utilities"}
		}

		var versionSummaries []domain.VersionSummary
		var compSummaries []domain.ComponentSummary
		var reqSummaries []string

		// Default component summary
		compSummaries = append(compSummaries, domain.ComponentSummary{
			Kind: domain.ComponentMCPProvider,
			Name: "server",
		})

		// Process packages
		for _, pkg := range srv.Packages {
			verStr := pkg.Version
			if verStr == "" {
				verStr = "1.0.0"
			}

			versionSummaries = append(versionSummaries, domain.VersionSummary{
				Version: verStr,
			})

			// Map artifact type
			artType := domain.ArtifactNPM
			switch pkg.RegistryType {
			case "pypi":
				artType = domain.ArtifactPyPI
			case "cargo":
				artType = domain.ArtifactCargo
			case "oci":
				artType = domain.ArtifactOCI
			case "mcpb":
				artType = domain.ArtifactMCPB
			}

			artID := fmt.Sprintf("art_%s_%s", pkg.Name, verStr)
			locator := pkg.Name
			if pkg.RegistryType == "npm" {
				locator = fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz", pkg.Name, pkg.Name, verStr)
			}

			artifact := domain.ArtifactRef{
				ArtifactID:  artID,
				Type:        artType,
				Locator:     locator,
				Digest:      pkg.Digest,
				FetchPolicy: domain.FetchImmutable,
			}

			component := domain.Component{
				ID:   string(domain.NewComponentID(listingID, verStr, domain.ComponentMCPProvider, "server")),
				Kind: domain.ComponentMCPProvider,
				Name: "server",
				Runtime: &domain.RuntimeDescriptor{
					Type:    pkg.Transport,
					Command: pkg.Command,
					Args:    pkg.Args,
					Env:     pkg.Env,
				},
				SupportedByLitePSM: domain.SupportYes,
			}

			var reqs []domain.Requirement
			if pkg.Runtime != "" {
				reqs = append(reqs, domain.Requirement{
					Type: "runtime",
					Name: pkg.Runtime,
				})
				reqSummaries = append(reqSummaries, pkg.Runtime)
			}

			verRecord := &domain.VersionRecord{
				ListingID:        string(listingID),
				Version:          verStr,
				SourceSnapshotID: snapshotID,
				Artifacts:        []domain.ArtifactRef{artifact},
				Components:       []domain.Component{component},
				Requirements:     reqs,
				FetchedAt:        now,
			}
			versions = append(versions, verRecord)
		}

		// Process remotes if no packages or in addition to packages
		for _, rem := range srv.Remotes {
			verStr := "remote"
			versionSummaries = append(versionSummaries, domain.VersionSummary{
				Version: verStr,
			})

			component := domain.Component{
				ID:   string(domain.NewComponentID(listingID, verStr, domain.ComponentMCPProvider, "remote-server")),
				Kind: domain.ComponentMCPProvider,
				Name: "remote-server",
				Runtime: &domain.RuntimeDescriptor{
					Type:     rem.Transport,
					Endpoint: rem.URL,
				},
				SupportedByLitePSM: domain.SupportYes,
			}

			verRecord := &domain.VersionRecord{
				ListingID:        string(listingID),
				Version:          verStr,
				SourceSnapshotID: snapshotID,
				Components:       []domain.Component{component},
				FetchedAt:        now,
			}
			versions = append(versions, verRecord)
		}

		status := domain.ListingStatusActive
		if srv.Status == "deprecated" {
			status = domain.ListingStatusDeprecated
		}

		listing := &domain.Listing{
			SchemaVersion:  1,
			ID:             string(listingID),
			Kind:           domain.KindMCP,
			Name:           srv.Name,
			Title:          srv.Title,
			Summary:        srv.Description,
			Categories:     categories,
			Keywords:       srv.Keywords,
			PublisherClaim: pubClaim,
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: srv.Name,
				URL:        srv.Repository,
			},
			Versions:             versionSummaries,
			ComponentsSummary:    compSummaries,
			RequirementsSummary:  reqSummaries,
			CompatibilitySummary: []domain.CompatibilityFact{},
			VerificationSummary: domain.VerificationSummary{
				Level: "unverified",
			},
			Provenance: domain.ProvenanceRecord{
				SourceSnapshotID: snapshotID,
				IngestedAt:       now,
			},
			Status: status,
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
