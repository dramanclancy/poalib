package domain

import (
	"strings"
	"testing"
)

// line is a POA product with its text in Description, as most suppliers
// print it.
func line(desc string, qty int, unit float64) POAProduct {
	return POAProduct{Description: desc, Qty: qty, UnitPrice: unit}
}

// groupOf returns the group every listed row belongs to, failing the test if
// any of them is ungrouped or they disagree.
func groupOf(t *testing.T, r Result, rows ...int) LineGroup {
	t.Helper()
	if len(r.Products) <= rows[len(rows)-1] {
		t.Fatalf("only %d rows, want at least %d", len(r.Products), rows[len(rows)-1]+1)
	}
	g := r.Products[rows[0]].Group
	if g == nil {
		t.Fatalf("row %d is not grouped", rows[0])
	}
	for _, n := range rows[1:] {
		if r.Products[n].Group != g {
			t.Fatalf("rows %v are not in one group", rows)
		}
	}
	return *g
}

func describeRows(r Result) string {
	var b strings.Builder
	for _, p := range r.Products {
		b.WriteString("\n  ")
		b.WriteString(p.POA.Text())
		b.WriteString(" -> ")
		b.WriteString(p.BC.Label())
		for _, d := range p.Discrepancies() {
			b.WriteString(" [" + d.Message + "]")
		}
	}
	return b.String()
}

