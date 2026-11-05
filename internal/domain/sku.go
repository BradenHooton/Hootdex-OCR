package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// SKUPrefix builds the identity portion of a SKU, e.g. "HY-FB-2023-PRZ-339".
// The repository appends a per-prefix sequence with FormatSKU.
func SKUPrefix(c *Card) string {
	return fmt.Sprintf("HY-%s-%04d-%s-%s", c.Category.SKUCode(), c.ReleaseYear, setAbbrev(c.SetName), skuToken(c.CardNumber, 12))
}

// FormatSKU appends a zero-padded sequence: "HY-FB-2023-PRZ-339-001".
func FormatSKU(prefix string, seq int) string { return fmt.Sprintf("%s-%03d", prefix, seq) }

// setAbbrev turns a set name into a 3-letter code: "Prizm" -> "PRZ", "Topps Chrome" -> "TCH", "Base Set" -> "BST".
func setAbbrev(set string) string {
	words := strings.FieldsFunc(strings.ToUpper(set), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return "UNK"
	}
	var out []rune
	if len(words) == 1 {
		w := []rune(words[0])
		out = append(out, w[0])
		for _, r := range w[1:] { // consonants first for a readable code
			if !strings.ContainsRune("AEIOU", r) {
				out = append(out, r)
			}
		}
		for _, r := range w[1:] {
			out = append(out, r)
		}
	} else {
		for _, w := range words {
			out = append(out, []rune(w)[0])
		}
		for _, r := range []rune(words[len(words)-1])[1:] {
			if !strings.ContainsRune("AEIOU", r) {
				out = append(out, r)
			}
		}
	}
	for len(out) < 3 {
		out = append(out, 'X')
	}
	return string(out[:3])
}

// skuToken keeps only A-Z0-9 (so "102/102" -> "102102") truncated to max characters.
func skuToken(s string, max int) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = "0"
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}
