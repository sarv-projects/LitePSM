package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sarv-projects/litepsm/internal/catalogbuild"
	"github.com/sarv-projects/litepsm/internal/domain"
)

// SyncResult details the outcome of a catalog synchronization check.
type SyncResult struct {
	ReleaseID string `json:"releaseId"`
	Sequence  int    `json:"sequence"`
	ItemCount int    `json:"itemCount"`
	Updated   bool   `json:"updated"`
}

// Client manages downloading, verifying, caching, and searching static catalog releases.
type Client struct {
	baseURL          string
	cacheDir         string
	httpClient       *http.Client
	index            *SearchIndex
	currentReleaseID string
	currentSequence  int
	mu               sync.RWMutex
}

// NewClient creates a new catalog client.
func NewClient(baseURL, cacheDir string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	c := &Client{
		baseURL:    baseURL,
		cacheDir:   cacheDir,
		httpClient: httpClient,
		index:      NewSearchIndex(),
	}
	// Try loading cached release on startup
	_ = c.LoadFromCache()
	return c
}

// LoadFromCache loads existing cached catalog files from disk into the in-memory search index.
func (c *Client) LoadFromCache() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentPath := filepath.Join(c.cacheDir, "v1", "current.json")
	data, err := os.ReadFile(currentPath)
	if err != nil {
		return err
	}

	var current catalogbuild.CurrentPointer
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}

	listingsPath := filepath.Join(c.cacheDir, "releases", current.ReleaseID, "listings.json")
	listingsData, err := os.ReadFile(listingsPath)
	if err != nil {
		return err
	}

	var listings []*domain.Listing
	if err := json.Unmarshal(listingsData, &listings); err != nil {
		return err
	}

	c.index.IndexListings(listings)
	c.currentReleaseID = current.ReleaseID
	c.currentSequence = current.Sequence

	return nil
}

// FetchCurrent retrieves the /v1/current.json release pointer from the remote registry or local cache.
func (c *Client) FetchCurrent(ctx context.Context) (*catalogbuild.CurrentPointer, error) {
	url := fmt.Sprintf("%s/v1/current.json", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch /v1/current.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var current catalogbuild.CurrentPointer
	if err := json.Unmarshal(body, &current); err != nil {
		return nil, fmt.Errorf("failed to parse current.json: %w", err)
	}

	return &current, nil
}

// FetchManifest retrieves and verifies the root manifest for a specific release.
func (c *Client) FetchManifest(ctx context.Context, releaseID string) (*catalogbuild.ReleaseManifest, error) {
	url := fmt.Sprintf("%s/releases/%s/manifest.json", c.baseURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch manifest: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var manifest catalogbuild.ReleaseManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest.json: %w", err)
	}

	return &manifest, nil
}

// FetchListings downloads listings.json and validates its SHA-256 against the manifest.
func (c *Client) FetchListings(ctx context.Context, releaseID string, manifest *catalogbuild.ReleaseManifest) ([]*domain.Listing, error) {
	fileMeta, exists := manifest.Files["listings.json"]
	if !exists {
		return nil, fmt.Errorf("manifest missing listings.json entry")
	}

	url := fmt.Sprintf("%s/releases/%s/listings.json", c.baseURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download listings.json: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Verify digest
	actualDigest := domain.ComputeBytesDigest(body)
	if actualDigest != fileMeta.Digest {
		return nil, domain.ErrChecksumMismatch(fileMeta.Digest, actualDigest)
	}

	var listings []*domain.Listing
	if err := json.Unmarshal(body, &listings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal listings.json: %w", err)
	}

	return listings, nil
}

// Sync checks for remote catalog updates, verifies integrity, and updates the local cache and index.
func (c *Client) Sync(ctx context.Context) (*SyncResult, error) {
	current, err := c.FetchCurrent(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	localSeq := c.currentSequence
	c.mu.RUnlock()

	// If local cache is already up-to-date
	if current.Sequence <= localSeq && localSeq > 0 {
		return &SyncResult{
			ReleaseID: current.ReleaseID,
			Sequence:  current.Sequence,
			ItemCount: c.index.Count(),
			Updated:   false,
		}, nil
	}

	// Download manifest
	manifest, err := c.FetchManifest(ctx, current.ReleaseID)
	if err != nil {
		return nil, err
	}

	// Download listings
	listings, err := c.FetchListings(ctx, current.ReleaseID, manifest)
	if err != nil {
		return nil, err
	}

	// Persist to local disk cache if cacheDir is set
	if c.cacheDir != "" {
		releaseDir := filepath.Join(c.cacheDir, "releases", current.ReleaseID)
		_ = os.MkdirAll(releaseDir, 0755)
		_ = os.MkdirAll(filepath.Join(c.cacheDir, "v1"), 0755)

		manifestBytes, _ := json.Marshal(manifest)
		_ = os.WriteFile(filepath.Join(releaseDir, "manifest.json"), manifestBytes, 0644)

		listingsBytes, _ := json.Marshal(listings)
		_ = os.WriteFile(filepath.Join(releaseDir, "listings.json"), listingsBytes, 0644)

		currentBytes, _ := json.Marshal(current)
		_ = os.WriteFile(filepath.Join(c.cacheDir, "v1", "current.json"), currentBytes, 0644)
	}

	// Update in-memory index
	c.mu.Lock()
	c.index.IndexListings(listings)
	c.currentReleaseID = current.ReleaseID
	c.currentSequence = current.Sequence
	c.mu.Unlock()

	return &SyncResult{
		ReleaseID: current.ReleaseID,
		Sequence:  current.Sequence,
		ItemCount: len(listings),
		Updated:   true,
	}, nil
}

// IndexListings indexes listings directly into the client's search index.
func (c *Client) IndexListings(listings []*domain.Listing) {
	c.index.IndexListings(listings)
}

// Search queries the in-memory catalog index.
func (c *Client) Search(query string, opts SearchOptions) []*SearchResult {
	return c.index.Search(query, opts)
}

// GetListing retrieves a listing by ID.
func (c *Client) GetListing(id string) (*domain.Listing, error) {
	l, ok := c.index.Get(id)
	if !ok {
		return nil, domain.ErrNotFound("listing", id)
	}
	return l, nil
}
