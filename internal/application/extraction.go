package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// UncheckedIdentityCap caps the confidence of an LLM answer whose identity is not one of
// the retrieved checklist candidates, so invented identities always get a human look.
const UncheckedIdentityCap = 0.84

// ExtractionRouter runs the local extractor first; when its confidence is below the review
// threshold and LLM cleanup is enabled, it hands the raw OCR text plus the top checklist
// candidates to each cleanup engine in order (e.g. Ollama VLM, then Claude) and keeps the
// most confident valid answer.
type ExtractionRouter struct {
	local   ports.OCRExtractor
	cleanup []ports.OCRExtractor
	enabled bool
	log     *slog.Logger
}

var _ ports.OCRExtractor = (*ExtractionRouter)(nil)

// NewExtractionRouter constructs the router. enableLLMCleanup mirrors ENABLE_LLM_CLEANUP.
func NewExtractionRouter(local ports.OCRExtractor, enableLLMCleanup bool, log *slog.Logger, cleanup ...ports.OCRExtractor) *ExtractionRouter {
	if log == nil {
		log = slog.Default()
	}
	return &ExtractionRouter{local: local, cleanup: cleanup, enabled: enableLLMCleanup, log: log}
}

// Kind reports the primary (local) engine.
func (r *ExtractionRouter) Kind() domain.ExtractorKind { return r.local.Kind() }

// Extract implements ports.OCRExtractor.
func (r *ExtractionRouter) Extract(ctx context.Context, req ports.ExtractionRequest) (*ports.ExtractionResult, error) {
	best, err := r.local.Extract(ctx, req)
	if err != nil {
		if !r.enabled || len(r.cleanup) == 0 {
			return nil, fmt.Errorf("application.ExtractionRouter [%s]: %w", req.JobID, err)
		}
		r.log.WarnContext(ctx, "local extraction failed; trying LLM cleanup", "job_id", req.JobID, "err", err)
	}
	if best != nil && (!r.enabled || best.Payload.ConfidenceScore >= domain.ReviewThreshold) {
		return best, nil
	}
	creq := req
	if best != nil {
		creq.RawOCRFront, creq.RawOCRBack, creq.Candidates = best.RawOCRFront, best.RawOCRBack, best.Candidates
	}
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, c := range r.cleanup {
		res, cerr := c.Extract(ctx, creq)
		if cerr != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("application.ExtractionRouter [%s]: %w", req.JobID, ctx.Err())
			}
			r.log.WarnContext(ctx, "LLM cleanup failed", "job_id", req.JobID, "engine", c.Kind(), "err", cerr)
			errs = append(errs, cerr)
			continue
		}
		res.ChecklistID = ResolveChecklist(&res.Payload, creq.Candidates)
		if res.ChecklistID == nil && res.Payload.ConfidenceScore > UncheckedIdentityCap {
			res.Payload.ConfidenceScore = UncheckedIdentityCap
			res.Payload.ExtractionNotes = strings.TrimSpace(res.Payload.ExtractionNotes + " Identity not found among checklist candidates; capped for review.")
		}
		if res.RawOCRFront == "" && best != nil {
			res.RawOCRFront, res.RawOCRBack = best.RawOCRFront, best.RawOCRBack
		}
		if len(res.Candidates) == 0 {
			res.Candidates = creq.Candidates
		}
		if best == nil || res.Payload.ConfidenceScore > best.Payload.ConfidenceScore {
			best = res
		}
		if best.Payload.ConfidenceScore >= domain.ReviewThreshold {
			break
		}
	}
	if best == nil {
		return nil, fmt.Errorf("application.ExtractionRouter [%s]: all extractors failed: %w", req.JobID, errors.Join(errs...))
	}
	return best, nil
}

// ResolveChecklist returns the ID of the candidate whose identity equals the payload's
// (case/punctuation-insensitive), snapping spelling to the checklist when found.
func ResolveChecklist(p *domain.CardExtractionPayload, cands []domain.ChecklistCandidate) *int64 {
	key := func(cat domain.Category, year int, mfr, set, num, player string) string {
		return fmt.Sprintf("%s|%d|%s|%s|%s|%s", cat, year, domain.AlphaNum(mfr), domain.AlphaNum(set), domain.AlphaNum(domain.NormalizeCardNumber(num)), domain.AlphaNum(player))
	}
	want := key(p.Category, p.ReleaseYear, p.Manufacturer, p.SetName, p.CardNumber, p.PlayerOrCharacter)
	for _, c := range cands {
		e := c.Entry
		if key(e.Category, e.ReleaseYear, e.Manufacturer, e.SetName, e.CardNumber, e.PlayerOrCharacter) == want {
			p.SnapToChecklist(e)
			id := e.ID
			return &id
		}
	}
	return nil
}
