package source

import (
	"context"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// IngestResult encapsulates the normalized domain records produced by a source adapter.
type IngestResult struct {
	SourceID   domain.SourceID
	SnapshotID string
	Listings   []*domain.Listing
	Versions   []*domain.VersionRecord
	ItemCount  int
	Digest     string
	IngestedAt time.Time
}

// Adapter defines the interface for an upstream catalog/registry ingestion adapter.
// Source adapters only parse and normalize discovery metadata; they perform zero package execution.
type Adapter interface {
	SourceID() domain.SourceID
	Ingest(ctx context.Context, snapshotID string) (*IngestResult, error)
}
