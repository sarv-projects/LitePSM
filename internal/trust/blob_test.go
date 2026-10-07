package trust

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func regexpMatches(pattern, s string) bool {
	ok, err := regexp.MatchString(pattern, s)
	return err == nil && ok
}

// The verifier's contract: verified only on cosign exit 0, failed on a
// non-zero exit, unavailable when cosign is absent — never a pass by default.
func TestBlobVerifierResultMatrix(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "litespm-linux-amd64")
	if err := os.WriteFile(blob, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := blob + DefaultBundleSuffix
	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("verified on exit 0", func(t *testing.T) {
		var gotArgs []string
		v := BlobVerifier{
			LookPath: func(string) (string, error) { return "/usr/bin/cosign", nil },
			Run: func(_ context.Context, args []string) ([]byte, error) {
				gotArgs = args
				return []byte("ok"), nil
			},
		}
		verdict := v.VerifyBundle(context.Background(), blob, bundle)
		if verdict.Result != ResultVerified {
			t.Fatalf("result = %q (%s), want verified", verdict.Result, verdict.Detail)
		}
		joined := strings.Join(gotArgs, " ")
		for _, want := range []string{
			"verify-blob",
			"--bundle " + bundle,
			"--certificate-identity-regexp " + DefaultBlobIdentityRegexp,
			"--certificate-oidc-issuer " + DefaultBlobIssuer,
			blob,
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("args missing %q: %v", want, gotArgs)
			}
		}
		// The blob path must be the final argument (cosign verifies what it
		// names last).
		if gotArgs[len(gotArgs)-1] != blob {
			t.Errorf("blob is not the final argument: %v", gotArgs)
		}
	})

	t.Run("failed on non-zero exit", func(t *testing.T) {
		v := BlobVerifier{
			LookPath: func(string) (string, error) { return "/usr/bin/cosign", nil },
			Run: func(context.Context, []string) ([]byte, error) {
				return []byte("signature mismatch"), errors.New("exit 1")
			},
		}
		verdict := v.VerifyBundle(context.Background(), blob, bundle)
		if verdict.Result != ResultFailed {
			t.Fatalf("result = %q, want failed", verdict.Result)
		}
		if !strings.Contains(verdict.Detail, "signature mismatch") {
			t.Errorf("detail must carry cosign's output, got: %q", verdict.Detail)
		}
	})

	t.Run("unavailable when cosign is missing", func(t *testing.T) {
		v := BlobVerifier{
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
			Run: func(context.Context, []string) ([]byte, error) {
				t.Fatal("must not run cosign when it is not installed")
				return nil, nil
			},
		}
		verdict := v.VerifyBundle(context.Background(), blob, bundle)
		if verdict.Result != ResultUnavailable {
			t.Fatalf("result = %q, want unavailable", verdict.Result)
		}
		if !strings.Contains(verdict.Detail, "cosign binary not found") {
			t.Errorf("detail = %q", verdict.Detail)
		}
	})

	t.Run("unavailable for missing inputs", func(t *testing.T) {
		v := BlobVerifier{
			LookPath: func(string) (string, error) { return "/usr/bin/cosign", nil },
			Run: func(context.Context, []string) ([]byte, error) {
				t.Fatal("must not run cosign without inputs")
				return nil, nil
			},
		}
		if got := v.VerifyBundle(context.Background(), "", bundle); got.Result != ResultUnavailable {
			t.Errorf("empty path result = %q, want unavailable", got.Result)
		}
		if got := v.VerifyBundle(context.Background(), blob, ""); got.Result != ResultUnavailable {
			t.Errorf("empty bundle result = %q, want unavailable", got.Result)
		}
	})
}

// The default identity regexp must pin the release workflow on a tag ref and
// reject everything else — it is the whole point of keyless signing.
func TestDefaultBlobIdentityRegexp(t *testing.T) {
	matches := []string{
		"https://github.com/sarv-projects/LiteSPM/.github/workflows/release.yml@refs/tags/v0.3.0",
		"https://github.com/sarv-projects/LiteSPM/.github/workflows/release.yml@refs/tags/v1.2.3-rc.1",
		// GitHub's OIDC claim may normalize the repo case differently from
		// the clone URL; repo names are case-insensitively unique, so this
		// cannot be a different repository.
		"https://github.com/sarv-projects/litespm/.github/workflows/release.yml@refs/tags/v0.3.0",
	}
	misses := []string{
		// A fork's workflow, same tag name.
		"https://github.com/attacker/LiteSPM/.github/workflows/release.yml@refs/tags/v0.3.0",
		// PR-triggered run.
		"https://github.com/sarv-projects/LiteSPM/.github/workflows/release.yml@refs/pull/12/merge",
		// A different workflow in the same repo.
		"https://github.com/sarv-projects/LiteSPM/.github/workflows/ci.yml@refs/tags/v0.3.0",
		// Branch ref, not a release tag.
		"https://github.com/sarv-projects/LiteSPM/.github/workflows/release.yml@refs/heads/main",
	}
	for _, s := range matches {
		if !regexpMatches(DefaultBlobIdentityRegexp, s) {
			t.Errorf("expected match: %s", s)
		}
	}
	for _, s := range misses {
		if regexpMatches(DefaultBlobIdentityRegexp, s) {
			t.Errorf("expected NO match: %s", s)
		}
	}
}
