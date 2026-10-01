// Pairing: deciding which POA product is which BC product.
//
// This file answers one question — "are these two the same line?" — and
// nothing else. Whether the supplier agrees with us about money, count and
// handedness is checks.go's job, on the pair once it exists.
//
// That separation is the design. When net line value carried part of the
// pairing score, a line whose price differed could not clear the identity bar
// and came back UNMATCHED, which made "matched, but the supplier confirmed
// £541 against our £565" a verdict the engine could not emit — the single most
// useful thing it can say. Never fold price back into the pairing score.
//
// Two further rules this file exists to keep:
//
//   - Ambiguity is a property of the candidate set, not of a string. A code
//     matching two BC products claims neither; a POA line resembling two BC
//     products equally has identified neither.
//
//   - Thresholds flag, they do not refuse. Supplier and BC vocabularies
//     genuinely differ ("HANSSON 2.5 STR END" vs "2.5 Seater End"), so any
//     threshold high enough to reject wrong pairs also rejects right ones.
//     Take the best assignment; use thresholds only to mark low confidence.

package domain

import (
	"math"
	"sort"
)

// MatchEvidence is what the pairing pass learned about why a POA product and a
// BC product were put together. descriptionCheck turns it into a verdict; it
// is the only thing that reads it.
type MatchEvidence struct {
	// Similarity is cosine similarity of the two embeddings, or nil when
	// either side was never embedded — "no evidence", not zero.
	Similarity *float64 `json:"similarity"`
	// Margin is Similarity minus the best similarity this POA product
	// achieves against any BC product STILL AVAILABLE to it. Meaningless, and
	// MarginNA, when there is no alternative.
	Margin   float64 `json:"margin"`
	MarginNA bool    `json:"marginNA"`
	RunnerUp string  `json:"runnerUp"`

	CodeMatch         bool   `json:"codeMatch"`
	CodeMatchSource   string `json:"codeMatchSource"`
	CodeAmbiguousWith string `json:"codeAmbiguousWith"`
	VariantAmbiguity  string `json:"variantAmbiguity"`
}

// pairing is one candidate POA/BC pair and the evidence behind it.
type pairing struct {
	poaIndex int
	bcIndex  int
	poa      POAProduct
	bc       BCProduct
	ev       MatchEvidence
	score    float64
}

// pairCandidates scores every POA product against every BC product.
func pairCandidates(poa POAOrder, bc BCOrder, cfg Config) []pairing {
	all := make([]pairing, 0, len(poa.Products)*len(bc.Products))
	for i, p := range poa.Products {
		for j, b := range bc.Products {
			all = append(all, scorePairing(i, p, j, b, bc.VendorNo, cfg))
		}
	}
	return all
}

// scorePairing scores one pair on identity alone: how alike the descriptions
// are, and whether the quantities describe the same goods. Money and handedness
// are deliberately absent.
func scorePairing(poaIndex int, poa POAProduct, bcIndex int, bc BCProduct, vendorNo string, cfg Config) pairing {
	sim := similarityOf(poa.Embedding, bc.Embedding)
	codeMatched, codeSource := codeMatch(squash(poa.Text()), bc, vendorNo, cfg)

	var score float64
	if sim != nil {
		score = cfg.DescWeight * *sim
	}
	if !quantityCheck(poa, bc).Failed() {
		score += cfg.QtyBonus
	}

	return pairing{
		poaIndex: poaIndex, bcIndex: bcIndex, poa: poa, bc: bc, score: score,
		ev: MatchEvidence{Similarity: sim, CodeMatch: codeMatched, CodeMatchSource: codeSource},
	}
}

// similarityOf is the description-identity signal, or nil when either side has
// no embedding to compare.
func similarityOf(a, b []float32) *float64 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	return Ptr(cosineSimilarity(a, b))
}

// cosineSimilarity is how alike two texts' embeddings are, in [-1,1] in
// principle though real embeddings of unrelated text rarely go negative.
// Mismatched or empty vectors score 0 rather than panicking.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

