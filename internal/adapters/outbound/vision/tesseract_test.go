package vision

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
	"github.com/hootyeah/hootdex/internal/seed"
)

func seededChecklist(t *testing.T) ports.ChecklistRepository {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := sqlite.NewChecklistRepository(db)
	if _, err := seed.Apply(context.Background(), repo, sqlite.NewStorageRepository(db)); err != nil {
		t.Fatal(err)
	}
	return repo
}

func rawPre(withBack bool) *domain.PreprocessResult {
	zones := []domain.ZoneImage{{Zone: domain.ZoneFrontHeader, Path: "/p/FRONT_HEADER.png"}, {Zone: domain.ZoneFrontFooter, Path: "/p/FRONT_FOOTER.png"}}
	if withBack {
		zones = append(zones, domain.ZoneImage{Zone: domain.ZoneBackHeader, Path: "/p/BACK_HEADER.png"}, domain.ZoneImage{Zone: domain.ZoneBackFooter, Path: "/p/BACK_FOOTER.png"})
	}
	return &domain.PreprocessResult{CardType: domain.CardTypeRaw, Zones: zones, CenteringLR: "52/48", CenteringTB: "55/45", EstimatedCondition: domain.ConditionNMMT}
}

func fixture(texts map[domain.ZoneName]string) RecognizerFunc {
	return func(_ context.Context, _ string, z domain.ZoneName) (string, error) { return texts[z], nil }
}

func TestTesseractExtractorNoisyStroudSilverPrizm(t *testing.T) {
	ex := NewTesseractExtractor(fixture(map[domain.ZoneName]string{
		domain.ZoneFrontHeader: "PRI2M",
		domain.ZoneFrontFooter: "C.J. STR0UD  [RC]  HOUSTON TEXANS",
		domain.ZoneBackHeader:  "#339 C.J. STROUD QUARTERBACK PRIZM",
		domain.ZoneBackFooter:  "©2023 Panini America, Inc. 2022 STATS 2021",
	}), seededChecklist(t), nil)
	res, err := ex.Extract(context.Background(), ports.ExtractionRequest{JobID: "j", Preprocess: rawPre(true)})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Payload
	if p.PlayerOrCharacter != "C.J. Stroud" || p.CardNumber != "339" || p.ReleaseYear != 2023 || p.Manufacturer != "Panini" {
		t.Fatalf("identity: %+v", p)
	}
	if !p.IsRookie || p.ParallelVariant != "Silver Prizm" || p.SerialNumber != nil {
		t.Fatalf("attributes: rookie=%v parallel=%q serial=%v", p.IsRookie, p.ParallelVariant, p.SerialNumber)
	}
	if p.ConfidenceScore < domain.ReviewThreshold || p.NeedsReview() {
		t.Fatalf("confidence %.3f should auto-accept", p.ConfidenceScore)
	}
	if *p.CenteringRatioLR != "52/48" || *p.EstimatedRawCondition != "NM-MT" || res.ChecklistID == nil || res.Extractor != domain.ExtractorTesseract {
		t.Fatalf("condition/checklist: %+v", res)
	}
	if !strings.Contains(res.RawOCRBack, "[BACK_FOOTER]") || len(res.Candidates) == 0 {
		t.Fatal("raw OCR/candidates not returned")
	}
}

func TestTesseractExtractorSerialNumberedAndPokemon(t *testing.T) {
	repo := seededChecklist(t)
	ex := NewTesseractExtractor(fixture(map[domain.ZoneName]string{
		domain.ZoneFrontFooter: "BRYCE YOUNG RC O4S/I99",
		domain.ZoneBackHeader:  "#301 BRYCE YOUNG",
		domain.ZoneBackFooter:  "(C) 2023 PANINI AMERICA",
	}), repo, nil)
	res, err := ex.Extract(context.Background(), ports.ExtractionRequest{JobID: "s", Preprocess: rawPre(true)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload.SerialNumber == nil || *res.Payload.SerialNumber != "045/199" || *res.Payload.SerialMax != 199 || res.Payload.ParallelVariant != "Numbered /199" {
		t.Fatalf("serial: %+v", res.Payload)
	}

	pk := NewTesseractExtractor(fixture(map[domain.ZoneName]string{
		domain.ZoneFrontHeader: "Charizard 120 HP",
		domain.ZoneFrontFooter: "illus. Mitsuhiro Arita 4/102 © 1999 Wizards",
	}), repo, nil)
	res, err = pk.Extract(context.Background(), ports.ExtractionRequest{JobID: "p", Preprocess: rawPre(false)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload.PlayerOrCharacter != "Charizard" || res.Payload.CardNumber != "4/102" || res.Payload.SerialNumber != nil {
		t.Fatalf("pokemon: %+v", res.Payload)
	}
}

func TestTesseractExtractorIllegibleRoutesToReview(t *testing.T) {
	ex := NewTesseractExtractor(fixture(map[domain.ZoneName]string{domain.ZoneFrontFooter: "~~ ## ..", domain.ZoneBackHeader: "x"}), seededChecklist(t), nil)
	res, err := ex.Extract(context.Background(), ports.ExtractionRequest{JobID: "x", Preprocess: rawPre(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Payload.NeedsReview() || res.ChecklistID != nil {
		t.Fatalf("illegible card must need review: %+v", res.Payload)
	}
	if _, err := ex.Extract(context.Background(), ports.ExtractionRequest{JobID: "x"}); err == nil {
		t.Fatal("expected error without preprocess result")
	}
}

func TestTesseractExtractorGradedSlab(t *testing.T) {
	pre := &domain.PreprocessResult{CardType: domain.CardTypeGraded, Zones: []domain.ZoneImage{
		{Zone: domain.ZoneFrontHeader, Path: "a"}, {Zone: domain.ZoneFrontFooter, Path: "b"}, {Zone: domain.ZoneSlabFlip, Path: "c"}}}
	ex := NewTesseractExtractor(fixture(map[domain.ZoneName]string{
		domain.ZoneSlabFlip: "2023 PANINI PRIZM #339 C.J. STROUD GEM MT 10 PSA 10 84512377",
	}), seededChecklist(t), nil)
	res, err := ex.Extract(context.Background(), ports.ExtractionRequest{JobID: "g", Preprocess: pre})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Payload
	if !p.IsGraded || p.GradingCompany != domain.GraderPSA || *p.GradeValue != 10 || *p.CertNumber != "84512377" || p.CenteringRatioLR != nil {
		t.Fatalf("slab: %+v", p)
	}
	if p.PlayerOrCharacter != "C.J. Stroud" {
		t.Fatalf("slab identity: %s", p.PlayerOrCharacter)
	}
}

func TestTesseractCLIExecutesBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tesseract")
	script := "#!/bin/sh\n# args: <image> stdout -l <lang> --psm <n>\necho \"OCR:$(basename $1) lang=$4 psm=$6\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (&TesseractCLI{Bin: fake}).Recognize(context.Background(), "/x/BACK_HEADER.png", domain.ZoneBackHeader)
	if err != nil || got != "OCR:BACK_HEADER.png lang=eng psm=6" {
		t.Fatalf("got %q %v", got, err)
	}
	failing := filepath.Join(dir, "bad")
	_ = os.WriteFile(failing, []byte("#!/bin/sh\necho boom >&2\nexit 2\n"), 0o755)
	if _, err := (&TesseractCLI{Bin: failing}).Recognize(context.Background(), "x", domain.ZoneFrontHeader); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped stderr, got %v", err)
	}
}
