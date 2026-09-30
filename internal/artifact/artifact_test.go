package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("failed to write zip content %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

func createTestTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("failed to write tar content %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestSpoolDownloadBounded(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("valid small payload within bounds", func(t *testing.T) {
		data := []byte("hello world payload")
		destPath := filepath.Join(tempDir, "download_1.bin")
		destFile, err := os.Create(destPath)
		if err != nil {
			t.Fatal(err)
		}
		defer destFile.Close()

		reader := bytes.NewReader(data)
		hash := sha256.Sum256(data)
		expectedDigest := "sha256:" + hex.EncodeToString(hash[:])

		written, digest, err := SpoolDownloadBounded(reader, 1024, destFile)
		if err != nil {
			t.Fatalf("unexpected spool error: %v", err)
		}
		if written != int64(len(data)) {
			t.Errorf("expected %d bytes, got %d", len(data), written)
		}
		if digest != expectedDigest {
			t.Errorf("expected digest %s, got %s", expectedDigest, digest)
		}
	})

	t.Run("payload exceeding byte limit rejected", func(t *testing.T) {
		data := bytes.Repeat([]byte("A"), 2000)
		destPath := filepath.Join(tempDir, "download_toolarge.bin")
		destFile, err := os.Create(destPath)
		if err != nil {
			t.Fatal(err)
		}
		defer destFile.Close()

		reader := bytes.NewReader(data)
		_, _, err = SpoolDownloadBounded(reader, 1000, destFile)
		if err == nil {
			t.Fatal("expected max size error, got nil")
		}
		if !strings.Contains(err.Error(), "LPSM-ARTIFACT-PATH-TRAVERSAL") && !strings.Contains(err.Error(), "exceeds maximum download limit") {
			t.Errorf("expected download limit error, got: %v", err)
		}
	})
}

func TestExtractArchiveSafely_Zip(t *testing.T) {
	t.Run("valid zip extraction and tree digest computation", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"index.js":     []byte("console.log('hello');\n"),
			"package.json": []byte("{\"name\": \"test-tool\", \"version\": \"1.0.0\"}\n"),
			"lib/util.js":  []byte("module.exports = { add: (a, b) => a + b };\n"),
		})

		zipPath := filepath.Join(tempDir, "test.zip")
		if err := os.WriteFile(zipPath, zipBytes, 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ExtractFileSafely(zipPath, "zip", stagingDir, DefaultExtractionLimits)
		if err != nil {
			t.Fatalf("failed to extract valid zip: %v", err)
		}

		if info.FileCount != 3 {
			t.Errorf("expected 3 files, got %d", info.FileCount)
		}
		if !strings.HasPrefix(info.TreeDigest, "sha256:") {
			t.Errorf("expected valid sha256 tree digest, got: %s", info.TreeDigest)
		}

		// Verify files exist in staging
		idxData, err := os.ReadFile(filepath.Join(stagingDir, "index.js"))
		if err != nil || string(idxData) != "console.log('hello');\n" {
			t.Errorf("index.js content mismatch")
		}

		utilData, err := os.ReadFile(filepath.Join(stagingDir, "lib", "util.js"))
		if err != nil || !strings.Contains(string(utilData), "module.exports") {
			t.Errorf("lib/util.js content mismatch")
		}
	})

	t.Run("zip slip path traversal relative rejected", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"../../evil.txt": []byte("evil content"),
		})
		zipPath := filepath.Join(tempDir, "slip.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		_, err := ExtractFileSafely(zipPath, "zip", stagingDir, DefaultExtractionLimits)
		if err == nil {
			t.Fatal("expected zip-slip error, got nil")
		}
		if !strings.Contains(err.Error(), "LPSM-CAS-ARCHIVE-SLIP") {
			t.Errorf("expected LPSM-CAS-ARCHIVE-SLIP error, got %v", err)
		}
	})

	t.Run("zip absolute path traversal rejected", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"/etc/passwd": []byte("root:x:0:0"),
		})
		zipPath := filepath.Join(tempDir, "abs.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		_, err := ExtractFileSafely(zipPath, "zip", stagingDir, DefaultExtractionLimits)
		if err == nil {
			t.Fatal("expected path traversal error, got nil")
		}
		if !strings.Contains(err.Error(), "LPSM-CAS-ARCHIVE-SLIP") {
			t.Errorf("expected LPSM-CAS-ARCHIVE-SLIP error, got %v", err)
		}
	})

	t.Run("case-fold collision rejected", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"file.txt": []byte("lowercase"),
			"FILE.TXT": []byte("UPPERCASE"),
		})
		zipPath := filepath.Join(tempDir, "collision.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		_, err := ExtractFileSafely(zipPath, "zip", stagingDir, DefaultExtractionLimits)
		if err == nil {
			t.Fatal("expected collision error, got nil")
		}
		if !strings.Contains(err.Error(), "LPSM-CAS-ARCHIVE-SLIP") || !strings.Contains(err.Error(), "case-fold collision") {
			t.Errorf("expected case-fold collision error, got %v", err)
		}
	})

	t.Run("max file count exceeded", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"f1.txt": []byte("1"),
			"f2.txt": []byte("2"),
			"f3.txt": []byte("3"),
		})
		zipPath := filepath.Join(tempDir, "count.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		limits := DefaultExtractionLimits
		limits.MaxFileCount = 2

		_, err := ExtractFileSafely(zipPath, "zip", stagingDir, limits)
		if err == nil {
			t.Fatal("expected max file count error, got nil")
		}
		if !strings.Contains(err.Error(), "exceeds max file count limit") {
			t.Errorf("expected max file count error, got %v", err)
		}
	})

	t.Run("single file size limit exceeded", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		zipBytes := createTestZip(t, map[string][]byte{
			"big.txt": bytes.Repeat([]byte("Z"), 5000),
		})
		zipPath := filepath.Join(tempDir, "single_big.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		limits := DefaultExtractionLimits
		limits.MaxSingleFileSize = 1000

		_, err := ExtractFileSafely(zipPath, "zip", stagingDir, limits)
		if err == nil {
			t.Fatal("expected single file size limit error, got nil")
		}
		if !strings.Contains(err.Error(), "exceeds single file size limit") {
			t.Errorf("expected single file size limit error, got %v", err)
		}
	})
}

