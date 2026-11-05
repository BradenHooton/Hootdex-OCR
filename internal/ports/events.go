package ports

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// EventPublisher fans pipeline progress out to subscribers (e.g. the SSE stream).
// Publish must never block the pipeline.
type EventPublisher interface {
	Publish(ctx context.Context, ev domain.PipelineEvent)
}
