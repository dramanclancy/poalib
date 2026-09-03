// POA line matching v6.
//
// v6 changes over v5:
//
//  1. IDENTITY AND VERIFICATION ARE SEPARATE. Pairing is decided by
//     description and quantity only — "are these the same line?". Money and
//     orientation are then checked ON the established pair — "does the
//     supplier agree with us?". In v5 net line value carried 0.35 of the
//     pairing score, so a line whose price differed could not clear the
//     identity bar and came back UNMATCHED. That made "matched, but the
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
//
// v6.1 adds description cleaning (vocab.go): a per-vendor stoplist/alias set
// strips comment-label boilerplate before scoring, and DescScore is judged
// with a threshold AND a margin over the runner-up, not an absolute cut
// alone. See phase1-description-matching.md.
//
// v6.2 enriches the BC side from CaseysItems (caseysitems.go,
// businesscentral.CaseysItem), an OData service carrying item detail the
// standard v2.0 API doesn't. Three additions, all guarded so a missing or
// failed enrichment degrades to v6.1 behaviour exactly:
//
//  1. buildCombinedDesc includes Description_3 and Range_Code when present —
//     a range word (POAs print it, e.g. "Metro") is evidence when it's on
//     the BC side to match, not noise to stoplist.
//  2. Exact-code identity: squash(POA text) containing squash(Model_No) or
//     squash(Vendor_Item_No) sets IdentityConfident() outright, subject to a
//     minimum length, vendor-scoping on Vendor_Item_No, and cross-group
//     ambiguity suppression. See phase1b-item-enrichment.md section 2 — this
//     measured near-zero on the two real samples available, so it's shipped
//     as cheap, well-guarded insurance for suppliers/orders it wasn't
//     possible to test against, not as the primary mechanism.
//  3. No_of_Seats verification: an independent structural check orthogonal
//     to price, since a supplier can print the wrong seat count at a price
//     that matches perfectly.
//
// v6.3 removes the last hand-maintained, vendor-keyed table from the engine.
// Token weights are derived from the documents being matched rather than from
// a per-vendor stoplist (vocab.go), a POA product code can abbreviate a BC
// Range_Code (codeidentity.go), a code match is withdrawn when it names a
// product family but not which variant the line is, and a low description
// score caused by a missing Vendor_Item_No now says so instead of reporting a
// similarity number that measures nothing. See docs/CURRENT_ARCHITECTURE.md.
package poa

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dramanclancy/poalib/businesscentral"
)

const (
	// EngineVersion identifies the matching/verification logic that produced
	// a Review, recorded on each ReviewLine so a later change in scoring
	// doesn't get silently attributed to an older run.
	EngineVersion = "v6.3"

	// DescThreshold is the cleaned-similarity floor. Higher than the old 0.5
	// because cleaning raises correct pairs into the 0.67-1.00 band.
	DescThreshold = 0.60
	// MarginThreshold is the minimum lead the chosen BC group must have over
	// the runner-up. Catches the "resembles two lines equally" failure that an
	// absolute score cannot see.
	MarginThreshold = 0.10

	// minCodeLen is the squash()ed-length floor a Model_No/Vendor_Item_No
	// must clear before it's eligible for exact-code matching. Below it, the
	// code is short enough to appear inside unrelated text by chance.
	minCodeLen = 6

	// minProductCodeLen is the floor for RECOGNISING a POA token as a product
	// code (productCodeIn), which is a lower bar than matching one: the result
	// only chooses the wording of a discrepancy message, never a pairing, so a
	// false positive costs a slightly wrong sentence on an already-flagged
	// line rather than a wrong match. It sits below minCodeLen so short codes
	// like CA24 are still explained rather than silently reported as a
	// meaningless similarity score.
	minProductCodeLen = 4

	// minRangePrefixLen is how much of a Range_Code a POA product code must
	// reproduce before it counts as abbreviating that range (HANCLR -> HANSSON
	// shares HAN). Three is what the live Ashwood data supports; lower would
	// pair ranges on a single shared letter.
	minRangePrefixLen = 3

	// codeMatchBonus is added to a code-matched pairing's assignment Score,
	// large enough that assign() picks it over any description-only
	// candidate. It's additive rather than a hard pre-assignment: an
	// overwhelming description match elsewhere can still outscore a wrong
	// code hit.
	codeMatchBonus = 2.0

	// minPlausibleSeats is the floor No_of_Seats must clear to be treated as
	// a real seat count rather than N/A. CaseysItems returned 0.5 on a
	// parasol — not a furniture seat count — while a genuine "2.5 Seater"
	// sofa is a real, valid reading, so the floor sits at 1, not at 0.
	minPlausibleSeats = 1.0
)

