package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// DefaultWorkers protects the RX 5700 XT's 8GB VRAM: at most two concurrent OCR/VLM jobs.
const DefaultWorkers = 2

// CardValuer prices a card and fills its valuation fields before it is finalized
// (implemented by the Sprint 4 ValuationService). Optional.
type CardValuer interface {
	Value(ctx context.Context, card *domain.Card) error
}

// JobImageURL is the API path that serves a job's cropped face ("front" / "back").
func JobImageURL(jobID, side string) string {
	return fmt.Sprintf("/api/v1/jobs/%s/images/%s", jobID, side)
}

// PipelineDeps are the pipeline's collaborators.
type PipelineDeps struct {
	Jobs       ports.JobRepository
	Inventory  ports.InventoryRepository
	Preprocess *PreprocessService
	Extractor  ports.OCRExtractor
	Valuer     CardValuer           // optional
	Events     ports.EventPublisher // optional
	NewID      func() string
	Log        *slog.Logger
}

// PipelineConfig tunes concurrency.
type PipelineConfig struct {
	Workers      int           // default 2
	PollInterval time.Duration // idle poll cadence (default 1s); Wake() short-circuits it
}

// Pipeline is the bounded worker pool draining the durable ingestion_jobs queue:
// preprocess -> extract -> checklist match -> Hoot-Score -> storage slot -> inventory.
type Pipeline struct {
	d    PipelineDeps
	cfg  PipelineConfig
	wake chan struct{}

	active    atomic.Int32
	processed atomic.Int64
	failed    atomic.Int64
	mu        sync.Mutex
	recent    []time.Time // completion times for throughput
}

// NewPipeline constructs the orchestrator.
func NewPipeline(d PipelineDeps, cfg PipelineConfig) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if d.Events == nil {
		d.Events = NopPublisher{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Pipeline{d: d, cfg: cfg, wake: make(chan struct{}, 1)}
}

// Wake nudges idle workers (e.g. right after a scan is enqueued).
func (p *Pipeline) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run requeues crash-interrupted jobs, then runs the workers until ctx is cancelled.
func (p *Pipeline) Run(ctx context.Context) error {
	n, err := p.d.Jobs.RequeueInFlight(ctx)
	if err != nil {
		return fmt.Errorf("application.Pipeline.Run: %w", err)
	}
	if n > 0 {
		p.d.Log.WarnContext(ctx, "requeued jobs interrupted by a previous shutdown", "count", n)
	}
	var wg sync.WaitGroup
	for w := 1; w <= p.cfg.Workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			p.worker(ctx, worker)
		}(w)
	}
	p.d.Log.InfoContext(ctx, "ingestion pipeline started", "workers", p.cfg.Workers)
	wg.Wait()
	return nil
}

func (p *Pipeline) worker(ctx context.Context, id int) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		did, err := p.ProcessNext(ctx, id)
		if err != nil && ctx.Err() == nil {
			p.d.Log.ErrorContext(ctx, "worker error", "worker", id, "err", err)
		}
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-ticker.C:
		}
	}
}

