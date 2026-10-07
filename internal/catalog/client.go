package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
//
// Trust notes (see ARCH/36 §4): releases are digest-pinned (SHA-256 pointer →
// manifest → files) but UNSIGNED today — there is no TUF root/timestamp/
// snapshot/signature envelope, so this client can detect tampering and
// rollback/equivocation but cannot authenticate the publisher. Signed
// releases are DESIGNED, not shipped.
type Client struct {
	baseURL          string
	cacheDir         string
	httpClient       *http.Client
	index            *SearchIndex
	currentReleaseID string
	currentSequence  int
	currentDigest    string
	mu               sync.RWMutex
}

// maxCatalogBodyBytes bounds every catalog HTTP body (ARCH/03 §5: 16 MiB per
// metadata response). Oversized bodies are refused, never truncated.
const maxCatalogBodyBytes = 16 << 20

// defaultCatalogTimeout bounds one catalog HTTP exchange when no timeout is
// configured. It matches config.DefaultConfig's Network.Timeout, so a caller
// that never looks at configuration still gets the documented 30s.
const defaultCatalogTimeout = 30 * time.Second

// NewClient creates a new catalog client with the default request timeout.
func NewClient(baseURL, cacheDir string, httpClient *http.Client) *Client {
	return NewClientWithTimeout(baseURL, cacheDir, 0, httpClient)
}

// NewClientWithTimeout creates a new catalog client whose transport is bounded
// by timeout — config.Network.Timeout (LITESPM_NETWORK_TIMEOUT_SEC) at the
// call sites that read configuration. timeout <= 0 selects
// defaultCatalogTimeout, so an unset knob cannot disable the bound. The
// timeout applies to the client LiteSPM builds; a caller-supplied httpClient
// owns its own timeout but still gets the redirect policy below when it has
// none of its own.
func NewClientWithTimeout(baseURL, cacheDir string, timeout time.Duration, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = secureCatalogHTTPClient(timeout)
	} else if httpClient.CheckRedirect == nil {
		httpClient.CheckRedirect = catalogCheckRedirect
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

// secureCatalogHTTPClient builds the default catalog transport: a bounded
// timeout, at most 3 redirects, no https→http downgrade (ARCH/03 §5).
func secureCatalogHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultCatalogTimeout
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: catalogCheckRedirect,
	}
}

// catalogCheckRedirect caps the chain at 3 hops and refuses a downgrade from
// https to http. Identical-origin is not required (CDN edges move), but a
// downgrade is never a CDN move — it is a strip.
func catalogCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return fmt.Errorf("catalog redirect chain exceeds 3 hops")
	}
	if len(via) > 0 {
		prev := via[len(via)-1].URL.Scheme
		if prev == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing catalog redirect downgrade from https to %s", req.URL.Scheme)
		}
	}
	return nil
}

// checkCatalogURL enforces https-only for non-loopback hosts. Loopback http
// (127.0.0.0/8, ::1, localhost) stays allowed so hermetic httptest servers
// keep working; everything else must be https.
func checkCatalogURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return domain.ErrEgressBlocked(raw, fmt.Sprintf("unparseable catalog URL: %v", err))
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isCatalogLoopbackHost(u.Hostname()) {
		return nil
	}
	return domain.ErrEgressBlocked(raw, fmt.Sprintf("catalog fetches require https (got %q)", u.Scheme))
}

// isCatalogLoopbackHost reports loopback/test hosts where plain http is
// acceptable (httptest binds 127.0.0.1; never a production origin).
func isCatalogLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "::1" {
		return true
	}
	if strings.HasPrefix(h, "127.") {
		return true
	}
	if strings.HasPrefix(h, "[::1") {
		return true
	}
	return false
}

