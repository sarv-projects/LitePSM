package update

import "time"

// ReleaseInfo holds metadata about an available release.
type ReleaseInfo struct {
	Version         string            `json:"version"`
	ReleaseURL      string            `json:"releaseUrl"`
	PublishedAt     time.Time         `json:"publishedAt"`
	ChecksumsSHA256 map[string]string `json:"checksumsSha256"` // filename -> sha256
	DownloadURLs    map[string]string `json:"downloadUrls"`    // filename -> url
	// BundleURLs maps an asset name to the Sigstore bundle release.yml
	// signed it with (`<asset>.sigstore.json`), when the release publishes
	// one. Absent on pre-signing releases; the consumer decides whether that
	// is acceptable (see LITESPM_REQUIRE_SIGNED_UPDATE).
	BundleURLs map[string]string `json:"bundleUrls,omitempty"`
}

// UpdateStatus describes update availability.
type UpdateStatus struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
}
