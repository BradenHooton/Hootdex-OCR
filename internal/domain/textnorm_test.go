package domain

import (
	"reflect"
	"testing"
)

func TestTokenVariants(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"PR1ZM", []string{"PR1ZM", "PRIZM"}},
		{"STR0UD", []string{"STR0UD", "STROUD"}},
		{"I4Z/Z0O", []string{"I4Z/Z0O", "142/200"}},
		{"O4S/I99", []string{"O4S/I99", "045/199"}},
		{"U5175", []string{"U5175", "US175"}},
		{"339", []string{"339"}},
		{"STROUD", []string{"STROUD"}},
	}
	for _, tt := range tests {
		got := TokenVariants(tt.in)
		for _, w := range tt.want {
			found := false
			for _, g := range got {
				if g == w {
					found = true
				}
			}
			if !found {
				t.Errorf("TokenVariants(%q) = %v, missing %q", tt.in, got, w)
			}
		}
		if got[0] != tt.in {
			t.Errorf("raw variant must come first: %v", got)
		}
	}
}

func TestTokenizeOCR(t *testing.T) {
	toks := TokenizeOCR("2023 PR1ZM #339 C.J. STR0UD © 2023 Panini")
	var raws []string
	for _, tk := range toks {
		raws = append(raws, tk.Raw)
	}
	want := []string{"2023", "PR1ZM", "339", "CJ", "STR0UD", "2023", "PANINI"}
	if !reflect.DeepEqual(raws, want) {
		t.Fatalf("got %v want %v", raws, want)
	}
}

func TestNameTokensAndAlphaNum(t *testing.T) {
	if got := NameTokens("C.J. Stroud"); !reflect.DeepEqual(got, []string{"CJ", "STROUD"}) {
		t.Fatalf("NameTokens: %v", got)
	}
	if got := NameTokens("Jaxon Smith-Njigba"); len(got) != 3 {
		t.Fatalf("NameTokens hyphen: %v", got)
	}
	if AlphaNum("Ronald Acuña Jr.") != "RONALDACUAJR" {
		t.Fatalf("AlphaNum: %s", AlphaNum("Ronald Acuña Jr."))
	}
}

func TestNormalizeCardNumber(t *testing.T) {
	tests := map[string]string{
		"#339": "339", "004/102": "4/102", "4/102": "4/102", "025/165": "25/165",
		"us175": "US175", "LOB-005": "LOB5", "100": "100", "0": "0", "SV049": "SV49", "BDA-CJ": "BDACJ",
	}
	for in, want := range tests {
		if got := NormalizeCardNumber(in); got != want {
			t.Errorf("NormalizeCardNumber(%q) = %q want %q", in, got, want)
		}
	}
}
