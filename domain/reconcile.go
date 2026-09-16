// Reconciliation: comparing one acknowledgement against one purchase order.
//
// Reconcile is the coordinator. It pairs the products, runs the checks on each
// pair, and collects the results — it holds no comparison logic of its own and
// performs no I/O. Both orders arrive fully built with their embeddings
// attached, so the same inputs always produce the same result, and a captured
// fixture replays offline with no credentials.
package domain

import (
	"fmt"
	"sort"
)

// ProductResult is everything discovered about one matched pair: the two
// products, and one result struct per check.
//
// This is what the report, the review queue and any card consume. None of them
// decides anything the checks have not already decided.
type ProductResult struct {
	POA POAProduct `json:"poa"`
	BC  BCProduct  `json:"bc"`

	Price       PriceCheckResult       `json:"price"`
	Quantity    QuantityCheckResult    `json:"quantity"`
	Arithmetic  ArithmeticCheckResult  `json:"arithmetic"`
	Orientation OrientationCheckResult `json:"orientation"`
	Description DescriptionCheckResult `json:"description"`
	Seats       SeatsCheckResult       `json:"seats"`

	// POAIndex/BCIndex are the positions of the two products in their orders.
	// A review row records POAIndex so feedback can be traced back to the line.
	POAIndex int `json:"poaLineIndex"`
	BCIndex  int `json:"bcLineIndex"`

	// Score is the identity score the assignment maximised. Reported for
	// debugging; nothing branches on it.
	Score float64 `json:"score"`

	score float64
}

// checkProduct runs every check on one pairing.
func checkProduct(p pairing, state EnrichmentState, cfg Config) ProductResult {
	return ProductResult{
		POA:         p.poa,
		BC:          p.bc,
		Price:       priceCheck(p.poa, p.bc, cfg),
		Quantity:    quantityCheck(p.poa, p.bc),
		Arithmetic:  arithmeticCheck(p.poa, cfg),
		Orientation: orientationCheck(p.poa, p.bc),
		Description: descriptionCheck(p.poa, p.bc, p.ev, state, cfg),
		Seats:       seatsCheck(p.poa, p.bc, state, cfg),
		POAIndex:    p.poaIndex,
		BCIndex:     p.bcIndex,
		Score:       p.score,
		score:       p.score,
	}
}

// Checks returns every check on this pair, in the order a human should read
// them.
func (p ProductResult) Checks() []CheckResult {
	return []CheckResult{
		p.Price.CheckResult,
		p.Quantity.CheckResult,
		p.Arithmetic.CheckResult,
		p.Orientation.CheckResult,
		p.Seats.CheckResult,
		p.Description.CheckResult,
	}
}

// OK reports whether this pair can be accepted without a human: every check
// either agreed or had nothing to compare. A warning does not block.
func (p ProductResult) OK() bool {
	for _, c := range p.Checks() {
		if c.Failed() {
			return false
		}
	}
	return true
}

// Discrepancies lists every reason this pair needs a human, one entry per
// reason.
//
// Separate entries, not one joined sentence: a reviewer approving
// "descriptions look different; net differs by £24" has pardoned both, and one
// of those must never be pardonable. Each carries a Kind rather than only
// prose, because whatever records a verdict has to know whether it may become
// a rule — deciding that by matching English breaks the first time anyone
// rewords a message.
func (p ProductResult) Discrepancies() []Discrepancy {
	var ds []Discrepancy
	add := func(k DiscrepancyKind, msg string) { ds = append(ds, Discrepancy{Kind: k, Message: msg}) }

	if p.Price.Failed() {
		add(DiscNetValue, p.Price.Message)
	}
	if p.Quantity.Failed() {
		add(DiscQuantity, p.Quantity.Message)
	}
	if p.Arithmetic.Failed() {
		add(DiscInternalMath, p.Arithmetic.Message)
	}
	if p.Orientation.Failed() {
		add(DiscOrientation, p.Orientation.Message)
	}
	if p.Seats.Failed() {
		add(DiscSeatCount, p.Seats.Message)
	}
	return append(ds, p.identityDiscrepancies()...)
}

