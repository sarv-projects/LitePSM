package test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litespm/internal/artifact"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/install"
	"github.com/sarv-projects/litespm/internal/state"
)

func zipBytes(t *testing.T, files map[string][]byte) []byte {
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

// TestArchiveInstallEndToEnd exercises the archive half of the install path
// with the real fetcher: the pinned artifact is downloaded over HTTPS, its
// digest is verified, the verified archive is extracted under the engine's
// limits, the tree is placed in the CAS, and an install record is committed.
//
// The origin is a loopback test server, so the fetcher runs with AllowLoopback;
// the SSRF refusals themselves are covered in internal/artifact/fetcher_test.go.
// The catalog does not carry artifact locators yet, which is why no production
// caller reaches this path — this test pins the mechanism that will carry them.
func TestArchiveInstallEndToEnd(t *testing.T) {
	payload := zipBytes(t, map[string][]byte{
		"package.json": []byte(`{"name":"demo","version":"1.0.0"}`),
		"index.js":     []byte("console.log('ok')\n"),
	})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-1.0.0.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	opts := artifact.DefaultFetcherOptions()
	opts.AllowLoopback = true
	opts.Transport = server.Client().Transport
	fetcher := artifact.NewHTTPArchiveFetcher(opts)

	ref := domain.ArtifactRef{
		ArtifactID:  "demo-1.0.0",
		Type:        domain.ArtifactArchive,
		Locator:     server.URL + "/demo-1.0.0.zip",
		Digest:      domain.ComputeBytesDigest(payload),
		Size:        int64(len(payload)),
		FetchPolicy: domain.FetchDigestPinned,
	}

	// A tampered origin (wrong bytes) must be rejected before extraction.
	tampered := ref
	tampered.Digest = domain.ComputeBytesDigest([]byte("not the archive"))
	if _, _, err := fetcher.FetchToTemp(context.Background(), tampered, t.TempDir()); err == nil {
		t.Fatal("expected a digest mismatch for tampered bytes")
	}

	base := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot:  filepath.Join(base, "config"),
		DataRoot:    filepath.Join(base, "data"),
		RuntimeRoot: filepath.Join(base, "run"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories: %v", err)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer db.Close()
	engine, err := install.NewEngine(db, paths.CASPath(), paths.StagingPath())
	if err != nil {
		t.Fatalf("install.NewEngine: %v", err)
	}

	ctx := context.Background()
	fetchDir := t.TempDir()
	rec, err := engine.Execute(ctx, install.InstallOptions{
		ListingID: "mcp:example:demo",
		Version:   "1.0.0",
		Scope:     domain.ScopeUser,
		ArchiveSource: func(ctx context.Context, listingID, version string) (io.ReadCloser, string, error) {
			verified, archiveType, err := fetcher.FetchToTemp(ctx, ref, fetchDir)
			if err != nil {
				return nil, "", err
			}
			f, err := os.Open(verified.Path)
			if err != nil {
				return nil, "", err
			}
			return f, archiveType, nil
		},
	})
	if err != nil {
		t.Fatalf("engine.Execute with a verified artifact: %v", err)
	}
	if rec.Status != domain.InstallActive {
		t.Errorf("install status %q, want active", rec.Status)
	}
	if rec.TreeDigest == "" {
		t.Error("install record has no CAS tree digest")
	}
	treePath, err := engine.TreePath(rec.TreeDigest)
	if err != nil {
		t.Fatalf("TreePath: %v", err)
	}
	if _, err := os.Stat(treePath); err != nil {
		t.Errorf("CAS tree not present at %s: %v", treePath, err)
	}
	if _, err := db.GetInstall(ctx, rec.InstallID); err != nil {
		t.Errorf("install record not persisted: %v", err)
	}
}
