// Package lockfile implements the deterministic LiteSPM lockfile (ARCH/32
// §3–§4): litespm.lock is generated from litespm.toml, byte-for-byte
// reproducible, sufficient for a frozen install with no network resolution,
// and content-addressed by sha256 digests throughout.
//
// Honesty rule: signature/provenance fields default to "none"/"unavailable"
// until a real verifier produces a result; no green value is fabricated.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sarv-projects/litespm/internal/manifest"
)

// Versions.
const (
	LockVersion   = 1
	SchemaVersion = 1
)

// LockFileName is the generated lockfile name.
const LockFileName = "litespm.lock"

// Signature schemes.
const (
	SchemeNone          = "none"
	ResultUnavailable   = "unavailable"
	ResultVerified      = "verified"
	ResultFailed        = "failed"
	LicenseNoAssertion  = "NOASSERTION"
)

// LockedEntry is one resolved capability, mirroring ARCH/32 §3.
type LockedEntry struct {
	ID             string   `json:"id"`
	Components     []string `json:"components,omitempty"`
	SourceIdentity string   `json:"sourceIdentity,omitempty"`
	SourceURL      string   `json:"sourceUrl,omitempty"`
	Constraint     string   `json:"constraint,omitempty"`
	Version        string   `json:"version"`
	Commit         string   `json:"commit,omitempty"`
	ArtifactType   string   `json:"artifactType,omitempty"`
	ArtifactLocator string  `json:"artifactLocator,omitempty"`
	ArtifactSHA256 string   `json:"artifactSha256,omitempty"`
	ArtifactSize   int64    `json:"artifactSize,omitempty"`
	TreeDigest     string   `json:"treeDigest,omitempty"`
	PublisherName  string   `json:"publisherName,omitempty"`
	SignatureScheme string  `json:"signatureScheme"`
	SignatureResult string  `json:"signatureResult"`
	License        string   `json:"license"`
	Transport      string   `json:"transport,omitempty"`
	PolicyDigest   string   `json:"policyDigest,omitempty"`
	PlanDigest     string   `json:"planDigest,omitempty"`
	Targets        []string `json:"targets,omitempty"`
}

// Lock is the parsed lockfile.
type Lock struct {
	LockVersion    int           `json:"lockVersion"`
	SchemaVersion  int           `json:"schemaVersion"`
	ManifestDigest string        `json:"manifestDigest"`
	Entries        []LockedEntry `json:"resolved"`
	LockDigest     string        `json:"lockDigest"`
	SourcePath     string        `json:"-"`
}

// Resolver maps a manifest requirement to a fully resolved entry. The
// production implementation queries the local catalog cache; tests inject a
// stub. Returning an error fails the freeze closed.
type Resolver func(id, constraint string) (LockedEntry, error)

// Freeze resolves every manifest requirement and returns a deterministic
// lock: entries sorted by canonical ID, digests computed over the canonical
// JSON projection (no timestamps, no map iteration).
func Freeze(m *manifest.Manifest, resolve Resolver) (*Lock, error) {
	if m == nil {
		return nil, fmt.Errorf("LPSM-LOCK-NIL-MANIFEST: manifest is required")
	}
	l := &Lock{
		LockVersion:    LockVersion,
		SchemaVersion:  SchemaVersion,
		ManifestDigest: DigestString(m.MarshalCanonical()),
	}
	for _, r := range m.Requires {
		e, err := resolve(r.ID, r.Constraint)
		if err != nil {
			return nil, fmt.Errorf("LPSM-LOCK-UNRESOLVED: %s (%s): %w", r.ID, r.Constraint, err)
		}
		if e.ID == "" {
			e.ID = r.ID
		}
		if e.ID != r.ID {
			return nil, fmt.Errorf("LPSM-LOCK-ID-MISMATCH: resolver returned %q for %q", e.ID, r.ID)
		}
		e.Constraint = r.Constraint
		e.Targets = append([]string(nil), r.Targets...)
		if e.SignatureScheme == "" {
			e.SignatureScheme = SchemeNone
		}
		if e.SignatureResult == "" {
			e.SignatureResult = ResultUnavailable
		}
		if e.License == "" {
			e.License = LicenseNoAssertion
		}
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].ID < l.Entries[j].ID })
	l.LockDigest = l.computeDigest()
	return l, nil
}

func (l *Lock) computeDigest() string {
	proj := map[string]any{
		"lockVersion":    l.LockVersion,
		"manifestDigest": l.ManifestDigest,
		"resolved":       l.Entries,
		"schemaVersion":  l.SchemaVersion,
	}
	return DigestString(canonicalJSON(proj))
}

// Verify recomputes the lock digest and reports drift.
func (l *Lock) Verify() error {
	if want := l.computeDigest(); want != l.LockDigest {
		return fmt.Errorf("LPSM-LOCK-DRIFT: lock digest mismatch (committed %s, recomputed %s)", l.LockDigest, want)
	}
	return nil
}

