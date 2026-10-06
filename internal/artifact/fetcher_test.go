package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func etagCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var lpsm *domain.LPSMError
	if !errors.As(err, &lpsm) {
		t.Fatalf("expected *domain.LPSMError, got %T: %v", err, err)
	}
	return lpsm.Code
}

// TestHTTPArchiveFetcherRefusesNonHTTPS pins the scheme rule: only https is
// fetched, so http/file/data/ftp are refused before any network use.
func TestHTTPArchiveFetcherRefusesNonHTTPS(t *testing.T) {
	f := NewHTTPArchiveFetcher(DefaultFetcherOptions())
	for _, locator := range []string{
		"http://example.com/pkg.zip",
		"file:///etc/passwd",
		"data:text/plain,hi",
		"ftp://example.com/pkg.zip",
	} {
		_, err := f.ResolveImmutable(context.Background(), domain.ArtifactRef{
			ArtifactID: "a1", Type: domain.ArtifactArchive, Locator: locator,
		})
		if code := etagCode(t, err); code != "LPSM-EGRESS-BLOCKED" {
			t.Errorf("%s: code=%s, want LPSM-EGRESS-BLOCKED", locator, code)
		}
	}
}

// TestHTTPArchiveFetcherRefusesCredentials keeps secrets out of URLs.
func TestHTTPArchiveFetcherRefusesCredentials(t *testing.T) {
	f := NewHTTPArchiveFetcher(DefaultFetcherOptions())
	_, err := f.ResolveImmutable(context.Background(), domain.ArtifactRef{
		ArtifactID: "a1", Type: domain.ArtifactArchive,
		Locator: "https://user:pass@example.com/pkg.zip",
	})
	if code := etagCode(t, err); code != "LPSM-EGRESS-BLOCKED" {
		t.Fatalf("code=%s, want LPSM-EGRESS-BLOCKED", code)
	}
}

// TestHTTPArchiveFetcherRefusesNonPublicHosts is the SSRF guard: loopback,
// link-local (cloud metadata) and private literals are refused by the default
// posture, before any request body is read.
func TestHTTPArchiveFetcherRefusesNonPublicHosts(t *testing.T) {
	f := NewHTTPArchiveFetcher(DefaultFetcherOptions())
	for _, locator := range []string{
		"https://127.0.0.1/pkg.zip",
		"https://[::1]/pkg.zip",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5/pkg.zip",
		"https://192.168.1.10/pkg.zip",
		"https://0.0.0.0/pkg.zip",
	} {
		_, err := f.ResolveImmutable(context.Background(), domain.ArtifactRef{
			ArtifactID: "a1", Type: domain.ArtifactArchive, Locator: locator,
		})
		if code := etagCode(t, err); code != "LPSM-EGRESS-BLOCKED" {
			t.Errorf("%s: code=%s, want LPSM-EGRESS-BLOCKED", locator, code)
		}
	}
}

// TestHTTPArchiveFetcherUnknownTypeFailsClosed refuses to guess a format.
func TestHTTPArchiveFetcherUnknownTypeFailsClosed(t *testing.T) {
	opts := DefaultFetcherOptions()
	opts.Transport = http.DefaultTransport // skip DNS; this must fail before any request
	f := NewHTTPArchiveFetcher(opts)
	_, err := f.ResolveImmutable(context.Background(), domain.ArtifactRef{
		ArtifactID: "a1", Type: domain.ArtifactArchive, Locator: "https://example.com/pkg.bin",
	})
	if code := etagCode(t, err); code != "LPSM-ARTIFACT-UNAVAILABLE" {
		t.Fatalf("code=%s, want LPSM-ARTIFACT-UNAVAILABLE", code)
	}
}

// TestHTTPArchiveFetcherFetchVerifyExtract is the happy path: download over
// HTTPS, verify the pinned digest, then extract safely.
func TestHTTPArchiveFetcherFetchVerifyExtract(t *testing.T) {
	zipBytes := makeZip(t, map[string][]byte{"index.js": []byte("ok\n")})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	opts := DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.Transport = server.Client().Transport
	f := NewHTTPArchiveFetcher(opts)

	ref := domain.ArtifactRef{
		ArtifactID:  "art-1",
		Type:        domain.ArtifactArchive,
		Locator:     server.URL + "/pkg.zip",
		Digest:      domain.ComputeBytesDigest(zipBytes),
		FetchPolicy: domain.FetchDigestPinned,
	}
	verified, archiveType, err := f.FetchToTemp(context.Background(), ref, t.TempDir())
	if err != nil {
		t.Fatalf("FetchToTemp: %v", err)
	}
	if archiveType != "zip" {
		t.Errorf("archive type %q, want zip", archiveType)
	}
	if verified.Digest != ref.Digest {
		t.Errorf("digest %s != expected %s", verified.Digest, ref.Digest)
	}

	tree, err := ExtractFileSafely(verified.Path, archiveType, t.TempDir(), DefaultExtractionLimits)
	if err != nil {
		t.Fatalf("extract verified artifact: %v", err)
	}
	if tree == nil || tree.TreeDigest == "" {
		t.Fatalf("extraction produced no tree digest: %+v", tree)
	}
}

