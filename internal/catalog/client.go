package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
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
//
// The cache is re-verified on every load: the pointer must name a safe release
// and carry a digest that matches the cached manifest bytes, and the cached
// listings must match the manifest's entry. A corrupted or tampered cache
// therefore loads as an empty index (caller receives the error) instead of as
// poisoned search results.
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
	if err := validateCurrent(&current); err != nil {
		return err
	}

	// The on-disk cache mirrors the remote tree exactly, so a cached release is
	// byte-identical to the served release and there is only one path convention
	// to reason about. Release files live under /v1/ because /v1/current.json
	// names them (see internal/catalogbuild/compiler.go and ARCH/18 §2).
	releaseDir := filepath.Join(c.cacheDir, "v1", "releases", current.ReleaseID)

	manifestData, err := os.ReadFile(filepath.Join(releaseDir, "manifest.json"))
	if err != nil {
		return err
	}
	if actual := domain.ComputeBytesDigest(manifestData); actual != current.ManifestDigest {
		return domain.ErrChecksumMismatch(current.ManifestDigest, actual)
	}
	var manifest catalogbuild.ReleaseManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	listingMeta, ok := manifest.Files["listings.json"]
	if !ok {
		return fmt.Errorf("cached manifest for %s has no listings.json entry", current.ReleaseID)
	}

	listingsData, err := os.ReadFile(filepath.Join(releaseDir, "listings.json"))
	if err != nil {
		return err
	}
	if actual := domain.ComputeBytesDigest(listingsData); actual != listingMeta.Digest {
		return domain.ErrChecksumMismatch(listingMeta.Digest, actual)
	}

	var listings []*domain.Listing
	if err := json.Unmarshal(listingsData, &listings); err != nil {
		return err
	}

	var versions []*domain.VersionRecord
	if versionMeta, ok := manifest.Files["versions.json"]; ok {
		versionsData, err := os.ReadFile(filepath.Join(releaseDir, "versions.json"))
		if err != nil {
			return err
		}
		if actual := domain.ComputeBytesDigest(versionsData); actual != versionMeta.Digest {
			return domain.ErrChecksumMismatch(versionMeta.Digest, actual)
		}
		if err := json.Unmarshal(versionsData, &versions); err != nil {
			return err
		}
	}

	c.index.IndexListings(listings)
	c.index.IndexVersions(versions)
	c.currentReleaseID = current.ReleaseID
	c.currentSequence = current.Sequence

	return nil
}

// validateCurrent enforces the pointer contract before its contents are used:
// a supported schema version, a path-safe release id (the id becomes a URL
// segment and a cache directory), and a manifest digest to anchor the rest of
// the release. It runs for both fetched and cached pointers.
func validateCurrent(current *catalogbuild.CurrentPointer) error {
	if current.SchemaVersion != catalogbuild.CurrentPointerSchemaVersion {
		return fmt.Errorf("current.json schemaVersion %d is not supported (want %d)",
			current.SchemaVersion, catalogbuild.CurrentPointerSchemaVersion)
	}
	if err := domain.ValidateReleaseID(current.ReleaseID); err != nil {
		return fmt.Errorf("current.json names an unsafe release id: %w", err)
	}
	if !domain.RegexDigest.MatchString(current.ManifestDigest) {
		return fmt.Errorf("current.json manifestDigest %q is not a sha256 digest", current.ManifestDigest)
	}
	return nil
}

// FetchCurrent retrieves the /v1/current.json release pointer from the remote registry or local cache.
func (c *Client) FetchCurrent(ctx context.Context) (*catalogbuild.CurrentPointer, error) {
	current, _, err := c.fetchCurrent(ctx)
	return current, err
}

