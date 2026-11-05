package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// ReviewItem is one NEEDS_REVIEW job prepared for the human verification UI.
type ReviewItem struct {
	Job                 *domain.IngestionJob          `json:"job"`
	Payload             *domain.CardExtractionPayload `json:"payload"`
	Preprocess          *domain.PreprocessResult      `json:"preprocess,omitempty"`
	Candidates          []domain.ChecklistCandidate   `json:"candidates"`
	Images              map[string]string             `json:"images"`
	LowConfidenceFields []string                      `json:"low_confidence_fields"`
}

// ApproveInput carries the operator's verified payload and optional overrides.
type ApproveInput struct {
	Payload              domain.CardExtractionPayload `json:"payload"`
	ChecklistID          *int64                       `json:"checklist_id,omitempty"`
	StorageLocationID    string                       `json:"storage_location_id,omitempty"` // empty = next open slot
	PlayerImpactTier     int                          `json:"player_impact_tier,omitempty"`
	AcquisitionCostCents int64                        `json:"acquisition_cost_cents,omitempty"`
	AskingPriceCents     int64                        `json:"asking_price_cents,omitempty"`
}

// ReviewService powers the human-in-the-loop queue.
type ReviewService struct {
	jobs      ports.JobRepository
	inventory ports.InventoryRepository
	checklist ports.ChecklistRepository
	valuer    CardValuer
	events    ports.EventPublisher
	newID     func() string
	log       *slog.Logger
}

