package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sarv-projects/litespm/internal/catalogbuild"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/egress"
	"github.com/sarv-projects/litespm/internal/trust"
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
// manifest → files), so this client detects tampering and rollback/
// equivocation. Publisher authenticity comes from the Sigstore bundle the
// deploy publishes beside /v1/current.json (keyless, identity = this
// repository's release workflow): verified when present, always fatal when
// present-but-invalid, and absent-with-a-stated-fallback when the origin
// predates signing — unless LITESPM_REQUIRE_CATALOG_SIGNATURE=1. A full
// TUF root/timestamp/snapshot envelope remains DESIGNED, not shipped.
type Client struct {
	baseURL          string
	cacheDir         string
	httpClient       *http.Client
	index            *SearchIndex
	currentReleaseID string
	currentSequence  int
	currentDigest    string
	mu               sync.RWMutex

	// bundleVerifier checks the Sigstore bundle the deploy publishes beside
	// /v1/current.json (B3). A nil field selects trust.BlobVerifier with the
	// release workflow's default identity; tests inject verdicts here rather
	// than stubbing a global.
	bundleVerifier *trust.BlobVerifier
}

// maxCatalogBodyBytes bounds catalog metadata HTTP bodies (ARCH/03 §5: 16 MiB
// per metadata response). Oversized bodies are refused, never truncated.
const maxCatalogBodyBytes = 16 << 20

// maxCatalogFileBytes bounds the manifest-sized listings/versions data files.
// A current real catalog can exceed the metadata cap; its manifest-declared
// byte size is still checked against this fixed upper bound before reading.
const maxCatalogFileBytes = 64 << 20

// defaultCatalogTimeout bounds one catalog HTTP exchange when no timeout is
// configured. It matches config.DefaultConfig's Network.Timeout, so a caller
// that never looks at configuration still gets the documented 30s.
const defaultCatalogTimeout = 30 * time.Second

// --- Egress guard -------------------------------------------------------
//
// The SSRF guard itself lives in internal/egress (shared with remote-MCP
// clients; a client package must not become the security library). Catalog
// keeps these thin, named delegation points so its call sites, tests, and
// behaviour are unchanged.

// catalogPolicy is the historical catalog egress posture: https-only with
// the loopback-http exception for hermetic test origins, at most 3 redirect
// hops, host-changing redirects allowed (CDN edges move — this is the one
// policy difference from remote MCP, which holds SameOriginRedirects),
// private destinations refused except the one configured literal host, no
// proxy, checked-IP dial.
func catalogPolicy(allowedPrivateHost string) egress.Policy {
	return egress.Policy{
		AllowLoopbackHTTP:   true,
		MaxRedirects:        3,
		SameOriginRedirects: false,
		AllowPrivate:        false,
		AllowedPrivateHost:  allowedPrivateHost,
		Timeout:             defaultCatalogTimeout,
		MaxBodyBytes:        maxCatalogBodyBytes,
	}
}

// NewClient creates a new catalog client with the default request timeout.
func NewClient(baseURL, cacheDir string, httpClient *http.Client) *Client {
	return NewClientWithTimeout(baseURL, cacheDir, 0, httpClient)
}

// NewClientWithTimeout creates a new catalog client whose transport is bounded
// by timeout — config.Network.Timeout (LITESPM_NETWORK_TIMEOUT_SEC) at the
// call sites that read configuration. timeout <= 0 selects
// defaultCatalogTimeout, so an unset knob cannot disable the bound. The
// timeout applies to the client LiteSPM builds; a caller-supplied httpClient
// keeps its configured timeout while receiving the catalog URL/redirect
// guards and, for net/http transports, the checked-IP dialer.
func NewClientWithTimeout(baseURL, cacheDir string, timeout time.Duration, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = secureCatalogHTTPClient(timeout, baseURL)
	} else {
		// Do not mutate a caller-owned client: the catalog's redirect and
		// destination policy belongs to this client instance.
		clone := *httpClient
		callerRedirect := clone.CheckRedirect
		clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if err := catalogCheckRedirect(req, via); err != nil {
				return err
			}
			if callerRedirect != nil {
				return callerRedirect(req, via)
			}
			return nil
		}
		clone.Transport = guardedCatalogTransport(clone.Transport, configuredPrivateHost(baseURL))
		httpClient = &clone
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
// timeout, at most 3 redirects, no downgrade, and dial-time public-address
// validation for every redirect target. An explicitly configured literal
// private base address remains available for private registries.
func secureCatalogHTTPClient(timeout time.Duration, baseURL string) *http.Client {
	if timeout <= 0 {
		timeout = defaultCatalogTimeout
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     guardedCatalogTransport(nil, configuredPrivateHost(baseURL)),
		CheckRedirect: catalogCheckRedirect,
	}
}

