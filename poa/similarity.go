package poa

import (
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Normalisation & similarity
// ---------------------------------------------------------------------------

var nonAlnum = regexp.MustCompile(`[^a-z0-9 ]+`)

func normalize(s string) string {
	s = strings.ReplaceAll(s, "Â", " ") // mojibake 'Â'
	s = strings.ReplaceAll(s, " ", " ") // NBSP
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// stem folds trivial English plurals so "surcharge" and "surcharges" are one
// token. Without it PF129268's surcharge scored 0.000 against the BC line
// literally named "Vendor Surcharges".
func stem(t string) string {
	switch {
	case len(t) > 4 && strings.HasSuffix(t, "ies"):
		return t[:len(t)-3] + "y"
	case len(t) > 3 && strings.HasSuffix(t, "es") && hasSibilantStem(t[:len(t)-2]):
		return t[:len(t)-2] // boxes -> box, dishes -> dish
	case len(t) > 3 && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss"):
		return t[:len(t)-1]
	}
	return t
}

// hasSibilantStem reports whether an "-es" plural is the sibilant kind
// (box/boxes) rather than a plain +s on a word already ending in e
// (surcharge/surcharges).
func hasSibilantStem(base string) bool {
	for _, suf := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range strings.Fields(s) {
		set[stem(t)] = true
	}
	return set
}

// overlapSimilarity is |A ∩ B| / min(|A|,|B|).
//
// Unlike Dice it does not penalise the side carrying more tokens, which
// matters because a BC group's comment count is a property of how the product
// was configured, not of how well it matches.
func overlapSimilarity(a, b string) float64 {
	setA, setB := tokenSet(normalize(a)), tokenSet(normalize(b))
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	inter := 0
	for t := range setA {
		if setB[t] {
			inter++
		}
	}
	return float64(inter) / float64(min(len(setA), len(setB)))
}

// overlapSimilaritySets is overlapSimilarity for already-tokenized sets —
// used for the cleaned score, where tokenization has already gone through
// cleanTokenSet's alias/stem/stopword pipeline and must not be redone.
func overlapSimilaritySets(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(min(len(a), len(b)))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