// NewReviewService constructs the service. valuer and events may be nil.
func NewReviewService(jobs ports.JobRepository, inventory ports.InventoryRepository, checklist ports.ChecklistRepository,
	valuer CardValuer, events ports.EventPublisher, newID func() string, log *slog.Logger) *ReviewService {
	if events == nil {
		events = NopPublisher{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &ReviewService{jobs: jobs, inventory: inventory, checklist: checklist, valuer: valuer, events: events, newID: newID, log: log}
}

// List returns the review queue, oldest first.
func (s *ReviewService) List(ctx context.Context, limit int) ([]ReviewItem, error) {
	jobs, err := s.jobs.ListByStatus(ctx, domain.JobNeedsReview, limit)
	if err != nil {
		return nil, fmt.Errorf("application.ReviewService.List: %w", err)
	}
	out := make([]ReviewItem, 0, len(jobs))
	for _, j := range jobs {
		item, err := s.item(ctx, j)
		if err != nil {
			return nil, fmt.Errorf("application.ReviewService.List: %w", err)
		}
		out = append(out, *item)
	}
	return out, nil
}

// Get returns one review item.
func (s *ReviewService) Get(ctx context.Context, jobID string) (*ReviewItem, error) {
	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("application.ReviewService.Get: %w", err)
	}
	if j.Status != domain.JobNeedsReview {
		return nil, fmt.Errorf("application.ReviewService.Get [%s]: %w: job is %s", jobID, domain.ErrConflict, j.Status)
	}
	return s.item(ctx, j)
}

func (s *ReviewService) item(ctx context.Context, j *domain.IngestionJob) (*ReviewItem, error) {
	item := &ReviewItem{Job: j, Payload: &domain.CardExtractionPayload{ParallelVariant: domain.BaseVariant, GradingCompany: domain.GraderRaw},
		Images: map[string]string{"front": JobImageURL(j.ID, "front")}}
	if j.BackImagePath != "" {
		item.Images["back"] = JobImageURL(j.ID, "back")
	}
	if j.ExtractedPayloadJSON != "" {
		p, err := domain.ParsePayloadJSON([]byte(j.ExtractedPayloadJSON))
		if err != nil {
			return nil, fmt.Errorf("job %s payload: %w", j.ID, err)
		}
		item.Payload = p
	}
	pre, err := DecodePreprocessMeta(j)
	if err != nil {
		return nil, err
	}
	item.Preprocess = pre
	text := strings.TrimSpace(j.RawOCRFront + " " + j.RawOCRBack + " " + item.Payload.PlayerOrCharacter + " " + item.Payload.CardNumber)
	cands, err := s.checklist.MatchCandidates(ctx, text, item.Payload.ReleaseYear, "")
	if err != nil {
		return nil, err
	}
	item.Candidates = cands
	item.LowConfidenceFields = lowConfidenceFields(item.Payload, cands)
	return item, nil
}

// lowConfidenceFields flags empty identity fields and fields that disagree with the best candidate.
func lowConfidenceFields(p *domain.CardExtractionPayload, cands []domain.ChecklistCandidate) []string {
	fields := []struct {
		name, got, want string
	}{
		{"category", string(p.Category), ""}, {"release_year", yearStr(p.ReleaseYear), ""}, {"manufacturer", p.Manufacturer, ""},
		{"set_name", p.SetName, ""}, {"card_number", p.CardNumber, ""}, {"player_or_character", p.PlayerOrCharacter, ""},
	}
	if len(cands) > 0 {
		e := cands[0].Entry
		wants := []string{string(e.Category), yearStr(e.ReleaseYear), e.Manufacturer, e.SetName, e.CardNumber, e.PlayerOrCharacter}
		for i := range fields {
			fields[i].want = wants[i]
		}
	}
	var out []string
	for _, f := range fields {
		if f.got == "" || (f.want != "" && domain.AlphaNum(f.got) != domain.AlphaNum(f.want)) {
			out = append(out, f.name)
		}
	}
	if p.ConfidenceScore < domain.ReviewThreshold && len(out) == 0 {
		out = append(out, "confidence_score")
	}
	return out
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return fmt.Sprint(y)
}

// Approve finalizes a reviewed card: it applies the operator's edits, snaps to the chosen
// checklist entry, recalculates valuation + Hoot-Score, and in one transaction assigns the
// storage slot, generates the SKU and completes the job.
func (s *ReviewService) Approve(ctx context.Context, jobID string, in ApproveInput) (*domain.InventoryRecord, error) {
	wrap := func(err error) error { return fmt.Errorf("application.ReviewService.Approve [%s]: %w", jobID, err) }
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, wrap(err)
	}
	if job.Status != domain.JobNeedsReview {
		return nil, wrap(fmt.Errorf("%w: job is %s", domain.ErrConflict, job.Status))
	}
	p := in.Payload
	p.Normalize()
	var checklistID *int64
	if in.ChecklistID != nil {
		e, err := s.checklist.Get(ctx, *in.ChecklistID)
		if err != nil {
			return nil, wrap(err)
		}
		p.SnapToChecklist(*e)
		checklistID = &e.ID
	} else {
		cands, err := s.checklist.MatchCandidates(ctx, p.PlayerOrCharacter+" "+p.SetName+" "+p.CardNumber, p.ReleaseYear, p.Category)
		if err != nil {
			return nil, wrap(err)
		}
		checklistID = ResolveChecklist(&p, cands)
	}
	p.ConfidenceScore = 1
	p.ExtractionNotes = strings.TrimSpace(p.ExtractionNotes + " Human verified.")
	if err := p.Validate(); err != nil {
		return nil, wrap(err)
	}
	if !p.IsComplete() {
		return nil, wrap(fmt.Errorf("%w: category, year, manufacturer, set, card number and player are required", domain.ErrInvalid))
	}
	pre, err := DecodePreprocessMeta(job)
	if err != nil {
		return nil, wrap(err)
	}
	card := BuildCard(s.newID(), job, &p, checklistID, pre)
	card.InventoryStatus = domain.StatusInStock
	card.StorageLocationID = in.StorageLocationID
	card.AcquisitionCostCents, card.AskingPriceCents = in.AcquisitionCostCents, in.AskingPriceCents
	if in.PlayerImpactTier != 0 {
		card.PlayerImpactTier = in.PlayerImpactTier
	}
	if err := card.Validate(); err != nil {
		return nil, wrap(err)
	}
	// Persist the human-verified payload before finalizing (job stays NEEDS_REVIEW until the
	// finalize transaction flips it to COMPLETED atomically with the card insert).
	verified, err := json.Marshal(p)
	if err != nil {
		return nil, wrap(err)
	}
	job.ExtractedPayloadJSON, job.ConfidenceScore, job.ErrorMessage = string(verified), 1, ""
	if err := s.jobs.Update(ctx, job); err != nil {
		return nil, wrap(err)
	}
	if err := FinalizeWithValuation(ctx, s.valuer, s.inventory, s.log, card); err != nil {
		return nil, wrap(err)
	}
	rec, err := s.inventory.Get(ctx, card.ID)
	if err != nil {
		return nil, wrap(err)
	}
	s.events.Publish(ctx, domain.PipelineEvent{Type: domain.EventCardApproved, JobID: job.ID, BatchID: job.BatchID,
		Status: domain.JobCompleted, SKU: card.SKU, Message: card.StorageLocationID, Confidence: 1})
	return rec, nil
}

