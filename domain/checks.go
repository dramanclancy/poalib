// The comparison checks.
//
// Each takes one POAProduct and the BCProduct it was paired with, and returns
// a struct — never a bare bool. The card, the report and the logs all read the
// same result, so none of them re-runs a comparison or needs to understand how
// it was reached.
//
// Every result embeds CheckResult: what was checked, how it came out, and what
// to tell a human. The fields alongside are the values the check actually
// compared, so a consumer can show its working.

package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// CheckStatus is the fixed vocabulary every check answers in.
type CheckStatus string

const (
	// StatusMatch — the two sides agree.
	StatusMatch CheckStatus = "match"
	// StatusMismatch — they disagree. The only status that blocks.
	StatusMismatch CheckStatus = "mismatch"
	// StatusWarning — worth a human's attention, but not a disagreement
	// about the goods. Does not block.
	StatusWarning CheckStatus = "warning"
	// StatusUnknown — the check could not be performed, because one side
	// prints nothing to compare or the data was never fetched. Absent is not
	// zero, and a check that cannot be performed passes.
	StatusUnknown CheckStatus = "unknown"
)

// CheckResult is what every check returns, whatever it checked.
type CheckResult struct {
	CheckType string      `json:"checkType"`
	Status    CheckStatus `json:"status"`
	Message   string      `json:"message"`
}

// Failed reports whether this check found a real disagreement — the only
// thing that blocks the write gate. Warning and unknown do not.
func (c CheckResult) Failed() bool { return c.Status == StatusMismatch }

// ---------------------------------------------------------------------------
// Price
// ---------------------------------------------------------------------------

// PriceCheckResult compares what the line is worth on each side.
type PriceCheckResult struct {
	CheckResult

	POAPrice   float64 `json:"poaPrice"`   // the POA's own post-discount net
	BCPrice    float64 `json:"bcPrice"`    // BC's net amount
	Difference float64 `json:"difference"` // POA minus BC, signed

	POAUnitPrice float64 `json:"poaUnitPrice"`
	BCUnitPrice  float64 `json:"bcUnitPrice"`
	Tolerance    float64 `json:"tolerance"`
}

// priceCheck compares the net line value the supplier states against BC's.
//
// Money is a control: a price difference always needs a human, so this check
// has no unknown — both sides always state a figure.
func priceCheck(poa POAProduct, bc BCProduct, cfg Config) PriceCheckResult {
	diff := poa.Net() - bc.Net
	r := PriceCheckResult{
		POAPrice: poa.Net(), BCPrice: bc.Net, Difference: diff,
		POAUnitPrice: poa.UnitPrice, BCUnitPrice: bc.UnitCost,
		Tolerance: cfg.MoneyTolerance,
	}
	r.CheckType = "price"
	if math.Abs(diff) < cfg.MoneyTolerance {
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("net line value agrees (%.2f)", poa.Net())
		return r
	}
	r.Status = StatusMismatch
	r.Message = fmt.Sprintf("net line value differs from BC by %+.2f (POA %.2f, BC %.2f)", diff, poa.Net(), bc.Net)
	return r
}

// ---------------------------------------------------------------------------
// Quantity
// ---------------------------------------------------------------------------

// QuantityCheckResult compares how many the two sides are talking about.
type QuantityCheckResult struct {
	CheckResult

	POAQty float64 `json:"poaQty"`
	BCQty  float64 `json:"bcQty"`
	// PackRatio is 1 when both sides count the same unit (and on every row of
	// a LineGroup, whose Rule says how it reconciled), >1 when the supplier
	// counts packs of that size, 0 when they do not reconcile as packs.
	PackRatio int `json:"packRatio"`
	// PartsPerUnit is >1 when the supplier counts parts of what BC holds as
	// one unit — a Zip & Link mattress acknowledged as 2 mattresses — and 0
	// otherwise. The mirror of PackRatio.
	PartsPerUnit int `json:"partsPerUnit,omitempty"`
	// Rule names how a quantity spread over several lines reconciled. Empty
	// for an ordinary one-to-one pair; see LineGroup.
	Rule QuantityRule `json:"rule,omitempty"`
}