// TestReconcile_AggregateSum is PF131114: the supplier prints one line per
// chair, BC holds one line for both. One-to-one, this reported a quantity
// mismatch, a price mismatch and an unmatched line on an order that agreed to
// the penny.
func TestReconcile_AggregateSum(t *testing.T) {
	chair := "Penshurst Wing Chair Noah Denim Mahogany Leg"
	poa, bc := order(
		POAOrder{PF: "PF-AGG", Products: []POAProduct{
			{Code: chair, Description2: "S21210", Qty: 1, UnitPrice: 700, DiscountAmount: 122.5},
			{Description: chair, Description2: "S21210 " + chair, Qty: 1, UnitPrice: 700, DiscountAmount: 122.5},
			line("Manhattan 2 Seater Power Plus Milo Denim", 1, 1512.23),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Penshurst Wing Chair Noah Denim Mahogany", 2, 577.5, 1155),
			bcProduct("bc-2", "IT2", "Manhattan 2 Seater Power Plus Milo Denim", 1, 1512.23, 1512.23),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK || len(r.UnmatchedPOA) != 0 || len(r.UnmatchedBC) != 0 {
		t.Fatalf("WriteOK=%v unmatched POA %d BC %d, want a clean order:%s",
			r.WriteOK, len(r.UnmatchedPOA), len(r.UnmatchedBC), describeRows(r))
	}
	g := groupOf(t, r, 0, 1)
	if g.Kind != GroupAggregate || g.Rule != RuleSum || g.Evidence != EvidenceIdentity {
		t.Errorf("group = %s, want aggregate / sum / identity", g)
	}
	if r.Products[0].BC.LineID != "bc-1" || r.Products[1].BC.LineID != "bc-1" {
		t.Errorf("both chair lines should pair with bc-1")
	}
	if q := r.Products[0].Quantity; q.POAQty != 2 || q.BCQty != 2 || q.Rule != RuleSum {
		t.Errorf("quantity = POA %g / BC %g / %q, want the combined 2 / 2 / sum", q.POAQty, q.BCQty, q.Rule)
	}
	if p := r.Products[1].Price; p.POAPrice != 1155 || p.BCPrice != 1155 {
		t.Errorf("price = POA %g / BC %g, want the combined 1155 on both sides", p.POAPrice, p.BCPrice)
	}
	if r.Products[2].Group != nil {
		t.Errorf("the sofa is an ordinary pair and must not be grouped")
	}
	if r.Totals.MatchedTotal != bc.TotalExVAT+1155+1512.23 {
		t.Errorf("matched total = %.2f, want each BC product counted once (2667.23)", r.Totals.MatchedTotal)
	}
}

// TestReconcile_SplitSum is the mirror: BC carries the same item twice where
// the supplier prints it once with qty 2 (PF130543's shape).
func TestReconcile_SplitSum(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-SPLIT", Products: []POAProduct{
			line("Toronto Armless Unit Dusky Natural", 2, 390),
			line("Toronto Corner Box Dusky Natural", 1, 451),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Toronto Armless Unit Dusky Natural", 1, 390, 390),
			bcProduct("bc-2", "IT2", "Toronto Corner Box Dusky Natural", 1, 451, 451),
			bcProduct("bc-3", "IT1", "Toronto Armless Unit Dusky Natural", 1, 390, 390),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK || len(r.UnmatchedBC) != 0 {
		t.Fatalf("WriteOK=%v unmatched BC %d, want a clean order:%s", r.WriteOK, len(r.UnmatchedBC), describeRows(r))
	}
	g := groupOf(t, r, 0, 1)
	if g.Kind != GroupSplit || g.Rule != RuleSum || len(g.BCIndexes) != 2 {
		t.Errorf("group = %s, want one POA line split over two BC lines", g)
	}
	// The two BC lines are the same product, not rivals: a margin measured
	// between them would call the pairing ambiguous.
	for _, p := range r.Products[:2] {
		if p.Description.Margin != nil && *p.Description.Margin < cfg().MarginThreshold {
			t.Errorf("row %s scored margin %.2f against a fellow group member", p.BC.LineID, *p.Description.Margin)
		}
	}
}

// TestReconcile_AggregateParts: the supplier prints a product as its parts,
// each carrying BC's quantity, priced so they add up to BC's unit — two 90cm
// divan halves for one 180cm base (PF131012).
func TestReconcile_AggregateParts(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-PARTS", Products: []POAProduct{
			line("Padded Top Divan Drawer Base Oxford Sand", 1, 245.44),
			line("Padded Top Divan Drawer Base Oxford Sand", 1, 245.44),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Padded Top Divan Drawer Base Oxford Sand 180cm", 1, 490.88, 490.88),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK {
		t.Fatalf("WriteOK=false, want two priced halves of one base to pass:%s", describeRows(r))
	}
	g := groupOf(t, r, 0, 1)
	if g.Kind != GroupAggregate || g.Rule != RuleParts {
		t.Errorf("group = %s, want aggregate / parts", g)
	}
}

// TestReconcile_SplitParts: one acknowledgement line for what BC orders as
// distinct parts — A on one side, A + B on the other.
func TestReconcile_SplitParts(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-SPLITPARTS", Products: []POAProduct{
			line("Hansson Corner Sofa Conifer", 1, 1000),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Hansson Corner Sofa Conifer Chaise Section", 1, 600, 600),
			bcProduct("bc-2", "IT2", "Hansson Corner Sofa Conifer Corner Section", 1, 400, 400),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK || len(r.UnmatchedBC) != 0 {
		t.Fatalf("WriteOK=%v unmatched BC %d, want the two sections to make up the sofa:%s",
			r.WriteOK, len(r.UnmatchedBC), describeRows(r))
	}
	if g := groupOf(t, r, 0, 1); g.Kind != GroupSplit || g.Rule != RuleParts {
		t.Errorf("group = %s, want split / parts", g)
	}
}

// TestReconcile_PartsWithWeakDescriptionsAreFlaggedNotApproved: the money
// corroborates the layout, so the lines are grouped — the report then says
// "these two are that base" instead of "net differs by -245.44, one line
// unmatched" — but descriptions too weak to identify the product still block.
// A group changes what the flag says, never whether identity was established.
func TestReconcile_PartsWithWeakDescriptionsAreFlaggedNotApproved(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-WEAK", Products: []POAProduct{
			line("IN20 Conti Stor LINK", 1, 245.44),
			line("IN20 Conti Stor LINK", 1, 245.44),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Padded Top Deep 4 Drawer Base 180cm", 1, 490.88, 490.88),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true: unidentified lines must never be approved because the money agreed")
	}
	if len(r.UnmatchedPOA) != 0 {
		t.Fatalf("unmatched POA = %d, want both halves grouped onto the base", len(r.UnmatchedPOA))
	}
	if g := groupOf(t, r, 0, 1); g.Evidence != EvidenceMoney {
		t.Errorf("group = %s, want evidence money", g)
	}
	for _, p := range r.Products {
		if !p.Description.Failed() {
			t.Errorf("row %d description = %q, want the weak description still flagged", p.POAIndex, p.Description.Status)
		}
		if p.Price.Failed() || p.Quantity.Failed() {
			t.Errorf("row %d: the combined money and quantity agree and must not be flagged", p.POAIndex)
		}
	}
}

// TestReconcile_DuplicateAcknowledgementStaysUnmatched: qty 1 printed twice
// against BC's qty 1 is a duplicate, not a split — neither the quantities nor
// the unit prices add up — and the pair that agrees is never broken up to
// explain it away.
func TestReconcile_DuplicateAcknowledgementStaysUnmatched(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-DUP", Products: []POAProduct{
			line("Felix Chair Marni Olive", 1, 376),
			line("Felix Chair Marni Olive", 1, 376),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Felix Chair Marni Olive", 1, 376, 376),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK || len(r.UnmatchedPOA) != 1 {
		t.Fatalf("WriteOK=%v unmatched POA %d, want the duplicate reported unmatched", r.WriteOK, len(r.UnmatchedPOA))
	}
	if len(r.Products) != 1 || r.Products[0].Group != nil || !r.Products[0].OK() {
		t.Errorf("want the one agreeing pair left exactly as it was:%s", describeRows(r))
	}
}