// fetchCurrent fetches the pointer and retains the served bytes.
func (c *Client) fetchCurrent(ctx context.Context) (*catalogbuild.CurrentPointer, []byte, error) {
	url := fmt.Sprintf("%s/v1/current.json", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch /v1/current.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("server returned status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var current catalogbuild.CurrentPointer
	if err := json.Unmarshal(body, &current); err != nil {
		return nil, nil, fmt.Errorf("failed to parse current.json: %w", err)
	}
	if err := validateCurrent(&current); err != nil {
		return nil, nil, err
	}

	return &current, body, nil
}

// FetchManifest retrieves and verifies the root manifest for a specific release.
func (c *Client) FetchManifest(ctx context.Context, releaseID string) (*catalogbuild.ReleaseManifest, error) {
	manifest, _, err := c.fetchManifest(ctx, releaseID)
	return manifest, err
}

// fetchManifest fetches the manifest and retains the served bytes; the
// pointer's digest is computed over exactly these bytes.
func (c *Client) fetchManifest(ctx context.Context, releaseID string) (*catalogbuild.ReleaseManifest, []byte, error) {
	if err := domain.ValidateReleaseID(releaseID); err != nil {
		return nil, nil, fmt.Errorf("refusing to fetch manifest for unsafe release id: %w", err)
	}
	url := fmt.Sprintf("%s/v1/releases/%s/manifest.json", c.baseURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to fetch manifest: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var manifest catalogbuild.ReleaseManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal manifest.json: %w", err)
	}

	return &manifest, body, nil
}

// FetchListings downloads listings.json and validates its SHA-256 against the manifest.
func (c *Client) FetchListings(ctx context.Context, releaseID string, manifest *catalogbuild.ReleaseManifest) ([]*domain.Listing, error) {
	listings, _, err := c.fetchListings(ctx, releaseID, manifest)
	return listings, err
}

// fetchListings downloads listings.json, verifies it against the manifest, and
// retains the served bytes for cache persistence.
func (c *Client) fetchListings(ctx context.Context, releaseID string, manifest *catalogbuild.ReleaseManifest) ([]*domain.Listing, []byte, error) {
	fileMeta, exists := manifest.Files["listings.json"]
	if !exists {
		return nil, nil, fmt.Errorf("manifest missing listings.json entry")
	}

	if err := domain.ValidateReleaseID(releaseID); err != nil {
		return nil, nil, fmt.Errorf("refusing to fetch listings for unsafe release id: %w", err)
	}
	url := fmt.Sprintf("%s/v1/releases/%s/listings.json", c.baseURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to download listings.json: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	// Verify digest
	actualDigest := domain.ComputeBytesDigest(body)
	if actualDigest != fileMeta.Digest {
		return nil, nil, domain.ErrChecksumMismatch(fileMeta.Digest, actualDigest)
	}

	var listings []*domain.Listing
	if err := json.Unmarshal(body, &listings); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal listings.json: %w", err)
	}

	return listings, body, nil
}

// FetchVersions downloads versions.json, verifies it against the manifest, and
// returns the parsed records.
//
// versions.json is where the release publishes each listing's components, and a
// component's runtime descriptor is the real launch line (`command`, `args`,
// `type`). Listings carry no launch line, so an installer that wants to actually
// start a server needs this file — which is why Sync caches it too.
func (c *Client) FetchVersions(ctx context.Context, releaseID string, manifest *catalogbuild.ReleaseManifest) ([]*domain.VersionRecord, error) {
	records, _, err := c.fetchVersions(ctx, releaseID, manifest)
	return records, err
}

func (c *Client) fetchVersions(ctx context.Context, releaseID string, manifest *catalogbuild.ReleaseManifest) ([]*domain.VersionRecord, []byte, error) {
	fileMeta, exists := manifest.Files["versions.json"]
	if !exists {
		return nil, nil, fmt.Errorf("manifest missing versions.json entry")
	}
	if err := domain.ValidateReleaseID(releaseID); err != nil {
		return nil, nil, fmt.Errorf("refusing to fetch versions for unsafe release id: %w", err)
	}
	url := fmt.Sprintf("%s/v1/releases/%s/versions.json", c.baseURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to download versions.json: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if actualDigest := domain.ComputeBytesDigest(body); actualDigest != fileMeta.Digest {
		return nil, nil, domain.ErrChecksumMismatch(fileMeta.Digest, actualDigest)
	}
	var versions []*domain.VersionRecord
	if err := json.Unmarshal(body, &versions); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal versions.json: %w", err)
	}
	return versions, body, nil
}

// VersionRecordFor returns the published version record for a listing, if the
// catalog has been synced. Installers use it to read the runtime descriptor; a
// listing alone cannot start anything.
func (c *Client) VersionRecordFor(listingID, version string) (*domain.VersionRecord, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	records := c.index.VersionRecords(listingID)
	if len(records) == 0 {
		return nil, fmt.Errorf("no published version record for %s; run 'litespm catalog sync' first", listingID)
	}
	if version == "" || version == "latest" {
		return records[0], nil
	}
	for _, rec := range records {
		if rec.Version == version {
			return rec, nil
		}
	}
	return nil, fmt.Errorf("listing %s has no published version %q", listingID, version)
}

// RuntimeForListing returns the stdio launch line for a listing, taken from the
// published version record. It fails closed when the record declares no runtime
// or no command, rather than letting an installer write a broken entry.
func (c *Client) RuntimeForListing(listingID, version string) (*domain.RuntimeDescriptor, error) {
	rec, err := c.VersionRecordFor(listingID, version)
	if err != nil {
		return nil, err
	}
	for _, component := range rec.Components {
		if component.Runtime == nil {
			continue
		}
		if strings.TrimSpace(component.Runtime.Command) == "" {
			continue
		}
		return component.Runtime, nil
	}
	return nil, fmt.Errorf("listing %s publishes no runnable command for version %s", listingID, rec.Version)
}

// Sync checks for remote catalog updates, verifies integrity, and updates the local cache and index.
func (c *Client) Sync(ctx context.Context) (*SyncResult, error) {
	current, currentRaw, err := c.fetchCurrent(ctx)
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

	// Download manifest; the served bytes are retained because the pointer's
	// digest is computed over exactly those bytes (a re-marshal would change
	// key order and formatting and fail the comparison).
	manifest, manifestRaw, err := c.fetchManifest(ctx, current.ReleaseID)
	if err != nil {
		return nil, err
	}

	// The pointer is the trust anchor for the whole release: without this
	// check a swapped manifest would be accepted as long as its own internal
	// digests were self-consistent.
	if actual := domain.ComputeBytesDigest(manifestRaw); actual != current.ManifestDigest {
		return nil, domain.ErrChecksumMismatch(current.ManifestDigest, actual)
	}

	// Download listings
	listings, listingsRaw, err := c.fetchListings(ctx, current.ReleaseID, manifest)
	if err != nil {
		return nil, err
	}

	// Download versions: the launch line lives here, not in the listings.
	versions, versionsRaw, err := c.fetchVersions(ctx, current.ReleaseID, manifest)
	if err != nil {
		return nil, err
	}

	// Persist to local disk cache if cacheDir is set
	if c.cacheDir != "" {
		releaseDir := filepath.Join(c.cacheDir, "v1", "releases", current.ReleaseID)
		if err := os.MkdirAll(releaseDir, 0755); err != nil {
			return nil, fmt.Errorf("create catalog cache: %w", err)
		}

		// Served bytes are persisted verbatim so the cache mirrors the origin
		// byte for byte and LoadFromCache's digest checks verify against the
		// same manifest the pointer anchored. The pointer goes last: it is the
		// commit marker LoadFromCache reads first, so an interrupted sync
		// leaves the previous cache as the consistent state.
		for _, w := range []struct {
			path string
			data []byte
		}{
			{filepath.Join(releaseDir, "manifest.json"), manifestRaw},
			{filepath.Join(releaseDir, "listings.json"), listingsRaw},
			{filepath.Join(releaseDir, "versions.json"), versionsRaw},
			{filepath.Join(c.cacheDir, "v1", "current.json"), currentRaw},
		} {
			if err := os.WriteFile(w.path, w.data, 0644); err != nil {
				return nil, fmt.Errorf("persist catalog cache (%s): %w", w.path, err)
			}
		}
	}

	// Update in-memory index
	c.mu.Lock()
	c.index.IndexListings(listings)
	c.index.IndexVersions(versions)
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

// Count returns the total number of indexed listings.
func (c *Client) Count() int {
	return c.index.Count()
}
