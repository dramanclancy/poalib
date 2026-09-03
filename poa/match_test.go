package poa

import (
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

// neutralVocab is the vocabulary for a single isolated pair. With no corpus
// nothing repeats, so every token weighs 1 and the weighted score equals the
// plain overlap — which is what a test scoring one pair in isolation wants,
// since it is exercising scorePair rather than the weighting.
func neutralVocab() Vocab { return buildVocab(nil, nil) }

// ---------------------------------------------------------------------------
// PF128964 regression fixture (see phase1-description-matching.md section 1)
// ---------------------------------------------------------------------------
//
// whitemeadowVendorNo is the BC vendor number for the supplier this fixture
// reconstructs ("Whitemeadow Furniture Ltd", confirmed against CaseysItems in
// both Production and Upgrade_Sandbox). It lives here rather than in vocab.go
// because as of v6.3 nothing in the engine is keyed by vendor identity — this
// is fixture data describing whose order the fixture is, not configuration.
//
// testdata/poa-samples/ is gitignored (real supplier data), so the real
// order can't be inlined verbatim. This reconstructs the same shape reported
// against the live sample — a Whitemeadow "Metro" sofa broken into four BC
// lines (two mirror-image arm units distinguished only by orientation, a
// corner unit, and an unrelated footstool) with matching POA lines whose
// vocabulary differs from BC's the same way the real order's did: BC states
// fabric/feet as labelled comments ("WM FABRIC GRADE B", "Feet Options 2"),
// the POA states them as its own free text ("Body Seats Backs", "K Foot").
// Before the fix (cleanTokenSet unimplemented / DescThreshold=0.5, no
// margin) this fails on the IdentityConfident assertion — that failure is
// the bug this task fixes.

const whitemeadowVendorNo = "VX00020"

func pf128964POALines() []OrderDetail {
	return []OrderDetail{
		{ // 0: arm unit, LHF — foot fitting position 2 (handing-specific)
			Product:      "Metro 1 Arm Sofa Unit LHF Digby Beige",
			Description:  "Body Seats Backs - Digby Beige",
			Description2: "K Foot Weathered Oak (Set of 4)",
			Description3: "Feet Position 2",
			Qty:          1,
		},
		{ // 1: arm unit, RHF — mirror of line 0, foot fitting position 3
			// (not 4: POA lines all carry "(Set of 4)" for the leg count, so a
			// fitting position of 4 would coincidentally collide with that
			// unrelated token instead of actually distinguishing the pair)
			Product:      "Metro 1 Arm Sofa Unit RHF Digby Beige",
			Description:  "Body Seats Backs - Digby Beige",
			Description2: "K Foot Weathered Oak (Set of 4)",
			Description3: "Feet Position 3",
			Qty:          1,
		},
		{ // 2: corner unit
			Product:      "Metro Corner Unit Digby Beige",
			Description:  "Body Seats Backs - Digby Beige",
			Description2: "K Foot Weathered Oak (Set of 4)",
			Qty:          1,
		},
		{ // 3: footstool — unrelated to the other three
			Product:      "Metro Footstool Digby Beige",
			Description:  "All Over Self Finished",
			Description2: "K Foot Weathered Oak (Set of 4)",
			Qty:          1,
		},
	}
}

// pf128964BCLines returns the four BC groups as raw PurchaseOrderLines (Item
// + Comment), the shape MatchPOA actually receives, so the test exercises
// GroupBCLines too.
func pf128964BCLines() []businesscentral.PurchaseOrderLine {
	line := func(seq int, lineType, id, itemNo, displayName, desc string, qty float64) businesscentral.PurchaseOrderLine {
		return businesscentral.PurchaseOrderLine{
			ID: id, Sequence: seq, LineType: lineType, LineObjectNumber: itemNo,
			DisplayName: displayName, Description: desc, Quantity: qty,
		}
	}
	comment := func(seq int, desc string) businesscentral.PurchaseOrderLine {
		return businesscentral.PurchaseOrderLine{Sequence: seq, LineType: "Comment", Description: desc}
	}

	var lines []businesscentral.PurchaseOrderLine
	// BC0: 1 Arm Sofa Unit, LHF, foot fitting position 2
	lines = append(lines,
		line(100, "Item", "bc-0", "SOFA-ARM", "1 Arm Sofa Unit", "", 1),
		comment(101, "WM FABRIC GRADE B Digby Beige"),
		comment(102, "LEFT/RIGHT OPTION Left Hand Facing"),
		comment(103, "Feet Options 2 Weather Oak"),
	)
	// BC1: 1 Arm Sofa Unit, RHF, foot fitting position 4 — otherwise
	// identical to BC0 apart from orientation, same as the real order's
	// mirror-image arm units.
	lines = append(lines,
		line(200, "Item", "bc-1", "SOFA-ARM", "1 Arm Sofa Unit", "", 1),
		comment(201, "WM FABRIC GRADE B Digby Beige"),
		comment(202, "LEFT/RIGHT OPTION Right Hand Facing"),
		comment(203, "Feet Options 3 Weather Oak"),
	)
	// BC2: Corner Unit
	lines = append(lines,
		line(300, "Item", "bc-2", "SOFA-CNR", "Corner Unit", "", 1),
		comment(301, "WM FABRIC GRADE B Digby Beige"),
		comment(302, "Feet Options 2 Weather Oak"),
	)
	// BC3: Footstool — no shared item family with 0-2
	lines = append(lines,
		line(400, "Item", "bc-3", "SOFA-FS", "Footstool", "", 1),
		comment(401, "WM FABRIC GRADE B Digby Beige"),
		comment(402, "Feet Options 2 Weather Oak"),
	)
	return lines
}

// TestMatchPOA_PF128964_CorrectAssignmentAndConfident is the regression
// fixture required before touching the scoring: it asserts the FULL
// assignment (0->0, 1->1, 2->2, 3->3), not just individual scores, and that
// every line clears IdentityConfident() — the thing DescThreshold=0.5 alone
// could not do, because the raw overlap score for every one of these four
// pairs sits below 0.5 (BC comment-label boilerplate and POA scaffolding
// words dominate the denominator on both sides).
func TestMatchPOA_PF128964_CorrectAssignmentAndConfident(t *testing.T) {
	doc := Products{PF: "PF128964", Products: pf128964POALines()}
	bcLines := pf128964BCLines()

	matches, unmatched, unmatchedBC := MatchPOA(doc, bcLines, MatchContext{VendorNo: whitemeadowVendorNo})

	if len(unmatched) != 0 {
		t.Fatalf("unmatched POA lines = %d, want 0: %+v", len(unmatched), unmatched)
	}
	if len(unmatchedBC) != 0 {
		t.Fatalf("unmatched BC groups = %d, want 0", len(unmatchedBC))
	}
	if len(matches) != 4 {
		t.Fatalf("matches = %d, want 4", len(matches))
	}

	wantBCID := map[int]string{0: "bc-0", 1: "bc-1", 2: "bc-2", 3: "bc-3"}
	for _, m := range matches {
		if got, want := m.BC.Item.ID, wantBCID[m.poaIndex]; got != want {
			t.Errorf("POA line %d paired with BC %q, want %q", m.poaIndex, got, want)
		}
		if !m.IdentityConfident() {
			t.Errorf("POA line %d (-> BC %s) not IdentityConfident: score=%.3f raw=%.3f margin=%.3f marginNA=%v",
				m.poaIndex, m.BC.Item.ID, m.DescScore, m.DescScoreRaw, m.DescMargin, m.DescMarginNA)
		}
		t.Logf("POA%d -> %s: raw=%.3f clean=%.3f margin=%.3f marginNA=%v runnerUp=%q",
			m.poaIndex, m.BC.Item.ID, m.DescScoreRaw, m.DescScore, m.DescMargin, m.DescMarginNA, m.DescRunnerUp)
	}
}

// TestScorePair_WrongPairingNotConfident is the negative half of the
// regression fixture: a POA line paired against a BC group it plainly is not
// (the footstool line against the arm-unit group) must not be
// IdentityConfident, so the test can fail in either direction — a scoring
// change that's too lenient trips this one.
func TestScorePair_WrongPairingNotConfident(t *testing.T) {
	bcLines := pf128964BCLines()
	groups := GroupBCLines(bcLines)
	poaTexts, vocab := prepareMatch(pf128964POALines(), groups)

	footstool := pf128964POALines()[3]
	var armUnit BCLineGroup
	for _, g := range groups {
		if g.Item.ID == "bc-0" {
			armUnit = g
		}
	}
	if armUnit.Item.ID == "" {
		t.Fatal("fixture setup: bc-0 group not found")
	}

	m := scorePair(footstool, poaTexts[3], armUnit, vocab, MatchContext{VendorNo: whitemeadowVendorNo})
	if m.DescScore >= DescThreshold {
		t.Fatalf("footstool vs arm-unit DescScore = %.3f, want < %.2f (they are not the same line)", m.DescScore, DescThreshold)
	}
	if m.IdentityConfident() {
		t.Errorf("footstool incorrectly paired with arm-unit reports IdentityConfident")
	}
}

// TestScorePair_AllBoilerplateFallsBackToRaw covers the edge case called out
// in phase1-description-matching.md, in its v6.3 form. Under the old stoplist
// the failure mode was cleaning emptying a token set; under weighting it is
// every surviving token scoring zero because it repeats across every
// candidate. Either way weightedOverlap has no evidence to divide by, and
// scorePair must fall back to the raw (unweighted) score rather than flag an
// otherwise perfectly matching line.
func TestScorePair_AllBoilerplateFallsBackToRaw(t *testing.T) {
	// "Feet Options" on every document, both sides: df == n on each side, so
	// every token weighs 0 and both sides carry zero mass. The raw text is
	// identical, so without the fallback a perfect match would score 0.
	products := []OrderDetail{{Product: "Feet Options"}, {Product: "Feet Options"}}
	groups := []BCLineGroup{
		{Item: businesscentral.PurchaseOrderLine{ID: "bc-x"}, CombinedDesc: "Feet Options"},
		{Item: businesscentral.PurchaseOrderLine{ID: "bc-y"}, CombinedDesc: "Feet Options"},
	}
	poaTexts, vocab := prepareMatch(products, groups)

	toks := cleanTokenSet(products[0].Product)
	if len(toks) == 0 {
		t.Fatal("test setup invalid: cleaning now discards tokens; v6.3 weights them instead")
	}
	if mass := vocab.mass(toks); mass != 0 {
		t.Fatalf("test setup invalid: token mass = %.3f, want 0 (every token repeats on every document)", mass)
	}

	m := scorePair(products[0], poaTexts[0], groups[0], vocab, MatchContext{VendorNo: whitemeadowVendorNo})
	if m.DescScore != m.DescScoreRaw {
		t.Errorf("DescScore = %.3f, DescScoreRaw = %.3f; want equal (fallback to raw when neither side carries weight)",
			m.DescScore, m.DescScoreRaw)
	}
	if m.DescScore == 0 {
		t.Errorf("DescScore = 0; want the raw overlap (identical text both sides), not the zero-mass 0")
	}
}

// ---------------------------------------------------------------------------
// Phase 1b: CaseysItems enrichment (see phase1b-item-enrichment.md)
// ---------------------------------------------------------------------------

// realPF128964CaseysItems is the actual CaseysItems enrichment fetched for
// PF128964's four real BC items (Production environment, tenant/company
// main.go hardcodes; fetched 2026-08-20). Range_Code is "METRO" on every one
// — confirming Phase 1's prediction that the POA's leading "Metro" token,
// stoplisted as noise for lack of a BC-side equivalent, becomes a real match
// once CaseysItems supplies one. Model_No and Vendor_Item_No are both empty
// on all four: Whitemeadow's live fill rate for those fields measured at
// ~0% (1/929 and 2/929 respectively, sampled Production, Blocked=false and
// Discontinued=false) — so this order has no code-match signal, consistent
// with the near-zero POA-line code-match hit rate measured across every
// real sample in testdata/poa-samples/.
func realPF128964CaseysItems() map[string]businesscentral.CaseysItem {
	item := func(no string, seats float64) businesscentral.CaseysItem {
		return businesscentral.CaseysItem{
			No: no, RangeCode: "METRO", NoOfSeats: seats,
			VendorNo: whitemeadowVendorNo, SupplierName: "Whitemeadow Furniture Ltd",
		}
	}
	return map[string]businesscentral.CaseysItem{
		"IT0163911": item("IT0163911", 1), // 1 Arm Sofa Unit
		"IT0204176": item("IT0204176", 1), // 1 Arm 1 Seater
		"IT0163917": item("IT0163917", 0), // Combi Stool
		"IT0204300": item("IT0204300", 0), // Scatter Package
	}
}

// pf128964BCLinesRealItemNumbers is pf128964BCLines with the four synthetic
// item numbers swapped for the real ones CaseysItems was queried against —
// same comment scaffolding (fabric/orientation/feet), so this isolates
// exactly what enrichment adds rather than re-deriving the whole fixture.
func pf128964BCLinesRealItemNumbers() []businesscentral.PurchaseOrderLine {
	realNo := map[string]string{
		"bc-0": "IT0163911", "bc-1": "IT0204176", "bc-2": "IT0163917", "bc-3": "IT0204300",
	}
	lines := pf128964BCLines()
	for i := range lines {
		if no, ok := realNo[lines[i].ID]; ok {
			lines[i].LineObjectNumber = no
		}
	}
	return lines
}

// TestMatchPOA_PF128964_CaseysEnrichment_RangeCodeRaisesScore is section 5
// point 2: with the real Range_Code ("METRO") attached, every line's
// DescScore should rise from its Phase 1 (v6.1) value, since "metro" now
// matches on the BC side instead of being absent from it, and every pairing
// should remain (or become) IdentityConfident.
func TestMatchPOA_PF128964_CaseysEnrichment_RangeCodeRaisesScore(t *testing.T) {
	doc := Products{PF: "PF128964", Products: pf128964POALines()}
	bcLines := pf128964BCLinesRealItemNumbers()

	withoutEnrichment, _, _ := MatchPOA(doc, bcLines, MatchContext{VendorNo: whitemeadowVendorNo})
	withEnrichment, unmatched, unmatchedBC := MatchPOA(doc, bcLines, MatchContext{
		VendorNo: whitemeadowVendorNo, Items: realPF128964CaseysItems(),
	})

	if len(unmatched) != 0 || len(unmatchedBC) != 0 {
		t.Fatalf("expected a full assignment with enrichment, got unmatched=%d unmatchedBC=%d", len(unmatched), len(unmatchedBC))
	}

	scoreOf := func(matches []LineMatch, poaIdx int) LineMatch {
		for _, m := range matches {
			if m.poaIndex == poaIdx {
				return m
			}
		}
		t.Fatalf("no match found for POA line %d", poaIdx)
		return LineMatch{}
	}

	for i := 0; i < 4; i++ {
		before, after := scoreOf(withoutEnrichment, i), scoreOf(withEnrichment, i)
		t.Logf("POA%d DescScore: v6.1=%.3f -> v6.2(+Range_Code)=%.3f  margin=%.3f runnerUp=%s",
			i, before.DescScore, after.DescScore, after.DescMargin, after.DescRunnerUp)
		if after.DescScore < before.DescScore {
			t.Errorf("POA%d: enrichment LOWERED DescScore (%.3f -> %.3f), want >= (Range_Code should only add evidence)",
				i, before.DescScore, after.DescScore)
		}
		if after.CodeMatch {
			t.Errorf("POA%d: CodeMatch = true, want false — these items have no Model_No/Vendor_Item_No in the real data", i)
		}
	}

	// POA2 (Combi Stool) and POA3 (Scatter Package) share no range-mate close
	// enough to threaten their margin, so they stay comfortably confident.
	for _, i := range []int{2, 3} {
		after := scoreOf(withEnrichment, i)
		if !after.IdentityConfident() {
			t.Errorf("POA%d not IdentityConfident with enrichment: DescScore=%.3f margin=%.3f", i, after.DescScore, after.DescMargin)
		}
	}

	// POA0/POA1 are the LHF/RHF mirror-image arm units — the hardest pair in
	// the fixture, because Range_Code is common to every item IN THE RANGE and
	// so raises the correct pairing's score and its closest alternative's
	// (each other's) by nearly the same amount.
	//
	// Under v6.2's stoplist this squeezed the discriminative margin down onto
	// MarginThreshold itself (0.09999999999999998 vs the 0.10 constant), close
	// enough that float rounding decided IdentityConfident() either way. That
	// state was pinned here rather than asserted, with a note asking that any
	// change to it register as an intentional decision.
	//
	// This is that decision. v6.3's weighting resolves the squeeze rather than
	// tiptoeing around it: tokens the two units share — the range name, "sofa",
	// "arm", "unit" — repeat across every candidate and are discounted towards
	// zero, so what remains in the score is precisely what tells the two apart.
	// The margin clears the threshold outright instead of landing on it, and
	// the float-comparison fragility stops being reachable from this fixture.
	for _, i := range []int{0, 1} {
		after := scoreOf(withEnrichment, i)
		if after.DescMargin < MarginThreshold {
			t.Errorf("POA%d: DescMargin %.5f below MarginThreshold %.2f — the v6.2 mirror-image squeeze has returned",
				i, after.DescMargin, MarginThreshold)
		}
		if !after.IdentityConfident() {
			t.Errorf("POA%d not IdentityConfident: DescScore=%.3f margin=%.5f", i, after.DescScore, after.DescMargin)
		}
	}
}

// TestScorePair_CodeMatchSetsIdentityConfidentDespiteLowDescScore is section
// 5 point 3 (the code-match half): a POA line whose text contains the BC
// item's Model_No/Vendor_Item_No verbatim is IdentityConfident even though
// the surrounding description text barely overlaps — the real-world case
// this covers is Egoitaliano's IT0236398, whose Model_No/Vendor_Item_No
// (1341CASHSKY21RAL9005) is a genuine separate code, not something
// squashing the description would ever produce.
func TestScorePair_CodeMatchSetsIdentityConfidentDespiteLowDescScore(t *testing.T) {
	vocab := neutralVocab()
	d := OrderDetail{Product: "Some unrelated free text 1341CASHSKY21RAL9005 more words"}
	g := BCLineGroup{
		Item:         businesscentral.PurchaseOrderLine{ID: "bc-x", LineObjectNumber: "IT0236398"},
		CombinedDesc: "1341 MICROCASH SKY21 RAL 9005", // real BC description; deliberately low raw overlap with the POA text above
		Detail: &businesscentral.CaseysItem{
			No: "IT0236398", ModelNo: "1341CASHSKY21RAL9005", VendorItemNo: "1341CASHSKY21RAL9005",
			VendorNo: "VX01568",
		},
	}
	mc := MatchContext{VendorNo: "VX01568"}

	m := scorePair(d, combinedPOADesc(d), g, vocab, mc)
	if m.DescScore >= DescThreshold {
		t.Fatalf("test setup invalid: DescScore %.3f already clears %.2f without help from the code match", m.DescScore, DescThreshold)
	}
	if !m.CodeMatch {
		t.Fatalf("CodeMatch = false, want true (code %.20q present in POA text)", g.Detail.ModelNo)
	}
	if m.CodeMatchSource != "both" {
		t.Errorf("CodeMatchSource = %q, want %q — Model_No and Vendor_Item_No are identical here", m.CodeMatchSource, "both")
	}
	if !m.IdentityConfident() {
		t.Errorf("IdentityConfident() = false despite CodeMatch; an exact code must settle identity regardless of DescScore")
	}
}

// TestMatchPOA_AmbiguousCodeMatch_NeitherConfident is section 5 point 3 (the
// ambiguity half): the same code appearing on two different BC items (a data
// error, but one CaseysItems cannot rule out) must not let either group claim
// confident identity — "ambiguity kills it" per phase1b-item-enrichment.md
// section 2.
func TestMatchPOA_AmbiguousCodeMatch_NeitherConfident(t *testing.T) {
	doc := Products{PF: "PF-CODE-AMBIG", Products: []OrderDetail{
		{Product: "Widget CODE123456 Extra Text", Qty: 1},
	}}
	bcLines := []businesscentral.PurchaseOrderLine{
		{ID: "bc-a", Sequence: 100, LineType: "Item", LineObjectNumber: "ITEM-A", DisplayName: "Something Else Entirely", Quantity: 1},
		{ID: "bc-b", Sequence: 200, LineType: "Item", LineObjectNumber: "ITEM-B", DisplayName: "Another Thing Altogether", Quantity: 1},
	}
	items := map[string]businesscentral.CaseysItem{
		"ITEM-A": {No: "ITEM-A", ModelNo: "CODE123456"},
		"ITEM-B": {No: "ITEM-B", ModelNo: "CODE123456"}, // same code on both -- must not be silently picked
	}
	mc := MatchContext{Items: items}

	matches, unmatched, _ := MatchPOA(doc, bcLines, mc)
	if len(unmatched) != 0 {
		t.Fatalf("unmatched = %d, want 0 (a low-confidence pairing is still made, just flagged)", len(unmatched))
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	m := matches[0]
	if m.CodeMatch {
		t.Errorf("CodeMatch = true; a code shared by two BC groups must not be confident for either")
	}
	if m.CodeAmbiguousWith == "" {
		t.Errorf("CodeAmbiguousWith is empty, want the other BC group named")
	}
	if m.IdentityConfident() {
		t.Errorf("IdentityConfident() = true for an ambiguous code match with no other identity signal")
	}
}

// TestLineMatch_CodeMatchWithPriceDelta_ConfidentButNotFullyVerified is
// section 5 point 4 — "the single most important test in the file". A code
// match settles IDENTITY only; it must never suppress a genuine price
// discrepancy. If this test ever fails because FullyVerified() went true,
// the v6 identity/verification separation has been silently broken.
func TestLineMatch_CodeMatchWithPriceDelta_ConfidentButNotFullyVerified(t *testing.T) {
	vocab := neutralVocab()
	d := OrderDetail{
		Product: "Sofa CODEXYZ9001 Something", Qty: 1,
		UnitProductPrice: 541, TotalProductQtyPrice: Ptr(541.0),
	}
	g := BCLineGroup{
		Item: businesscentral.PurchaseOrderLine{
			ID: "bc-price-mismatch", LineObjectNumber: "ITX", Quantity: 1,
			DirectUnitCost: 565, NetAmount: 565, // £24 more than the POA states
		},
		CombinedDesc: "Completely unrelated BC description text",
		Detail:       &businesscentral.CaseysItem{No: "ITX", ModelNo: "CODEXYZ9001"},
	}

	m := scorePair(d, combinedPOADesc(d), g, vocab, MatchContext{})
	if !m.CodeMatch {
		t.Fatalf("test setup invalid: expected CodeMatch, got false")
	}
	if !m.IdentityConfident() {
		t.Fatalf("IdentityConfident() = false; a code match must settle identity regardless of the price delta")
	}
	if m.NetOK {
		t.Fatalf("test setup invalid: NetOK = true, want false (deliberate £24 delta: POA 541 vs BC 565)")
	}
	if m.FullyVerified() {
		t.Errorf("FullyVerified() = true; a code match must NOT suppress a real price discrepancy — this is the v6 identity/verification separation")
	}
}

// TestScorePair_SeatCountMismatchFailsVerificationNotIdentity mirrors the
// price-delta guard above for the section 3 seat-count check: a wrong seat
// count is a verification failure, not an identity failure, and must not be
// masked by an otherwise-perfect description match.
func TestScorePair_SeatCountMismatchFailsVerificationNotIdentity(t *testing.T) {
	// Routed through MatchPOA (single POA line, single BC group) rather than
	// calling scorePair directly, so DescMargin is real: fillDescMargins sets
	// DescMarginNA true when a POA line has only one candidate, which
	// scorePair alone never computes (that's MatchPOA's job).
	doc := Products{PF: "PF-SEATS", Products: []OrderDetail{
		{Product: "Metro 3 Seater Digby Beige", Qty: 1},
	}}
	bcLines := []businesscentral.PurchaseOrderLine{
		{ID: "bc-seats", Sequence: 100, LineType: "Item", LineObjectNumber: "ITSEAT",
			DisplayName: "Metro 3 Seater Digby Beige", Quantity: 1}, // identical text: DescScore will be high
	}
	items := map[string]businesscentral.CaseysItem{"ITSEAT": {No: "ITSEAT", NoOfSeats: 2}}

	matches, unmatched, _ := MatchPOA(doc, bcLines, MatchContext{Items: items})
	if len(unmatched) != 0 || len(matches) != 1 {
		t.Fatalf("test setup invalid: matches=%d unmatched=%d, want 1 and 0", len(matches), len(unmatched))
	}
	m := matches[0]
	if m.DescScore < DescThreshold {
		t.Fatalf("test setup invalid: DescScore %.3f too low to isolate the seat check", m.DescScore)
	}
	if !m.IdentityConfident() {
		t.Fatalf("test setup invalid: IdentityConfident() = false (marginNA=%v margin=%.3f)", m.DescMarginNA, m.DescMargin)
	}
	if m.SeatsNA {
		t.Fatalf("test setup invalid: SeatsNA = true, want a real comparison (POA states 3, BC states 2)")
	}
	if m.SeatsOK {
		t.Errorf("SeatsOK = true, want false (POA states 3 seats, BC No_of_Seats is 2)")
	}
	if m.FullyVerified() {
		t.Errorf("FullyVerified() = true; a seat-count mismatch must fail verification even though identity is confident")
	}
}

// TestScorePair_SeatCountZeroOrImplausibleIsNA covers the note in
// phase1b-item-enrichment.md section 3: No_of_Seats came back 0.5 on a
// parasol, so 0 and other sub-1 values must be treated as "no real seat
// count on this item", not compared as if they were one.
func TestScorePair_SeatCountZeroOrImplausibleIsNA(t *testing.T) {
	vocab := neutralVocab()
	for _, bcSeats := range []float64{0, 0.5} {
		d := OrderDetail{Product: "3 Seater Widget", Qty: 1}
		g := BCLineGroup{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-na", Quantity: 1},
			CombinedDesc: "3 Seater Widget",
			Detail:       &businesscentral.CaseysItem{No: "ITNA", NoOfSeats: bcSeats},
		}
		m := scorePair(d, combinedPOADesc(d), g, vocab, MatchContext{})
		if !m.SeatsNA {
			t.Errorf("No_of_Seats=%.1f: SeatsNA = false, want true (implausible/zero BC seat count)", bcSeats)
		}
		if !m.SeatsOK {
			t.Errorf("No_of_Seats=%.1f: SeatsOK = false, want true (a check that cannot be performed passes)", bcSeats)
		}
	}
}
