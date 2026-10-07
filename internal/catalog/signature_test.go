package catalog

// signature_test.go — B3 acceptance: the catalog client authenticates the
// trust anchor itself.
//
// The digest chain (pointer → manifest → files) proves integrity FROM the
// pointer down. These tests pin the layer above it: a published Sigstore
// bundle is verified before the pointer is consumed, a bundle that fails is
// always fatal, and an origin that publishes none proceeds only on the
// stated fallback (or refuses when LITESPM_REQUIRE_CATALOG_SIGNATURE is set).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/trust"
)

// signedReleaseFiles builds a valid release tree with the given pointer bytes
// and (when bundleBody is non-nil) publishes the pointer's signature bundle.
func signedReleaseFiles(t *testing.T, pointer []byte, bundleBody []byte) map[string][]byte {
	t.Helper()
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	files := map[string][]byte{}
	for path, data := range compiled.Files {
		files[path] = data
	}
	files["v1/current.json"] = pointer
	if bundleBody != nil {
		files["v1/current.json"+trust.DefaultBundleSuffix] = bundleBody
	}
	return files
}

// pointerFor returns a pointer the rest of the release agrees with.
func pointerFor(t *testing.T) []byte {
	t.Helper()
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	return compiled.Files["v1/current.json"]
}

// stubVerifier builds a verifier whose verdict is scripted, and which records
// the arguments it was asked about plus the staged pointer bytes it saw (the
// scratch directory is removed when Sync returns, so content must be
// snapshotted at call time).
func stubVerifier(scriptedVerdict string) (*trust.BlobVerifier, *[]string, *[]byte) {
	var seen []string
	var stagedPointer []byte
	v := &trust.BlobVerifier{
		LookPath: func(string) (string, error) {
			if scriptedVerdict == trust.ResultUnavailable {
				return "", os.ErrNotExist
			}
			return "/usr/bin/cosign", nil
		},
		Run: func(_ context.Context, args []string) ([]byte, error) {
			seen = append(seen, args...)
			for _, a := range args {
				if strings.HasSuffix(a, "current.json") {
					if data, err := os.ReadFile(a); err == nil {
						stagedPointer = data
					}
				}
			}
			if scriptedVerdict == trust.ResultFailed {
				return []byte("certificate identity mismatch"), errStubCosign
			}
			return []byte("ok"), nil
		},
	}
	return v, &seen, &stagedPointer
}

// errStubCosign is a non-nil error for the scripted "failed" verdict.
var errStubCosign = &stubError{"cosign exited non-zero"}

type stubError struct{ s string }

func (e *stubError) Error() string { return e.s }

// A published bundle that verifies lets sync proceed, and the verifier really
// saw the served pointer bytes (not some other file).
func TestSyncVerifiesPublishedPointerSignature(t *testing.T) {
	pointer := pointerFor(t)
	files := signedReleaseFiles(t, pointer, []byte(`{"bundle":"real"}`))
	server := serveFiles(t, files)
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	v, seen, staged := stubVerifier(trust.ResultVerified)
	client.bundleVerifier = v

	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("sync with a verifying bundle must succeed: %v", err)
	}
	if len(*seen) == 0 {
		t.Fatal("verifier was never invoked")
	}
	joined := strings.Join(*seen, "\n")
	if !strings.Contains(joined, trust.DefaultBundleSuffix) {
		t.Errorf("verifier was not pointed at the bundle: %v", *seen)
	}
	if !strings.Contains(joined, "current.json") {
		t.Errorf("verifier was not pointed at the pointer: %v", *seen)
	}
	// The staged pointer the verifier checked must be the served bytes.
	if string(*staged) != string(pointer) {
		t.Errorf("staged pointer bytes differ from served bytes:\nstaged: %s\nserved: %s", *staged, pointer)
	}
}

// A bundle that exists but does not verify is always fatal — the required
// flag changes nothing about the attack case.
func TestSyncRejectsInvalidPointerSignature(t *testing.T) {
	for _, required := range []bool{false, true} {
		files := signedReleaseFiles(t, pointerFor(t), []byte(`{"bundle":"attacker"}`))
		server := serveFiles(t, files)
		client := NewClient(server.URL, t.TempDir(), server.Client())
		v, _, _ := stubVerifier(trust.ResultFailed)
		client.bundleVerifier = v

		if required {
			t.Setenv("LITESPM_REQUIRE_CATALOG_SIGNATURE", "1")
		}
		_, err := client.Sync(context.Background())
		if err == nil {
			t.Fatalf("required=%v: invalid signature must fail sync", required)
		}
		if !strings.Contains(err.Error(), "signature verification FAILED") {
			t.Errorf("required=%v: unexpected error: %v", required, err)
		}
		if files := cacheFiles(t, client.cacheDir); len(files) != 0 {
			t.Errorf("required=%v: rejected sync wrote cache files: %v", required, files)
		}
		server.Close()
	}
}

