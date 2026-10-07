package trust

// blob.go — verifying a detached Sigstore bundle over a release artifact.
//
// The release workflow signs every published asset with
// `cosign sign-blob --bundle` using its OIDC identity (keyless, no long-lived
// signing key). That gives the consumer something a checksum cannot: an
// IDENTITY. The checksum lives in the same release as the binary, so a
// compromised release channel forges both; a Fulcio certificate binds the
// signature to "this exact GitHub workflow, running on a v-tag ref of this
// repository", which an attacker who only compromised the artifact store
// cannot mint.
//
// Verification shells out to `cosign verify-blob` (same honesty rule as
// CosignVerifier: cosign absent or identity unconfigured is "unavailable",
// never a pass).

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const (
	// DefaultBundleSuffix is the asset-name suffix release.yml appends to
	// every artifact it signs.
	DefaultBundleSuffix = ".sigstore.json"

	// DefaultBlobIdentityRegexp pins the certificate SAN to this
	// repository's release workflow on a release tag. Forks, mirrors and PR
	// runs produce different SANs and therefore do not verify — by design.
	DefaultBlobIdentityRegexp = `^https://github\.com/sarv-projects/LiteSPM/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+[^/]*$`

	// DefaultBlobIssuer is the OIDC issuer that signs GitHub Actions'
	// federated identity.
	DefaultBlobIssuer = "https://token.actions.githubusercontent.com"
)

// BlobVerifier verifies a file against a detached Sigstore bundle written by
// `cosign sign-blob --bundle`. Zero values select the release workflow's
// default identity and issuer.
type BlobVerifier struct {
	// IdentityRegexp matches the certificate's SAN URI (Go RE2).
	IdentityRegexp string
	// Issuer matches the certificate's OIDC issuer claim.
	Issuer string
	// LookPath allows tests to stub binary discovery.
	LookPath func(name string) (string, error)
	// Run allows tests to stub the cosign invocation. When nil, the real
	// binary is executed with a bounded timeout.
	Run func(ctx context.Context, args []string) (combinedOutput []byte, err error)
}

// VerifyBundle checks path against the Sigstore bundle at bundlePath.
//
// Result semantics (package honesty rule): verified only on cosign exit 0;
// failed on a non-zero exit; unavailable when cosign is missing or no
// identity is configured — the caller decides whether unavailable may
// proceed or must fail closed.
func (v BlobVerifier) VerifyBundle(ctx context.Context, path, bundlePath string) Verdict {
	subject := path
	if strings.TrimSpace(path) == "" || strings.TrimSpace(bundlePath) == "" {
		return Verdict{Subject: subject, Method: "sigstore-bundle", Result: ResultUnavailable, Detail: "no file or bundle path to verify"}
	}
	look := v.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if _, err := look("cosign"); err != nil {
		return Verdict{Subject: subject, Method: "sigstore-bundle", Result: ResultUnavailable, Detail: "cosign binary not found; install cosign to verify release signatures"}
	}
	identity := v.IdentityRegexp
	if strings.TrimSpace(identity) == "" {
		identity = DefaultBlobIdentityRegexp
	}
	issuer := v.Issuer
	if strings.TrimSpace(issuer) == "" {
		issuer = DefaultBlobIssuer
	}
	if strings.TrimSpace(identity) == "" || strings.TrimSpace(issuer) == "" {
		return Verdict{Subject: subject, Method: "sigstore-bundle", Result: ResultUnavailable, Detail: "no expected identity/issuer configured; refusing to verify against an unknown signer"}
	}

	args := []string{
		"verify-blob",
		"--bundle", bundlePath,
		"--certificate-identity-regexp", identity,
		"--certificate-oidc-issuer", issuer,
		path,
	}
	run := v.Run
	if run == nil {
		run = defaultCosignRun
	}
	out, err := run(ctx, args)
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 500 {
			detail = detail[:500]
		}
		if detail == "" {
			detail = err.Error()
		}
		return Verdict{Subject: subject, Method: "sigstore-bundle", Result: ResultFailed, Detail: "cosign: " + detail}
	}
	return Verdict{Subject: subject, Method: "sigstore-bundle", Result: ResultVerified,
		Detail: fmt.Sprintf("bundle verified against identity %s", identity)}
}

// defaultCosignRun executes cosign with a bounded timeout.
func defaultCosignRun(ctx context.Context, args []string) ([]byte, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cosign", args...)
	return cmd.CombinedOutput()
}
