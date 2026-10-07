package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// limitsFor builds extraction limits with only the field under test changed.
func limitsFor(mutate func(*ExtractionLimits)) ExtractionLimits {
	l := DefaultExtractionLimits
	if mutate != nil {
		mutate(&l)
	}
	return l
}

// The tree-size bound is enforced for zip: an archive whose extracted bytes
// exceed MaxTreeSize is refused with the archive-slip error class before it
// can fill the disk.
func TestExtractZipHonorsMaxTreeSize(t *testing.T) {
	tempDir := t.TempDir()
	zipBytes := createTestZip(t, map[string][]byte{
		"big.bin": []byte(strings.Repeat("x", 4096)),
	})
	zipPath := filepath.Join(tempDir, "big.zip")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	limits := limitsFor(func(l *ExtractionLimits) { l.MaxTreeSize = 100 })
	_, err := ExtractFileSafely(zipPath, "zip", filepath.Join(tempDir, "staging"), limits)
	if err == nil {
		t.Fatal("expected the tree-size bound to refuse the archive")
	}
	if !strings.Contains(err.Error(), "total extracted size limit") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "LPSM-CAS-ARCHIVE-SLIP") {
		t.Errorf("error must carry the archive-slip class, got: %v", err)
	}
}

// Same bound for tar.gz.
func TestExtractTarGzHonorsMaxTreeSize(t *testing.T) {
	tempDir := t.TempDir()
	tarBytes := createTestTarGz(t, map[string][]byte{
		"big.bin": []byte(strings.Repeat("y", 4096)),
	})
	tarPath := filepath.Join(tempDir, "big.tar.gz")
	if err := os.WriteFile(tarPath, tarBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	limits := limitsFor(func(l *ExtractionLimits) { l.MaxTreeSize = 100 })
	_, err := ExtractFileSafely(tarPath, "tar.gz", filepath.Join(tempDir, "staging"), limits)
	if err == nil {
		t.Fatal("expected the tree-size bound to refuse the archive")
	}
	if !strings.Contains(err.Error(), "total extracted size limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

// A single oversized member is refused even when the tree total would fit.
func TestExtractZipHonorsMaxSingleFileSize(t *testing.T) {
	tempDir := t.TempDir()
	zipBytes := createTestZip(t, map[string][]byte{
		"member.bin": []byte(strings.Repeat("z", 4096)),
	})
	zipPath := filepath.Join(tempDir, "member.zip")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	limits := limitsFor(func(l *ExtractionLimits) { l.MaxSingleFileSize = 16 })
	_, err := ExtractFileSafely(zipPath, "zip", filepath.Join(tempDir, "staging"), limits)
	if err == nil {
		t.Fatal("expected the single-file bound to refuse the archive")
	}
	if !strings.Contains(err.Error(), "single file size limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The file-count bound stops decompression bombs one entry at a time.
func TestExtractZipHonorsMaxFileCount(t *testing.T) {
	tempDir := t.TempDir()
	files := map[string][]byte{}
	for i := 0; i < 10; i++ {
		files[filepath.Join("d", string(rune('a'+i))+".txt")] = []byte("x")
	}
	zipBytes := createTestZip(t, files)
	zipPath := filepath.Join(tempDir, "many.zip")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	limits := limitsFor(func(l *ExtractionLimits) { l.MaxFileCount = 3 })
	_, err := ExtractFileSafely(zipPath, "zip", filepath.Join(tempDir, "staging"), limits)
	if err == nil {
		t.Fatal("expected the file-count bound to refuse the archive")
	}
	if !strings.Contains(err.Error(), "max file count") {
		t.Errorf("unexpected error: %v", err)
	}
}