// Reject marks a reviewed scan as unusable (e.g. a mis-feed) so it leaves the queue.
func (s *ReviewService) Reject(ctx context.Context, jobID, reason string) error {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("application.ReviewService.Reject [%s]: %w", jobID, err)
	}
	if err := job.TransitionTo(domain.JobFailed); err != nil {
		return fmt.Errorf("application.ReviewService.Reject [%s]: %w", jobID, err)
	}
	job.ErrorMessage = "rejected in review: " + strings.TrimSpace(reason)
	if err := s.jobs.Update(ctx, job); err != nil {
		return fmt.Errorf("application.ReviewService.Reject [%s]: %w", jobID, err)
	}
	s.events.Publish(ctx, domain.PipelineEvent{Type: domain.EventJobFailed, JobID: job.ID, BatchID: job.BatchID, Status: job.Status, Message: job.ErrorMessage})
	return nil
}

// InventoryService answers inventory, storage and checklist queries for the UI.
type InventoryService struct {
	inventory ports.InventoryRepository
	storage   ports.StorageRepository
	checklist ports.ChecklistRepository
	jobs      ports.JobRepository
}

// NewInventoryService constructs the service.
func NewInventoryService(inv ports.InventoryRepository, storage ports.StorageRepository, checklist ports.ChecklistRepository, jobs ports.JobRepository) *InventoryService {
	return &InventoryService{inventory: inv, storage: storage, checklist: checklist, jobs: jobs}
}

// List filters inventory.
func (s *InventoryService) List(ctx context.Context, f domain.InventoryFilter) ([]domain.InventoryRecord, error) {
	return s.inventory.List(ctx, f)
}

// Get returns a card with its price history.
func (s *InventoryService) Get(ctx context.Context, id string) (*domain.InventoryRecord, []domain.PriceQuote, error) {
	rec, err := s.inventory.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	snaps, err := s.inventory.ListPriceSnapshots(ctx, rec.Card.ID)
	if err != nil {
		return nil, nil, err
	}
	return rec, snaps, nil
}

// Storage lists every storage location with occupancy.
func (s *InventoryService) Storage(ctx context.Context) ([]domain.StorageLocation, error) {
	return s.storage.List(ctx)
}

// SearchChecklist powers "snap to checklist" lookups.
func (s *InventoryService) SearchChecklist(ctx context.Context, q string, year int, cat domain.Category) ([]domain.ChecklistCandidate, error) {
	return s.checklist.MatchCandidates(ctx, q, year, cat)
}

// Job loads one ingestion job.
func (s *InventoryService) Job(ctx context.Context, id string) (*domain.IngestionJob, error) {
	return s.jobs.Get(ctx, id)
}

// QueueCounts returns jobs per status.
func (s *InventoryService) QueueCounts(ctx context.Context) (map[domain.JobStatus]int, error) {
	return s.jobs.CountByStatus(ctx)
}