// No bundle + LITESPM_REQUIRE_CATALOG_SIGNATURE=1 → refusal before any of the
// pointer's content is consumed.
func TestSyncRequiresSignatureWhenConfigured(t *testing.T) {
	t.Setenv("LITESPM_REQUIRE_CATALOG_SIGNATURE", "1")

	server := serveFiles(t, signedReleaseFiles(t, pointerFor(t), nil))
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	_, err := client.Sync(context.Background())
	if err == nil {
		t.Fatal("required signature mode must refuse an origin that publishes none")
	}
	if !strings.Contains(err.Error(), "not published") {
		t.Errorf("unexpected error: %v", err)
	}
	if files := cacheFiles(t, client.cacheDir); len(files) != 0 {
		t.Errorf("refused sync wrote cache files: %v", files)
	}
}

// No bundle, default mode → the digest chain still guards sync (existing
// integrity tests), and this test pins the fallback as a decision, not an
// accident.
func TestSyncProceedsWithoutBundleByDefault(t *testing.T) {
	server := serveFiles(t, signedReleaseFiles(t, pointerFor(t), nil))
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	v, seen, _ := stubVerifier(trust.ResultVerified)
	client.bundleVerifier = v

	if _, err := client.Sync(context.Background()); err != nil {
		t.Fatalf("default mode must tolerate an unsigned origin: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("verifier must not run when no bundle is published, ran: %v", *seen)
	}
}

// Published bundle + verifier unavailable (cosign not installed): default
// mode falls back with the gap left visible; required mode refuses.
func TestSyncUnavailableVerifierRespectsRequireFlag(t *testing.T) {
	files := signedReleaseFiles(t, pointerFor(t), []byte(`{"bundle":"real"}`))

	t.Run("default falls back", func(t *testing.T) {
		server := serveFiles(t, files)
		defer server.Close()
		client := NewClient(server.URL, t.TempDir(), server.Client())
		v, _, _ := stubVerifier(trust.ResultUnavailable)
		client.bundleVerifier = v
		if _, err := client.Sync(context.Background()); err != nil {
			t.Fatalf("default mode must fall back when the verifier is unavailable: %v", err)
		}
	})

	t.Run("required refuses", func(t *testing.T) {
		t.Setenv("LITESPM_REQUIRE_CATALOG_SIGNATURE", "1")
		server := serveFiles(t, files)
		defer server.Close()
		client := NewClient(server.URL, t.TempDir(), server.Client())
		v, _, _ := stubVerifier(trust.ResultUnavailable)
		client.bundleVerifier = v
		_, err := client.Sync(context.Background())
		if err == nil {
			t.Fatal("required mode must refuse an unverifiable published bundle")
		}
		if !strings.Contains(err.Error(), "verification unavailable") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// An origin that errors (not 404s) on the signature while serving the
// pointer fails closed only when required — a static origin returns 404 for
// unpublished files, so anything else is worth the strict reading.
func TestSyncSignatureFetchErrorRespectsRequireFlag(t *testing.T) {
	compiled := compileSampleRelease(t, "rel-2026-10-05-01", 1)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, trust.DefaultBundleSuffix) {
			http.Error(w, "origin exploded", http.StatusInternalServerError)
			return
		}
		data, ok := compiled.Files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	t.Run("default proceeds", func(t *testing.T) {
		client := NewClient(srv.URL, t.TempDir(), srv.Client())
		if _, err := client.Sync(context.Background()); err != nil {
			t.Fatalf("default mode must proceed on the digest chain: %v", err)
		}
	})

	t.Run("required refuses", func(t *testing.T) {
		t.Setenv("LITESPM_REQUIRE_CATALOG_SIGNATURE", "1")
		client := NewClient(srv.URL, t.TempDir(), srv.Client())
		_, err := client.Sync(context.Background())
		if err == nil {
			t.Fatal("required mode must refuse when the signature cannot be fetched")
		}
		if !strings.Contains(err.Error(), "could not be fetched") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// The fallback must not silently claim verification: the sync result is a
// result, not a verdict — but the pointer the CLI prints comes from the same
// bytes. Keep the JSON shape stable here so callers cannot misread an
// unsigned sync as a signed one later.
func TestSyncResultCarriesNoVerificationClaim(t *testing.T) {
	server := serveFiles(t, signedReleaseFiles(t, pointerFor(t), nil))
	defer server.Close()

	client := NewClient(server.URL, t.TempDir(), server.Client())
	result, err := client.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "verified") || strings.Contains(string(encoded), "signed") {
		t.Errorf("sync result must not claim a signature state it does not have: %s", encoded)
	}
}