// quantityCheck reports whether the two quantities describe the same physical
// goods: equal, or one a whole multiple of the other with the unit prices
// confirming the same ratio — BC = POA x r when the supplier counts packs and
// BC singles, POA = BC x r when the supplier counts parts of what BC holds as
// one unit. Both conditions must hold, so a genuine quantity change can never
// slip through dressed as a pack size.
//
// Quantity is a control, like price — no unknown.
func quantityCheck(poa POAProduct, bc BCProduct) QuantityCheckResult {
	pq, bq := float64(poa.Qty), bc.Qty
	r := QuantityCheckResult{POAQty: pq, BCQty: bq}
	r.CheckType = "quantity"

	switch ratio, parts := packRatio(poa, bc), partsRatio(poa, bc); {
	case ratio == 1:
		r.PackRatio, r.Status = 1, StatusMatch
		r.Message = fmt.Sprintf("quantity agrees (%g)", pq)
	case ratio > 1:
		r.PackRatio, r.Status = ratio, StatusMatch
		r.Message = fmt.Sprintf("quantity agrees at %d per pack (POA %g packs, BC %g singles)", ratio, pq, bq)
	case parts > 1:
		r.PartsPerUnit, r.Status = parts, StatusMatch
		r.Message = fmt.Sprintf("quantity agrees at %d parts per unit (POA %g parts, BC %g units)", parts, pq, bq)
	default:
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("quantity differs (POA %g, BC %g)", pq, bq)
	}
	return r
}

// partsRatio is packRatio the other way round: the supplier counts r parts for
// every unit BC orders, each priced at 1/r of BC's unit. CA24 acknowledges a
// Zip & Link mattress as 2 x 466.00 where BC holds 1 x 932.00, which read as a
// quantity mismatch on an order that agreed to the penny.
//
// The unit prices must confirm the ratio for the same reason as a pack's.
func partsRatio(poa POAProduct, bc BCProduct) int {
	pq, bq := float64(poa.Qty), bc.Qty
	if pq <= bq || bq <= 0 {
		return 0
	}
	r := math.Round(pq / bq)
	if r < 2 || math.Abs(pq/bq-r) > 0.001 {
		return 0
	}
	if math.Abs(poa.UnitNet()*r-bc.UnitNet()) > 0.01 {
		return 0
	}
	return int(r)
}

func packRatio(poa POAProduct, bc BCProduct) int {
	pq, bq := float64(poa.Qty), bc.Qty
	if pq <= 0 || bq <= 0 {
		return 0
	}
	if math.Abs(pq-bq) < 0.001 {
		return 1
	}
	r := math.Round(bq / pq)
	if r < 2 || math.Abs(bq/pq-r) > 0.001 {
		return 0
	}
	// The unit prices must confirm the ratio independently, or a real
	// quantity change would pass as a pack size.
	if math.Abs(poa.UnitNet()-bc.UnitNet()*r) > 0.01 {
		return 0
	}
	return int(r)
}

// ---------------------------------------------------------------------------
// The POA's own arithmetic
// ---------------------------------------------------------------------------

// ArithmeticCheckResult compares the POA against itself.
type ArithmeticCheckResult struct {
	CheckResult

	Net float64 `json:"net"`
	// PrintedLineTotal is nil when the supplier prints no line-total column,
	// which is the usual reason this comes back unknown.
	PrintedLineTotal *float64 `json:"printedLineTotal"`
	Difference       *float64 `json:"difference"`
}

// arithmeticCheck verifies the supplier's figures against each other. It needs
// no BC side: a POA that contradicts itself is an extraction misread or a
// broken document, worth saying separately from any disagreement with the order.
func arithmeticCheck(poa POAProduct, cfg Config) ArithmeticCheckResult {
	r := ArithmeticCheckResult{Net: poa.Net(), PrintedLineTotal: poa.LineTotal}
	r.CheckType = "arithmetic"

	printed, ok := Deref(poa.LineTotal)
	if !ok {
		r.Status = StatusUnknown
		r.Message = "the supplier prints no line total, so there is nothing to reconcile against"
		return r
	}
	diff := poa.Net() - printed
	r.Difference = Ptr(diff)
	if math.Abs(diff) < cfg.MoneyTolerance {
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("the POA's own figures add up (%.2f)", poa.Net())
		return r
	}
	r.Status = StatusMismatch
	r.Message = fmt.Sprintf(
		"the POA's own figures don't add up: net %.2f vs printed line total %.2f (diff %+.2f) — likely extraction misread",
		poa.Net(), printed, diff)
	return r
}