// identityDiscrepancies is the description half, which can produce more than
// one entry: a line can be both hard to identify and ambiguous between two BC
// products, and a reviewer answers those separately.
//
// Skipped entirely when a code matched — identity is settled by the code, so a
// low description score against it is expected, not a discrepancy.
func (p ProductResult) identityDiscrepancies() []Discrepancy {
	d := p.Description
	var ds []Discrepancy
	add := func(k DiscrepancyKind, msg string) { ds = append(ds, Discrepancy{Kind: k, Message: msg}) }

	if !d.CodeMatch {
		if similarityValue(d.SimilarityScore) < d.Threshold {
			// The message the check already reached, rather than a second
			// wording maintained alongside it.
			kind := DiscDescription
			if d.POACode != "" && d.CodeDataState != "code_on_file" {
				kind = DiscMissingBCCode
			}
			add(kind, d.Message)
		}
		if d.Margin != nil && *d.Margin < d.MarginThreshold {
			add(DiscAmbiguousPairing, fmt.Sprintf(
				"ambiguous pairing: resembles %q almost equally (margin %.2f)", d.RunnerUp, *d.Margin))
		}
	}
	if d.CodeAmbiguousWith != "" {
		add(DiscCodeAmbiguous, fmt.Sprintf(
			"code match on %s is ambiguous: also matches BC group(s) %s", d.CodeMatchSource, d.CodeAmbiguousWith))
	}
	if d.VariantAmbiguity != "" {
		add(DiscVariantAmbiguous, fmt.Sprintf(
			"the code identifies the product family but not the variant: this order carries %s, and BC item %s holds no code that tells them apart",
			d.VariantAmbiguity, p.BC.ItemNo))
	}
	return ds
}

// ---------------------------------------------------------------------------
// Order level
// ---------------------------------------------------------------------------

// Result is the full comparison of one acknowledgement against one purchase
// order.
type Result struct {
	POA POAOrder `json:"poa"`
	BC  BCOrder  `json:"bc"`

	// Products is one entry per matched pair, in POA line order.
	Products []ProductResult `json:"products"`
	// UnmatchedPOA is what the supplier acknowledged that could not be paired.
	UnmatchedPOA []POAProduct `json:"unmatchedPOA"`
	// UnmatchedBC is what the order asked for that the acknowledgement never
	// mentioned — reported so a review can name the line, not merely count it.
	UnmatchedBC []BCProduct `json:"unmatchedBC"`

	Totals TotalsCheckResult `json:"totals"`

	// WriteOK is the write gate: may this be accepted without a human?
	WriteOK bool `json:"writeOK"`

	// Config is the tuning this result was produced under, carried with it so
	// a workbook or a fixture is never ambiguous about which numbers applied.
	Config Config `json:"config"`
}