// TestReconcile_SimilarButDifferentProductIsCaughtByMoney: a footstool
// described almost exactly like the chair it goes with can join the chair's
// group on description and quantity — but money is checked on the group, so
// the order is still flagged, with the combined figures that show why.
func TestReconcile_SimilarButDifferentProductIsCaughtByMoney(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-SIMILAR", Products: []POAProduct{
			line("Penshurst Wing Chair Noah Denim", 1, 577.5),
			line("Penshurst Wing Chair Footstool Noah Denim", 1, 200),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Penshurst Wing Chair Noah Denim", 2, 577.5, 1155),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true: a chair and a footstool are not two chairs:%s", describeRows(r))
	}
	for _, p := range r.Products {
		if !p.Price.Failed() {
			t.Errorf("row %d price = %q, want the combined 777.50 vs 1155.00 flagged", p.POAIndex, p.Price.Status)
		}
	}
}

// TestReconcile_UnrelatedLineDoesNotJoin: a carriage line has no identity with
// the product and its money does not complete it, so it is not pulled in to
// make the quantities add up.
func TestReconcile_UnrelatedLineDoesNotJoin(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-CARRIAGE", Products: []POAProduct{
			line("Felix Chair Marni Olive", 1, 376),
			line("Carriage Charge", 1, 12),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Felix Chair Marni Olive", 2, 376, 752),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true, want the order flagged")
	}
	for _, p := range r.Products {
		if p.Group != nil {
			t.Errorf("formed group %s from a chair and a carriage charge", p.Group)
		}
	}
	if len(r.UnmatchedPOA) != 1 || r.Products[0].Quantity.Status != StatusMismatch {
		t.Errorf("want the chair's quantity mismatch and the unmatched carriage line, as before:%s", describeRows(r))
	}
}

// TestReconcile_GroupPriceDifferenceIsReported: identity and quantity decide
// the group; money is verified on it. "Matched, but the supplier's combined
// figure differs" must stay a sentence the engine can say — that is the rule
// that keeps price out of the pairing score.
func TestReconcile_GroupPriceDifferenceIsReported(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-GROUPPRICE", Products: []POAProduct{
			line("Eastbury Large 2 Seater Ragley Stripe", 1, 820),
			line("Eastbury Large 2 Seater Ragley Stripe", 1, 820),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Eastbury Large 2 Seater Ragley Stripe", 2, 838.5, 1677),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK || len(r.UnmatchedPOA) != 0 {
		t.Fatalf("WriteOK=%v unmatched POA %d, want both lines grouped and the price flagged", r.WriteOK, len(r.UnmatchedPOA))
	}
	groupOf(t, r, 0, 1)
	p := r.Products[0]
	if !p.Price.Failed() || p.Price.Difference != 1640-1677 {
		t.Errorf("price = %q diff %.2f, want mismatch at -37.00", p.Price.Status, p.Price.Difference)
	}
	if p.Quantity.Failed() || p.Description.Failed() {
		t.Errorf("quantity %q / description %q, want both to agree: only the money differs",
			p.Quantity.Status, p.Description.Status)
	}
}

// TestReconcile_GroupReclaimsLineFromAForcedPair: the assignment never leaves
// a POA line unmatched while a BC product is free, so the second chair is
// forced onto the surcharge line. It resembles the chair line clearly more,
// so it moves into the chair's group and the surcharge is reported as not
// acknowledged — which is what happened.
func TestReconcile_GroupReclaimsLineFromAForcedPair(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-REHOME", Products: []POAProduct{
			line("Felix Chair Marni Olive", 1, 376),
			line("Felix Chair Marni Olive", 1, 376),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Felix Chair Marni Olive", 2, 376, 752),
			{LineID: "bc-2", ItemNo: "718000", LineType: "Account", Description: "Vendor Surcharges", Qty: 1, UnitCost: 20, Net: 20},
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK {
		t.Fatalf("WriteOK=false, want both chairs grouped onto bc-1:%s", describeRows(r))
	}
	if len(r.UnmatchedBC) != 1 || r.UnmatchedBC[0].LineID != "bc-2" {
		t.Errorf("want the surcharge reported as not acknowledged")
	}
}

// TestReconcile_GroupMembersAreCheckedForOrientation: grouping never relaxes
// a per-line check. A part with the wrong hand fails on its own row.
func TestReconcile_GroupMembersAreCheckedForOrientation(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-HAND", Products: []POAProduct{
			line("Mateo 1 Arm Chaise Unit LHF Dusk Natural", 1, 591),
			line("Mateo 1 Arm Small Sofa RHF Dusk Natural", 1, 587),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{{
			LineID: "bc-1", ItemNo: "IT1", LineType: "Item",
			Description:     "Mateo Small Chaise Dusk Natural Left Hand Facing",
			LineDescription: "Small Chaise", Comments: []string{"Left Hand Facing"},
			Qty: 1, UnitCost: 1178, Net: 1178,
		}}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true, want the right-handed part flagged against a left-handed order")
	}
	groupOf(t, r, 0, 1)
	if r.Products[0].Orientation.Failed() || !r.Products[1].Orientation.Failed() {
		t.Errorf("orientation = %q / %q, want the LHF part to agree and the RHF part to fail",
			r.Products[0].Orientation.Status, r.Products[1].Orientation.Status)
	}
}