// ---------------------------------------------------------------------------
// Orientation
// ---------------------------------------------------------------------------

// OrientationCheckResult compares handedness (LHF / RHF).
type OrientationCheckResult struct {
	CheckResult

	POAOrientation string `json:"poaOrientation"`
	BCOrientation  string `json:"bcOrientation"`
	// BCSource is the text BC's reading came from, so a reviewer sees what
	// was read rather than only what it was read as.
	BCSource string `json:"bcSource"`
}

// orientationCheck compares the handedness each side states.
//
// A hard constraint, resolved independently on each side and compared only
// after the pairing exists — it never contributes to the score that decides
// which lines pair up. It fails closed: anything short of two definite, equal
// readings is a mismatch, except a line where neither side is handed at all
// (surcharges, carriage, discounts), which is unknown.
func orientationCheck(poa POAProduct, bc BCProduct) OrientationCheckResult {
	poaOrient := Classify(poa.Text())
	bcOrient, bcSrc := ResolveBC(bc.LineDescription, bc.Comments)

	r := OrientationCheckResult{
		POAOrientation: poaOrient.String(),
		BCOrientation:  bcOrient.String(),
		BCSource:       bcSrc,
	}
	r.CheckType = "orientation"

	switch {
	case poaOrient == OrientUnknown && bcOrient == OrientUnknown:
		r.Status, r.Message = StatusUnknown, "not a handed line"
	case poaOrient.definite() && bcOrient.definite() && poaOrient == bcOrient:
		r.Status, r.Message = StatusMatch, fmt.Sprintf("both %s", poaOrient)
	case poaOrient.definite() && bcOrient.definite():
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("PO specifies %s, supplier confirmed %s", bcOrient, poaOrient)
	case bcOrient.definite() && !poaOrient.definite():
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("PO specifies %s, acknowledgement does not state an orientation", bcOrient)
	case poaOrient.definite() && !bcOrient.definite():
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("supplier confirmed %s, PO does not specify an orientation (%s)", poaOrient, bcOrient)
	default:
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("orientation unresolved (POA %s, BC %s)", poaOrient, bcOrient)
	}
	return r
}

// ---------------------------------------------------------------------------
// Seat count
// ---------------------------------------------------------------------------

// SeatsCheckResult compares the seat count the POA states with the one BC
// holds. Both figures are nil when there was nothing to compare, and Message
// says which of the several possible reasons applied.
type SeatsCheckResult struct {
	CheckResult

	POASeats *float64 `json:"poaSeats"`
	BCSeats  *float64 `json:"bcSeats"`
	// DataState names why a comparison could not be made, for a report that
	// wants to count the reasons apart rather than read the prose.
	DataState string `json:"dataState"`
}

// Seat-count data states. A report distinguishes these because they call for
// different actions: one is a configuration problem, one is a transient
// failure, one is a master-data gap, one is normal.
const (
	SeatsComparable       = "comparable"
	SeatsEnrichmentOff    = "enrichment_not_run"
	SeatsEnrichmentFailed = "enrichment_failed"
	SeatsNoItemRecord     = "no_item_record"
	SeatsNotOnItem        = "not_recorded_on_item"
	SeatsImplausible      = "implausible_on_item"
	SeatsNotOnPOA         = "not_stated_on_poa"
	// SeatsFromDescription means the comparison WAS made, but BC's figure came
	// from its own line description rather than the item record. The verdict
	// is worth exactly as much; the item record is still missing data someone
	// should fill in, and naming the source separately keeps that visible
	// instead of letting a working check hide a master-data gap.
	SeatsFromDescription = "comparable_from_description"
)

