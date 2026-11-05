package domain

import (
	"errors"
	"testing"
)

func TestClassifyAspect(t *testing.T) {
	tests := []struct {
		w, h float64
		want CardType
	}{
		{2.5, 3.5, CardTypeRaw},
		{2.32, 3.38, CardTypeRaw},
		{3.25, 5.3, CardTypeGraded},
		{100, 150, CardTypeRaw}, // exactly 1.50 is not > 1.50
		{0, 10, CardTypeRaw},
	}
	for _, tt := range tests {
		if got := ClassifyAspect(tt.w, tt.h); got != tt.want {
			t.Errorf("ClassifyAspect(%v,%v) = %s want %s", tt.w, tt.h, got, tt.want)
		}
	}
	if w, h := CanonicalSize(CardTypeGraded); w != 900 || h != 1450 {
		t.Fatal("slab canonical size")
	}
}

func TestZoneBounds(t *testing.T) {
	tests := []struct {
		z    ZoneName
		want Rect
	}{
		{ZoneFrontHeader, Rect{0, 0, 750, 189}},
		{ZoneFrontFooter, Rect{0, 819, 750, 231}},
		{ZoneBackHeader, Rect{0, 0, 750, 210}},
		{ZoneBackFooter, Rect{0, 893, 750, 157}},
		{ZoneSlabFlip, Rect{0, 0, 750, 231}},
	}
	for _, tt := range tests {
		got := ZoneBounds(tt.z, 750, 1050)
		if got != tt.want {
			t.Errorf("%s: %+v want %+v", tt.z, got, tt.want)
		}
		if got.Y+got.H > 1050 {
			t.Errorf("%s exceeds image", tt.z)
		}
	}
	if n := len(ZonesFor(CardTypeGraded, true)); n != 5 {
		t.Fatalf("slab zones %d", n)
	}
	if n := len(ZonesFor(CardTypeRaw, false)); n != 2 {
		t.Fatalf("front-only zones %d", n)
	}
	if ZoneSide(ZoneBackFooter) != SideBack {
		t.Fatal("zone side")
	}
}

func TestCenteringRatio(t *testing.T) {
	tests := []struct {
		a, b float64
		want string
	}{
		{40, 30, "57/43"}, {30, 30, "50/50"}, {52, 48, "52/48"}, {0, 10, "0/100"},
	}
	for _, tt := range tests {
		got, err := CenteringRatio(tt.a, tt.b)
		if err != nil || got != tt.want {
			t.Errorf("CenteringRatio(%v,%v) = %s, %v", tt.a, tt.b, got, err)
		}
	}
	if _, err := CenteringRatio(0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("expected invalid")
	}
}

func TestEstimateRawCondition(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		lr, tb string
		wear   *float64
		want   RawCondition
	}{
		{"51/49", "53/47", f(0.02), ConditionNMMT},
		{"", "", nil, ConditionNMMT},
		{"58/42", "50/50", f(0.05), ConditionNM},
		{"50/50", "50/50", f(0.2), ConditionNM},
		{"35/65", "50/50", f(0.1), ConditionEXMT},
		{"80/20", "50/50", f(0.1), ConditionVGEX},
		{"50/50", "50/50", f(0.9), ConditionPoor},
	}
	for _, tt := range tests {
		if got := EstimateRawCondition(tt.lr, tt.tb, tt.wear); got != tt.want {
			t.Errorf("EstimateRawCondition(%s,%s) = %s want %s", tt.lr, tt.tb, got, tt.want)
		}
	}
}
