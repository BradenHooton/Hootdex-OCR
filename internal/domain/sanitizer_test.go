package domain

import "testing"

func TestExtractCopyrightYear(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"© 2023 Panini America, Inc.", 2023, true},
		{"(C) 2024 THE TOPPS COMPANY", 2024, true},
		{"©2OZ3 Panini", 2023, true},
		{"COPYRIGHT 1999 WIZARDS", 1999, true},
		{"2022 STATS 1,520 YDS  2021 3,001", 0, false}, // stat years must not be used
		{"no year here", 0, false},
	}
	for _, tt := range tests {
		got, ok := ExtractCopyrightYear(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ExtractCopyrightYear(%q) = %d,%v want %d,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDeconfuseAndParseSerial(t *testing.T) {
	if got := DeconfuseNumeric("#I4Z/Z0O"); got != "142/200" {
		t.Fatalf("DeconfuseNumeric = %s", got)
	}
	tests := []struct {
		in     string
		serial string
		max    int
		ok     bool
	}{
		{"#I4Z/Z0O", "142/200", 200, true},
		{"O4S/I99", "045/199", 199, true},
		{"stamped 1/1 foil", "1/1", 1, true},
		{"08 / 10", "08/10", 10, true},
		{"300/199", "", 0, false}, // number above print run is not a serial
		{"nothing", "", 0, false},
	}
	for _, tt := range tests {
		s, m, ok := ParseSerial(tt.in)
		if s != tt.serial || m != tt.max || ok != tt.ok {
			t.Errorf("ParseSerial(%q) = %q,%d,%v want %q,%d,%v", tt.in, s, m, ok, tt.serial, tt.max, tt.ok)
		}
	}
}

func TestCardNumbers(t *testing.T) {
	if n, ok := ParseCollectorNumber("Charizard illus. Mitsuhiro Arita 4/102 ★"); !ok || n != "4/102" {
		t.Fatalf("collector: %s %v", n, ok)
	}
	if n, ok := ParseCollectorNumber("SV 151 O25/165"); !ok || n != "025/165" {
		t.Fatalf("collector deconfuse: %s %v", n, ok)
	}
	tests := map[string]string{"#339 C.J. STROUD": "339", "NO. U5175": "US175", "#3Z9": "329", "CARD #BDA-CJ": "BDA-CJ"}
	for in, want := range tests {
		if got, ok := ParseSportsCardNumber(in); !ok || got != want {
			t.Errorf("ParseSportsCardNumber(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

func TestRookieAndPrizm(t *testing.T) {
	for _, s := range []string{"C.J. STROUD RC", "[RC] HOUSTON", "R C TEXANS", "ROOKIE CARD"} {
		if !HasRookieBadge(s) {
			t.Errorf("rookie not detected in %q", s)
		}
	}
	for _, s := range []string{"ARCHER", "TRACKS", "HOUSTON TEXANS"} {
		if HasRookieBadge(s) {
			t.Errorf("false rookie in %q", s)
		}
	}
	if !HasPrizmStamp("#339 PRI2M") || HasPrizmStamp("#339 SELECT") {
		t.Fatal("prizm stamp")
	}
}

func TestParseSlabLabel(t *testing.T) {
	l, ok := ParseSlabLabel("2023 PANINI PRIZM #339 C.J. STROUD GEM MT PSA 10 84512377")
	if !ok || l.Company != GraderPSA || l.Grade != 10 || l.Cert != "84512377" {
		t.Fatalf("PSA: %+v %v", l, ok)
	}
	l, ok = ParseSlabLabel("BECKETT GRADING ... 9.5 GEM MINT")
	if !ok || l.Company != GraderBGS || l.Grade != 9.5 {
		t.Fatalf("BGS: %+v %v", l, ok)
	}
	if _, ok := ParseSlabLabel("no slab"); ok {
		t.Fatal("false slab")
	}
}

func TestBestVocabularyMatch(t *testing.T) {
	vocab := []string{"Prizm", "Optic", "Select", "Topps Chrome"}
	for in, want := range map[string]string{"PRI2M": "Prizm", "0PTIC": "Optic", "SELECT": "Select"} {
		got, _, ok := BestVocabularyMatch(in, vocab, 0.75)
		if !ok || got != want {
			t.Errorf("BestVocabularyMatch(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, _, ok := BestVocabularyMatch("ZZZZZ", vocab, 0.75); ok {
		t.Fatal("garbage matched")
	}
	if Similarity("abc", "abc") != 1 || Similarity("", "x") != 0 {
		t.Fatal("similarity")
	}
}