// Drain processes jobs with the configured worker count until the queue is empty.
func (p *Pipeline) Drain(ctx context.Context) error {
	var wg sync.WaitGroup
	errCh := make(chan error, p.cfg.Workers)
	for w := 1; w <= p.cfg.Workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				did, err := p.ProcessNext(ctx, worker)
				if err != nil {
					errCh <- err
					return
				}
				if !did {
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	return <-errCh
}

// ProcessNext claims and processes one job. It returns false when the queue is empty.
// Per-job failures are recorded on the job (retry or FAILED) and are not returned.
func (p *Pipeline) ProcessNext(ctx context.Context, worker int) (bool, error) {
	job, err := p.d.Jobs.ClaimNextPending(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("application.Pipeline.ProcessNext: %w", err)
	}
	p.active.Add(1)
	defer p.active.Add(-1)
	p.publish(ctx, domain.PipelineEvent{Type: domain.EventJobStatus, JobID: job.ID, BatchID: job.BatchID, Status: job.Status, Worker: worker})
	if err := p.process(ctx, job, worker); err != nil {
		p.fail(ctx, job, worker, err)
	}
	p.publishStats(ctx)
	return true, nil
}

func (p *Pipeline) process(ctx context.Context, job *domain.IngestionJob, worker int) error {
	pre, err := p.d.Preprocess.Run(ctx, job)
	if err != nil {
		return err
	}
	res, err := p.d.Extractor.Extract(ctx, ports.ExtractionRequest{JobID: job.ID, Preprocess: pre})
	if err != nil {
		return err
	}
	payloadJSON, err := json.Marshal(res.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	job.ExtractorUsed, job.RawOCRFront, job.RawOCRBack = res.Extractor, res.RawOCRFront, res.RawOCRBack
	job.ExtractedPayloadJSON, job.ConfidenceScore, job.ErrorMessage = string(payloadJSON), res.Payload.ConfidenceScore, ""
	if err := job.TransitionTo(domain.JobMatching); err != nil {
		return err
	}
	if err := p.d.Jobs.Update(ctx, job); err != nil {
		return err
	}
	p.publish(ctx, domain.PipelineEvent{Type: domain.EventJobStatus, JobID: job.ID, BatchID: job.BatchID, Status: job.Status,
		Worker: worker, Confidence: res.Payload.ConfidenceScore, Message: string(res.Extractor)})

	if res.Payload.NeedsReview() {
		return p.toReview(ctx, job, worker, fmt.Sprintf("confidence %.2f below %.2f", res.Payload.ConfidenceScore, domain.ReviewThreshold))
	}
	card := BuildCard(p.d.NewID(), job, &res.Payload, res.ChecklistID, pre)
	card.InventoryStatus = domain.StatusInStock
	if err := p.finalize(ctx, card); err != nil {
		if errors.Is(err, domain.ErrNoStorageCapacity) {
			return p.toReview(ctx, job, worker, "no open storage slot; assign one during review")
		}
		return err
	}
	p.markDone()
	p.publish(ctx, domain.PipelineEvent{Type: domain.EventJobCompleted, JobID: job.ID, BatchID: job.BatchID, Status: domain.JobCompleted,
		Worker: worker, Confidence: res.Payload.ConfidenceScore, SKU: card.SKU, Message: card.StorageLocationID})
	p.d.Log.InfoContext(ctx, "card stocked", "job_id", job.ID, "sku", card.SKU, "slot", card.StorageLocationID, "hoot_score", card.HootScore)
	return nil
}

// finalize prices the card, computes the Hoot-Score and persists it with a storage slot.
func (p *Pipeline) finalize(ctx context.Context, card *domain.Card) error {
	return FinalizeWithValuation(ctx, p.d.Valuer, p.d.Inventory, p.d.Log, card)
}

// FinalizeWithValuation is shared by the pipeline and the review service.
func FinalizeWithValuation(ctx context.Context, valuer CardValuer, inv ports.InventoryRepository, log *slog.Logger, card *domain.Card) error {
	if valuer != nil {
		if err := valuer.Value(ctx, card); err != nil {
			log.WarnContext(ctx, "valuation failed; stocking without a market price", "card_id", card.ID, "err", err)
		}
	}
	card.HootScore = domain.CalculateHootScore(card)
	return inv.FinalizeCard(ctx, card)
}

func (p *Pipeline) toReview(ctx context.Context, job *domain.IngestionJob, worker int, reason string) error {
	if err := job.TransitionTo(domain.JobNeedsReview); err != nil {
		return err
	}
	job.ErrorMessage = reason
	if err := p.d.Jobs.Update(ctx, job); err != nil {
		return err
	}
	p.markDone()
	p.publish(ctx, domain.PipelineEvent{Type: domain.EventJobReview, JobID: job.ID, BatchID: job.BatchID, Status: job.Status,
		Worker: worker, Confidence: job.ConfidenceScore, Message: reason})
	return nil
}

// fail records a processing error: retry (back to PENDING) until MaxJobRetries, then FAILED.
func (p *Pipeline) fail(ctx context.Context, job *domain.IngestionJob, worker int, cause error) {
	// Persist against a fresh context so a shutdown mid-job still records the outcome.
	ctx2, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	job.RetryCount++
	job.ErrorMessage = cause.Error()
	if ctx.Err() != nil {
		job.RetryCount-- // interrupted, not failed: requeue without burning a retry
		job.Status = domain.JobPending
	} else if job.RetryCount < domain.MaxJobRetries {
		job.Status = domain.JobPending
	} else {
		job.Status = domain.JobFailed
		p.failed.Add(1)
	}
	if err := p.d.Jobs.Update(ctx2, job); err != nil {
		p.d.Log.ErrorContext(ctx2, "could not record job failure", "job_id", job.ID, "err", err, "cause", cause)
		return
	}
	p.d.Log.WarnContext(ctx2, "job failed", "job_id", job.ID, "status", job.Status, "retry", job.RetryCount, "err", cause)
	ev := domain.EventJobFailed
	if job.Status == domain.JobPending {
		ev = domain.EventJobStatus
		p.Wake()
	}
	p.publish(ctx2, domain.PipelineEvent{Type: ev, JobID: job.ID, BatchID: job.BatchID, Status: job.Status, Worker: worker, Message: cause.Error()})
}

// BuildCard maps an extraction onto a new inventory card (status/valuation set by caller).
func BuildCard(id string, job *domain.IngestionJob, payload *domain.CardExtractionPayload, checklistID *int64, pre *domain.PreprocessResult) *domain.Card {
	card := domain.NewCardFromPayload(*payload)
	card.ID, card.IngestionJobID, card.ChecklistID = id, job.ID, checklistID
	card.FrontImageURL = JobImageURL(job.ID, "front")
	if job.BackImagePath != "" {
		card.BackImageURL = JobImageURL(job.ID, "back")
	}
	if pre != nil && pre.CornerWearScore != nil {
		w := *pre.CornerWearScore
		card.CornerWearScore = &w
	}
	return card
}

func (p *Pipeline) markDone() {
	p.processed.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.recent = append(p.recent, now)
	cut := 0
	for cut < len(p.recent) && now.Sub(p.recent[cut]) > 5*time.Minute {
		cut++
	}
	p.recent = p.recent[cut:]
}

// Stats reports worker activity and a 5-minute moving throughput.
func (p *Pipeline) Stats() domain.PipelineStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	cpm := 0.0
	if n := len(p.recent); n > 1 {
		span := p.recent[n-1].Sub(p.recent[0]).Minutes()
		if span < 1 {
			span = 1
		}
		cpm = float64(n) / span
	} else if n == 1 {
		cpm = 1
	}
	return domain.PipelineStats{Workers: p.cfg.Workers, ActiveWorkers: int(p.active.Load()), Processed: p.processed.Load(),
		Failed: p.failed.Load(), CardsPerMinute: cpm}
}

func (p *Pipeline) publishStats(ctx context.Context) {
	counts, err := p.d.Jobs.CountByStatus(ctx)
	if err != nil {
		return
	}
	s := p.Stats()
	p.publish(ctx, domain.PipelineEvent{Type: domain.EventQueueStats, Queue: counts, Stats: &s})
}

func (p *Pipeline) publish(ctx context.Context, ev domain.PipelineEvent) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	p.d.Events.Publish(ctx, ev)
}
