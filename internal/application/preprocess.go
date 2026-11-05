package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// PreprocessService runs the image pipeline for a claimed job and persists the result.
type PreprocessService struct {
	jobs         ports.JobRepository
	proc         ports.ImageProcessor
	processedDir string
	events       ports.EventPublisher
	log          *slog.Logger
}

// NewPreprocessService constructs the service; outputs go to <processedDir>/<job_id>/.
func NewPreprocessService(jobs ports.JobRepository, proc ports.ImageProcessor, processedDir string, events ports.EventPublisher, log *slog.Logger) *PreprocessService {
	if events == nil {
		events = NopPublisher{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &PreprocessService{jobs: jobs, proc: proc, processedDir: processedDir, events: events, log: log}
}

// Run preprocesses a job in PREPROCESSING state and advances it to EXTRACTING.
func (s *PreprocessService) Run(ctx context.Context, job *domain.IngestionJob) (*domain.PreprocessResult, error) {
	if job.Status != domain.JobPreprocessing {
		return nil, fmt.Errorf("application.Preprocess [%s]: %w: status %s", job.ID, domain.ErrConflict, job.Status)
	}
	res, err := s.proc.Preprocess(ctx, ports.PreprocessRequest{
		JobID: job.ID, FrontPath: job.FrontImagePath, BackPath: job.BackImagePath,
		OutDir: filepath.Join(s.processedDir, job.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("application.Preprocess [%s]: %w", job.ID, err)
	}
	meta, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("application.Preprocess [%s]: marshal: %w", job.ID, err)
	}
	job.CroppedFrontPath, job.CroppedBackPath, job.PreprocessMetaJSON = res.CroppedFrontPath, res.CroppedBackPath, string(meta)
	if err := job.TransitionTo(domain.JobExtracting); err != nil {
		return nil, fmt.Errorf("application.Preprocess [%s]: %w", job.ID, err)
	}
	if err := s.jobs.Update(ctx, job); err != nil {
		return nil, fmt.Errorf("application.Preprocess [%s]: %w", job.ID, err)
	}
	s.events.Publish(ctx, domain.PipelineEvent{Type: domain.EventJobStatus, JobID: job.ID, BatchID: job.BatchID, Status: job.Status,
		Message: fmt.Sprintf("%s cropped; centering %s / %s", res.CardType, res.CenteringLR, res.CenteringTB)})
	return res, nil
}

// DecodePreprocessMeta parses a job's stored preprocessing result (nil when absent).
func DecodePreprocessMeta(job *domain.IngestionJob) (*domain.PreprocessResult, error) {
	if job.PreprocessMetaJSON == "" {
		return nil, nil
	}
	var r domain.PreprocessResult
	if err := json.Unmarshal([]byte(job.PreprocessMetaJSON), &r); err != nil {
		return nil, fmt.Errorf("application.DecodePreprocessMeta [%s]: %w", job.ID, err)
	}
	return &r, nil
}
