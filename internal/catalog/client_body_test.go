package catalog

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadCatalogFileBodyAllowsManifestSizedDataAboveMetadataLimit(t *testing.T) {
	want := bytes.Repeat([]byte("x"), int(maxCatalogBodyBytes+1))
	got, err := readCatalogFileBody(bytes.NewReader(want), int64(len(want)))
	if err != nil {
		t.Fatalf("read manifest-sized catalog data: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read %d bytes, want %d", len(got), len(want))
	}
}

func TestReadCatalogFileBodyRejectsOversizeAndManifestSizeMismatch(t *testing.T) {
	tooLarge := int64(maxCatalogFileBytes + 1)
	if _, err := readCatalogFileBody(strings.NewReader("unused"), tooLarge); err == nil {
		t.Fatal("file larger than the hard catalog-file cap was accepted")
	}
	if _, err := readCatalogFileBody(strings.NewReader("short"), int64(len("longer"))); err == nil {
		t.Fatal("body length differing from the manifest was accepted")
	}
}

type unreadableReader struct{}

func (unreadableReader) Read([]byte) (int, error) {
	panic("oversized manifest must be rejected before reading the body")
}

var _ io.Reader = unreadableReader{}

func TestReadCatalogFileBodyRejectsOversizedManifestBeforeRead(t *testing.T) {
	if _, err := readCatalogFileBody(unreadableReader{}, int64(maxCatalogFileBytes+1)); err == nil {
		t.Fatal("oversized manifest declaration was accepted")
	}
}