// MatchContext bundles everything MatchPOA needs beyond the two documents
// being compared. It replaces a bare vendorNo parameter (Phase 1) now that a
// second parameter (CaseysItems enrichment) is arriving too — one struct
// avoids further signature churn if a third input shows up later.
type MatchContext struct {
	// VendorNo is po.VendorNumber: gates Vendor_Item_No code matching to
	// items that actually belong to this PO's vendor. It no longer selects a
	// description vocabulary — as of v6.3 token weights are derived from the
	// documents themselves rather than keyed by vendor (see vocab.go).
	VendorNo string
	// Items is CaseysItems enrichment keyed by item number
	// (PurchaseOrderLine.LineObjectNumber). A missing or empty map degrades
	// every BCLineGroup.Detail to nil — Phase 1 behaviour, not a failure.
	Items map[string]businesscentral.CaseysItem
}

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
		groups[i].CombinedDesc = buildCombinedDesc(groups[i])
	}
	return groups
}

// buildCombinedDesc puts the item's own displayName/displayName2 first,
// falling back to the PO line's free-text Description when there's no item
// (Account/G/L/Fixed Asset/Resource lines, or an Item line whose enhancement
// lookup failed). Comment lines are appended after, except any comment that
// just restates the display name — a supplier's PO frequently repeats the
// item name as its own first comment line, and counting it twice would
// double its weight in the overlap score.
func buildCombinedDesc(g BCLineGroup) string {
	var parts []string
	seen := map[string]bool{}
	for _, name := range []string{
		g.Item.DisplayName,
		g.Item.DisplayName2,
	} {
		if name == "" {
			continue
		}
		parts = append(parts, name)
		seen[normalize(name)] = true
	}
	if len(parts) == 0 {
		parts = append(parts, g.Item.Description)
	}
	// CaseysItems enrichment: Description_3 is free extra description text
	// DisplayName/DisplayName2 don't carry; Range_Code is a product range
	// word ("METRO", "DUSK") that POAs print and Phase 1 could only stoplist
	// as noise for lack of a BC-side equivalent to match it against.
	if g.Detail != nil {
		for _, extra := range []string{g.Detail.Description3, g.Detail.RangeCode} {
			if extra == "" || seen[normalize(extra)] {
				continue
			}
			parts = append(parts, extra)
			seen[normalize(extra)] = true
		}
	}
	for _, c := range g.Comments {
		if seen[normalize(c.Description)] {
			continue
		}
		parts = append(parts, c.Description)
	}
	// Separator must be " " — joining with "" glues adjacent words into
	// unmatchable tokens ("FootstoolWM").
	return strings.Join(parts, " ")
}

func combinedPOADesc(d OrderDetail) string {
	return strings.Join([]string{d.Product, d.Description, d.Description2, d.Description3}, " ")
}

// ---------------------------------------------------------------------------
// Seat count
// ---------------------------------------------------------------------------

