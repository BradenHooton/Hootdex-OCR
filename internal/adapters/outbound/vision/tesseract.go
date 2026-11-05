package vision

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// TesseractCLI is a TextRecognizer that shells out to the `tesseract` binary
// (no CGO, so it builds on linux/amd64 and darwin/arm64 alike).
type TesseractCLI struct {
	Bin  string // default "tesseract"
	Lang string // default "eng"
}

var _ ports.TextRecognizer = (*TesseractCLI)(nil)

// pageSegMode picks a Tesseract PSM per zone: 6 = uniform block, 11 = sparse text.
func pageSegMode(z domain.ZoneName) int {
	switch z {
	case domain.ZoneFrontHeader, domain.ZoneFrontFooter:
		return 11 // logos, names and badges scattered over artwork
	default:
		return 6
	}
}

// Recognize runs OCR on one zone image and returns the text.
func (t *TesseractCLI) Recognize(ctx context.Context, imagePath string, zone domain.ZoneName) (string, error) {
	bin, lang := t.Bin, t.Lang
	if bin == "" {
		bin = "tesseract"
	}
	if lang == "" {
		lang = "eng"
	}
	cmd := exec.CommandContext(ctx, bin, imagePath, "stdout", "-l", lang, "--psm", strconv.Itoa(pageSegMode(zone)))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("vision.TesseractCLI [%s]: %w: %s", zone, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RecognizerFunc adapts a function to ports.TextRecognizer (used for fixtures and tests).
type RecognizerFunc func(ctx context.Context, imagePath string, zone domain.ZoneName) (string, error)

// Recognize implements ports.TextRecognizer.
func (f RecognizerFunc) Recognize(ctx context.Context, imagePath string, zone domain.ZoneName) (string, error) {
	return f(ctx, imagePath, zone)
}

// TesseractExtractor OCRs the sliced zones locally, retrieves checklist candidates (RAG)
// and assembles a CardExtractionPayload deterministically.
type TesseractExtractor struct {
	rec       ports.TextRecognizer
	checklist ports.ChecklistRepository
	log       *slog.Logger
}

var _ ports.OCRExtractor = (*TesseractExtractor)(nil)

// NewTesseractExtractor constructs the adapter.
func NewTesseractExtractor(rec ports.TextRecognizer, checklist ports.ChecklistRepository, log *slog.Logger) *TesseractExtractor {
	if log == nil {
		log = slog.Default()
	}
	return &TesseractExtractor{rec: rec, checklist: checklist, log: log}
}

// Kind implements ports.OCRExtractor.
func (e *TesseractExtractor) Kind() domain.ExtractorKind { return domain.ExtractorTesseract }

// Extract implements ports.OCRExtractor.
func (e *TesseractExtractor) Extract(ctx context.Context, req ports.ExtractionRequest) (*ports.ExtractionResult, error) {
	if req.Preprocess == nil {
		return nil, fmt.Errorf("vision.TesseractExtractor [%s]: %w: preprocess result required", req.JobID, domain.ErrInvalid)
	}
	texts := map[domain.ZoneName]string{}
	for _, z := range req.Preprocess.Zones {
		txt, err := e.rec.Recognize(ctx, z.Path, z.Zone)
		if err != nil {
			return nil, fmt.Errorf("vision.TesseractExtractor [%s]: %w", req.JobID, err)
		}
		texts[z.Zone] = txt
	}
	res, err := AssembleFromZoneText(ctx, e.checklist, texts, req.Preprocess)
	if err != nil {
		return nil, fmt.Errorf("vision.TesseractExtractor [%s]: %w", req.JobID, err)
	}
	res.Extractor = domain.ExtractorTesseract
	return res, nil
}

// Thresholds for snapping OCR text to the master checklist.
const (
	snapMinScore     = 0.60
	ambiguousMargin  = 0.05
	ambiguityPenalty = 0.90
	yearConflict     = 0.80
	unsnappedFactor  = 0.30
	missingFlipPenal = 0.90
)

func joinZones(texts map[domain.ZoneName]string, zones ...domain.ZoneName) string {
	var parts []string
	for _, z := range zones {
		if t := strings.TrimSpace(texts[z]); t != "" {
			parts = append(parts, fmt.Sprintf("[%s] %s", z, t))
		}
	}
	return strings.Join(parts, "\n")
}

// AssembleFromZoneText builds a payload from per-zone OCR text following the domain's
// source-of-truth rules, snapping identity to the best checklist candidate.
func AssembleFromZoneText(ctx context.Context, checklist ports.ChecklistRepository, texts map[domain.ZoneName]string, pre *domain.PreprocessResult) (*ports.ExtractionResult, error) {
	front := joinZones(texts, domain.ZoneFrontHeader, domain.ZoneFrontFooter, domain.ZoneSlabFlip)
	back := joinZones(texts, domain.ZoneBackHeader, domain.ZoneBackFooter)
	plain := func(zs ...domain.ZoneName) string {
		var s []string
		for _, z := range zs {
			s = append(s, texts[z])
		}
		return strings.Join(s, " ")
	}

	year, hasYear := domain.ExtractCopyrightYear(texts[domain.ZoneBackFooter])
	if !hasYear {
		year, hasYear = domain.ExtractCopyrightYear(plain(domain.ZoneBackHeader, domain.ZoneFrontFooter, domain.ZoneSlabFlip))
	}
	yearHint := 0
	if hasYear {
		yearHint = year
	}
	cands, err := checklist.MatchCandidates(ctx, plain(domain.ZoneFrontHeader, domain.ZoneFrontFooter, domain.ZoneSlabFlip, domain.ZoneBackHeader, domain.ZoneBackFooter), yearHint, "")
	if err != nil {
		return nil, err
	}

	p := domain.CardExtractionPayload{ParallelVariant: domain.BaseVariant, GradingCompany: domain.GraderRaw}
	var notes []string
	var checklistID *int64
	conf := 0.0
	if len(cands) > 0 && cands[0].Score >= snapMinScore {
		top := cands[0]
		p.SnapToChecklist(top.Entry)
		id := top.Entry.ID
		checklistID = &id
		conf = top.Score
		notes = append(notes, fmt.Sprintf("Matched checklist %q (score %.2f).", top.Entry.DisplayName(), top.Score))
		if len(cands) > 1 && top.Score-cands[1].Score < ambiguousMargin {
			conf *= ambiguityPenalty
			notes = append(notes, fmt.Sprintf("Ambiguous with %q (%.2f).", cands[1].Entry.DisplayName(), cands[1].Score))
		}
		if hasYear && year != top.Entry.ReleaseYear {
			conf *= yearConflict
			notes = append(notes, fmt.Sprintf("Copyright year %d disagrees with checklist year %d.", year, top.Entry.ReleaseYear))
		}
		if strings.EqualFold(top.Entry.SetName, "Prizm") && domain.HasPrizmStamp(plain(domain.ZoneBackHeader, domain.ZoneBackFooter)) &&
			contains(top.Entry.KnownParallels, "Silver Prizm") {
			p.ParallelVariant = "Silver Prizm"
			notes = append(notes, "PRIZM stamp on back indicates Silver Prizm parallel.")
		}
	} else {
		// No trustworthy checklist hit: best-effort raw fields, routed to review.
		if hasYear {
			p.ReleaseYear = year
		}
		if n, ok := domain.ParseSportsCardNumber(plain(domain.ZoneBackHeader)); ok {
			p.CardNumber = n
		} else if n, ok := domain.ParseCollectorNumber(plain(domain.ZoneFrontFooter)); ok {
			p.CardNumber = n
		}
		if len(cands) > 0 {
			conf = cands[0].Score * unsnappedFactor
		}
		notes = append(notes, "No checklist candidate above snap threshold.")
	}
	if domain.HasRookieBadge(plain(domain.ZoneFrontHeader, domain.ZoneFrontFooter)) {
		p.IsRookie = true
	}
	if !p.Category.IsTCG() { // TCG "025/165" collector numbers are not serials
		if s, max, ok := domain.ParseSerial(plain(domain.ZoneFrontHeader, domain.ZoneFrontFooter, domain.ZoneBackHeader)); ok {
			p.SerialNumber, p.SerialMax = domain.StrPtr(s), &max
			if p.ParallelVariant == domain.BaseVariant {
				p.ParallelVariant = fmt.Sprintf("Numbered /%d", max)
			}
			notes = append(notes, "Serial "+s+" detected.")
		}
	}
	if pre != nil && pre.CardType == domain.CardTypeGraded {
		if l, ok := domain.ParseSlabLabel(texts[domain.ZoneSlabFlip]); ok {
			g := l.Grade
			p.IsGraded, p.GradingCompany, p.GradeValue, p.CertNumber = true, l.Company, &g, domain.StrPtr(l.Cert)
			notes = append(notes, fmt.Sprintf("Slab label %s %.1f.", l.Company, l.Grade))
		} else {
			conf *= missingFlipPenal
			notes = append(notes, "Graded slab detected but flip label unreadable.")
		}
	}
	if pre != nil && !p.IsGraded {
		p.CenteringRatioLR, p.CenteringRatioTB = domain.StrPtr(pre.CenteringLR), domain.StrPtr(pre.CenteringTB)
		p.EstimatedRawCondition = domain.StrPtr(string(pre.EstimatedCondition))
	}
	p.ConfidenceScore = conf
	p.ExtractionNotes = strings.Join(notes, " ")
	p.Normalize()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &ports.ExtractionResult{Payload: p, RawOCRFront: front, RawOCRBack: back, Candidates: cands, ChecklistID: checklistID}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
