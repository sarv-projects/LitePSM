package catalogbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

// ReleaseManifestFile describes an immutable file entry within a release manifest.
type ReleaseManifestFile struct {
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // sha256:<hex>
}

// ReleaseManifest represents the immutable root manifest of a release.
// It strictly adheres to schemas/catalog-release.schema.json.
type ReleaseManifest struct {
	SchemaVersion   int                            `json:"schemaVersion"`
	ReleaseID       string                         `json:"releaseId"`
	CreatedAt       string                         `json:"createdAt"`
	ItemCount       int                            `json:"itemCount"`
	ContentDigest   string                         `json:"contentDigest"`
	Status          string                         `json:"status"`
	SourceSnapshots []string                       `json:"sourceSnapshots,omitempty"`
	Files           map[string]ReleaseManifestFile `json:"files"`
}

// CurrentPointer represents /v1/current.json pointing to the latest verified release.
type CurrentPointer struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ReleaseID      string `json:"releaseId"`
	Sequence       int    `json:"sequence"`
	ManifestDigest string `json:"manifestDigest"` // sha256:<hex> of manifest.json
	ItemCount      int    `json:"itemCount"`
	CreatedAt      string `json:"createdAt"`
}

// BuildOutput contains all generated release files keyed by their relative path.
type BuildOutput struct {
	ReleaseID      string
	ManifestDigest string
	Manifest       *ReleaseManifest
	Current        *CurrentPointer
	Files          map[string][]byte // relative path -> canonical byte content
}

// CompileRelease compiles normalized domain listings and version records into a deterministic static release.
func CompileRelease(
	releaseID string,
	sequence int,
	sourceSnapshots []string,
	listings []*domain.Listing,
	versions []*domain.VersionRecord,
	createdAt time.Time,
) (*BuildOutput, error) {
	if releaseID == "" {
		return nil, fmt.Errorf("releaseID cannot be empty")
	}

	createdAtISO := createdAt.UTC().Format(time.RFC3339)
	outputFiles := make(map[string][]byte)
	manifestFiles := make(map[string]ReleaseManifestFile)

	// 1. Sort listings lexicographically by ID for determinism
	sortedListings := make([]*domain.Listing, len(listings))
	copy(sortedListings, listings)
	sort.Slice(sortedListings, func(i, j int) bool {
		return sortedListings[i].ID < sortedListings[j].ID
	})

	// 2. Sort versions by ListingID + Version
	sortedVersions := make([]*domain.VersionRecord, len(versions))
	copy(sortedVersions, versions)
	sort.Slice(sortedVersions, func(i, j int) bool {
		if sortedVersions[i].ListingID == sortedVersions[j].ListingID {
			return sortedVersions[i].Version < sortedVersions[j].Version
		}
		return sortedVersions[i].ListingID < sortedVersions[j].ListingID
	})

	// 3. Serialize listings.json
	rawListingsJSON, err := json.Marshal(sortedListings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal listings: %w", err)
	}
	canonicalListings, err := domain.CanonicalizeJSON(rawListingsJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize listings: %w", err)
	}
	// Release files live under the same /v1 API namespace as the pointer that
	// names them, so a client that fetched <base>/v1/current.json resolves every
	// relative path below <base>/v1/. ARCH/18 §2 and the CDN cache policy both
	// specify /v1/releases/; this path must not drift from them.
	listingsPath := fmt.Sprintf("v1/releases/%s/listings.json", releaseID)
	outputFiles[listingsPath] = canonicalListings
	manifestFiles["listings.json"] = ReleaseManifestFile{
		Size:   int64(len(canonicalListings)),
		Digest: domain.ComputeBytesDigest(canonicalListings),
	}

	// 4. Serialize versions.json
	rawVersionsJSON, err := json.Marshal(sortedVersions)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal versions: %w", err)
	}
	canonicalVersions, err := domain.CanonicalizeJSON(rawVersionsJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize versions: %w", err)
	}
	versionsPath := fmt.Sprintf("v1/releases/%s/versions.json", releaseID)
	outputFiles[versionsPath] = canonicalVersions
	manifestFiles["versions.json"] = ReleaseManifestFile{
		Size:   int64(len(canonicalVersions)),
		Digest: domain.ComputeBytesDigest(canonicalVersions),
	}

	// 5. Compute contentDigest over combined files
	contentHasher := sha256.New()
	contentHasher.Write(canonicalListings)
	contentHasher.Write(canonicalVersions)
	contentDigest := fmt.Sprintf("sha256:%s", hex.EncodeToString(contentHasher.Sum(nil)))

	// 6. Build Manifest
	manifest := &ReleaseManifest{
		SchemaVersion:   1,
		ReleaseID:       releaseID,
		CreatedAt:       createdAtISO,
		ItemCount:       len(sortedListings),
		ContentDigest:   contentDigest,
		Status:          "active",
		SourceSnapshots: sourceSnapshots,
		Files:           manifestFiles,
	}

	rawManifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	canonicalManifest, err := domain.CanonicalizeJSON(rawManifestJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize manifest: %w", err)
	}
	manifestPath := fmt.Sprintf("v1/releases/%s/manifest.json", releaseID)
	outputFiles[manifestPath] = canonicalManifest
	manifestDigest := domain.ComputeBytesDigest(canonicalManifest)

	// 7. Build current.json
	current := &CurrentPointer{
		SchemaVersion:  1,
		ReleaseID:      releaseID,
		Sequence:       sequence,
		ManifestDigest: manifestDigest,
		ItemCount:      len(sortedListings),
		CreatedAt:      createdAtISO,
	}

	rawCurrentJSON, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal current: %w", err)
	}
	canonicalCurrent, err := domain.CanonicalizeJSON(rawCurrentJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize current: %w", err)
	}
	outputFiles["v1/current.json"] = canonicalCurrent

	return &BuildOutput{
		ReleaseID:      releaseID,
		ManifestDigest: manifestDigest,
		Manifest:       manifest,
		Current:        current,
		Files:          outputFiles,
	}, nil
}

// WriteToDirectory writes all build output files to the target directory.
func (b *BuildOutput) WriteToDirectory(baseDir string) error {
	for relPath, content := range b.Files {
		fullPath := filepath.Join(baseDir, relPath)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write release file %s: %w", fullPath, err)
		}
	}
	return nil
}
