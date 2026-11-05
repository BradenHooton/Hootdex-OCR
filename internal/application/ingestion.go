// Package application holds the use-case orchestrators that wire domain rules to ports.
package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// IngestionService turns archived scan pairs into durable PENDING jobs.
type IngestionService struct {
	jobs   ports.JobRepository
	events ports.EventPublisher
	newID  func() string
	log    *slog.Logger
}

var _ ports.ScanIntake = (*IngestionService)(nil)

// NewIngestionService constructs the service. events may be nil.
func NewIngestionService(jobs ports.JobRepository, events ports.EventPublisher, newID func() string, log *slog.Logger) *IngestionService {
	if events == nil {
		events = NopPublisher{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &IngestionService{jobs: jobs, events: events, newID: newID, log: log}
}

// EnqueueScan persists a PENDING job for the pair and announces it.
func (s *IngestionService) EnqueueScan(ctx context.Context, pair domain.ScanPair) (*domain.IngestionJob, error) {
	job, err := domain.NewIngestionJob(s.newID(), pair.BatchID, pair.FrontPath, pair.BackPath)
	if err != nil {
		return nil, fmt.Errorf("application.EnqueueScan [%s]: %w", pair.Prefix, err)
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("application.EnqueueScan [%s]: %w", pair.Prefix, err)
	}
	s.log.InfoContext(ctx, "scan enqueued", "job_id", job.ID, "batch_id", job.BatchID, "front", job.FrontImagePath, "back", job.BackImagePath)
	s.events.Publish(ctx, domain.PipelineEvent{Type: domain.EventJobQueued, JobID: job.ID, BatchID: job.BatchID, Status: job.Status})
	return job, nil
}