// seatCountRe matches "3STR", "3 STR", "3 SEATER", "2.5 SEATER" — the POA
// vocabulary for seat count, case-insensitively.
var seatCountRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:str|seaters?)\b`)

// extractSeatCount reads a seat count out of free POA text, if it states one.
func extractSeatCount(s string) (float64, bool) {
	m := seatCountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
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
	return m.NetOK && m.QtyOK && m.InternalOK && m.OrientationOK && m.SeatsOK &&
		m.IdentityConfident()
}

// IdentityConfident reports whether the pairing is trustworthy.
//
// An exact code match (Model_No / Vendor_Item_No printed verbatim in the
// POA text, unambiguous — see codeMatch) settles identity outright: it beats
// fuzzy text entirely, INCLUDING when the description score is low, because
// a supplier's free text can legitimately describe a different attribute of
// the same item than its own catalogue code does (e.g. spring count on the
// POA vs. size in Model_No) without the pairing being wrong.
//
// Otherwise, descriptions must agree well enough in absolute terms AND the
// chosen BC group must clearly beat the alternatives. Both must hold — a
// high score against two equally plausible groups is not confidence, and a
// clear lead over three bad options is not either.
func (m LineMatch) IdentityConfident() bool {
	if m.CodeMatch {
		return true
	}
	if m.DescScore < DescThreshold {
		return false
	}
	return m.DescMarginNA || m.DescMargin >= MarginThreshold
}

// identityGapReason explains a low description score in the most actionable
// terms the available data supports.
//
// The default — "descriptions look different (similarity 0.33)" — is
// technically true and practically useless when the two sides are not
// speaking the same kind of language at all: the POA prints a supplier SKU
// (NFX2S) and BC prints a generic type name (2 Seater Sofa). Those share zero
// tokens by construction, so the number measures incidental overlap between
// comment lines rather than evidence of identity. A number that measures
// nothing, appearing on nearly every line, is how reviewers are trained to
// approve without reading — which matters more the moment Phase 2 starts
// pushing these to a card.
//
// Where the real obstacle is a missing Vendor_Item_No, say so: that names a
// fix (populate the field, once, for every future order) instead of a
// symptom. See phase2-feedback-loop.md section 4 point 3.
// The two cases are separate Kinds, not one Kind with two wordings: a missing
// Vendor_Item_No is a master-data gap a reviewer's confirmation can close
// permanently, while two prose descriptions disagreeing is a vocabulary
// problem. Both are learnable, but the weekly metric wants to count them apart
// — the first should fall as Phase 2 does its job, and the second should not.
func (m LineMatch) identityGapReason() (DiscrepancyKind, string) {
	if code := productCodeIn(combinedPOADesc(m.POA)); code != "" && !m.BC.hasCodeOnFile() {
		return DiscMissingBCCode, fmt.Sprintf(
			"POA prints product code %q but BC item %s has no Vendor_Item_No or Model_No on file to match it against (descriptions alone scored %.2f)",
			code, m.BC.Item.LineObjectNumber, m.DescScore)
	}
	return DiscDescription, fmt.Sprintf(
		"descriptions look different (similarity %.2f, raw %.2f)", m.DescScore, m.DescScoreRaw)
}

// Discrepancies lists every reason this pairing needs a human, one entry per
// reason.
//
// Each carries a Kind rather than only prose: from Phase 2 on, a reviewer
// gives a verdict against one discrepancy at a time, and the endpoint
// recording it has to know whether that verdict may become a rule. Deciding
// that by matching the English of the message would break the first time
// anyone rewords one — which has already happened twice in Stage 0.
func (m LineMatch) Discrepancies() []Discrepancy {
	var d []Discrepancy
	add := func(k DiscrepancyKind, format string, args ...any) {
		d = append(d, Discrepancy{Kind: k, Message: fmt.Sprintf(format, args...)})
	}

	if !m.NetOK {
		add(DiscNetValue, "net line value differs from BC by %+.2f (POA %.2f, BC %.2f)",
			m.NetDelta, effectiveNetLine(m.POA), m.BC.Item.NetAmount)
	}
	if !m.QtyOK {
		add(DiscQuantity, "quantity differs (POA %d, BC %g)", m.POA.Qty, m.BC.Item.Quantity)
	}
	if !m.InternalOK {
		poaNet := effectiveNetLine(m.POA)
		if lineTotal, ok := Deref(m.POA.TotalProductQtyPrice); ok {
			add(DiscInternalMath,
				"the POA's own figures don't add up: net %.2f vs printed line total %.2f (diff %+.2f) — likely extraction misread",
				poaNet, lineTotal, poaNet-lineTotal)
		} else {
			// InternalOK false with no printed total => the invalid-discount case
			add(DiscInternalMath,
				"the POA's own figures don't add up: discount %.2f%% is out of range — likely extraction misread",
				m.POA.DiscountPercent)
		}
	}
	if !m.OrientationOK {
		add(DiscOrientation, "%s", m.OrientReason)
	}
	if !m.SeatsOK {
		add(DiscSeatCount, "seat count differs (POA states %g, BC No_of_Seats %g)", m.POASeats, m.BCSeats)
	}
	// Same comparisons as IdentityConfident, so a line cannot be both
	// verified and flagged at exactly the threshold/margin. Skipped when
	// CodeMatch is true: identity is already settled by the code, so a low
	// description score against it is expected, not a discrepancy.
	if !m.CodeMatch {
		if m.DescScore < DescThreshold {
			kind, msg := m.identityGapReason()
			add(kind, "%s", msg)
		}
		if !m.DescMarginNA && m.DescMargin < MarginThreshold {
			add(DiscAmbiguousPairing, "ambiguous pairing: resembles %q almost equally (margin %.2f)",
				m.DescRunnerUp, m.DescMargin)
		}
	}
	if m.CodeAmbiguousWith != "" {
		add(DiscCodeAmbiguous, "code match on %s is ambiguous: also matches BC group(s) %s",
			m.CodeMatchSource, m.CodeAmbiguousWith)
	}
	if m.CodeVariantAmbiguous != "" {
		add(DiscVariantAmbiguous,
			"the code identifies the product family but not the variant: this order carries %s, and BC item %s holds no code that tells them apart",
			m.CodeVariantAmbiguous, m.BC.Item.LineObjectNumber)
	}
	return d
}

// ---------------------------------------------------------------------------
// Scoring
// ---------------------------------------------------------------------------

// poaText is the POA line's description as prepareMatch left it — the raw
// text plus any BC Range_Code its product code abbreviates. It is used for the
// cleaned identity score only. Everything else here deliberately reads the
// unaugmented text: DescScoreRaw must stay a faithful record of what the two
// documents literally say, and codeMatch must never see an injected range
// code, which would let augmentation manufacture an exact-code hit.
func scorePair(d OrderDetail, poaText string, g BCLineGroup, vocab Vocab, mc MatchContext) LineMatch {
	// --- identity: is this the same line? ---
	// Orientation tokens are stripped: they are verified separately below, and
	// "RHF/LHF" will never resemble "RIGHT" to any string metric.
	descRaw := overlapSimilarity(
		StripOrientation(combinedPOADesc(d)),
		StripOrientation(g.CombinedDesc),
	)

	// Cleaned score discounts boilerplate (WM FABRIC GRADE B, Feet Options,
	// Set of 4, ...) that dominates the raw overlap's denominator without
	// carrying product identity. Which tokens those are is not asserted by a
	// list any more — vocab weights each token by how far it goes towards
	// telling this PO's candidates apart (see vocab.go). If every token on
	// either side turns out to be boilerplate there is no evidence to compute
	// a similarity from, so fall back to the raw score rather than reporting a
	// confident-looking 0.
	cleanPOA := cleanTokenSet(poaText)
	cleanBC := cleanTokenSet(g.CombinedDesc)
	desc := descRaw
	if w, ok := weightedOverlap(vocab, cleanPOA, cleanBC); ok {
		desc = w
	}

	// Exact-code identity. Ambiguity across BC groups is resolved afterwards
	// by resolveCodeAmbiguity, once every pairing for this POA line exists.
	codeMatched, codeSource := codeMatch(squash(combinedPOADesc(d)), g, mc)

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

	// Seat count: an independent structural check. A check that cannot be
	// performed passes — no seat count printed on the POA, or a BC
	// No_of_Seats too small to be a genuine seat count (0, or the 0.5 seen
	// on a parasol), both mean there's nothing to compare.
	seatsOK, seatsNA := true, true
	var poaSeats, bcSeats float64
	if g.Detail != nil && g.Detail.NoOfSeats >= minPlausibleSeats {
		if ps, ok := extractSeatCount(combinedPOADesc(d)); ok {
			seatsNA = false
			poaSeats, bcSeats = ps, g.Detail.NoOfSeats
			seatsOK = math.Abs(poaSeats-bcSeats) < 0.01
		}
	}

	poaOrient := Classify(combinedPOADesc(d))
	bcOrient, bcSrc := ResolveBC(g.Item.Description, CommentDescs(g.Comments))
	oc := Check(poaOrient, bcOrient, combinedPOADesc(d), bcSrc)

	return LineMatch{
		POA: d, BC: g,
		Score: score, DescScore: desc, DescScoreRaw: descRaw, PackRatio: packRatio,
		CodeMatch: codeMatched, CodeMatchSource: codeSource,
		NetOK: netOK, QtyOK: qtyOK, InternalOK: internalOK,
		OrientationOK: oc.OK, SeatsOK: seatsOK, SeatsNA: seatsNA,
		NetDelta:  netDelta,
		POAOrient: poaOrient, BCOrient: bcOrient, OrientReason: oc.Reason,
		InternalNA: internalNA,
		POASeats:   poaSeats, BCSeats: bcSeats,
	}
}

// ---------------------------------------------------------------------------
// Assignment
// ---------------------------------------------------------------------------

// MatchPOA pairs extracted POA products with BC line groups. Optimal
// assignment on identity; each POA product and each BC group is used at most
// once. Pairs are matched REGARDLESS of whether the money agrees — a price
// discrepancy is reported on the match, not hidden by refusing to make one.
//
// mc.VendorNo (po.VendorNumber) gates Vendor_Item_No code matching to the PO's
// own vendor. Description token weights are not keyed by it: they are derived
// from this document's own POA lines and BC groups (vocab.go), so a supplier
// nobody has configured scores as well as one somebody has.
// mc.Items is CaseysItems enrichment
// keyed by item number — a group whose item has no entry (or mc.Items is
// nil/empty because the lookup failed) gets Detail == nil and the pairing
// falls back to v6.1 behaviour exactly.
//
// Write-gating is EvaluateWriteGate's job, not this function's. Callers must
// not infer permission to write from a non-empty matches slice.
func MatchPOA(doc Products, bcLines []businesscentral.PurchaseOrderLine, mc MatchContext) (matches []LineMatch, unmatched []OrderDetail, unmatchedBC []BCLineGroup) {
	groups := GroupBCLines(bcLines)

	for i := range groups {
		item, ok := mc.Items[groups[i].Item.LineObjectNumber]
		if !ok || groups[i].Item.LineObjectNumber == "" {
			continue
		}
		groups[i].Detail = &item
		// Recompute now that Detail is attached — GroupBCLines built
		// CombinedDesc before enrichment existed.
		groups[i].CombinedDesc = buildCombinedDesc(groups[i])
	}

	// Range augmentation and token weights both come from this document's own
	// candidates, so they must be derived AFTER the enrichment loop above
	// rewrites CombinedDesc: Range_Code arrives with enrichment, and text the
	// corpus never saw weights 1, which would score enrichment boilerplate as
	// though it were distinctive.
	poaTexts, vocab := prepareMatch(doc.Products, groups)

	var all []LineMatch
	for i, d := range doc.Products {
		for _, g := range groups {
			m := scorePair(d, poaTexts[i], g, vocab, mc)
			m.poaIndex = i
			all = append(all, m)
		}
	}
	// Ambiguity resolution and the code-match bonus must both run before
	// fillDescMargins/the sort: FullyVerified (the sort's primary key, and
	// what the >8-line greedy fallback relies on) depends on the final
	// CodeMatch value, and the bonus must land in Score before assign() sees
	// it.
	resolveCodeAmbiguity(all)
	// After resolveCodeAmbiguity, so the two withdrawal reasons compose rather
	// than race: a pairing already withdrawn for matching several BC groups is
	// skipped here, and keeps the more specific explanation.
	resolveVariantAmbiguity(all)
	for i := range all {
		if all[i].CodeMatch {
			all[i].Score += codeMatchBonus
		}
	}
	// Margin must be known before the sort below, not only for the pairs
	// assign() eventually chooses: the sort's primary key is FullyVerified,
	// which now depends on margin, and the greedy (>8 lines) fallback relies
	// on that sort order.
	fillDescMargins(all)

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

// fillDescMargins computes DescMargin (or DescMarginNA) in place for every
// pairing in all: the gap between a pairing's DescScore and the best
// DescScore its POA line achieves against any OTHER BC group. Computed for
// every candidate pairing, not only the ones assign() eventually chooses, so
// FullyVerified is well-defined wherever it's evaluated — including the sort
// immediately below, which the greedy (>8 lines) fallback relies on.
func fillDescMargins(all []LineMatch) {
	byPOA := map[int][]int{}
	for i, m := range all {
		byPOA[m.poaIndex] = append(byPOA[m.poaIndex], i)
	}
	for _, idxs := range byPOA {
		for _, i := range idxs {
			best, bestLabel, found := -1.0, "", false
			for _, j := range idxs {
				if all[j].BC.Item.ID == all[i].BC.Item.ID {
					continue
				}
				found = true
				if all[j].DescScore > best {
					best, bestLabel = all[j].DescScore, bcGroupLabel(all[j].BC)
				}
			}
			if !found {
				all[i].DescMarginNA = true
				continue
			}
			all[i].DescMargin = all[i].DescScore - best
			all[i].DescRunnerUp = bestLabel
		}
	}
}

// bcGroupLabel names a BC group for the review sheet / discrepancy messages,
// preferring the item number since a full CombinedDesc can be long.
func bcGroupLabel(g BCLineGroup) string {
	if g.Item.LineObjectNumber != "" {
		return g.Item.LineObjectNumber
	}
	if len(g.CombinedDesc) > 40 {
		return g.CombinedDesc[:40] + "..."
	}
	return g.CombinedDesc
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