// seatCountRe matches "3STR", "3 STR", "3 SEATER", "2.5 SEATER" — the POA
// vocabulary for seat count, case-insensitively.
var seatCountRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:str|seaters?)\b`)

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

// seatsCheck is an independent structural check: the seat count the supplier
// prints against the one BC holds. A supplier can print the wrong seat count
// at a price that matches perfectly, which is why this does not ride on price.
//
// It is unknown — and therefore passing — whenever either side has nothing to
// say, but it never conflates the reasons. "BC holds no seat count for this
// item" is a master-data gap someone can fix; "enrichment did not run" is a
// configuration problem and says nothing about BC at all. Across the batch of
// 2026-09-04 not one of thirty lines ran this check, and the report could not
// distinguish those two cases — which is what this state machine fixes.
func seatsCheck(poa POAProduct, bc BCProduct, state EnrichmentState, cfg Config) SeatsCheckResult {
	r := SeatsCheckResult{}
	r.CheckType = "seats"
	r.Status = StatusUnknown

	// Why the ITEM RECORD had no usable figure, recorded rather than returned:
	// BC's description may still be able to answer, and the reason only needs
	// telling if it cannot.
	var gap, gapMsg string
	switch {
	case !state.Ran():
		gap = SeatsEnrichmentOff
		gapMsg = "item enrichment was not run for this order, so BC's seat count is unknown"
	case bc.Detail == nil && state == EnrichmentFailed:
		gap = SeatsEnrichmentFailed
		gapMsg = "item enrichment failed for this order, so BC's seat count is unknown"
	case bc.Detail == nil:
		gap = SeatsNoItemRecord
		gapMsg = fmt.Sprintf("item enrichment returned no record for BC item %s", bc.ItemNo)
	case bc.Detail.Seats == nil:
		gap = SeatsNotOnItem
		gapMsg = fmt.Sprintf("BC item %s records no seat count", bc.ItemNo)
	case *bc.Detail.Seats < cfg.MinSeats:
		// CaseysItems returned 0.5 on a parasol. Below the floor it is not a
		// furniture seat count, whatever it is.
		//
		// Deliberately no description fallback here: an item record holding a
		// figure that cannot be a seat count is a data-quality signal in its
		// own right, and reading past it to the description would hide it.
		r.DataState = SeatsImplausible
		r.BCSeats = bc.Detail.Seats
		r.Message = fmt.Sprintf("BC item %s records %g seats, too low to be a real seat count", bc.ItemNo, *bc.Detail.Seats)
		return r
	}

	bcSeats, fromDescription := 0.0, false
	if gap == "" {
		bcSeats = *bc.Detail.Seats
	} else {
		v, ok := bcSeatCount(bc, cfg)
		if !ok {
			r.DataState, r.Message = gap, gapMsg
			return r
		}
		bcSeats, fromDescription = v, true
	}

	poaSeats, ok := extractSeatCount(poa.Text())
	if !ok {
		r.DataState = SeatsNotOnPOA
		r.BCSeats = Ptr(bcSeats)
		r.Message = "the acknowledgement states no seat count"
		return r
	}

	source := ""
	r.DataState = SeatsComparable
	if fromDescription {
		r.DataState = SeatsFromDescription
		source = ", read from BC's description rather than the item record"
	}
	r.POASeats, r.BCSeats = Ptr(poaSeats), Ptr(bcSeats)
	if math.Abs(poaSeats-bcSeats) < 0.01 {
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("seat count agrees (%g)%s", poaSeats, source)
		return r
	}
	r.Status = StatusMismatch
	r.Message = fmt.Sprintf("seat count differs (POA states %g, BC records %g)%s", poaSeats, bcSeats, source)
	return r
}

// bcSeatCount reads a seat count out of BC's own text. BCProduct.Description
// is the merged description — item name, item detail and the comment lines —
// so "4 Seater Sofa" and "2.5 Seater End RHF/LHF" are already in one string.
//
// This exists because No_of_Seats is barely populated. Across the orders
// captured to testdata/orders the item record carried a seat count on 2 of 48
// enriched lines — and both of those were on products that do not seat anyone
// (a 2-drawer bed base reading 2, a dining table reading 1), so the field is
// not merely sparse but unreliable where it is filled. BC's description, by
// contrast, stated a seat count on all 17 lines where the POA stated one, and
// every one of the 17 agreed.
//
// A check that reads only the item record is a check that never runs, and a
// seat mismatch is one of the few things that blocks a write.
//
// The MinSeats floor applies to this figure for the same reason it applies to
// the item record's: a number below it is not a furniture seat count.
func bcSeatCount(bc BCProduct, cfg Config) (float64, bool) {
	v, ok := extractSeatCount(bc.Description)
	if !ok || v < cfg.MinSeats {
		return 0, false
	}
	return v, true
}

// ---------------------------------------------------------------------------
// Description — the identity verdict
// ---------------------------------------------------------------------------

// DescriptionCheckResult answers "are these two lines the same product?".
//
// Deliberately the richest result: when identity fails, the fix is usually
// master data (a Vendor_Item_No BC lacks), so everything a person needs to
// make that fix travels with it.
type DescriptionCheckResult struct {
	CheckResult

	// POADescription and BCDescription are the exact strings compared —
	// BCDescription is the merged text, never the raw purchase order line.
	POADescription string `json:"poaDescription"`
	BCDescription  string `json:"bcDescription"`

	// SimilarityScore is cosine similarity of the two embeddings. nil means
	// it was not calculated, which is not the same as scoring zero.
	SimilarityScore *float64 `json:"similarityScore"`
	// Margin is how far ahead of the best STILL-AVAILABLE alternative this
	// pairing scored. nil when there was no alternative to compare against.
	Margin   *float64 `json:"margin"`
	RunnerUp string   `json:"runnerUp"`

	CodeMatch       bool   `json:"codeMatch"`
	CodeMatchSource string `json:"codeMatchSource"`
	// CodeAmbiguousWith / VariantAmbiguity name why a code match was
	// withdrawn: it matched several BC products, or it names a family without
	// saying which variant this line is.
	CodeAmbiguousWith string `json:"codeAmbiguousWith"`
	VariantAmbiguity  string `json:"variantAmbiguity"`

	// POACode is the product code found in the POA's own text, whether or not
	// it matched — the lookup key a rules table would use.
	POACode string `json:"poaCode"`
	// CodeDataState records what is actually known about BC's codes, so a
	// message never blames master data for a lookup that never ran.
	CodeDataState string `json:"codeDataState"`

	Threshold       float64 `json:"threshold"`
	MarginThreshold float64 `json:"marginThreshold"`
}

// descriptionCheck decides whether the description evidence identifies the line.
//
// An exact code match settles identity outright — it beats fuzzy text
// entirely, INCLUDING when similarity is low, because a supplier's free text
// can legitimately describe a different attribute of the same item than its
// catalogue code does. Otherwise descriptions must agree well enough in
// absolute terms AND clearly beat the alternatives: a high score against two
// equally plausible products is not confidence, and a clear lead over three
// bad options is not either.
func descriptionCheck(poa POAProduct, bc BCProduct, ev MatchEvidence, state EnrichmentState, cfg Config) DescriptionCheckResult {
	known, note := codeStatus(bc, state)
	r := DescriptionCheckResult{
		POADescription:    poa.Text(),
		BCDescription:     bc.Description,
		SimilarityScore:   ev.Similarity,
		CodeMatch:         ev.CodeMatch,
		CodeMatchSource:   ev.CodeMatchSource,
		CodeAmbiguousWith: ev.CodeAmbiguousWith,
		VariantAmbiguity:  ev.VariantAmbiguity,
		POACode:           productCodeIn(poa.Text(), cfg),
		Threshold:         cfg.DescThreshold,
		MarginThreshold:   cfg.MarginThreshold,
	}
	if !ev.MarginNA {
		r.Margin = Ptr(ev.Margin)
		r.RunnerUp = ev.RunnerUp
	}
	switch {
	case !known:
		r.CodeDataState = "unknown"
	case note != "":
		r.CodeDataState = "no_code_on_file"
	default:
		r.CodeDataState = "code_on_file"
	}
	r.CheckType = "description"

	switch {
	case ev.CodeMatch:
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("identified by %s printed on the acknowledgement", ev.CodeMatchSource)
	case ev.Similarity == nil:
		// No embedding for one of the two texts. Fail closed: there is no
		// fallback identity signal, so this is "no evidence", never "close
		// enough".
		r.Status = StatusMismatch
		r.Message = "description similarity was not calculated for this line"
	case *ev.Similarity < cfg.DescThreshold:
		r.Status = StatusMismatch
		_, r.Message = identityGap(r, bc, note)
	case !ev.MarginNA && ev.Margin < cfg.MarginThreshold:
		r.Status = StatusMismatch
		r.Message = fmt.Sprintf("ambiguous pairing: resembles %q almost equally (margin %.2f)", ev.RunnerUp, ev.Margin)
	case ev.CodeAmbiguousWith != "" || ev.VariantAmbiguity != "":
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("descriptions agree (similarity %.2f), but a code match was withdrawn as ambiguous", *ev.Similarity)
	default:
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("descriptions agree (similarity %.2f)", *ev.Similarity)
	}
	return r
}

// identityGap explains a low description score in the most actionable terms
// the available data supports.
//
// "descriptions look different (similarity 0.33)" is technically true and
// practically useless when the two sides are not speaking the same kind of
// language at all: the POA prints a SKU and BC prints a generic type name.
// Where the real obstacle is a missing Vendor_Item_No, say so — that names a
// fix, once, for every future order.
//
// codeNote carries what codeStatus established, so this never claims BC holds
// no code when nobody asked BC.
func identityGap(r DescriptionCheckResult, bc BCProduct, codeNote string) (DiscrepancyKind, string) {
	score := 0.0
	if r.SimilarityScore != nil {
		score = *r.SimilarityScore
	}
	if r.POACode != "" && codeNote != "" {
		return DiscMissingBCCode, fmt.Sprintf(
			"POA prints product code %q and %s, so there was no code to match it against (descriptions alone scored %.2f)",
			r.POACode, codeNote, score)
	}
	return DiscDescription, fmt.Sprintf("descriptions look different (similarity %.2f)", score)
}

// ---------------------------------------------------------------------------
// Order totals
// ---------------------------------------------------------------------------

// TotalsCheckResult is the document-level comparison above the per-product
// checks.
type TotalsCheckResult struct {
	CheckResult

	POATotal    float64 `json:"poaTotal"`
	TotalSource string  `json:"totalSource"`
	BCTotal     float64 `json:"bcTotal"`
	Difference  float64 `json:"difference"`
	// MatchedTotal is the sum over paired BC products. Short of BCTotal means
	// the acknowledgement never mentioned part of the order.
	MatchedTotal   float64 `json:"matchedTotal"`
	LinesMatched   int     `json:"linesMatched"`
	LinesUnmatched int     `json:"linesUnmatched"`
	Tolerance      float64 `json:"tolerance"`
}

// totalsCheck compares the supplier's order total against BC's, catching what
// per-line checks cannot: drift that survived them, VAT included where it
// should not be, or a misread of the total field.
func totalsCheck(poa POAOrder, bc BCOrder, matched []ProductResult, unmatched []POAProduct, cfg Config) TotalsCheckResult {
	total, source := poa.Total()

	// Once per BC product: an aggregate group pairs several POA lines with the
	// same one, and counting it per row would overstate what was matched.
	var matchedTotal float64
	counted := map[int]bool{}
	for _, m := range matched {
		if !counted[m.BCIndex] {
			counted[m.BCIndex] = true
			matchedTotal += m.BC.Net
		}
	}

	diff := total - bc.TotalExVAT
	r := TotalsCheckResult{
		POATotal: total, TotalSource: source, BCTotal: bc.TotalExVAT, Difference: diff,
		MatchedTotal: matchedTotal, LinesMatched: len(matched), LinesUnmatched: len(unmatched),
		Tolerance: cfg.OrderTolerance,
	}
	r.CheckType = "totals"
	if math.Abs(diff) < cfg.OrderTolerance {
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("order total agrees (%.2f, %s)", total, source)
		return r
	}
	r.Status = StatusMismatch
	r.Message = fmt.Sprintf("order total differs from BC by %+.2f (POA %.2f %s, BC %.2f)", diff, total, source, bc.TotalExVAT)
	return r
}