func TestExtractArchiveSafely_TarGz(t *testing.T) {
	t.Run("valid tar.gz extraction", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		tarBytes := createTestTarGz(t, map[string][]byte{
			"server.py": []byte("print('server running')\n"),
			"README.md": []byte("# Test Server\n"),
		})
		tarPath := filepath.Join(tempDir, "test.tar.gz")
		_ = os.WriteFile(tarPath, tarBytes, 0644)

		info, err := ExtractFileSafely(tarPath, "tar.gz", stagingDir, DefaultExtractionLimits)
		if err != nil {
			t.Fatalf("failed to extract tar.gz: %v", err)
		}
		if info.FileCount != 2 {
			t.Errorf("expected 2 files, got %d", info.FileCount)
		}
		if !strings.HasPrefix(info.TreeDigest, "sha256:") {
			t.Errorf("expected valid sha256 tree digest, got %s", info.TreeDigest)
		}
	})

	t.Run("symlink in tar.gz rejected", func(t *testing.T) {
		tempDir := t.TempDir()
		stagingDir := filepath.Join(tempDir, "staging")

		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		hdr := &tar.Header{
			Name:     "symlink_file",
			Typeflag: tar.TypeSymlink,
			Linkname: "/etc/passwd",
		}
		_ = tw.WriteHeader(hdr)
		_ = tw.Close()
		_ = gw.Close()

		tarPath := filepath.Join(tempDir, "symlink.tar.gz")
		_ = os.WriteFile(tarPath, buf.Bytes(), 0644)

		_, err := ExtractFileSafely(tarPath, "tar.gz", stagingDir, DefaultExtractionLimits)
		if err == nil {
			t.Fatal("expected symlink rejection error, got nil")
		}
		if !strings.Contains(err.Error(), "prohibited tar entry type") {
			t.Errorf("expected prohibited mode error, got %v", err)
		}
	})
}

func TestComputeCanonicalTreeDigestDeterminism(t *testing.T) {
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	// Write same files in different creation order
	_ = os.WriteFile(filepath.Join(tempDir1, "b.txt"), []byte("second"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir1, "a.txt"), []byte("first"), 0644)

	_ = os.WriteFile(filepath.Join(tempDir2, "a.txt"), []byte("first"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir2, "b.txt"), []byte("second"), 0644)

	digest1, err1 := ComputeCanonicalTreeDigest(tempDir1)
	if err1 != nil {
		t.Fatalf("digest 1 error: %v", err1)
	}

	digest2, err2 := ComputeCanonicalTreeDigest(tempDir2)
	if err2 != nil {
		t.Fatalf("digest 2 error: %v", err2)
	}

	if digest1 != digest2 {
		t.Errorf("expected identical Merkle tree digest, got %s vs %s", digest1, digest2)
	}
}
