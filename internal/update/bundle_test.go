package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sarv-projects/litespm/internal/trust"
)

// bundleMode selects what kind of Sigstore bundle asset the fake release
// publishes.
type bundleMode int

const (
	bundleNone bundleMode = iota
	bundleHTTPS
	bundleHTTP // deliberately insecure, for the refusal test
)

// bundleManifestServer serves a release manifest whose assets include the
// target binary, the checksum listing, and (per mode) a Sigstore bundle.
func bundleManifestServer(t *testing.T, mode bundleMode) *httptest.Server {
	t.Helper()
	binName := TargetBinaryName()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		assets := []map[string]string{
			{"name": binName, "browser_download_url": srv.URL + "/" + binName},
			{"name": checksumManifestAsset, "browser_download_url": srv.URL + "/" + checksumManifestAsset},
		}
		switch mode {
		case bundleHTTPS:
			assets = append(assets, map[string]string{
				"name":                 binName + trust.DefaultBundleSuffix,
				"browser_download_url": srv.URL + "/bundle.sigstore.json",
			})
		case bundleHTTP:
			assets = append(assets, map[string]string{
				"name":                 binName + trust.DefaultBundleSuffix,
				"browser_download_url": "http://example.invalid/bundle.sigstore.json",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v9.9.9",
			"html_url":     "https://example.invalid/releases/tag/v9.9.9",
			"published_at": "2026-03-04T05:06:07Z",
			"assets":       assets,
		})
	})
	mux.HandleFunc("/"+checksumManifestAsset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  "+binName+"\n")
	})
	mux.HandleFunc("/bundle.sigstore.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"foo":"bar"}`)
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The manifest's Sigstore bundle asset is captured with its download URL so
// the self-update path can verify the payload before installing it, and the
// bundle body is fetchable under the same bounded, HTTPS-only rules.
func TestCheckForUpdateCapturesSignatureBundleURL(t *testing.T) {
	srv := bundleManifestServer(t, bundleHTTPS)
	u := newTLSUpdater(srv)

	_, info, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	bundleName := TargetBinaryName() + trust.DefaultBundleSuffix
	got, ok := info.BundleURLs[bundleName]
	if !ok || got == "" {
		t.Fatalf("bundle URL not captured: %+v", info.BundleURLs)
	}

	body, err := u.FetchAsset(context.Background(), got)
	if err != nil {
		t.Fatalf("FetchAsset: %v", err)
	}
	if string(body) != `{"foo":"bar"}` {
		t.Errorf("bundle body = %q", body)
	}
}

// An insecure bundle URL is refused the same way an insecure binary URL is:
// signature material fetched over plaintext defeats the point of signing.
func TestCheckForUpdateRejectsInsecureBundleURL(t *testing.T) {
	srv := bundleManifestServer(t, bundleHTTP)
	u := newTLSUpdater(srv)

	_, _, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err == nil {
		t.Fatal("expected an insecure bundle URL to be refused")
	}
}

// A release that predates signing simply has no bundle asset: BundleURLs
// stays empty and the caller decides whether checksum-only is acceptable.
func TestCheckForUpdateWithoutBundleLeavesBundleURLsEmpty(t *testing.T) {
	srv := bundleManifestServer(t, bundleNone)
	u := newTLSUpdater(srv)

	_, info, err := u.CheckForUpdate(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if len(info.BundleURLs) != 0 {
		t.Errorf("expected no bundle URLs, got %+v", info.BundleURLs)
	}
}
