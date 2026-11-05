package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

type stubExtractor struct {
	kind  domain.ExtractorKind
	res   *ports.ExtractionResult
	err   error
	calls int
	got   ports.ExtractionRequest
}

func (s *stubExtractor) Kind() domain.ExtractorKind { return s.kind }
func (s *stubExtractor) Extract(_ context.Context, req ports.ExtractionRequest) (*ports.ExtractionResult, error) {
	s.calls++
	s.got = req
	if s.err != nil {
		return nil, s.err
	}
	cp := *s.res
	return &cp, nil
}

var stroudEntry = domain.ChecklistEntry{ID: 7, Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini",
	SetName: "Prizm", CardNumber: "339", PlayerOrCharacter: "C.J. Stroud", IsRookieBase: true}

func result(kind domain.ExtractorKind, conf float64, player string) *ports.ExtractionResult {
	return &ports.ExtractionResult{Extractor: kind, RawOCRFront: "raw-front", RawOCRBack: "raw-back",
		Candidates: []domain.ChecklistCandidate{{Entry: stroudEntry, Score: 0.7}},
		Payload: domain.CardExtractionPayload{Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "PANINI",
			SetName: "prizm", CardNumber: "#339", PlayerOrCharacter: player, ConfidenceScore: conf}}
}

func TestRouterSkipsCleanupWhenConfident(t *testing.T) {
	local := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.93, "C.J. Stroud")}
	llm := &stubExtractor{kind: domain.ExtractorClaude, res: result(domain.ExtractorClaude, 0.99, "C.J. Stroud")}
	r := application.NewExtractionRouter(local, true, nil, llm)
	res, err := r.Extract(context.Background(), ports.ExtractionRequest{JobID: "j"})
	if err != nil || res.Extractor != domain.ExtractorTesseract || llm.calls != 0 {
		t.Fatalf("res=%+v err=%v llm.calls=%d", res, err, llm.calls)
	}
}

func TestRouterDisabledKeepsLowConfidence(t *testing.T) {
	local := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.5, "CJ STROUD")}
	llm := &stubExtractor{kind: domain.ExtractorClaude, res: result(domain.ExtractorClaude, 0.99, "C.J. Stroud")}
	res, _ := application.NewExtractionRouter(local, false, nil, llm).Extract(context.Background(), ports.ExtractionRequest{JobID: "j"})
	if res.Payload.ConfidenceScore != 0.5 || llm.calls != 0 {
		t.Fatal("cleanup must not run when ENABLE_LLM_CLEANUP=false")
	}
}

func TestRouterFallsBackAndResolvesChecklist(t *testing.T) {
	local := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.55, "CJ STR0UD")}
	ollama := &stubExtractor{kind: domain.ExtractorOllama, err: errors.New("ollama down")}
	claude := &stubExtractor{kind: domain.ExtractorClaude, res: result(domain.ExtractorClaude, 0.95, "CJ Stroud")}
	res, err := application.NewExtractionRouter(local, true, nil, ollama, claude).Extract(context.Background(), ports.ExtractionRequest{JobID: "j"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Extractor != domain.ExtractorClaude || res.ChecklistID == nil || *res.ChecklistID != 7 || res.Payload.PlayerOrCharacter != "C.J. Stroud" {
		t.Fatalf("result %+v", res)
	}
	if claude.got.RawOCRFront != "raw-front" || len(claude.got.Candidates) != 1 {
		t.Fatal("LLM did not receive raw OCR + candidates")
	}
	if res.RawOCRBack != "raw-back" {
		t.Fatal("raw OCR from local pass not preserved")
	}
}

func TestRouterCapsUnknownIdentity(t *testing.T) {
	local := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.4, "?")}
	claude := &stubExtractor{kind: domain.ExtractorClaude, res: result(domain.ExtractorClaude, 0.97, "Somebody Else")}
	res, _ := application.NewExtractionRouter(local, true, nil, claude).Extract(context.Background(), ports.ExtractionRequest{JobID: "j"})
	if res.Payload.ConfidenceScore != application.UncheckedIdentityCap || res.ChecklistID != nil {
		t.Fatalf("invented identity must be capped: %+v", res.Payload)
	}
}

func TestRouterAllFail(t *testing.T) {
	local := &stubExtractor{kind: domain.ExtractorTesseract, err: errors.New("tesseract missing")}
	claude := &stubExtractor{kind: domain.ExtractorClaude, err: errors.New("no key")}
	if _, err := application.NewExtractionRouter(local, true, nil, claude).Extract(context.Background(), ports.ExtractionRequest{JobID: "j"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := application.NewExtractionRouter(local, false, nil).Extract(context.Background(), ports.ExtractionRequest{JobID: "j"}); err == nil {
		t.Fatal("expected error")
	}
}