func similarityValue(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// Margins
// ---------------------------------------------------------------------------

// fillMargins computes each pairing's lead over the best alternative its POA
// product could still have had, in place.
//
// available decides which BC products count as alternatives. Before assignment
// every product is an alternative, since none is spoken for. Afterwards only
// the unassigned ones are — a line reported as "resembles IT0226779 almost
// equally" when IT0226779 was taken by another POA line describes a contest
// that never happened, which is what the 2026-09-04 batch showed on PF130365
// as a negative margin.
func fillMargins(all []pairing, available func(bcIndex int) bool) {
	for i := range all {
		all[i].ev = marginEvidence(all, all[i], available)
	}
}

// marginEvidence returns p's evidence with its margin recomputed against the
// BC products available to p's POA line, other than p's own. Only the margin
// fields change; similarity and code evidence are carried over as they are.
func marginEvidence(all []pairing, p pairing, available func(bcIndex int) bool) MatchEvidence {
	ev := p.ev
	best, bestLabel, found := -1.0, "", false
	for _, q := range all {
		if q.poaIndex != p.poaIndex || q.bcIndex == p.bcIndex || !available(q.bcIndex) {
			continue
		}
		found = true
		if s := similarityValue(q.ev.Similarity); s > best {
			best, bestLabel = s, q.bc.Label()
		}
	}
	if !found {
		ev.MarginNA, ev.Margin, ev.RunnerUp = true, 0, ""
		return ev
	}
	ev.MarginNA = false
	ev.Margin = similarityValue(ev.Similarity) - best
	ev.RunnerUp = bestLabel
	return ev
}

// pairIndex looks pairings up by (POA index, BC index).
func pairIndex(all []pairing) map[[2]int]pairing {
	at := make(map[[2]int]pairing, len(all))
	for _, p := range all {
		at[[2]int{p.poaIndex, p.bcIndex}] = p
	}
	return at
}

// ---------------------------------------------------------------------------
// Assignment
// ---------------------------------------------------------------------------

// assign chooses the set of pairs maximising total identity score, using each
// POA product and each BC product at most once. Exhaustive for the small line
// counts real orders carry; greedy over the sorted candidates beyond that.
//
// It never refuses a pair for scoring badly: a POA line with no good candidate
// still gets its best one, flagged. Leaving it unmatched would report "the
// supplier acknowledged a line we do not have" when what happened is "we could
// not tell which of ours it is".
//
// One-to-one is an assumption about layout, not about goods: it holds only when
// both sides spend the same number of lines on a product. findGroups, in
// group.go, repairs what it gets wrong afterwards rather than complicating the
// search here.
func assign(candidates []ProductResult, nPOA, nBC int, cfg Config) []ProductResult {
	if nPOA > cfg.ExhaustiveLimit || nBC > cfg.ExhaustiveLimit {
		return greedyAssign(candidates)
	}

	byPair := make(map[[2]int]ProductResult, len(candidates))
	for _, c := range candidates {
		byPair[[2]int{c.POAIndex, c.BCIndex}] = c
	}

	var bestSet []ProductResult
	bestScore := -1.0
	var walk func(poa int, usedBC map[int]bool, cur []ProductResult, score float64)
	walk = func(poa int, usedBC map[int]bool, cur []ProductResult, score float64) {
		if poa == nPOA {
			if score > bestScore {
				bestScore = score
				bestSet = append([]ProductResult(nil), cur...)
			}
			return
		}
		// Option: leave this POA product unmatched.
		walk(poa+1, usedBC, cur, score)
		for bi := 0; bi < nBC; bi++ {
			if usedBC[bi] {
				continue
			}
			c, ok := byPair[[2]int{poa, bi}]
			if !ok {
				continue
			}
			usedBC[bi] = true
			walk(poa+1, usedBC, append(cur, c), score+c.score)
			delete(usedBC, bi)
		}
	}
	walk(0, map[int]bool{}, nil, 0)
	return bestSet
}

// greedyAssign takes candidates in the order given — best first — and keeps
// the first pairing it sees for each POA and BC product.
func greedyAssign(candidates []ProductResult) []ProductResult {
	var out []ProductResult
	usedPOA, usedBC := map[int]bool{}, map[int]bool{}
	for _, c := range candidates {
		if usedPOA[c.POAIndex] || usedBC[c.BCIndex] {
			continue
		}
		usedPOA[c.POAIndex], usedBC[c.BCIndex] = true, true
		out = append(out, c)
	}
	return out
}

// sortCandidates orders pairings best-first by identity score alone, ties
// going to the lowest line positions. greedyAssign depends on this order.
//
// It used to put pairs that passed every check first. OK() includes price, so
// above ExhaustiveLimit a pair whose money happened to agree beat a better
// description match — price deciding identity, on large orders only, while
// the exhaustive search below the limit maximised score alone. Both paths now
// pursue the same objective.
func sortCandidates(candidates []ProductResult) {
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.POAIndex != b.POAIndex {
			return a.POAIndex < b.POAIndex
		}
		return a.BCIndex < b.BCIndex
	})
}
