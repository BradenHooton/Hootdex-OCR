package application

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// NopPublisher discards events.
type NopPublisher struct{}

// Publish implements ports.EventPublisher.
func (NopPublisher) Publish(context.Context, domain.PipelineEvent) {}
