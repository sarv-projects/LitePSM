package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sarv-projects/litepsm/internal/catalogbuild"
	"github.com/sarv-projects/litepsm/internal/domain"
)

func sampleListings() []*domain.Listing {
	return []*domain.Listing{
		{
			SchemaVersion: 1,
			ID:            "mcp:builtin:mcp-registry:postgres",
			Kind:          domain.KindMCP,
			Name:          "postgres",
			Title:         "PostgreSQL MCP Server",
			Summary:       "Inspect and query PostgreSQL relational databases",
			Categories:    []string{"database", "sql"},
			Keywords:      []string{"postgres", "rdbms"},
			Status:        domain.ListingStatusActive,
			VerificationSummary: domain.VerificationSummary{
				Level: "security_audited",
			},
		},
		{
			SchemaVersion: 1,
			ID:            "mcp:builtin:mcp-registry:sqlite",
			Kind:          domain.KindMCP,
			Name:          "sqlite",
			Title:         "SQLite MCP Server",
			Summary:       "Local embedded database query executor",
			Categories:    []string{"database"},
			Keywords:      []string{"sqlite", "embedded"},
			Status:        domain.ListingStatusActive,
		},
		{
			SchemaVersion: 1,
			ID:            "mcp:builtin:mcp-registry:github",
			Kind:          domain.KindMCP,
			Name:          "github",
			Title:         "GitHub MCP Server",
			Summary:       "Manage pull requests, issues, and repositories on GitHub",
			Categories:    []string{"developer-tools"},
			Keywords:      []string{"git", "vcs"},
			Status:        domain.ListingStatusActive,
		},
		{
			SchemaVersion: 1,
			ID:            "skill:builtin:agent-skills:git-commit",
			Kind:          domain.KindSkill,
			Name:          "git-commit",
			Title:         "Semantic Git Commit Workflow",
			Summary:       "Automated git commit conventions and branch inspection",
			Categories:    []string{"version-control", "git"},
			Keywords:      []string{"git", "commit"},
			Status:        domain.ListingStatusActive,
		},
	}
}

func TestSearchRankingAndFiltering(t *testing.T) {
	idx := NewSearchIndex()
	idx.IndexListings(sampleListings())

	if idx.Count() != 4 {
		t.Fatalf("expected 4 listings indexed, got %d", idx.Count())
	}

	// 1. Search "postgres"
	res := idx.Search("postgres", SearchOptions{})
	if len(res) == 0 || res[0].Listing.Name != "postgres" {
		t.Fatalf("expected 'postgres' top result, got %+v", res)
	}

	// 2. Search "database" -> should return both postgres and sqlite
	resDB := idx.Search("database", SearchOptions{})
	if len(resDB) < 2 {
		t.Fatalf("expected at least 2 database results, got %d", len(resDB))
	}

	// 3. Search "git" filtered by Kind = skill
	resSkill := idx.Search("git", SearchOptions{Kind: domain.KindSkill})
	if len(resSkill) != 1 || resSkill[0].Listing.Kind != domain.KindSkill {
		t.Fatalf("expected 1 skill result, got %d", len(resSkill))
	}
	if resSkill[0].Listing.Name != "git-commit" {
		t.Fatalf("expected 'git-commit', got %s", resSkill[0].Listing.Name)
	}

	// 4. Empty query returns all listings
	resAll := idx.Search("", SearchOptions{})
	if len(resAll) != 4 {
		t.Fatalf("expected 4 results for empty query, got %d", len(resAll))
	}
}

func TestCatalogClientSyncAndIntegrity(t *testing.T) {
	listings := sampleListings()
	versions := []*domain.VersionRecord{
		{ListingID: listings[0].ID, Version: "1.0.0"},
		{ListingID: listings[1].ID, Version: "1.0.0"},
		{ListingID: listings[2].ID, Version: "1.0.0"},
		{ListingID: listings[3].ID, Version: "1.0.0"},
	}

	releaseID := "rel_01J9X8K2M4N5P6Q7R8S9T0U1V2"
	now := time.Now().UTC()

	// Compile release files
	compiled, err := catalogbuild.CompileRelease(releaseID, 1, nil, listings, versions, now)
	if err != nil {
		t.Fatalf("CompileRelease failed: %v", err)
	}

	// Set up mock HTTP server
	tamperListings := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relPath := r.URL.Path
		if len(relPath) > 0 && relPath[0] == '/' {
			relPath = relPath[1:]
		}

		data, exists := compiled.Files[relPath]
		if !exists {
			http.NotFound(w, r)
			return
		}

		if tamperListings && strings.HasSuffix(relPath, "listings.json") {
			// Tamper with content to simulate corrupt/malicious payload
			data = []byte(string(data) + " ")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	tmpCacheDir, err := os.MkdirTemp("", "litepsm-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp cache dir: %v", err)
	}
	defer os.RemoveAll(tmpCacheDir)

	client := NewClient(server.URL, tmpCacheDir, server.Client())

	// 1. Initial Sync
	ctx := context.Background()
	syncRes, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if !syncRes.Updated || syncRes.ItemCount != 4 {
		t.Fatalf("unexpected sync result: %+v", syncRes)
	}

	// 2. Second Sync with same sequence should be a no-op
	syncRes2, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("second Sync failed: %v", err)
	}
	if syncRes2.Updated {
		t.Fatal("expected second sync with same sequence to report updated=false")
	}

	// 3. Verify search works from synced index
	searchRes := client.Search("postgres", SearchOptions{})
	if len(searchRes) == 0 {
		t.Fatal("expected search to find postgres after sync")
	}

	// 4. Test Checksum Tampering
	tamperListings = true
	// Create client with higher sequence to trigger new sync
	tamperCompiled, _ := catalogbuild.CompileRelease("rel_tampered_002", 2, nil, listings, versions, now)
	compiled = tamperCompiled // update server files to tampered release

	_, err = client.Sync(ctx)
	if err == nil {
		t.Fatal("expected sync to fail with checksum mismatch on tampered listings.json, but succeeded")
	}
	if lpsmErr, ok := err.(*domain.LPSMError); ok {
		if lpsmErr.Code != "LPSM-VERIFY-CHECKSUM-MISMATCH" {
			t.Fatalf("expected LPSM-VERIFY-CHECKSUM-MISMATCH, got %s", lpsmErr.Code)
		}
	}

	// 5. Test Offline Cache Loading
	tamperListings = false
	offlineClient := NewClient("http://unreachable-domain-12345.com", tmpCacheDir, nil)
	offlineResults := offlineClient.Search("postgres", SearchOptions{})
	if len(offlineResults) == 0 {
		t.Fatal("expected offline client to load cached catalog from disk")
	}
}
