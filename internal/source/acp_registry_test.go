package source

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestACPAgentAdapterIngest(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/source/acp/registry.json")
	if err != nil {
		t.Fatalf("failed to read ACP fixture: %v", err)
	}

	adapter, err := NewACPAgentAdapter("", data)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	res, err := adapter.Ingest(context.Background(), "snap_acp_001")
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}

	if res.ItemCount < 40 {
		t.Fatalf("expected the full registry to yield 40+ agents, got %d", res.ItemCount)
	}
	if !strings.HasPrefix(res.Digest, "sha256:") {
		t.Fatalf("expected sha256 digest, got %q", res.Digest)
	}
	if res.SourceID != domain.SourceID("builtin:acp-registry") {
		t.Fatalf("unexpected source id %q", res.SourceID)
	}

	byID := map[string]*domain.Listing{}
	for _, l := range res.Listings {
		byID[l.Source.UpstreamID] = l
		if l.Kind != domain.KindAgent {
			t.Fatalf("expected kind agent, got %q for %s", l.Kind, l.ID)
		}
		if !strings.HasPrefix(l.ID, "agent:builtin:acp-registry:") {
			t.Fatalf("unexpected listing id %q", l.ID)
		}
		if l.VerificationSummary.Level != "unverified" {
			t.Fatalf("expected unverified level, got %q", l.VerificationSummary.Level)
		}
	}

	for _, id := range []string{"claude-acp", "gemini", "goose", "opencode", "cursor"} {
		if byID[id] == nil {
			t.Errorf("expected registry agent %q to be ingested", id)
		}
	}

	// Version records must carry resolvable artifacts for known distribution types.
	foundNpm, foundBinary := false, false
	for _, v := range res.Versions {
		if strings.Contains(v.ListingID, "claude-acp") || strings.Contains(v.ListingID, "gemini") {
			for _, art := range v.Artifacts {
				if art.Type == domain.ArtifactNPM {
					foundNpm = true
				}
			}
		}
		if strings.Contains(v.ListingID, "goose") || strings.Contains(v.ListingID, "opencode") {
			for _, art := range v.Artifacts {
				if art.Type == domain.ArtifactArchive && art.Digest != "" {
					foundBinary = true
				}
			}
		}
	}
	if !foundNpm {
		t.Error("expected npm artifacts for npx-distributed agents")
	}
	if !foundBinary {
		t.Error("expected checksummed archive artifacts for binary-distributed agents")
	}
}

func TestACPAgentAdapterRejectsEmpty(t *testing.T) {
	adapter, _ := NewACPAgentAdapter("", nil)
	if _, err := adapter.Ingest(context.Background(), "snap"); err == nil {
		t.Fatal("expected ingest to fail with an empty feed")
	}
}
