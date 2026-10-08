package catalogbuild

import (
	"testing"
	"time"
)

func TestDatasetConversionPreservesExplicitSourceWithoutInventingSnapshot(t *testing.T) {
	raw := []byte(`[{"id":"skill:publisher:demo","kind":"skill","name":"demo","summary":"A demo skill","publisher":{"name":"publisher"},"source":"feed:skills-sh"}]`)
	rows, err := ParseDataset(raw)
	if err != nil {
		t.Fatalf("ParseDataset: %v", err)
	}

	listings, versions, err := ConvertDataset(rows, "rel-provenance", DatasetSnapshotID(raw), time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	if got := listings[0].Source.SourceID; got != "feed:skills-sh" {
		t.Errorf("source.sourceId = %q, want producer's explicit source ID", got)
	}
	if got := listings[0].Provenance.SourceSnapshotID; got != "" {
		t.Errorf("listing sourceSnapshotId = %q, want absent when producer supplied no source snapshot", got)
	}
	if got := versions[0].SourceSnapshotID; got != "" {
		t.Errorf("version sourceSnapshotId = %q, want absent when producer supplied no source snapshot", got)
	}
}

func TestDatasetConversionPreservesExplicitPerRowSnapshot(t *testing.T) {
	const sourceSnapshotID = "snap_01J9X8K2M4N5P6Q7R8S9T0U1V2"
	raw := []byte(`[{"id":"skill:publisher:demo","kind":"skill","name":"demo","summary":"A demo skill","publisher":{"name":"publisher"},"source":"feed:skills-sh","sourceSnapshotId":"` + sourceSnapshotID + `"}]`)
	rows, err := ParseDataset(raw)
	if err != nil {
		t.Fatalf("ParseDataset: %v", err)
	}
	listings, versions, err := ConvertDataset(rows, "rel-provenance", "dataset-fingerprint-not-source-snapshot", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("ConvertDataset: %v", err)
	}
	if got := listings[0].Provenance.SourceSnapshotID; got != sourceSnapshotID {
		t.Errorf("listing sourceSnapshotId = %q, want row value %q", got, sourceSnapshotID)
	}
	if got := versions[0].SourceSnapshotID; got != sourceSnapshotID {
		t.Errorf("version sourceSnapshotId = %q, want row value %q", got, sourceSnapshotID)
	}
}

func TestParseDatasetRejectsInvalidExplicitSource(t *testing.T) {
	raw := []byte(`[{"id":"skill:publisher:demo","kind":"skill","name":"demo","summary":"A demo skill","publisher":{"name":"publisher"},"source":"not-a-source-id"}]`)
	if _, err := ParseDataset(raw); err == nil {
		t.Fatal("expected invalid producer source to be rejected")
	}
}
