// POA line matching v6.
//
// v6 changes over v5:
//
//  1. IDENTITY AND VERIFICATION ARE SEPARATE. Pairing is decided by
//     description and quantity only — "are these the same line?". Money and
//     orientation are then checked ON the established pair — "does the
//     supplier agree with us?". In v5 net line value carried 0.35 of the
//     pairing score, so a line whose price differed could not reach
//     MatchThreshold and came back UNMATCHED. That made "matched, but the
//     supplier confirmed £541 against our £565" a verdict the matcher could
//     not emit — which is the single most useful thing it can say.
//
//  2. OVERLAP COEFFICIENT REPLACES SØRENSEN–DICE. Dice divides by
//     |A| + |B|, so a BC group carrying more comment lines is penalised
//     regardless of content. BC comment counts vary per line by design
//     (fabric, bolster, legs, scatter cushions), so on PF129268 the 2.5
//     seater scored HIGHER against the chaise group (0.740) than against its
//     own (0.671) purely because the chaise had four fewer comments. Overlap
//     divides by min(|A|,|B|) and is insensitive to one side carrying extra
//     detail.
//
//  3. ORIENTATION IS A VERIFICATION FLAG. Resolved once here, stored on the
//     match, folded into FullyVerified() — so the handler, the write gate and
//     the Excel builder all read one verdict instead of each recomputing it.
//     Orientation tokens are stripped before scoring, since they are checked
//     separately and only add noise to the similarity.
//
//  4. ABSENT IS NOT ZERO. TotalProductQtyPrice is *float64. Suppliers who
//     print no line-total column (Ashwood) previously failed InternalOK on
//     every line — |541.00 - 0| > tol — which meant no Ashwood order could
//     ever pass the write gate no matter how correct it was. A check that
//     cannot be performed now passes rather than fails.
//
//  5. moneyTol IS 0.005, NOT 0.5. The v5 constant was documented as "half a
//     cent" and set to fifty pence, so a 49p error on every line verified
//     clean.
package poa

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/dramanclancy/poalib/businesscentral"
)

const (
	// MatchThreshold gates pairing on identity alone. Lower than v5's 0.80
	// because money no longer contributes to the score.
	MatchThreshold = 0.80
	// DescThreshold is the description quality below which a pair is reported
	// as weakly identified even when the money agrees.
	DescThreshold = 0.5
)

// ---------------------------------------------------------------------------
// BC grouping
// ---------------------------------------------------------------------------

// GroupBCLines walks lines in sequence order, starting a new group on each
// value-bearing line and attaching following Comment lines to it.
//
// Account lines start a group too. In v5 only "Item" did, so PF129268's
// surcharge (seq 200000, G/L 718000, £21.85) never became a group: it could
// not be matched, and CoverageOK would have been short by exactly that amount
// on every Ashwood order.
func GroupBCLines(lines []businesscentral.PurchaseOrderLine) []BCLineGroup {
	sorted := make([]businesscentral.PurchaseOrderLine, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })

	var groups []BCLineGroup
	for _, l := range sorted {
		switch l.LineType {
		case "Item", "Account", "G/L Account", "Fixed Asset", "Resource":
			groups = append(groups, BCLineGroup{Item: l})
		case "Comment":
			if len(groups) > 0 {
				last := &groups[len(groups)-1]
				last.Comments = append(last.Comments, l)
			}
			// A Comment before any value line has no owner; dropping it is
			// correct — it is a document header, not a line qualifier.
		}
	}

	for i := range groups {
		parts := []string{groups[i].Item.Description}
		for _, c := range groups[i].Comments {
			parts = append(parts, c.Description)
		}
		// Separator must be " " — joining with "" glues adjacent words into
		// unmatchable tokens ("FootstoolWM").
		groups[i].CombinedDesc = strings.Join(parts, " ")
	}
	return groups
}

func combinedPOADesc(d OrderDetail) string {
	return strings.Join([]string{d.Product, d.Description, d.Description2, d.Description3}, " ")
}

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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Line values
// ---------------------------------------------------------------------------

// effectiveNetLine is the line's post-discount value from the POA's own
// numbers, however the supplier expressed the discount.
//
// DiscountPercent and DiscountFigure are two REPRESENTATIONS of one discount,
// mirroring BC's Line Discount % / Line Discount Amount pair — they are never
// additive. The cash figure wins: it is what posts, and it survives rounding
// that a recomputed percentage would not.
func effectiveNetLine(d OrderDetail) float64 {
	gross := float64(d.Qty) * d.UnitProductPrice
	switch {
	case d.DiscountFigure != 0:
		return gross - d.DiscountFigure
	case d.DiscountPercent != 0:
		return gross * (1 - d.DiscountPercent/100)
	default:
		return gross
	}
}
func LineNet(d OrderDetail) float64 { return effectiveNetLine(d) }

