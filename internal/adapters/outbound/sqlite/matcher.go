package sqlite

import (
	"strconv"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/lithammer/fuzzysearch/fuzzy"
)

// Field weights for candidate re-ranking. Manufacturer is a small bonus because OCR
// zones frequently miss the legal line.
const (
	wPlayer       = 0.40
	wNumber       = 0.25
	wYear         = 0.15
	wSet          = 0.20
	mfrBonus      = 0.03
	categoryPenal = 0.85
)

// matchQuery is the parsed, de-confused OCR text.
type matchQuery struct {
	tokens       []domain.OCRToken
	years        map[int]bool
	yearHint     int
	categoryHint domain.Category
}

func parseMatchQuery(raw string, yearHint int, categoryHint domain.Category) matchQuery {
	q := matchQuery{tokens: domain.TokenizeOCR(raw), years: map[int]bool{}, yearHint: yearHint, categoryHint: categoryHint}
	for _, t := range q.tokens {
		for _, v := range t.Variants {
			if len(v) == 4 && (strings.HasPrefix(v, "19") || strings.HasPrefix(v, "20")) {
				if y, err := strconv.Atoi(v); err == nil {
					q.years[y] = true
				}
			}
		}
	}
	return q
}

func (q matchQuery) allYears() []int {
	var out []int
	for y := range q.years {
		out = append(out, y)
	}
	if q.yearHint > 0 && !q.years[q.yearHint] {
		out = append(out, q.yearHint)
	}
	return out
}

// ftsExpression ORs every distinct alphanumeric sub-token of every variant, each quoted
// so FTS5 operators in OCR garbage can never be interpreted as query syntax.
func (q matchQuery) ftsExpression() string {
	seen := map[string]bool{}
	var terms []string
	for _, t := range q.tokens {
		for _, v := range t.Variants {
			for _, sub := range strings.FieldsFunc(v, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') }) {
				if len(sub) < 2 && !(sub >= "0" && sub <= "9") || seen[sub] {
					continue
				}
				seen[sub] = true
				terms = append(terms, `"`+sub+`"`)
			}
		}
	}
	return strings.Join(terms, " OR ")
}

// score returns 0..1 for how well the entry explains the OCR text.
func (q matchQuery) score(e domain.ChecklistEntry) float64 {
	s := wPlayer*q.playerScore(e.PlayerOrCharacter) +
		wNumber*q.numberScore(e.CardNumber) +
		wYear*q.yearScore(e.ReleaseYear) +
		wSet*q.wordsScore(domain.NameTokens(e.SetName))
	if q.wordsScore(domain.NameTokens(e.Manufacturer)) >= 0.8 {
		s += mfrBonus
	}
	if q.categoryHint != "" && q.categoryHint != e.Category {
		s *= categoryPenal
	}
	if s > 1 {
		s = 1
	}
	return s
}

func (q matchQuery) yearScore(year int) float64 {
	switch {
	case q.years[year]:
		return 1
	case q.yearHint == year:
		return 1
	case len(q.years) == 0 && q.yearHint == 0:
		return 0.5 // no evidence either way
	default:
		return 0
	}
}

// numberScore: exact canonical match = 1; near misses are discounted heavily since one
// wrong digit is a different card.
func (q matchQuery) numberScore(number string) float64 {
	want := domain.AlphaNum(domain.NormalizeCardNumber(number))
	if want == "" {
		return 0
	}
	best := 0.0
	for _, t := range q.tokens {
		for _, v := range t.Variants {
			got := domain.AlphaNum(domain.NormalizeCardNumber(v))
			if got == "" {
				continue
			}
			if got == want {
				return 1
			}
			if sim := similarity(got, want); sim*0.5 > best {
				best = sim * 0.5
			}
		}
	}
	return best
}

// playerScore compares the name against every window of consecutive OCR tokens
// (so "CJ STR0UD" and "CJSTROUD" both match "C.J. Stroud"), and against per-token
// best matches (so a surname alone still earns partial credit).
func (q matchQuery) playerScore(name string) float64 {
	nameToks := domain.NameTokens(name)
	if len(nameToks) == 0 {
		return 0
	}
	target := strings.Join(nameToks, "")
	alpha := q.alphaForms()
	best := 0.0
	maxWin := len(nameToks) + 1
	for i := range alpha {
		var b strings.Builder
		for w := 0; w < maxWin && i+w < len(alpha); w++ {
			b.WriteString(alpha[i+w])
			if sim := similarity(b.String(), target); sim > best {
				best = sim
			}
		}
	}
	if per := q.wordsScore(nameToks); per > best {
		best = per
	}
	return best
}

// wordsScore averages, over the target words, the best similarity to any OCR token variant.
func (q matchQuery) wordsScore(words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	total := 0.0
	for _, w := range words {
		best := 0.0
		for _, t := range q.tokens {
			for _, v := range t.Variants {
				if sim := similarity(domain.AlphaNum(v), w); sim > best {
					best = sim
				}
			}
		}
		total += best
	}
	return total / float64(len(words))
}

// alphaForms picks, per token, the most letter-like variant (the de-confused word form).
func (q matchQuery) alphaForms() []string {
	out := make([]string, 0, len(q.tokens))
	for _, t := range q.tokens {
		best, bestLetters := domain.AlphaNum(t.Variants[0]), -1
		for _, v := range t.Variants {
			a := domain.AlphaNum(v)
			n := 0
			for _, r := range a {
				if r >= 'A' && r <= 'Z' {
					n++
				}
			}
			if n > bestLetters {
				best, bestLetters = a, n
			}
		}
		out = append(out, best)
	}
	return out
}

// similarity is 1 - normalized Levenshtein distance.
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	d := fuzzy.LevenshteinDistance(a, b)
	m := len(a)
	if len(b) > m {
		m = len(b)
	}
	s := 1 - float64(d)/float64(m)
	if s < 0 {
		return 0
	}
	return s
}