// readCatalogBody reads at most maxCatalogBodyBytes+1 and refuses oversized
// payloads instead of truncating them into corrupt JSON.
func readCatalogBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxCatalogBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxCatalogBodyBytes {
		return nil, fmt.Errorf("catalog response exceeds the %d byte limit", maxCatalogBodyBytes)
	}
	return data, nil
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

	c.index.Replace(listings, versions)
	c.currentReleaseID = current.ReleaseID
	c.currentSequence = current.Sequence
	c.currentDigest = current.ManifestDigest

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
	fetchURL := fmt.Sprintf("%s/v1/current.json", c.baseURL)
	if err := checkCatalogURL(fetchURL); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch /v1/current.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("server returned status %d for %s", resp.StatusCode, fetchURL)
	}

	body, err := readCatalogBody(resp.Body)
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
	fetchURL := fmt.Sprintf("%s/v1/releases/%s/manifest.json", c.baseURL, releaseID)
	if err := checkCatalogURL(fetchURL); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
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

	body, err := readCatalogBody(resp.Body)
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
	fetchURL := fmt.Sprintf("%s/v1/releases/%s/listings.json", c.baseURL, releaseID)
	if err := checkCatalogURL(fetchURL); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
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

	body, err := readCatalogBody(resp.Body)
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
	fetchURL := fmt.Sprintf("%s/v1/releases/%s/versions.json", c.baseURL, releaseID)
	if err := checkCatalogURL(fetchURL); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
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
	body, err := readCatalogBody(resp.Body)
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
	localRelease := c.currentReleaseID
	localDigest := c.currentDigest
	c.mu.RUnlock()

	// C1: sequence monotonicity. A remote pointer that moves backwards is a
	// rollback (freeze/downgrade); a remote pointer that reuses our sequence
	// number for different bytes is an equivocation. Both fail closed and
	// leave the cache untouched. Only an identical pointer is a no-op.
	if localSeq > 0 {
		switch {
		case current.Sequence < localSeq:
			return nil, domain.ErrCatalogRollback(localSeq, current.Sequence, localRelease, current.ReleaseID)
		case current.Sequence == localSeq:
			if current.ReleaseID == localRelease && current.ManifestDigest == localDigest {
				return &SyncResult{
					ReleaseID: current.ReleaseID,
					Sequence:  current.Sequence,
					ItemCount: c.index.Count(),
					Updated:   false,
				}, nil
			}
			return nil, domain.ErrCatalogEquivocation(current.Sequence, localRelease, current.ReleaseID, localDigest, current.ManifestDigest)
		}
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
		// same manifest the pointer anchored. C4: each file lands via
		// temp+fsync+rename+fsync(parent), so a crash can never leave a
		// half-written manifest in place. The pointer goes last: it is the
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
			if err := writeAtomicFile(w.path, w.data, 0644); err != nil {
				return nil, fmt.Errorf("persist catalog cache (%s): %w", w.path, err)
			}
		}
	}

	// C2: update the in-memory index by atomic swap. The release was fully
	// verified above, so build nothing incrementally: a fresh index replaces
	// the old one, and listings withdrawn upstream disappear locally instead
	// of lingering via merge-retain.
	c.mu.Lock()
	c.index.Replace(listings, versions)
	c.currentReleaseID = current.ReleaseID
	c.currentSequence = current.Sequence
	c.currentDigest = current.ManifestDigest
	c.mu.Unlock()

	return &SyncResult{
		ReleaseID: current.ReleaseID,
		Sequence:  current.Sequence,
		ItemCount: len(listings),
		Updated:   true,
	}, nil
}

// writeAtomicFile persists data via temp+fsync+rename+fsync(parent): the
// temp file is created in the destination directory (same filesystem),
// fsynced, renamed over the target, and the parent directory is fsynced so
// the rename itself is durable. A crash can leave the temp behind but never a
// half-written target.
func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on failure; a surviving temp is harmless.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return fsyncParentDir(dir)
}

// fsyncParentDir fsyncs a directory so a just-completed rename is durable.
func fsyncParentDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
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
