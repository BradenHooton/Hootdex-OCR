package vision

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
	"github.com/hootyeah/hootdex/internal/testutil/synthcard"
)

func writeSpec(t *testing.T, dir, name string, s synthcard.Spec) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := synthcard.Save(synthcard.Render(s), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func ratioNear(t *testing.T, label, got string, wantA int, tol int) {
	t.Helper()
	var a, b int
	if _, err := fmt.Sscanf(got, "%d/%d", &a, &b); err != nil {
		t.Fatalf("%s: unparsable ratio %q", label, got)
	}
	if a+b != 100 || abs(a-wantA) > tol {
		t.Fatalf("%s = %s, want ~%d/%d (±%d)", label, got, wantA, 100-wantA, tol)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func assertBinaryPNG(t *testing.T, path string, wantW, wantH int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("%s is %T, want *image.Gray", path, img)
	}
	if g.Bounds().Dx() != wantW || g.Bounds().Dy() != wantH {
		t.Fatalf("%s size %v", path, g.Bounds())
	}
	black, white := 0, 0
	for _, v := range g.Pix {
		switch v {
		case 0:
			black++
		case 255:
			white++
		default:
			t.Fatalf("%s not binarized: value %d", path, v)
		}
	}
	if black == 0 || white == 0 {
		t.Fatalf("%s degenerate binarization (black=%d white=%d)", path, black, white)
	}
}

func TestPreprocessRawCardDeskewCenteringWearAndZones(t *testing.T) {
	dir := t.TempDir()
	front := writeSpec(t, dir, "B1_0001_front.jpg", synthcard.Front())
	back := writeSpec(t, dir, "B1_0001_back.jpg", synthcard.Back())
	out := filepath.Join(dir, "processed", "job1")

	res, err := NewPreprocessor(nil).Preprocess(context.Background(), ports.PreprocessRequest{JobID: "job1", FrontPath: front, BackPath: back, OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.CardType != domain.CardTypeRaw || res.CanonicalWidth != 750 || res.CanonicalHeight != 1050 {
		t.Fatalf("type/size: %s %dx%d", res.CardType, res.CanonicalWidth, res.CanonicalHeight)
	}
	// Synthetic front: L=40 R=30 -> 57/43 ; T=B=36 -> 50/50.
	ratioNear(t, "centering_lr", res.CenteringLR, 57, 2)
	ratioNear(t, "centering_tb", res.CenteringTB, 50, 2)
	if res.BorderPx == nil || math.Abs(res.BorderPx.Left-40) > 4 || math.Abs(res.BorderPx.Right-30) > 4 {
		t.Fatalf("borders %+v", res.BorderPx)
	}
	// Back fray is on TL (0) and BR (2) only.
	if len(res.CornerWear) != 4 || res.CornerWearScore == nil {
		t.Fatalf("corner wear missing: %+v", res.CornerWear)
	}
	cw := res.CornerWear
	if cw[0] < 0.3 || cw[2] < 0.3 || cw[1] > 0.05 || cw[3] > 0.05 {
		t.Fatalf("per-corner wear %v", cw)
	}
	if *res.CornerWearScore <= 0.3 {
		t.Fatalf("wear score %.3f too low for two frayed corners", *res.CornerWearScore)
	}
	if res.EstimatedCondition == "" {
		t.Fatal("condition not estimated")
	}
	if len(res.Zones) != 4 {
		t.Fatalf("zones %d", len(res.Zones))
	}
	for _, z := range res.Zones {
		assertBinaryPNG(t, z.Path, z.Bounds.W, z.Bounds.H)
	}
	for _, p := range []string{res.CroppedFrontPath, res.CroppedBackPath} {
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Fatalf("crop %s missing", p)
		}
	}
	// Deskew: detected TL corner should be near the rendered card's rotated TL corner.
	if res.FrontQuad[0].X < 100 || res.FrontQuad[0].X > 200 || res.FrontQuad[0].Y < 90 || res.FrontQuad[0].Y > 160 {
		t.Fatalf("front TL corner %+v", res.FrontQuad[0])
	}
}

func TestPreprocessGradedSlabAddsFlipZone(t *testing.T) {
	dir := t.TempDir()
	front := writeSpec(t, dir, "S_0001_front.jpg", synthcard.Slab())
	res, err := NewPreprocessor(nil).Preprocess(context.Background(), ports.PreprocessRequest{JobID: "slab", FrontPath: front, OutDir: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatal(err)
	}
	if res.CardType != domain.CardTypeGraded || res.CanonicalWidth != 900 || res.CanonicalHeight != 1450 {
		t.Fatalf("slab: %s %dx%d", res.CardType, res.CanonicalWidth, res.CanonicalHeight)
	}
	flip, ok := res.Zone(domain.ZoneSlabFlip)
	if !ok || flip.Bounds.H != int(math.Round(0.22*1450)) {
		t.Fatalf("slab flip zone: %+v %v", flip, ok)
	}
	assertBinaryPNG(t, flip.Path, 900, flip.Bounds.H)
	if len(res.Zones) != 3 || res.CroppedBackPath != "" || res.CenteringLR != "" {
		t.Fatalf("slab zones=%d back=%q lr=%q", len(res.Zones), res.CroppedBackPath, res.CenteringLR)
	}
}

func TestPreprocessAlreadyCroppedScan(t *testing.T) {
	// Scanner auto-crop: the image *is* the card (no bed visible).
	s := synthcard.Front()
	s.CanvasW, s.CanvasH, s.AngleDeg = 750, 1050, 0
	dir := t.TempDir()
	front := writeSpec(t, dir, "crop_front.jpg", s)
	res, err := NewPreprocessor(nil).Preprocess(context.Background(), ports.PreprocessRequest{JobID: "c", FrontPath: front, OutDir: filepath.Join(dir, "o")})
	if err != nil {
		t.Fatal(err)
	}
	if res.FrontQuad[0].X != 0 || res.FrontQuad[2].X != 749 {
		t.Fatalf("expected full-frame quad, got %+v", res.FrontQuad)
	}
	ratioNear(t, "centering_lr", res.CenteringLR, 57, 2)
}

func TestPreprocessErrors(t *testing.T) {
	_, err := NewPreprocessor(nil).Preprocess(context.Background(), ports.PreprocessRequest{JobID: "x", FrontPath: "/nope.jpg", OutDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	front := writeSpec(t, dir, "f.jpg", synthcard.Front())
	if _, err := NewPreprocessor(nil).Preprocess(ctx, ports.PreprocessRequest{JobID: "x", FrontPath: front, OutDir: dir}); err == nil {
		t.Fatal("expected context error")
	}
}

func TestHomographyRoundTrip(t *testing.T) {
	from := [4]domain.Point{{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 140}, {X: 0, Y: 140}}
	to := [4]domain.Point{{X: 10, Y: 5}, {X: 115, Y: 12}, {X: 108, Y: 150}, {X: 3, Y: 146}}
	h, err := homography(from, to)
	if err != nil {
		t.Fatal(err)
	}
	for i := range from {
		x, y := from[i].X, from[i].Y
		den := h[6]*x + h[7]*y + 1
		u, v := (h[0]*x+h[1]*y+h[2])/den, (h[3]*x+h[4]*y+h[5])/den
		if math.Abs(u-to[i].X) > 1e-6 || math.Abs(v-to[i].Y) > 1e-6 {
			t.Fatalf("corner %d maps to (%f,%f) want %+v", i, u, v, to[i])
		}
	}
	if _, err := homography(from, [4]domain.Point{}); err == nil {
		t.Fatal("expected degenerate error")
	}
}

func TestClaheAndThresholdBounds(t *testing.T) {
	p := newPlane(64, 48)
	for i := range p.pix {
		p.pix[i] = float32(100 + (i%64)/4) // low-contrast ramp
	}
	eq := clahe(p, 2.0, 8)
	lo, hi := float32(255), float32(0)
	for _, v := range eq.pix {
		lo, hi = min(lo, v), max(hi, v)
		if v < 0 || v > 255 {
			t.Fatalf("clahe out of range %f", v)
		}
	}
	if hi-lo <= 16 {
		t.Fatalf("CLAHE did not stretch contrast: %f..%f", lo, hi)
	}
	g := adaptiveGaussianThreshold(eq, 15, 4)
	if g.Bounds().Dx() != 64 {
		t.Fatal("threshold size")
	}
}