// guardedCatalogTransport delegates the guard to internal/egress: the inner
// transport is configured there (proxy off, DialTLSContext cleared,
// checked-IP dial) and wrapped with the catalog's request checks.
func guardedCatalogTransport(base http.RoundTripper, allowedPrivateHost string) http.RoundTripper {
	guard := egress.NewGuardedTransport(base, catalogPolicy(allowedPrivateHost))
	return catalogURLGuardTransport{base: guard.Base, guard: guard}
}

type catalogURLGuardTransport struct {
	// base is the configured inner transport (proxy disabled, DialTLSContext
	// cleared, checked-IP dial); kept as a field so tests can introspect it.
	base http.RoundTripper
	// guard carries the catalog policy applied to every request.
	guard egress.GuardedTransport
}

func (t catalogURLGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.guard.RoundTrip(req)
}

func configuredPrivateHost(raw string) string {
	return egress.ConfiguredPrivateHost(raw)
}

func catalogDialContext(allowedPrivateHost string) func(context.Context, string, string) (net.Conn, error) {
	return catalogDialContextWithLookup(allowedPrivateHost, net.DefaultResolver.LookupIPAddr)
}

func catalogDialContextWithLookup(allowedPrivateHost string, lookup egress.Lookup) func(context.Context, string, string) (net.Conn, error) {
	return egress.DialContextWithLookup(catalogPolicy(allowedPrivateHost), lookup)
}

// catalogIPIsPublic reports whether ip is a globally routable address. It
// delegates to the shared guard (the extracted rule now lives in
// internal/egress); retained as this package's named entry point.
func catalogIPIsPublic(ip net.IP) bool {
	return egress.IsPublicIP(ip)
}

// catalogCheckRedirect caps the chain at 3 hops and refuses a downgrade from
// https to http. Identical-origin is not required (CDN edges move), but a
// downgrade is never a CDN move — it is a strip.
func catalogCheckRedirect(req *http.Request, via []*http.Request) error {
	return egress.CheckRedirect(catalogPolicy(""), req, via)
}

func catalogCheckRedirectWithLookup(req *http.Request, via []*http.Request, lookup egress.Lookup) error {
	return egress.CheckRedirectWithLookup(catalogPolicy(""), req, via, lookup)
}

// checkCatalogURL enforces https-only for non-loopback hosts. Loopback http
// (127.0.0.0/8, ::1, localhost) stays allowed so hermetic httptest servers
// keep working; everything else must be https.
func checkCatalogURL(raw string) error {
	return egress.CheckURL(raw, catalogPolicy(""))
}

// readCatalogBody refuses oversized metadata bodies instead of truncating them
// into corrupt JSON.
func readCatalogBody(body io.Reader) ([]byte, error) {
	return readCatalogBodyLimit(body, maxCatalogBodyBytes)
}

func readCatalogBodyLimit(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("catalog response exceeds the %d byte limit", limit)
	}
	return data, nil
}

