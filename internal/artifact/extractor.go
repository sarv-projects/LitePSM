package artifact

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
)

const (
	// DefaultMaxArchiveSize is the maximum permitted size for compressed archives (256 MiB).
	DefaultMaxArchiveSize int64 = 256 * 1024 * 1024

	// DefaultMaxTreeSize is the maximum cumulative extracted size (1 GiB).
	DefaultMaxTreeSize int64 = 1024 * 1024 * 1024

	// DefaultMaxFileCount is the maximum total number of files in an archive.
	DefaultMaxFileCount int = 20000

	// DefaultMaxSingleFileSize is the maximum size for an individual file (128 MiB).
	DefaultMaxSingleFileSize int64 = 128 * 1024 * 1024

	// DefaultMaxPathLength is the maximum length of a relative file path.
	DefaultMaxPathLength int = 1024
)

// ExtractionLimits defines resource constraints during artifact decompression.
type ExtractionLimits struct {
	MaxArchiveSize    int64
	MaxTreeSize       int64
	MaxFileCount      int
	MaxSingleFileSize int64
	MaxPathLength     int
}

// DefaultExtractionLimits provides production default limits.
var DefaultExtractionLimits = ExtractionLimits{
	MaxArchiveSize:    DefaultMaxArchiveSize,
	MaxTreeSize:       DefaultMaxTreeSize,
	MaxFileCount:      DefaultMaxFileCount,
	MaxSingleFileSize: DefaultMaxSingleFileSize,
	MaxPathLength:     DefaultMaxPathLength,
}

// SpoolDownloadBounded streams data from r into tempFile while enforcing maxBytes limit.
// It returns total bytes written, the computed sha256 digest ("sha256:..."), and any error.
func SpoolDownloadBounded(r io.Reader, maxBytes int64, tempFile *os.File) (int64, string, error) {
	if maxBytes <= 0 || maxBytes > DefaultMaxArchiveSize {
		maxBytes = DefaultMaxArchiveSize
	}

	hasher := sha256.New()
	limitedReader := io.LimitReader(r, maxBytes+1)
	writer := io.MultiWriter(tempFile, hasher)

	written, err := io.Copy(writer, limitedReader)
	if err != nil {
		return 0, "", fmt.Errorf("failed during spooling: %w", err)
	}

	if written > maxBytes {
		return 0, "", domain.ErrArchiveSlip(fmt.Sprintf("archive exceeds maximum download limit of %d bytes", maxBytes))
	}

	digest := fmt.Sprintf("sha256:%s", hex.EncodeToString(hasher.Sum(nil)))
	return written, digest, nil
}

// ExtractFileSafely opens the archive file on disk and extracts it using specified limits.
func ExtractFileSafely(archivePath string, archiveType string, stagingDir string, limits ExtractionLimits) (*domain.ExtractedTreeInfo, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive %s: %w", archivePath, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat archive %s: %w", archivePath, err)
	}

	maxArchive := limits.MaxArchiveSize
	if maxArchive <= 0 {
		maxArchive = DefaultMaxArchiveSize
	}
	if stat.Size() > maxArchive {
		return nil, domain.ErrArchiveSlip(fmt.Sprintf("archive file size %d exceeds limit of %d", stat.Size(), maxArchive))
	}

	return ExtractArchiveSafelyWithLimits(file, stat.Size(), archiveType, stagingDir, limits)
}

// ExtractArchiveSafely extracts a ZIP or TAR.GZ archive safely using default limits.
func ExtractArchiveSafely(r io.ReaderAt, size int64, archiveType string, stagingDir string) (*domain.ExtractedTreeInfo, error) {
	return ExtractArchiveSafelyWithLimits(r, size, archiveType, stagingDir, DefaultExtractionLimits)
}

// ExtractArchiveSafelyWithLimits extracts a ZIP or TAR.GZ archive safely with custom limits.
func ExtractArchiveSafelyWithLimits(r io.ReaderAt, size int64, archiveType string, stagingDir string, limits ExtractionLimits) (*domain.ExtractedTreeInfo, error) {
	cleanStaging := filepath.Clean(stagingDir)
	if err := os.MkdirAll(cleanStaging, 0700); err != nil {
		return nil, fmt.Errorf("failed to create staging directory %s: %w", cleanStaging, err)
	}

	if limits.MaxFileCount <= 0 {
		limits.MaxFileCount = DefaultMaxFileCount
	}
	if limits.MaxSingleFileSize <= 0 {
		limits.MaxSingleFileSize = DefaultMaxSingleFileSize
	}
	if limits.MaxTreeSize <= 0 {
		limits.MaxTreeSize = DefaultMaxTreeSize
	}
	if limits.MaxPathLength <= 0 {
		limits.MaxPathLength = DefaultMaxPathLength
	}

	switch strings.ToLower(archiveType) {
	case "zip", "application/zip", ".zip":
		return extractZip(r, size, cleanStaging, limits)
	case "tar.gz", "tgz", "application/gzip", ".tar.gz", ".tgz", "npm", "pypi":
		return extractTarGz(r, size, cleanStaging, limits)
	default:
		// Attempt zip first, fallback to tar.gz
		if info, err := extractZip(r, size, cleanStaging, limits); err == nil {
			return info, nil
		}
		return extractTarGz(r, size, cleanStaging, limits)
	}
}