// Reconcile compares one acknowledgement against one purchase order.
//
// It coordinates; it does not compare. Pairing is match.go's, the verdicts are
// checks.go's, and this decides only what runs in what order.
func Reconcile(poa POAOrder, bc BCOrder, cfg Config) Result {
	pairs := pairCandidates(poa, bc, cfg)

	// Ambiguity resolution runs before the code bonus and before the margins:
	// both the score the assignment sees and the verdict the checks reach
	// depend on the final CodeMatch value.
	resolveCodeAmbiguity(pairs)
	// After resolveCodeAmbiguity, so the two withdrawal reasons compose rather
	// than race: a pairing already withdrawn for matching several BC products
	// keeps the more specific explanation.
	resolveVariantAmbiguity(pairs, cfg)
	for i := range pairs {
		if pairs[i].ev.CodeMatch {
			pairs[i].score += cfg.CodeMatchBonus
		}
	}

	// Provisional margins, against every alternative, because before the
	// assignment none of them is spoken for. The sort below needs a verdict,
	// and a verdict needs a margin.
	fillMargins(pairs, func(int) bool { return true })

	candidates := make([]ProductResult, len(pairs))
	for i, p := range pairs {
		candidates[i] = checkProduct(p, bc.Enrichment, cfg)
	}
	sortCandidates(candidates)

	matched := assign(candidates, len(poa.Products), len(bc.Products), cfg)

	// Now that the assignment is settled, recompute each margin against the BC
	// products that were actually still available, and re-run the checks on
	// the winners. Reporting a line as ambiguous against a product another POA
	// line had already taken describes a contest that never happened.
	takenBy := map[int]int{}
	for _, m := range matched {
		takenBy[m.BCIndex] = m.POAIndex
	}
	final := make([]ProductResult, 0, len(matched))
	for _, m := range matched {
		fillMarginsFor(pairs, m.POAIndex, m.BCIndex, takenBy)
		for i := range pairs {
			if pairs[i].poaIndex == m.POAIndex && pairs[i].bcIndex == m.BCIndex {
				final = append(final, checkProduct(pairs[i], bc.Enrichment, cfg))
				break
			}
		}
	}
	sort.Slice(final, func(i, j int) bool { return final[i].POAIndex < final[j].POAIndex })

	usedPOA, usedBC := map[int]bool{}, map[int]bool{}
	for _, m := range final {
		usedPOA[m.POAIndex], usedBC[m.BCIndex] = true, true
	}
	var unmatchedPOA []POAProduct
	for i, p := range poa.Products {
		if !usedPOA[i] {
			unmatchedPOA = append(unmatchedPOA, p)
		}
	}
	var unmatchedBC []BCProduct
	for i, b := range bc.Products {
		if !usedBC[i] {
			unmatchedBC = append(unmatchedBC, b)
		}
	}

	return Result{
		POA:          poa,
		BC:           bc,
		Products:     final,
		UnmatchedPOA: unmatchedPOA,
		UnmatchedBC:  unmatchedBC,
		Totals:       totalsCheck(poa, bc, final, unmatchedPOA, cfg),
		WriteOK:      writeOK(final, unmatchedPOA),
		Config:       cfg,
	}
}

// fillMarginsFor recomputes one chosen pairing's margin against the BC
// products still available to its POA line: unassigned ones, plus its own.
func fillMarginsFor(pairs []pairing, poaIndex, bcIndex int, takenBy map[int]int) {
	best, bestLabel, found := -1.0, "", false
	self := -1
	for i := range pairs {
		if pairs[i].poaIndex != poaIndex {
			continue
		}
		if pairs[i].bcIndex == bcIndex {
			self = i
			continue
		}
		if owner, taken := takenBy[pairs[i].bcIndex]; taken && owner != poaIndex {
			continue // spoken for by another POA line; never a real alternative
		}
		found = true
		if s := similarityValue(pairs[i].ev.Similarity); s > best {
			best, bestLabel = s, pairs[i].bc.Label()
		}
	}
	if self < 0 {
		return
	}
	if !found {
		pairs[self].ev.MarginNA = true
		pairs[self].ev.Margin, pairs[self].ev.RunnerUp = 0, ""
		return
	}
	pairs[self].ev.MarginNA = false
	pairs[self].ev.Margin = similarityValue(pairs[self].ev.Similarity) - best
	pairs[self].ev.RunnerUp = bestLabel
}

// writeOK is the gate: every acknowledged line paired, and every pair clean.
//
// It deliberately ignores the order total and the BC lines the supplier never
// acknowledged. That is documented intentional behaviour, not an oversight —
// tightening it changes which orders auto-approve, which is a decision to take
// deliberately and measure over the corpus, not a tidy-up.
func writeOK(matched []ProductResult, unmatchedPOA []POAProduct) bool {
	if len(unmatchedPOA) > 0 {
		return false
	}
	for _, m := range matched {
		if !m.OK() {
			return false
		}
	}
	return true
}

// Flagged is every pair that needs a human.
func (r Result) Flagged() []ProductResult {
	var out []ProductResult
	for _, p := range r.Products {
		if !p.OK() {
			out = append(out, p)
		}
	}
	return out
}
