package ports

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// ScanIntake is the inbound port the hot-folder watcher and upload handler call to
// enqueue an archived scan pair as a durable PENDING ingestion job.
type ScanIntake interface {
	EnqueueScan(ctx context.Context, pair domain.ScanPair) (*domain.IngestionJob, error)
}