// effectiveUnitNet is the per-unit equivalent, used by the pack-ratio check.
// It applies the same discount rules as effectiveNetLine so the two cannot
// disagree about what a discount is.
func effectiveUnitNet(unit, discountPct, discountFig float64, qty float64) float64 {
	switch {
	case discountFig != 0 && qty > 0:
		return unit - discountFig/qty
	case discountPct != 0:
		return unit * (1 - discountPct/100)
	default:
		return unit
	}
}

// qtyConsistent reports whether the POA and BC quantities describe the same
// physical goods: equal, or BC = POA × r with the unit prices confirming the
// same ratio (the supplier counts packs, BC counts singles). Both conditions
// must hold, so a genuine quantity change can never slip through.
func qtyConsistent(d OrderDetail, item businesscentral.PurchaseOrderLine) (ok bool, ratio int) {
	pq, bq := float64(d.Qty), item.Quantity
	if pq <= 0 || bq <= 0 {
		return false, 0
	}
	if math.Abs(pq-bq) < 0.001 {
		return true, 1
	}
	r := math.Round(bq / pq)
	if r < 2 || math.Abs(bq/pq-r) > 0.001 {
		return false, 0
	}
	poaUnit := effectiveUnitNet(d.UnitProductPrice, d.DiscountPercent, d.DiscountFigure, pq)
	bcUnit := effectiveUnitNet(item.DirectUnitCost, item.DiscountPercent, 0, bq)
	if math.Abs(poaUnit-bcUnit*r) > 0.01 {
		return false, 0
	}
	return true, int(r)
}

// ---------------------------------------------------------------------------
// LineMatch verdicts
// ---------------------------------------------------------------------------

func (m LineMatch) FullyVerified() bool {
	return m.NetOK && m.QtyOK && m.InternalOK && m.OrientationOK &&
		m.DescScore >= DescThreshold
}

func (m LineMatch) Discrepancies() []string {
	var d []string
	if !m.NetOK {
		d = append(d, fmt.Sprintf("net line value differs from BC by %+.2f (POA %.2f, BC %.2f)",
			m.NetDelta, effectiveNetLine(m.POA), m.BC.Item.NetAmount))
	}
	if !m.QtyOK {
		d = append(d, fmt.Sprintf("quantity differs (POA %d, BC %g)", m.POA.Qty, m.BC.Item.Quantity))
	}
	if !m.InternalOK {
		poaNet := effectiveNetLine(m.POA)
		if lineTotal, ok := Deref(m.POA.TotalProductQtyPrice); ok {
			d = append(d, fmt.Sprintf(
				"the POA's own figures don't add up: net %.2f vs printed line total %.2f (diff %+.2f) — likely extraction misread",
				poaNet, lineTotal, poaNet-lineTotal))
		} else {
			// InternalOK false with no printed total => the invalid-discount case
			d = append(d, fmt.Sprintf(
				"the POA's own figures don't add up: discount %.2f%% is out of range — likely extraction misread",
				m.POA.DiscountPercent))
		}
	}
	if !m.OrientationOK {
		d = append(d, m.OrientReason)
	}
	// Same comparison as FullyVerified, so a line cannot be both verified and
	// flagged at exactly DescThreshold.
	if m.DescScore < DescThreshold {
		d = append(d, fmt.Sprintf("descriptions look different (similarity %.2f)", m.DescScore))
	}
	return d
}

// ---------------------------------------------------------------------------
// Scoring
// ---------------------------------------------------------------------------

func scorePair(d OrderDetail, g BCLineGroup) LineMatch {
	// --- identity: is this the same line? ---
	// Orientation tokens are stripped: they are verified separately below, and
	// "RHF/LHF" will never resemble "RIGHT" to any string metric.
	desc := overlapSimilarity(
		StripOrientation(combinedPOADesc(d)),
		StripOrientation(g.CombinedDesc),
	)
	qtyOK, packRatio := qtyConsistent(d, g.Item)

	score := 0.85 * desc
	if qtyOK {
		score += 0.15
	}

	// --- verification: does the supplier agree with us? ---
	poaNet := effectiveNetLine(d)
	netDelta := poaNet - g.Item.NetAmount
	netOK := math.Abs(netDelta) < moneyTol

	// A check that cannot be performed passes. Ashwood prints unit price only,
	// so there is no printed line total to reconcile against.
	internalOK, internalNA := true, true
	if lineTotal, ok := Deref(d.TotalProductQtyPrice); ok {
		internalNA = false
		internalOK = math.Abs(poaNet-lineTotal) < moneyTol
	}

	poaOrient := Classify(combinedPOADesc(d))
	bcOrient, bcSrc := ResolveBC(g.Item.Description, CommentDescs(g.Comments))
	oc := Check(poaOrient, bcOrient, combinedPOADesc(d), bcSrc)

	return LineMatch{
		POA: d, BC: g,
		Score: score, DescScore: desc, PackRatio: packRatio,
		NetOK: netOK, QtyOK: qtyOK, InternalOK: internalOK,
		OrientationOK: oc.OK,
		NetDelta:      netDelta,
		POAOrient:     poaOrient, BCOrient: bcOrient, OrientReason: oc.Reason,
		InternalNA: internalNA,
	}
}