// TestHTTPArchiveFetcherRejectsWrongDigest proves verification is fail-closed
// and that the download is removed when it does not match.
func TestHTTPArchiveFetcherRejectsWrongDigest(t *testing.T) {
	zipBytes := makeZip(t, map[string][]byte{"a.txt": []byte("a")})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	opts := DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.Transport = server.Client().Transport
	f := NewHTTPArchiveFetcher(opts)

	tempDir := t.TempDir()
	ref := domain.ArtifactRef{
		ArtifactID: "art-1", Type: domain.ArtifactArchive,
		Locator: server.URL + "/pkg.zip", Digest: domain.ComputeBytesDigest([]byte("not the archive")),
	}
	_, _, err := f.FetchToTemp(context.Background(), ref, tempDir)
	if code := etagCode(t, err); code != "LPSM-VERIFY-CHECKSUM-MISMATCH" {
		t.Fatalf("code=%s, want LPSM-VERIFY-CHECKSUM-MISMATCH", code)
	}
	if _, statErr := os.Stat(filepath.Join(tempDir, "art-1.download")); !os.IsNotExist(statErr) {
		t.Errorf("mismatched download was not removed (stat err: %v)", statErr)
	}
}

// TestHTTPArchiveFetcherVerifyRefusesUnpinned never accepts a download with no
// expected digest.
func TestHTTPArchiveFetcherVerifyRefusesUnpinned(t *testing.T) {
	f := NewHTTPArchiveFetcher(DefaultFetcherOptions())
	result := &FetchResult{Path: "/tmp/x", Digest: domain.ComputeBytesDigest([]byte("x"))}
	if _, err := f.Verify(context.Background(), result, ""); err == nil {
		t.Fatal("expected an unpinned verification to fail")
	}
}

// TestHTTPArchiveFetcherEnforcesByteBound stops an oversized download.
func TestHTTPArchiveFetcherEnforcesByteBound(t *testing.T) {
	big := bytes.Repeat([]byte("A"), 4096)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	}))
	defer server.Close()

	opts := DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.MaxBytes = 512
	opts.Transport = server.Client().Transport
	f := NewHTTPArchiveFetcher(opts)

	ref := domain.ArtifactRef{ArtifactID: "big", Type: domain.ArtifactArchive,
		Locator: server.URL + "/pkg.zip", Digest: domain.ComputeBytesDigest(big)}
	_, _, err := f.FetchToTemp(context.Background(), ref, t.TempDir())
	if code := etagCode(t, err); code != "LPSM-CAS-ARCHIVE-SLIP" {
		t.Fatalf("code=%s, want LPSM-CAS-ARCHIVE-SLIP", code)
	}
}

// TestHTTPArchiveFetcherRefusesRedirectDowngrade blocks https -> http redirects.
func TestHTTPArchiveFetcherRefusesRedirectDowngrade(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/pkg.zip", http.StatusFound)
	}))
	defer server.Close()

	opts := DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.Transport = server.Client().Transport
	f := NewHTTPArchiveFetcher(opts)

	ref := domain.ArtifactRef{ArtifactID: "r", Type: domain.ArtifactArchive,
		Locator: server.URL + "/pkg.zip", Digest: domain.ComputeBytesDigest([]byte("x"))}
	resolved, err := f.ResolveImmutable(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	_, err = f.Fetch(context.Background(), resolved, filepath.Join(t.TempDir(), "r.download"))
	if code := etagCode(t, err); code != "LPSM-EGRESS-BLOCKED" {
		t.Fatalf("code=%s, want LPSM-EGRESS-BLOCKED", code)
	}
}

// TestHTTPArchiveFetcherCapsRedirects stops a redirect loop.
func TestHTTPArchiveFetcherCapsRedirects(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer server.Close()

	opts := DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.MaxRedirects = 2
	opts.Transport = server.Client().Transport
	f := NewHTTPArchiveFetcher(opts)

	ref := domain.ArtifactRef{ArtifactID: "loop", Type: domain.ArtifactArchive,
		Locator: server.URL + "/a.zip", Digest: domain.ComputeBytesDigest([]byte("x"))}
	resolved, err := f.ResolveImmutable(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	_, err = f.Fetch(context.Background(), resolved, filepath.Join(t.TempDir(), "loop.download"))
	if code := etagCode(t, err); code != "LPSM-EGRESS-BLOCKED" {
		t.Fatalf("code=%s, want LPSM-EGRESS-BLOCKED", code)
	}
}