// CheckManifest reports whether the lock is stale relative to the manifest.
func (l *Lock) CheckManifest(m *manifest.Manifest) error {
	want := DigestString(m.MarshalCanonical())
	if want != l.ManifestDigest {
		return fmt.Errorf("LPSM-LOCK-DRIFT: manifest changed since lock (lock records %s, manifest is %s); run `litespm lock`", l.ManifestDigest, want)
	}
	return l.Verify()
}

// Lookup returns the locked entry for id.
func (l *Lock) Lookup(id string) (LockedEntry, bool) {
	for _, e := range l.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return LockedEntry{}, false
}

// Load reads a lockfile from path and verifies its digest.
func Load(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LPSM-LOCK-READ: %s: %w", path, err)
	}
	l, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("LPSM-LOCK-PARSE: %s: %w", path, err)
	}
	l.SourcePath = path
	if err := l.Verify(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// WriteFile writes the lock in canonical TOML form.
func (l *Lock) WriteFile(path string) error {
	return os.WriteFile(path, []byte(l.MarshalCanonical()), 0o644)
}

// MarshalCanonical renders the lock deterministically.
func (l *Lock) MarshalCanonical() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — generated by `litespm lock`; do not edit by hand\n", LockFileName)
	fmt.Fprintf(&sb, "lockVersion = %d\n", l.LockVersion)
	fmt.Fprintf(&sb, "schemaVersion = %d\n", l.SchemaVersion)
	fmt.Fprintf(&sb, "manifestDigest = %s\n", strconv_quote(l.ManifestDigest))
	for _, e := range l.Entries {
		sb.WriteString("\n[[resolved]]\n")
		fmt.Fprintf(&sb, "id = %s\n", strconv_quote(e.ID))
		for _, c := range e.Components {
			fmt.Fprintf(&sb, "component = %s\n", strconv_quote(c))
		}
		if e.SourceIdentity != "" {
			fmt.Fprintf(&sb, "sourceIdentity = %s\n", strconv_quote(e.SourceIdentity))
		}
		if e.SourceURL != "" {
			fmt.Fprintf(&sb, "sourceUrl = %s\n", strconv_quote(e.SourceURL))
		}
		if e.Constraint != "" {
			fmt.Fprintf(&sb, "constraint = %s\n", strconv_quote(e.Constraint))
		}
		fmt.Fprintf(&sb, "version = %s\n", strconv_quote(e.Version))
		if e.Commit != "" {
			fmt.Fprintf(&sb, "commit = %s\n", strconv_quote(e.Commit))
		}
		if e.ArtifactType != "" {
			fmt.Fprintf(&sb, "artifactType = %s\n", strconv_quote(e.ArtifactType))
		}
		if e.ArtifactLocator != "" {
			fmt.Fprintf(&sb, "artifactLocator = %s\n", strconv_quote(e.ArtifactLocator))
		}
		if e.ArtifactSHA256 != "" {
			fmt.Fprintf(&sb, "artifactSha256 = %s\n", strconv_quote(e.ArtifactSHA256))
		}
		if e.ArtifactSize != 0 {
			fmt.Fprintf(&sb, "artifactSize = %d\n", e.ArtifactSize)
		}
		if e.TreeDigest != "" {
			fmt.Fprintf(&sb, "treeDigest = %s\n", strconv_quote(e.TreeDigest))
		}
		if e.PublisherName != "" {
			fmt.Fprintf(&sb, "publisherName = %s\n", strconv_quote(e.PublisherName))
		}
		fmt.Fprintf(&sb, "signatureScheme = %s\n", strconv_quote(e.SignatureScheme))
		fmt.Fprintf(&sb, "signatureResult = %s\n", strconv_quote(e.SignatureResult))
		fmt.Fprintf(&sb, "license = %s\n", strconv_quote(e.License))
		if e.Transport != "" {
			fmt.Fprintf(&sb, "transport = %s\n", strconv_quote(e.Transport))
		}
		if e.PolicyDigest != "" {
			fmt.Fprintf(&sb, "policyDigest = %s\n", strconv_quote(e.PolicyDigest))
		}
		if e.PlanDigest != "" {
			fmt.Fprintf(&sb, "planDigest = %s\n", strconv_quote(e.PlanDigest))
		}
		for _, t := range e.Targets {
			fmt.Fprintf(&sb, "target = %s\n", strconv_quote(t))
		}
	}
	fmt.Fprintf(&sb, "\nlockDigest = %s\n", strconv_quote(l.LockDigest))
	return sb.String()
}

// DigestString returns "sha256:<hex>" over s.
func DigestString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DigestBytes returns "sha256:<hex>" over b.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalJSON renders v with sorted map keys (encoding/json already sorts
// map keys; structs marshal in field order, which is fixed here).
func canonicalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
