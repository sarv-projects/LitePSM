package artifact

// fetcher.go implements the ArtifactFetcher seam specified in ARCH/17 §4 and
// the ingestion security rules in ARCH/03 §5. Until now the extractor in
// extractor.go was the whole artifact layer: it operated on bytes somebody else
// had already supplied, and nothing supplied bytes. This is the retrieval half.
//
// Everything here is data-only. It downloads an archive to a temp file and
// verifies its digest; it never executes, unpacks in place, or interprets the
// payload. Extraction stays in extractor.go, behind its own limits.
//
// The security properties are the ones ARCH/03 §5 promised and the repo did not
// have:
//   - HTTPS only. http, file, data, ftp and every other scheme are refused.
//   - No credentials in the URL (https://user:pass@host/... is refused).
//   - SSRF defense: the hostname is resolved and every address is checked
//     against non-public ranges *before dialing*, and the dial uses the checked
//     literal IP so the name cannot be re-pointed between the check and the
//     connection (no TOCTOU). Redirects pass through the same dialer, so a
//     redirect cannot dodge the policy.
//   - No downgrade: a redirect to a non-HTTPS scheme is refused.
//   - Redirect cap, request timeout, and a hard byte bound (SpoolDownloadBounded)
//     with a streaming SHA-256.
//   - Fail-closed verification: an artifact with no expected digest is refused;
//     Verify never succeeds on an unpinned download.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// ResolvedArtifact is a validated, ready-to-fetch artifact target.
type ResolvedArtifact struct {
	Ref         domain.ArtifactRef
	URL         *url.URL
	ArchiveType string // "zip" or "tar.gz": the extractor's format token
	MediaType   string
}

// FetchResult is a completed download that has been streamed to disk with a
// computed digest but not yet compared against the expected digest.
type FetchResult struct {
	Path     string
	Bytes    int64
	Digest   string // sha256:<hex>
	FinalURL string
}

// VerifiedArtifact is a download whose digest matched the pinned expectation.
// Only this type should be handed to extraction.
type VerifiedArtifact struct {
	Path   string
	Digest string
	Bytes  int64
}

// ArtifactFetcher resolves, retrieves and verifies one immutable artifact.
// The interface matches ARCH/17 §4 so alternative strategies (git tree, OCI)
// can be added without changing callers.
type ArtifactFetcher interface {
	Supports(ref domain.ArtifactRef) bool
	ResolveImmutable(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error)
	Fetch(ctx context.Context, resolved *ResolvedArtifact, destinationFile string) (*FetchResult, error)
	Verify(ctx context.Context, result *FetchResult, expectedDigest string) (*VerifiedArtifact, error)
}

// FetcherOptions bounds and gates outbound retrieval. The zero value is not
// usable; use DefaultFetcherOptions.
type FetcherOptions struct {
	// MaxBytes caps the downloaded size. Zero means DefaultMaxArchiveSize.
	MaxBytes int64
	// Timeout caps the whole request. Zero means 60s.
	Timeout time.Duration
	// MaxRedirects caps the redirect chain. Zero means 5.
	MaxRedirects int
	// AllowLoopback and AllowPrivate disable the corresponding SSRF refusal.
	// They exist for tests and local development; production must leave both
	// false so the default is fail-closed.
	AllowLoopback bool
	AllowPrivate  bool
	// Resolver overrides DNS lookups. Nil uses net.DefaultResolver.
	Resolver *net.Resolver
	// Transport overrides the HTTP transport. Nil builds one with the SSRF
	// dialer below. Tests that must reach a loopback server set AllowLoopback
	// instead of replacing this.
	Transport http.RoundTripper
}

// DefaultFetcherOptions returns the production posture: HTTPS, SSRF-guarded,
// bounded, timed out, redirect-capped, and no loopback/private exceptions.
func DefaultFetcherOptions() FetcherOptions {
	return FetcherOptions{
		MaxBytes:     DefaultMaxArchiveSize,
		Timeout:      60 * time.Second,
		MaxRedirects: 5,
	}
}

// HTTPArchiveFetcher fetches https archive artifacts (zip / tar.gz).
type HTTPArchiveFetcher struct {
	opts   FetcherOptions
	client *http.Client
}

