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
// When the pair is one line of a LineGroup — several lines on one side that
// are together one line on the other — Group is set, and Quantity and Price
// describe the whole group rather than this pair alone. Every other check is
// still this pair's own.
//
// This is what the report and any card consume. None of them decides anything
// the checks have not already decided.
type ProductResult struct {
	POA POAProduct `json:"poa"`
	BC  BCProduct  `json:"bc"`

	Price       PriceCheckResult       `json:"price"`
	Quantity    QuantityCheckResult    `json:"quantity"`
	Arithmetic  ArithmeticCheckResult  `json:"arithmetic"`
	Orientation OrientationCheckResult `json:"orientation"`
	Description DescriptionCheckResult `json:"description"`
	Seats       SeatsCheckResult       `json:"seats"`

	// Group is the split or aggregated representation this pair belongs to,
	// or nil for an ordinary one-to-one pair.
	Group *LineGroup `json:"group,omitempty"`

	// POAIndex/BCIndex are the positions of the two products in their orders.
	// In a group, the anchor's index repeats across the group's rows.
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

	// Products is one entry per matched pair, in POA then BC line order. A
	// line in a LineGroup appears once per line on the other side of the
	// group, so a POA or BC index can repeat.
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

// Reconcile compares one acknowledgement against one purchase order. It is a
// pure function: the same orders and Config always produce the same Result.
//
// The steps, in order:
//
//  1. Score every POA line against every BC product on identity alone —
//     description similarity, quantity agreement, exact codes (match.go).
//  2. Choose the one-to-one assignment with the highest total score.
//  3. Run every check on each chosen pair (checks.go).
//  4. Where that left something broken, look for a product laid out as
//     different lines on each side — split or aggregated — and replace the
//     broken pairs with a LineGroup (group.go).
//  5. Re-run the checks on the final pairs, with margins measured against
//     what was really still available, and apply the write gate.
//
// It coordinates; it does not compare. Pairing is match.go's and group.go's,
// the verdicts are checks.go's, and this decides only what runs in what order.
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
	links := make([]link, len(matched))
	for i, m := range matched {
		links[i] = link{poa: m.POAIndex, bc: m.BCIndex}
	}
	final := settle(pairs, links, poa, bc, cfg)

	// The assignment could only express one line per product on each side.
	// Groups are judged against its settled verdicts — a pair that passed
	// every check is never broken up — and only then replace what they cover.
	if groups := findGroups(pairs, final, poa, bc, cfg); len(groups) > 0 {
		final = settle(pairs, regroup(links, groups), poa, bc, cfg)
	}

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

// link is one row of the result being built: a POA line, a BC product, and
// the group the pair belongs to, if any.
type link struct {
	poa, bc int
	group   *LineGroup
}

// regroup replaces every link that touches a grouped line with the group's
// own links. A line pulled into a group leaves its old partner unmatched.
func regroup(links []link, groups []LineGroup) []link {
	inGroup := map[[2]int]bool{} // {side, index}: 0 = POA, 1 = BC
	var out []link
	for n := range groups {
		g := &groups[n]
		for _, i := range g.POAIndexes {
			inGroup[[2]int{0, i}] = true
			for _, j := range g.BCIndexes {
				out = append(out, link{poa: i, bc: j, group: g})
			}
		}
		for _, j := range g.BCIndexes {
			inGroup[[2]int{1, j}] = true
		}
	}
	for _, l := range links {
		if !inGroup[[2]int{0, l.poa}] && !inGroup[[2]int{1, l.bc}] {
			out = append(out, l)
		}
	}
	return out
}

// settle runs every check on the chosen links, in POA then BC line order.
//
// Each margin is recomputed against the BC products nobody holds. Reporting a
// line as ambiguous against a product another POA line had already taken
// describes a contest that never happened — and in a split group, the other
// BC lines of the same group are the same product, not rivals to it.
func settle(pairs []pairing, links []link, poa POAOrder, bc BCOrder, cfg Config) []ProductResult {
	held := map[int]bool{}
	for _, l := range links {
		held[l.bc] = true
	}
	at := pairIndex(pairs)
	out := make([]ProductResult, 0, len(links))
	for _, l := range links {
		p := at[[2]int{l.poa, l.bc}]
		p.ev = marginEvidence(pairs, p, func(k int) bool { return !held[k] })
		r := checkProduct(p, bc.Enrichment, cfg)
		if l.group != nil {
			r.applyGroup(l.group, poa, bc, cfg)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].POAIndex != out[j].POAIndex {
			return out[i].POAIndex < out[j].POAIndex
		}
		return out[i].BCIndex < out[j].BCIndex
	})
	return out
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
