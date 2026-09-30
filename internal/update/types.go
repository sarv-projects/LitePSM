package update

import "time"

// ReleaseInfo holds metadata about an available release.
type ReleaseInfo struct {
	Version        string            `json:"version"`
	ReleaseURL     string            `json:"releaseUrl"`
	PublishedAt    time.Time         `json:"publishedAt"`
	ChecksumsSHA256 map[string]string `json:"checksumsSha256"` // filename -> sha256
	DownloadURLs   map[string]string `json:"downloadUrls"`     // filename -> url
}

// UpdateStatus describes update availability.
type UpdateStatus struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
}