func readCatalogFileBody(body io.Reader, expectedSize int64) ([]byte, error) {
	if expectedSize < 0 || expectedSize > maxCatalogFileBytes {
		return nil, fmt.Errorf("catalog file size %d exceeds the %d byte limit", expectedSize, maxCatalogFileBytes)
	}
	data, err := readCatalogBodyLimit(body, expectedSize)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("catalog file size mismatch: manifest declares %d bytes, received %d", expectedSize, len(data))
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

// verifyCurrentSignature checks the origin's Sigstore bundle over the bytes
// just fetched for /v1/current.json.
//
// Failure semantics (B3):
//   - a bundle that is published and does not verify ALWAYS fails — that is
//     the attack signal;
//   - an origin that publishes no bundle may proceed on the digest chain
//     alone (TLS + rollback/equivocation guards still apply), stated in the
//     docs rather than silently passed as "verified";
//   - LITESPM_REQUIRE_CATALOG_SIGNATURE=1 makes absence, unreachability, or
//     an unavailable verifier (cosign not installed) a hard refusal.
func (c *Client) verifyCurrentSignature(ctx context.Context, currentRaw []byte) error {
	required := catalogSignatureRequired()
	bundleURL := fmt.Sprintf("%s/v1/current.json%s", c.baseURL, trust.DefaultBundleSuffix)

	bundle, present, err := c.fetchSignatureBundle(ctx, bundleURL)
	if err != nil {
		// The signature could not be fetched while the pointer could: the
		// origin is behaving inconsistently. Unrequired mode continues on
		// the digest chain; required mode fails closed.
		if required {
			return fmt.Errorf("catalog signature required but %s could not be fetched: %w", bundleURL, err)
		}
		return nil
	}
	if !present {
		if required {
			return fmt.Errorf("catalog signature required but %s is not published by this origin; refusing unsigned catalog sync", bundleURL)
		}
		return nil
	}

	// Verify the pointer bytes against the bundle on disk (cosign reads
	// files). A scratch directory keeps the cache dir clean of them.
	scratch, err := os.MkdirTemp("", "litespm-catalog-verify-*")
	if err != nil {
		if required {
			return fmt.Errorf("prepare signature verification: %w", err)
		}
		return nil
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	pointerPath := filepath.Join(scratch, "current.json")
	bundlePath := filepath.Join(scratch, "current.json"+trust.DefaultBundleSuffix)
	if err := os.WriteFile(pointerPath, currentRaw, 0o600); err != nil {
		if required {
			return fmt.Errorf("stage catalog pointer for verification: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		if required {
			return fmt.Errorf("stage catalog signature for verification: %w", err)
		}
		return nil
	}

	v := c.bundleVerifier
	if v == nil {
		v = &trust.BlobVerifier{}
	}
	verdict := v.VerifyBundle(ctx, pointerPath, bundlePath)
	switch verdict.Result {
	case trust.ResultVerified:
		return nil
	case trust.ResultFailed:
		// Always fatal, required or not.
		return fmt.Errorf("catalog signature verification FAILED for /v1/current.json: %s — refusing to sync", verdict.Detail)
	default: // unavailable
		if required {
			return fmt.Errorf("catalog signature verification unavailable (%s) and LITESPM_REQUIRE_CATALOG_SIGNATURE is set; refusing unsigned catalog sync", verdict.Detail)
		}
		return nil
	}
}

// fetchSignatureBundle retrieves the origin's bundle for the pointer.
// 404/410 means "not published" (present=false); any other non-200 is an
// error the caller decides how to treat.
func (c *Client) fetchSignatureBundle(ctx context.Context, bundleURL string) ([]byte, bool, error) {
	if err := checkCatalogURL(bundleURL); err != nil {
		return nil, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := readCatalogBody(resp.Body)
		if err != nil {
			return nil, false, err
		}
		return body, true, nil
	case http.StatusNotFound, http.StatusGone:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("server returned status %d for %s", resp.StatusCode, bundleURL)
	}
}

// catalogSignatureRequired reports whether the operator demanded signed
// catalog syncs. An unrecognized value is "not required": a typo must not
// brick sync, while a published-but-invalid signature still always fails.
func catalogSignatureRequired() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("LITESPM_REQUIRE_CATALOG_SIGNATURE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
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

	body, err := readCatalogFileBody(resp.Body, fileMeta.Size)
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
	body, err := readCatalogFileBody(resp.Body, fileMeta.Size)
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

// RuntimeForListing returns the launch line for a listing, taken from the
// published version record: a stdio command, or -- for a remote (URL) row --
// the endpoint the client would connect to. It fails closed when the record
// declares neither, rather than letting an installer write a broken entry.
func (c *Client) RuntimeForListing(listingID, version string) (*domain.RuntimeDescriptor, error) {
	rec, err := c.VersionRecordFor(listingID, version)
	if err != nil {
		return nil, err
	}
	for _, component := range rec.Components {
		if component.Runtime == nil {
			continue
		}
		if strings.TrimSpace(component.Runtime.Command) == "" &&
			strings.TrimSpace(component.Runtime.Endpoint) == "" {
			continue
		}
		return component.Runtime, nil
	}
	return nil, fmt.Errorf("listing %s publishes no launch line (neither a command nor an endpoint) for version %s", listingID, rec.Version)
}

// Sync checks for remote catalog updates, verifies integrity, and updates the local cache and index.
func (c *Client) Sync(ctx context.Context) (*SyncResult, error) {
	current, currentRaw, err := c.fetchCurrent(ctx)
	if err != nil {
		return nil, err
	}

	// B3: authenticate the trust anchor before consuming any of it. The
	// digest chain below (pointer → manifest → files) proves integrity FROM
	// the pointer down; this signature proves who published the pointer
	// itself. It runs before the rollback/equivocation checks so a forged
	// pointer is rejected as forged first, not merely as stale.
	if err := c.verifyCurrentSignature(ctx, currentRaw); err != nil {
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
//
// On Windows this is a no-op by necessity, not by choice: FlushFileBuffers on
// a directory handle fails with ERROR_ACCESS_DENIED, so the POSIX pattern has
// no equivalent there. NTFS metadata durability does not depend on it the way
// ext4/APFS ordering does, and failing the whole sync over an unprovable
// durability nicety would break every catalog sync on Windows (it did — every
// persist died with "Access is denied").
func fsyncParentDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
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

// IndexVersionRecords indexes published version records directly into the
// client's in-memory index. Production callers normally populate these via
// Sync; this mirrors IndexListings for deterministic consumers and tests.
func (c *Client) IndexVersionRecords(records []*domain.VersionRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.index.IndexVersions(records)
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
