package test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/litespm/internal/artifact"
)

func TestHostileArchive_ZipSlipPathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "extracted")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Attempt path traversal
	f, err := zw.Create("../../../../../etc/evil.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("#!/bin/sh\necho pwned\n"))
	_ = zw.Close()

	zipPath := filepath.Join(tempDir, "zipslip.zip")
	_ = os.WriteFile(zipPath, buf.Bytes(), 0644)

	_, err = artifact.ExtractFileSafely(zipPath, "zip", destDir, artifact.DefaultExtractionLimits)
	if err == nil {
		t.Fatalf("expected ZipSlip path traversal to be rejected, but extraction succeeded")
	}
}

func TestHostileArchive_WindowsDriveLetterEscape(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "extracted")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Attempt Windows drive escape
	f, err := zw.Create("C:\\Windows\\System32\\evil.dll")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("binary payload"))
	_ = zw.Close()

	zipPath := filepath.Join(tempDir, "windrive.zip")
	_ = os.WriteFile(zipPath, buf.Bytes(), 0644)

	_, err = artifact.ExtractFileSafely(zipPath, "zip", destDir, artifact.DefaultExtractionLimits)
	if err == nil {
		t.Fatalf("expected Windows drive letter escape to be rejected, but extraction succeeded")
	}
}

func TestHostileArchive_CaseCollisionRejection(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "extracted")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f1, _ := zw.Create("payload.txt")
	_, _ = f1.Write([]byte("first file"))
	f2, _ := zw.Create("PAYLOAD.TXT")
	_, _ = f2.Write([]byte("second colliding file"))
	_ = zw.Close()

	zipPath := filepath.Join(tempDir, "casecol.zip")
	_ = os.WriteFile(zipPath, buf.Bytes(), 0644)

	_, err := artifact.ExtractFileSafely(zipPath, "zip", destDir, artifact.DefaultExtractionLimits)
	if err == nil {
		t.Fatalf("expected case-fold collision to be rejected, but extraction succeeded")
	}
}

func TestHostileArchive_SymlinkInTarGzRejected(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "extracted")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Create symlink header
	hdr := &tar.Header{
		Name:     "symlink_to_shadow",
		Mode:     0777,
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/shadow",
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gw.Close()

	tarPath := filepath.Join(tempDir, "symlink.tar.gz")
	_ = os.WriteFile(tarPath, buf.Bytes(), 0644)

	_, err := artifact.ExtractFileSafely(tarPath, "tar.gz", destDir, artifact.DefaultExtractionLimits)
	if err == nil {
		t.Fatalf("expected symlink in tar.gz to be rejected, but extraction succeeded")
	}
}
