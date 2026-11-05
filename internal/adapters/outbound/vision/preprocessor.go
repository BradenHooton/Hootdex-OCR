// Package vision contains the outbound image-processing and OCR/VLM adapters.
package vision

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// Glare-resistant binarization parameters (OCR_DOMAIN_CONTEXT.md §2.4).
const (
	claheClipLimit  = 2.0
	claheTileGrid   = 8
	threshBlockSize = 15
	threshC         = 4
	cropJPEGQuality = 92
)

// Preprocessor is the pure-Go ImageProcessor: deskew/crop, centering, corner wear,
// zonal slicing and CLAHE + adaptive-threshold binarization. It needs no CGO, so it
// runs identically on linux/amd64 and darwin/arm64. (An OpenCV/gocv implementation can
// be dropped in behind the same port inside the Docker image.)
type Preprocessor struct {
	log *slog.Logger
}

var _ ports.ImageProcessor = (*Preprocessor)(nil)

// NewPreprocessor constructs the adapter.
func NewPreprocessor(log *slog.Logger) *Preprocessor {
	if log == nil {
		log = slog.Default()
	}
	return &Preprocessor{log: log}
}

type face struct {
	quad [4]domain.Point
	img  *image.NRGBA
	luma *plane
	path string
}

// Preprocess implements ports.ImageProcessor.
func (p *Preprocessor) Preprocess(ctx context.Context, req ports.PreprocessRequest) (*domain.PreprocessResult, error) {
	wrap := func(err error) error { return fmt.Errorf("vision.PreprocessCard [%s]: %w", req.JobID, err) }
	if err := os.MkdirAll(req.OutDir, 0o755); err != nil {
		return nil, wrap(err)
	}
	frontSrc, err := loadNRGBA(req.FrontPath)
	if err != nil {
		return nil, wrap(err)
	}
	frontQuad := orientPortrait(detectCardQuad(frontSrc))
	qw, qh := quadSize(frontQuad)
	cardType := domain.ClassifyAspect(qw, qh)
	cw, ch := domain.CanonicalSize(cardType)
	res := &domain.PreprocessResult{CardType: cardType, CanonicalWidth: cw, CanonicalHeight: ch, FrontQuad: frontQuad}

	front, err := p.canonicalFace(ctx, frontSrc, frontQuad, cw, ch, filepath.Join(req.OutDir, "front.jpg"))
	if err != nil {
		return nil, wrap(fmt.Errorf("front: %w", err))
	}
	res.CroppedFrontPath = front.path

	var back *face
	if req.BackPath != "" {
		backSrc, err := loadNRGBA(req.BackPath)
		if err != nil {
			return nil, wrap(err)
		}
		bq := orientPortrait(detectCardQuad(backSrc))
		back, err = p.canonicalFace(ctx, backSrc, bq, cw, ch, filepath.Join(req.OutDir, "back.jpg"))
		if err != nil {
			return nil, wrap(fmt.Errorf("back: %w", err))
		}
		res.BackQuad, res.CroppedBackPath = &bq, back.path
	}
	if err := ctx.Err(); err != nil {
		return nil, wrap(err)
	}

	// Centering from the front print borders (slabs are measured on the card inside the holder
	// by the grader; we skip centering for slabs).
	if cardType == domain.CardTypeRaw {
		if bw, ok := measureBorders(front.luma); ok {
			res.BorderPx = &bw
			lr, err1 := domain.CenteringRatio(bw.Left, bw.Right)
			tb, err2 := domain.CenteringRatio(bw.Top, bw.Bottom)
			if err1 == nil && err2 == nil {
				res.CenteringLR, res.CenteringTB = lr, tb
			}
		}
		// Corner fray shows best on the (usually darker) back; fall back to the front.
		wearFace := front
		if back != nil {
			wearFace = back
		}
		corners, score := cornerWear(wearFace.luma)
		res.CornerWear, res.CornerWearScore = corners, &score
		res.EstimatedCondition = domain.EstimateRawCondition(res.CenteringLR, res.CenteringTB, res.CornerWearScore)
	}

	for _, z := range domain.ZonesFor(cardType, back != nil) {
		if err := ctx.Err(); err != nil {
			return nil, wrap(err)
		}
		src := front
		if domain.ZoneSide(z) == domain.SideBack {
			src = back
		}
		r := domain.ZoneBounds(z, cw, ch)
		bin := binarize(src.luma.crop(r.X, r.Y, r.W, r.H))
		path := filepath.Join(req.OutDir, string(z)+".png")
		if err := writePNG(path, bin); err != nil {
			return nil, wrap(fmt.Errorf("zone %s: %w", z, err))
		}
		res.Zones = append(res.Zones, domain.ZoneImage{Zone: z, Side: domain.ZoneSide(z), Bounds: r, Path: path})
	}
	p.log.DebugContext(ctx, "card preprocessed", "job_id", req.JobID, "type", cardType, "lr", res.CenteringLR, "tb", res.CenteringTB)
	return res, nil
}

func (p *Preprocessor) canonicalFace(ctx context.Context, src *image.NRGBA, q [4]domain.Point, w, h int, out string) (*face, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warped, err := warpPerspective(src, q, w, h)
	if err != nil {
		return nil, err
	}
	if err := imaging.Save(warped, out, imaging.JPEGQuality(cropJPEGQuality)); err != nil {
		return nil, fmt.Errorf("save %s: %w", out, err)
	}
	return &face{quad: q, img: warped, luma: lumaPlane(warped), path: out}, nil
}

// binarize applies CLAHE (clip 2.0, 8x8 tiles) then adaptive Gaussian thresholding (15, C=4).
func binarize(p *plane) *image.Gray {
	return adaptiveGaussianThreshold(clahe(p, claheClipLimit, claheTileGrid), threshBlockSize, threshC)
}

func loadNRGBA(path string) (*image.NRGBA, error) {
	img, err := imaging.Open(path, imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return imaging.Clone(img), nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