func extractZip(r io.ReaderAt, size int64, stagingDir string, limits ExtractionLimits) (*domain.ExtractedTreeInfo, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("invalid or corrupt zip archive: %w", err)
	}

	var totalBytes int64
	var fileCount int
	seenLower := make(map[string]string)

	for _, f := range zr.File {
		fileCount++
		if fileCount > limits.MaxFileCount {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("archive exceeds max file count limit of %d", limits.MaxFileCount))
		}

		relPath, isDir, err := validateAndNormalizePath(f.Name, limits.MaxPathLength, seenLower)
		if err != nil {
			return nil, err
		}

		// Prohibit symlinks, named pipes, devices
		if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeNamedPipe != 0 || f.Mode()&os.ModeDevice != 0 || f.Mode()&os.ModeSocket != 0 {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("prohibited file type detected in archive: %s (mode: %v)", f.Name, f.Mode()))
		}

		targetPath := filepath.Join(stagingDir, filepath.FromSlash(relPath))
		if !strings.HasPrefix(targetPath, stagingDir+string(filepath.Separator)) && targetPath != stagingDir {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("path traversal escape: %s", f.Name))
		}

		if isDir || f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return nil, err
			}
			continue
		}

		// Enforce single file size limit
		if int64(f.UncompressedSize64) > limits.MaxSingleFileSize {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("file %s exceeds single file size limit (%d bytes)", f.Name, limits.MaxSingleFileSize))
		}

		totalBytes += int64(f.UncompressedSize64)
		if totalBytes > limits.MaxTreeSize {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("archive exceeds total extracted size limit (%d bytes)", limits.MaxTreeSize))
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return nil, err
		}

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}

		if err := writeBoundedFile(rc, targetPath, int64(f.UncompressedSize64), limits.MaxSingleFileSize); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
	}

	treeDigest, err := ComputeCanonicalTreeDigest(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("failed to compute tree digest: %w", err)
	}

	return &domain.ExtractedTreeInfo{
		FileCount:  fileCount,
		TotalBytes: totalBytes,
		TreeDigest: treeDigest,
		RootPath:   stagingDir,
	}, nil
}

func extractTarGz(r io.ReaderAt, size int64, stagingDir string, limits ExtractionLimits) (*domain.ExtractedTreeInfo, error) {
	sectionReader := io.NewSectionReader(r, 0, size)
	gzr, err := gzip.NewReader(sectionReader)
	if err != nil {
		return nil, fmt.Errorf("invalid or corrupt gzip stream: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var totalBytes int64
	var fileCount int
	seenLower := make(map[string]string)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read error: %w", err)
		}

		fileCount++
		if fileCount > limits.MaxFileCount {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("archive exceeds max file count limit of %d", limits.MaxFileCount))
		}

		// Strip optional top-level "package/" prefix common in npm tarballs
		cleanName := hdr.Name
		if strings.HasPrefix(cleanName, "package/") {
			cleanName = strings.TrimPrefix(cleanName, "package/")
		}
		if cleanName == "" {
			continue
		}

		relPath, isDir, err := validateAndNormalizePath(cleanName, limits.MaxPathLength, seenLower)
		if err != nil {
			return nil, err
		}

		// Prohibit symlinks, hardlinks, FIFOs, devices
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink || hdr.Typeflag == tar.TypeFifo || hdr.Typeflag == tar.TypeChar || hdr.Typeflag == tar.TypeBlock {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("prohibited tar entry type (%c) for %s", hdr.Typeflag, hdr.Name))
		}

		targetPath := filepath.Join(stagingDir, filepath.FromSlash(relPath))
		if !strings.HasPrefix(targetPath, stagingDir+string(filepath.Separator)) && targetPath != stagingDir {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("path traversal escape: %s", hdr.Name))
		}

		if isDir || hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return nil, err
			}
			continue
		}

		if hdr.Size > limits.MaxSingleFileSize {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("file %s exceeds single file size limit (%d bytes)", hdr.Name, limits.MaxSingleFileSize))
		}

		totalBytes += hdr.Size
		if totalBytes > limits.MaxTreeSize {
			return nil, domain.ErrArchiveSlip(fmt.Sprintf("archive exceeds total extracted size limit (%d bytes)", limits.MaxTreeSize))
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return nil, err
		}

		if err := writeBoundedFile(tr, targetPath, hdr.Size, limits.MaxSingleFileSize); err != nil {
			return nil, err
		}
	}

	treeDigest, err := ComputeCanonicalTreeDigest(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("failed to compute tree digest: %w", err)
	}

	return &domain.ExtractedTreeInfo{
		FileCount:  fileCount,
		TotalBytes: totalBytes,
		TreeDigest: treeDigest,
		RootPath:   stagingDir,
	}, nil
}

