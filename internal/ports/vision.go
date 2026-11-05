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
