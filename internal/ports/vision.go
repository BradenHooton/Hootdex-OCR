package ports

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// PreprocessRequest identifies the scan images to deskew, crop and slice.
type PreprocessRequest struct {
	JobID     string
	FrontPath string
	BackPath  string // optional
	OutDir    string // where cropped faces and binarized zones are written
}

// ImageProcessor deskews/crops a card scan, estimates centering and corner wear,
// and slices binarized OCR zones.
type ImageProcessor interface {
	Preprocess(ctx context.Context, req PreprocessRequest) (*domain.PreprocessResult, error)
}

// ExtractionRequest carries everything an extractor may use for one card.
type ExtractionRequest struct {
	JobID      string
	Preprocess *domain.PreprocessResult // cropped faces + binarized zones (nil for text-only cleanup)
	// RawOCRFront / RawOCRBack are prior OCR dumps (set when an LLM cleans up local OCR).
	RawOCRFront string
	RawOCRBack  string
	// Candidates are checklist entries retrieved for the card (top 15 feed the LLM prompt).
	Candidates []domain.ChecklistCandidate
}

// ExtractionResult is one engine's structured answer.
type ExtractionResult struct {
	Payload     domain.CardExtractionPayload
	Extractor   domain.ExtractorKind
	RawOCRFront string
	RawOCRBack  string
	Candidates  []domain.ChecklistCandidate
	ChecklistID *int64 // checklist row the payload was snapped to, if any
}

// OCRExtractor turns a preprocessed card into a CardExtractionPayload.
// Implementations: local Tesseract, local Ollama VLM, Claude API.
type OCRExtractor interface {
	Kind() domain.ExtractorKind
	Extract(ctx context.Context, req ExtractionRequest) (*ExtractionResult, error)
}

// TextRecognizer runs raw OCR on one binarized zone image.
type TextRecognizer interface {
	Recognize(ctx context.Context, imagePath string, zone domain.ZoneName) (string, error)
}