func validateAndNormalizePath(name string, maxPathLength int, seenLower map[string]string) (string, bool, error) {
	if len(name) > maxPathLength {
		return "", false, domain.ErrArchiveSlip(fmt.Sprintf("path length exceeds %d bytes: %s", maxPathLength, name))
	}

	isDir := strings.HasSuffix(name, "/") || strings.HasSuffix(name, "\\")

	// Convert backslashes to forward slashes and clean
	normalized := filepath.ToSlash(name)
	cleaned := filepath.Clean(normalized)

	// Reject absolute paths, relative escapes, drive letters, and root-anchored paths cross-platform
	if filepath.IsAbs(cleaned) ||
		filepath.IsAbs(normalized) ||
		strings.HasPrefix(normalized, "/") ||
		strings.HasPrefix(normalized, "\\") ||
		strings.HasPrefix(cleaned, "/") ||
		strings.HasPrefix(cleaned, "\\") ||
		strings.HasPrefix(cleaned, "../") ||
		strings.HasPrefix(cleaned, "..\\") ||
		cleaned == ".." ||
		strings.Contains(cleaned, ":") ||
		filepath.VolumeName(cleaned) != "" ||
		filepath.VolumeName(normalized) != "" {
		return "", false, domain.ErrArchiveSlip(fmt.Sprintf("illegal path or path traversal: %s", name))
	}

	// Case-fold collision check (only for regular files, ignoring trailing slash dirs)
	if !isDir {
		lower := strings.ToLower(cleaned)
		if orig, exists := seenLower[lower]; exists && orig != cleaned {
			return "", false, domain.ErrArchiveSlip(fmt.Sprintf("case-fold collision detected between %q and %q", cleaned, orig))
		}
		seenLower[lower] = cleaned
	}

	return cleaned, isDir, nil
}

func writeBoundedFile(r io.Reader, targetPath string, expectedSize int64, maxSingleSize int64) error {
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", targetPath, err)
	}
	defer out.Close()

	limit := expectedSize + 1
	if limit > maxSingleSize+1 {
		limit = maxSingleSize + 1
	}

	written, err := io.Copy(out, io.LimitReader(r, limit))
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", targetPath, err)
	}

	if written > maxSingleSize {
		return domain.ErrArchiveSlip(fmt.Sprintf("file %s exceeded single file limit during extraction", targetPath))
	}

	return nil
}

// ComputeCanonicalTreeDigest traverses treeDir, sort-orders all relative paths,
// and hashes (relative_path + file_sha256 + size) into a single deterministic Merkle digest.
func ComputeCanonicalTreeDigest(treeDir string) (string, error) {
	cleanDir := filepath.Clean(treeDir)

	type fileMeta struct {
		relPath string
		digest  string
		size    int64
	}

	var files []fileMeta

	err := filepath.Walk(cleanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(cleanDir, path)
		if err != nil {
			return err
		}
		normRel := filepath.ToSlash(rel)

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			return err
		}

		digest := hex.EncodeToString(hasher.Sum(nil))
		files = append(files, fileMeta{
			relPath: normRel,
			digest:  digest,
			size:    info.Size(),
		})
		return nil
	})

	if err != nil {
		return "", err
	}

	// Sort files strictly by normalized relative path
	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	treeHasher := sha256.New()
	for _, f := range files {
		entryLine := fmt.Sprintf("%s:%s:%d\n", f.relPath, f.digest, f.size)
		treeHasher.Write([]byte(entryLine))
	}

	return fmt.Sprintf("sha256:%s", hex.EncodeToString(treeHasher.Sum(nil))), nil
}
