package source

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// SkillDocument represents a parsed SKILL.md file with frontmatter and body.
type SkillDocument struct {
	Name        string
	Title       string
	Description string
	Author      string
	Version     string
	Categories  []string
	Keywords    []string
	Triggers    []string
	ToolsUsed   []string
	BodyContent string
}

// AgentSkillsAdapter ingests portable SKILL.md workflows into LitePSM domain listings.
type AgentSkillsAdapter struct {
	sourceID  domain.SourceID
	documents map[string][]byte // filename -> content
}

// NewAgentSkillsAdapter creates a skills adapter.
func NewAgentSkillsAdapter(sourceID domain.SourceID, documents map[string][]byte) (*AgentSkillsAdapter, error) {
	if sourceID == "" {
		sourceID = domain.SourceID("builtin:agent-skills")
	}
	return &AgentSkillsAdapter{
		sourceID:  sourceID,
		documents: documents,
	}, nil
}

// SourceID returns the registered SourceID.
func (a *AgentSkillsAdapter) SourceID() domain.SourceID {
	return a.sourceID
}

// Ingest parses all provided SKILL.md documents.
func (a *AgentSkillsAdapter) Ingest(ctx context.Context, snapshotID string) (*IngestResult, error) {
	now := time.Now().UTC()
	var listings []*domain.Listing
	var versions []*domain.VersionRecord

	hasher := sha256.New()

	for filename, content := range a.documents {
		doc, err := ParseSkillMarkdown(content)
		if err != nil {
			// Skip malformed documents
			continue
		}

		if doc.Name == "" {
			doc.Name = strings.TrimSuffix(filename, ".md")
		}
		if doc.Version == "" {
			doc.Version = "1.0.0"
		}
		if len(doc.Categories) == 0 {
			doc.Categories = []string{"skills", "workflows"}
		}

		listingID := domain.NewListingID(domain.KindSkill, a.sourceID, doc.Name)

		compID := string(domain.NewComponentID(listingID, doc.Version, domain.ComponentSkill, doc.Name))
		component := domain.Component{
			ID:                 compID,
			Kind:               domain.ComponentSkill,
			Name:               doc.Name,
			SupportedByLitePSM: domain.SupportYes,
		}

		artDigest := domain.ComputeBytesDigest(content)
		artifact := domain.ArtifactRef{
			ArtifactID:   fmt.Sprintf("skill_%s_%s", doc.Name, doc.Version),
			Type:         domain.ArtifactGitTree,
			Locator:      filename,
			Digest:       artDigest,
			FetchPolicy:  domain.FetchImmutable,
		}

		verRecord := &domain.VersionRecord{
			ListingID:        string(listingID),
			Version:          doc.Version,
			SourceSnapshotID: snapshotID,
			Artifacts:        []domain.ArtifactRef{artifact},
			Components:       []domain.Component{component},
			FetchedAt:        now,
		}
		versions = append(versions, verRecord)

		listing := &domain.Listing{
			SchemaVersion: 1,
			ID:            string(listingID),
			Kind:          domain.KindSkill,
			Name:          doc.Name,
			Title:         doc.Title,
			Summary:       doc.Description,
			Categories:    doc.Categories,
			Keywords:      doc.Keywords,
			PublisherClaim: domain.PublisherClaim{
				Name: doc.Author,
			},
			Source: domain.SourceReference{
				SourceID:   string(a.sourceID),
				UpstreamID: doc.Name,
			},
			Versions: []domain.VersionSummary{
				{Version: doc.Version},
			},
			ComponentsSummary: []domain.ComponentSummary{
				{Kind: domain.ComponentSkill, Name: doc.Name},
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

// ParseSkillMarkdown extracts YAML frontmatter and body from a SKILL.md document.
func ParseSkillMarkdown(data []byte) (*SkillDocument, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	doc := &SkillDocument{
		Author: "agentskills.io",
	}

	// Must start with ---
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty skill file")
	}
	firstLine := strings.TrimSpace(scanner.Text())
	if firstLine != "---" {
		return nil, fmt.Errorf("skill document must begin with YAML frontmatter delimiter '---'")
	}

	inFrontmatter := true
	var bodyBuilder strings.Builder
	currentArrayKey := ""

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
				continue
			}

			// Handle list items
			if strings.HasPrefix(trimmed, "- ") {
				itemVal := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				switch currentArrayKey {
				case "categories":
					doc.Categories = append(doc.Categories, itemVal)
				case "keywords":
					doc.Keywords = append(doc.Keywords, itemVal)
				case "triggers":
					doc.Triggers = append(doc.Triggers, itemVal)
				case "tools":
					doc.ToolsUsed = append(doc.ToolsUsed, itemVal)
				}
				continue
			}

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")

			currentArrayKey = ""

			switch key {
			case "name":
				doc.Name = val
			case "title":
				doc.Title = val
			case "description":
				doc.Description = val
			case "author":
				doc.Author = val
			case "version":
				doc.Version = val
			case "categories":
				currentArrayKey = "categories"
				if val != "" {
					doc.Categories = append(doc.Categories, val)
				}
			case "keywords":
				currentArrayKey = "keywords"
				if val != "" {
					doc.Keywords = append(doc.Keywords, val)
				}
			case "triggers":
				currentArrayKey = "triggers"
				if val != "" {
					doc.Triggers = append(doc.Triggers, val)
				}
			case "tools":
				currentArrayKey = "tools"
				if val != "" {
					doc.ToolsUsed = append(doc.ToolsUsed, val)
				}
			}
		} else {
			bodyBuilder.WriteString(line)
			bodyBuilder.WriteByte('\n')
		}
	}

	doc.BodyContent = bodyBuilder.String()
	return doc, scanner.Err()
}
