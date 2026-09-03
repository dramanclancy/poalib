package poa

import (
	"math"
	"strings"
)

// Vocab holds the discriminative weight of each description token, derived
// from the documents being matched rather than from a hand-maintained
// per-vendor word list.
//
// v6.3 replaces the vendor-keyed stopword maps this file held in v6.1/v6.2.
// Those required someone to notice a new supplier's boilerplate — "WM FABRIC
// GRADE B" on the BC side, "Body: All Over Self Finished" on the POA side —
// and hand-add it before that supplier could match well. It only ever happened
// for the vendors somebody looked at; every other supplier scored against a
// denominator full of noise, and onboarding one meant a code change.
//
// The replacement rests on the observation the stoplist was approximating:
// boilerplate is not a property of a word, it is a property of how often the
// word repeats across the candidates being told apart. A token printed on
// every BC group of a purchase order cannot discriminate between those groups
// whatever it means; a token printed on one of them can. So each token is
// weighted by how rare it is in the corpus it came from, and the data says
// what the stoplist used to assert.
//
// Weights are computed per side and combined with min(): a token that is
// boilerplate on EITHER side is boilerplate for the pairing. That reproduces
// the old table's structure exactly — it was the union of BC comment labels
// and POA scaffolding words — without naming a single word.
type Vocab struct {
	// weight maps a stemmed token to its discriminative weight in [0,1].
	weight map[string]float64
}

// domainAliases expands notation that is furniture-trade shorthand rather than
// any one supplier's house style — "2STR" means "2 seater" on every
// acknowledgement that prints it — plus the one English inflection stem()
// deliberately does not cover, since it folds plurals only.
//
// This is normalisation of the same kind StripOrientation performs, so unlike
// the vendor tables it replaces, it stays fixed: it describes the language the
// documents are written in, not the vendor who wrote them.
var domainAliases = map[string]string{
	"1str":      "1 seater",
	"2str":      "2 seater",
	"3str":      "3 seater",
	"str":       "seater",
	"weathered": "weather",
}

// buildVocab derives token weights from both sides of one reconciliation:
// every POA line's combined description, and every BC group's.
//
// The corpus is this document's candidates rather than the vendor's whole
// catalogue, because the question being asked is "which of THESE BC groups is
// THIS POA line". A token shared by every candidate is uninformative for that
// choice however rare it may be across the catalogue at large. Widening the
// corpus, if a catalogue-wide fetch is ever added, is a change to this
// function alone.
//
// Call it after BCLineGroup.CombinedDesc has been rebuilt with CaseysItems
// enrichment attached — enrichment adds text, and text the corpus never saw
// weights 1 by default, which would treat enriched boilerplate as distinctive.
func buildVocab(poaDescs, bcDescs []string) Vocab {
	poaDF, poaN := docFreq(poaDescs)
	bcDF, bcN := docFreq(bcDescs)

	weight := make(map[string]float64, len(poaDF)+len(bcDF))
	for _, df := range []map[string]int{poaDF, bcDF} {
		for t := range df {
			if _, done := weight[t]; done {
				continue
			}
			weight[t] = math.Min(
				rarity(poaDF[t], poaN),
				rarity(bcDF[t], bcN),
			)
		}
	}
	return Vocab{weight: weight}
}

// docFreq counts, per stemmed token, how many of the given descriptions
// contain it — not how many times it occurs, since a comment repeating a word
// does not make that word more common across the document set.
//
// Empty descriptions are not counted: a BC group with no text is not evidence
// that every token is rare, it is an absence of evidence, and letting it into
// n would dilute every rarity score below it.
func docFreq(descs []string) (df map[string]int, n int) {
	df = map[string]int{}
	for _, d := range descs {
		toks := cleanTokenSet(d)
		if len(toks) == 0 {
			continue
		}
		n++
		for t := range toks {
			df[t]++
		}
	}
	return df, n
}

// rarity scores how far a token goes towards distinguishing one document in a
// corpus of n from the rest: 1 when it appears in none or in one of many, 0
// when it appears in all of them.
//
// A corpus of fewer than two documents carries no evidence either way — with a
// single candidate there is nothing to tell apart — so every token weights 1
// and scoring degrades to the plain unweighted overlap. df == 0 means the
// token is absent from this side entirely, which is likewise not evidence that
// it is boilerplate.
func rarity(df, n int) float64 {
	if n < 2 || df == 0 {
		return 1
	}
	return 1 - float64(df)/float64(n)
}

// weightOf defaults to 1 for a token the corpus never saw: unseen means
// unrepeated, and unrepeated is the opposite of boilerplate.
func (v Vocab) weightOf(t string) float64 {
	if w, ok := v.weight[t]; ok {
		return w
	}
	return 1
}

// mass is the total discriminative weight a token set carries.
func (v Vocab) mass(set map[string]bool) float64 {
	var sum float64
	for t := range set {
		sum += v.weightOf(t)
	}
	return sum
}

// weightedOverlap is overlapSimilaritySets with each token contributing its
// discriminative weight instead of 1.
//
// The denominator stays min(|A|,|B|), in weight terms, for the same reason
// overlapSimilarity uses min at all: a BC group's comment count is a property
// of how the product was configured, not of how well it matches.
//
// ok is false when either side's weight sums to zero — every token it holds is
// boilerplate. A ratio computed over no evidence is not a similarity, so
// scorePair falls back to the raw score there rather than reporting a
// confident-looking 0.
func weightedOverlap(v Vocab, a, b map[string]bool) (score float64, ok bool) {
	massA, massB := v.mass(a), v.mass(b)
	if massA == 0 || massB == 0 {
		return 0, false
	}
	var inter float64
	for t := range a {
		if b[t] {
			inter += v.weightOf(t)
		}
	}
	return inter / math.Min(massA, massB), true
}

// cleanTokenSet applies orientation strip -> normalize -> alias expansion ->
// stem.
//
// Orientation must be stripped before normalize, not after: normalize turns
// "L/H" into "l h" and "LEFT/RIGHT" into "left right", losing the separator
// character StripOrientation's "L/H"-style patterns match on. Once normalized
// the individual "left"/"right" tokens still get caught by StripOrientation's
// plain-word alternatives, but stripping first is exact and matches what
// scorePair already does for the raw score.
//
// Alias expansion happens before stemming so "1str" becomes "1 seater" and
// *then* stems. Nothing is discarded here: what used to be stopword removal is
// now a weight applied at scoring time by weightedOverlap, which makes a
// token's contribution proportionate to how much it discriminates instead of
// all-or-nothing.
func cleanTokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range strings.Fields(normalize(StripOrientation(s))) {
		toks := []string{t}
		if repl, ok := domainAliases[t]; ok {
			toks = strings.Fields(repl)
		}
		for _, tok := range toks {
			set[stem(tok)] = true
		}
	}
	return set
}