// ---------------------------------------------------------------------------
// Assignment
// ---------------------------------------------------------------------------

// MatchPOA pairs extracted POA products with BC line groups. Greedy
// best-first on identity; each POA product and each BC group is used at most
// once. Pairs that clear MatchThreshold are matched REGARDLESS of whether the
// money agrees — a price discrepancy is reported on the match, not hidden by
// refusing to make one.
//
// Write-gating is EvaluateWriteGate's job, not this function's. Callers must
// not infer permission to write from a non-empty matches slice.
func MatchPOA(doc Products, bcLines []businesscentral.PurchaseOrderLine) (matches []LineMatch, unmatched []OrderDetail, unmatchedBC []BCLineGroup) {
	groups := GroupBCLines(bcLines)

	var all []LineMatch
	for i, d := range doc.Products {
		for _, g := range groups {
			m := scorePair(d, g)
			m.poaIndex = i
			all = append(all, m)
		}
	}

	// Fully verified pairs first, then by identity score. Within "fully
	// verified" the money already agrees, so ordering by description simply
	// prefers the more plausible reading where one exists.
	sort.Slice(all, func(i, j int) bool {
		fi, fj := all[i].FullyVerified(), all[j].FullyVerified()
		if fi != fj {
			return fi
		}
		return all[i].Score > all[j].Score
	})

	// Optimal assignment rather than greedy-with-threshold.
	//
	// Supplier and BC vocabularies genuinely differ ("HANSSON 2.5 STR END" vs
	// "2.5 Seater End"), so even a correct pair scores only ~0.39 here. Any
	// absolute threshold high enough to reject wrong pairs also rejects right
	// ones. What IS reliable is the RANKING: the correct pair outscores the
	// alternatives. So take the assignment maximising total score, and use the
	// threshold only to FLAG low-confidence pairings, never to refuse them.
	best := assign(all, len(doc.Products), groups)

	usedPOA := map[int]bool{}
	usedBC := map[string]bool{}
	for _, m := range best {
		usedPOA[m.poaIndex] = true
		usedBC[m.BC.Item.ID] = true
		matches = append(matches, m)
	}

	for i, d := range doc.Products {
		if !usedPOA[i] {
			unmatched = append(unmatched, d)
		}
	}
	// Reported so the review email can name which PO line the supplier never
	// acknowledged, rather than only counting the gap.
	for _, g := range groups {
		if !usedBC[g.Item.ID] {
			unmatchedBC = append(unmatchedBC, g)
		}
	}
	return matches, unmatched, unmatchedBC
}

// assign chooses the set of pairs maximising total identity score, using each
// POA line and each BC group at most once. Exhaustive for the small line
// counts POAs actually carry; falls back to greedy beyond that.
func assign(all []LineMatch, nPOA int, groups []BCLineGroup) []LineMatch {
	byPair := map[[2]int]LineMatch{}
	idxOf := map[string]int{}
	for i, g := range groups {
		idxOf[g.Item.ID] = i
	}
	for _, m := range all {
		byPair[[2]int{m.poaIndex, idxOf[m.BC.Item.ID]}] = m
	}

	if nPOA > 8 || len(groups) > 8 {
		return greedyAssign(all)
	}

	var bestSet []LineMatch
	bestScore := -1.0
	var walk func(poa int, usedBC map[int]bool, cur []LineMatch, score float64)
	walk = func(poa int, usedBC map[int]bool, cur []LineMatch, score float64) {
		if poa == nPOA {
			if score > bestScore {
				bestScore = score
				bestSet = append([]LineMatch(nil), cur...)
			}
			return
		}
		// Option: leave this POA line unmatched.
		walk(poa+1, usedBC, cur, score)
		for gi := range groups {
			if usedBC[gi] {
				continue
			}
			m, ok := byPair[[2]int{poa, gi}]
			if !ok {
				continue
			}
			usedBC[gi] = true
			walk(poa+1, usedBC, append(cur, m), score+m.Score)
			delete(usedBC, gi)
		}
	}
	walk(0, map[int]bool{}, nil, 0)
	return bestSet
}

func greedyAssign(all []LineMatch) []LineMatch {
	var out []LineMatch
	usedPOA := map[int]bool{}
	usedBC := map[string]bool{}
	for _, m := range all {
		if usedPOA[m.poaIndex] || usedBC[m.BC.Item.ID] {
			continue
		}
		usedPOA[m.poaIndex] = true
		usedBC[m.BC.Item.ID] = true
		out = append(out, m)
	}
	return out
}

// POATotalFromLines sums the net of every extracted line — the correct
// order total for a multi-page POA, independent of any per-page printed total.
func POATotalFromLines(p Products) *float64 {
	var t float64
	for _, d := range p.Products {
		t += effectiveNetLine(d)
	}
	return &t
}