// NewHTTPArchiveFetcher builds a fetcher. The transport enforces the SSRF
// policy at dial time so redirects cannot bypass it.
func NewHTTPArchiveFetcher(opts FetcherOptions) *HTTPArchiveFetcher {
	if opts.MaxBytes <= 0 || opts.MaxBytes > DefaultMaxArchiveSize {
		opts.MaxBytes = DefaultMaxArchiveSize
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = 5
	}
	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           opts.dialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			DisableKeepAlives:     true,
		}
	}
	client := &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return domain.ErrEgressBlocked(req.URL.String(), "redirect would downgrade from https")
			}
			if len(via) > opts.MaxRedirects {
				return domain.ErrEgressBlocked(req.URL.String(),
					fmt.Sprintf("redirect chain exceeds %d hops", opts.MaxRedirects))
			}
			return nil
		},
	}
	return &HTTPArchiveFetcher{opts: opts, client: client}
}

// Supports reports whether this fetcher handles the reference: an archive
// artifact family over HTTPS.
func (f *HTTPArchiveFetcher) Supports(ref domain.ArtifactRef) bool {
	switch ref.Type {
	case domain.ArtifactArchive, domain.ArtifactMCPB:
	default:
		return false
	}
	u, err := url.Parse(ref.Locator)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// ResolveImmutable validates the reference and resolves it to a fetchable
// target. It refuses every non-HTTPS scheme, credentials in the URL, an
// unpinned or non-remote fetch policy, an undeterminable archive type, and a
// host that resolves into a blocked range.
func (f *HTTPArchiveFetcher) ResolveImmutable(ctx context.Context, ref domain.ArtifactRef) (*ResolvedArtifact, error) {
	if strings.TrimSpace(ref.Locator) == "" {
		return nil, domain.ErrArtifactUnavailable(ref.ArtifactID, "artifact has no locator")
	}
	u, err := url.Parse(ref.Locator)
	if err != nil {
		return nil, domain.ErrEgressBlocked(ref.Locator, fmt.Sprintf("unparseable URL: %v", err))
	}
	if u.Scheme != "https" {
		return nil, domain.ErrEgressBlocked(ref.Locator,
			fmt.Sprintf("scheme %q is not allowed; only https is fetched", u.Scheme))
	}
	if u.User != nil {
		return nil, domain.ErrEgressBlocked(ref.Locator, "URLs embedding credentials are refused")
	}
	if ref.FetchPolicy == domain.FetchLocalOnly {
		return nil, domain.ErrEgressBlocked(ref.Locator, "fetch policy is local-only")
	}
	if f.opts.Transport == nil {
		// Enforce the policy now so a bad host fails before any request body is
		// read; the dialer re-checks the concrete IP (no TOCTOU).
		if err := f.opts.checkHost(ctx, u.Hostname()); err != nil {
			return nil, err
		}
	}
	archiveType, err := detectArchiveType(ref.MediaType, u.Path)
	if err != nil {
		return nil, err
	}
	return &ResolvedArtifact{Ref: ref, URL: u, ArchiveType: archiveType, MediaType: ref.MediaType}, nil
}

// Fetch downloads the resolved artifact into destinationFile, enforcing the
// byte bound and computing a streaming SHA-256. The file is removed on failure
// so a partial download is never left behind.
func (f *HTTPArchiveFetcher) Fetch(ctx context.Context, resolved *ResolvedArtifact, destinationFile string) (*FetchResult, error) {
	if resolved == nil || resolved.URL == nil {
		return nil, domain.ErrArtifactUnavailable("", "fetch called without a resolved artifact")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.URL.String(), nil)
	if err != nil {
		return nil, domain.ErrEgressBlocked(resolved.URL.String(), err.Error())
	}
	req.Header.Set("Accept", "application/octet-stream, application/zip, application/gzip")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact fetch %s returned HTTP %d", resolved.URL.Redacted(), resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destinationFile), 0o755); err != nil {
		return nil, fmt.Errorf("prepare download directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destinationFile), ".fetch-*")
	if err != nil {
		return nil, fmt.Errorf("create download file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }

	bytes, digest, err := SpoolDownloadBounded(resp.Body, f.opts.MaxBytes, tmp)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("close download file: %w", err)
	}
	if err := os.Rename(tmpName, destinationFile); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("finalize download: %w", err)
	}
	return &FetchResult{Path: destinationFile, Bytes: bytes, Digest: digest, FinalURL: resp.Request.URL.String()}, nil
}

// Verify compares the downloaded digest with the pinned expectation. An empty
// expectation is refused: an unpinned artifact is never accepted.
func (f *HTTPArchiveFetcher) Verify(ctx context.Context, result *FetchResult, expectedDigest string) (*VerifiedArtifact, error) {
	if result == nil {
		return nil, domain.ErrArtifactUnavailable("", "verify called without a fetch result")
	}
	expected := strings.TrimSpace(expectedDigest)
	if expected == "" {
		return nil, domain.ErrChecksumMismatch("<unpinned>", result.Digest)
	}
	if !strings.EqualFold(expected, result.Digest) {
		return nil, domain.ErrChecksumMismatch(expected, result.Digest)
	}
	return &VerifiedArtifact{Path: result.Path, Digest: result.Digest, Bytes: result.Bytes}, nil
}

// FetchToTemp resolves, fetches and verifies one artifact in a single call,
// returning the verified archive path and its extractor format token. The
// caller owns the returned file.
func (f *HTTPArchiveFetcher) FetchToTemp(ctx context.Context, ref domain.ArtifactRef, tempDir string) (*VerifiedArtifact, string, error) {
	resolved, err := f.ResolveImmutable(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	dest := filepath.Join(tempDir, ref.ArtifactID+".download")
	result, err := f.Fetch(ctx, resolved, dest)
	if err != nil {
		return nil, "", err
	}
	verified, err := f.Verify(ctx, result, ref.Digest)
	if err != nil {
		_ = os.Remove(dest)
		return nil, "", err
	}
	return verified, resolved.ArchiveType, nil
}

// checkHost resolves hostname and refuses if any resolved address is outside
// the allowed public ranges.
func (o FetcherOptions) checkHost(ctx context.Context, hostname string) error {
	if ip := net.ParseIP(hostname); ip != nil {
		return o.checkIP(ip.String(), ip)
	}
	resolver := o.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return domain.ErrEgressBlocked(hostname, fmt.Sprintf("DNS resolution failed: %v", err))
	}
	if len(addrs) == 0 {
		return domain.ErrEgressBlocked(hostname, "DNS returned no addresses")
	}
	for _, addr := range addrs {
		if err := o.checkIP(hostname, addr.IP); err != nil {
			return err
		}
	}
	return nil
}

// dialContext resolves, policy-checks and dials the concrete IP. Dialing the
// literal that was checked closes the DNS-rebinding window.
func (o FetcherOptions) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if err := o.checkHost(ctx, host); err != nil {
		return nil, err
	}
	// Dial the first permitted literal address.
	var lastErr error
	resolver := o.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, lookupErr := resolver.LookupIPAddr(ctx, host)
		if lookupErr != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, lookupErr)
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	for _, ip := range ips {
		if err := o.checkIP(host, ip); err != nil {
			lastErr = err
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no permitted address for %s", host)
	}
	return nil, lastErr
}

// checkIP applies the SSRF policy to one resolved address.
func (o FetcherOptions) checkIP(host string, ip net.IP) error {
	label := func(reason string) error { return domain.ErrEgressBlocked(host, reason) }
	if ip == nil {
		return label("unparseable address")
	}
	if ip.IsUnspecified() {
		return label("unspecified address")
	}
	if ip.IsLoopback() && !o.AllowLoopback {
		return label("loopback address")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		// 169.254.0.0/16 (cloud metadata) and fe80::/10.
		return label("link-local address")
	}
	if ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return label("multicast address")
	}
	if ip.IsPrivate() && !o.AllowPrivate && !o.AllowLoopback {
		return label("private address")
	}
	return nil
}

// detectArchiveType maps a media type or URL path to the extractor's format
// token. An unknown type fails closed rather than guessing.
func detectArchiveType(mediaType, path string) (string, error) {
	candidates := []string{mediaType}
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		candidates = append(candidates, ext)
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".mcpb"):
		return "zip", nil
	}
	for _, c := range candidates {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "application/zip", "application/x-zip-compressed":
			return "zip", nil
		case "application/gzip", "application/x-gzip", "application/x-tar":
			return "tar.gz", nil
		}
	}
	return "", domain.ErrArtifactUnavailable("",
		fmt.Sprintf("cannot determine archive type for mediaType %q path %q", mediaType, path))
}
