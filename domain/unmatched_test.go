package domain

import (
	"strings"
	"testing"
)

// unmatchedOf returns the classification of POA line i, failing the test if
// the line was paired.
func unmatchedOf(t *testing.T, r Result, i int) UnmatchedLine {
	t.Helper()
	for _, l := range r.UnmatchedLines {
		if l.POAIndex == i {
			return l
		}
	}
	t.Fatalf("POA line %d is not unmatched:%s", i, describeRows(r))
	return UnmatchedLine{}
}

// barstool is PF131539's product, on both sides, agreeing to the penny.
func barstool() (POAProduct, BCProduct) {
	return product("M336 Slim Bar Stool Glasgow Sand Arizona Ice", 1, 245),
		bcProduct("bc-1", "IT1", "Slim Bar Stool Glasgow Sand Arizona Ice 65cm", 1, 245, 245)
}

// TestReconcile_ZeroValueNoteDoesNotBlock: PF131539's SEAT HEIGHT and rail
// lines are notes on the product. They carry no money and every ordered item
// is acknowledged, so they cannot change what is paid.
func TestReconcile_ZeroValueNoteDoesNotBlock(t *testing.T) {
	stool, bcStool := barstool()
	poa, bc := order(
		POAOrder{PF: "PF-NOTE", Products: []POAProduct{
			stool,
			{Code: "SH", Description: "SEAT HEIGHT = 65cm", Qty: 1, LineTotal: Ptr(0.0)},
			{Code: "-", Description: "-"},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{bcStool}},
	)

	r := Reconcile(poa, bc, cfg())

	if !r.WriteOK {
		t.Fatalf("WriteOK=false, want zero-value notes not to block:%s", describeRows(r))
	}
	if len(r.UnmatchedPOA) != 2 || len(r.UnmatchedLines) != 2 {
		t.Fatalf("unmatched POA %d / lines %d, want both notes still reported", len(r.UnmatchedPOA), len(r.UnmatchedLines))
	}
	for _, l := range r.UnmatchedLines {
		if l.Kind != UnmatchedNote || l.Blocks {
			t.Errorf("line %d: kind %q blocks %v, want a non-blocking note", l.POAIndex, l.Kind, l.Blocks)
		}
	}
}

// TestReconcile_ZeroValueLineBlocksWhileAnItemIsUnacknowledged: with an
// ordered item nobody acknowledged, a zero-value line may be that item
// printed without a price. (Had the quantities agreed, the assignment would
// have paired the two and the price check would have flagged it instead.)
func TestReconcile_ZeroValueLineBlocksWhileAnItemIsUnacknowledged(t *testing.T) {
	stool, bcStool := barstool()
	poa, bc := order(
		POAOrder{PF: "PF-OPEN", Products: []POAProduct{
			stool,
			{Code: "FS1", Description: "Footstool", Qty: 1},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcStool,
			bcProduct("bc-2", "IT2", "Ottoman Walnut Leg", 2, 120, 240),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true: a zero-value line must block while an ordered item is unacknowledged:%s", describeRows(r))
	}
	if l := unmatchedOf(t, r, 1); l.Kind != UnmatchedNote || !l.Blocks {
		t.Errorf("footstool: kind %q blocks %v, want a blocking note", l.Kind, l.Blocks)
	}
}

// TestReconcile_UnacknowledgedBCChargeDoesNotMakeANoteBlock: BC's Vendor
// Surcharges line is money, not an item a zero-value note could be, so the
// note itself does not block. The order still does: the surcharge leaves the
// total short (PF131540).
func TestReconcile_UnacknowledgedBCChargeDoesNotMakeANoteBlock(t *testing.T) {
	stool, bcStool := barstool()
	surcharge := bcProduct("bc-2", "", "Vendor Surcharges", 1, 11.03, 11.03)
	surcharge.LineType = "Account"
	poa, bc := order(
		POAOrder{PF: "PF-SURNOTE", Products: []POAProduct{
			stool,
			product("Customer Note QUICKSHIP ORDER", 1, 0),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{bcStool, surcharge}},
	)

	r := Reconcile(poa, bc, cfg())

	for _, l := range r.UnmatchedLines {
		if l.Blocks {
			t.Errorf("note %d blocks, want it harmless:%s", l.POAIndex, describeRows(r))
		}
	}
	if r.WriteOK {
		t.Errorf("WriteOK=true, want the unacknowledged surcharge to block through the total:%s", describeRows(r))
	}
}

// TestReconcile_SupplierChargesAreNamedAndBlock is PF131539: the product
// agrees, and the supplier adds three charges the order does not hold. They
// are money, so the order still goes to a person — but named as charges, with
// the stated 5% checked and found not to agree.
func TestReconcile_SupplierChargesAreNamedAndBlock(t *testing.T) {
	stool, bcStool := barstool()
	poa, bc := order(
		POAOrder{PF: "PF131539", TotalExVAT: Ptr(371.0), Products: []POAProduct{
			stool,
			{Code: "SH", Description: "SEAT HEIGHT = 65cm", Qty: 1, LineTotal: Ptr(0.0)},
			{Code: "Port2", Description: "5% Surcharge delivery to Ireland", Qty: 1, UnitPrice: 13},
			{Code: "OP", Description: "5% Operational cost adjustment surcharge", Qty: 1, UnitPrice: 13},
			{Code: "SUR", Description: "Surcharge delivery 1/5 items", Qty: 1, UnitPrice: 100},
		}},
		BCOrder{VendorNo: "VX1", TotalExVAT: 245, Enrichment: EnrichmentApplied, Products: []BCProduct{bcStool}},
	)

	r := Reconcile(poa, bc, cfg())

	if r.WriteOK {
		t.Fatalf("WriteOK=true: charges the order does not hold must never pass silently")
	}
	if len(r.Products) != 1 || r.Products[0].OK() == false {
		t.Fatalf("want the bar stool paired and clean:%s", describeRows(r))
	}
	for _, i := range []int{2, 3, 4} {
		if l := unmatchedOf(t, r, i); l.Kind != UnmatchedCharge || !l.Blocks {
			t.Errorf("line %d: kind %q blocks %v, want a blocking charge", i, l.Kind, l.Blocks)
		}
	}
	if l := unmatchedOf(t, r, 1); l.Blocks {
		t.Errorf("SEAT HEIGHT should not block")
	}
	port2 := strings.Join(unmatchedOf(t, r, 2).Evidence, "; ")
	if !strings.Contains(port2, "is 12.25, not 13.00") {
		t.Errorf("Port2 evidence should check the stated 5%% against 245.00: %s", port2)
	}
	if !strings.Contains(r.Totals.Message, "+126.00 is 3 supplier charge line(s)") {
		t.Errorf("totals message should attribute the difference to the charges: %s", r.Totals.Message)
	}
}

// TestReconcile_StatedPercentageThatAgreesIsRecorded: a 4.5% surcharge that is
// exactly 4.5% of the goods is evidence worth showing — and still blocks.
func TestReconcile_StatedPercentageThatAgreesIsRecorded(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-PCT", Products: []POAProduct{
			product("Hansson 3 Seater Sofa Conifer", 1, 1000),
			{Description: "4.5% temporary surcharge", Qty: 1, UnitPrice: 45},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Hansson 3 Seater Sofa Conifer", 1, 1000, 1000),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	l := unmatchedOf(t, r, 1)
	if l.Kind != UnmatchedCharge || !l.Blocks {
		t.Errorf("kind %q blocks %v, want a blocking charge", l.Kind, l.Blocks)
	}
	if ev := strings.Join(l.Evidence, "; "); !strings.Contains(ev, "is 45.00 — agrees") {
		t.Errorf("evidence should record the percentage agreeing: %s", ev)
	}
	if r.WriteOK {
		t.Errorf("WriteOK=true: an agreeing percentage is evidence, not approval")
	}
}

// TestReconcile_UnpairedProductIsNotCalledACharge: PF130884's Mateo 1 Arm
// Small Sofa resembles BC's sofa; it is a product the order does not hold, and
// no amount or wording makes it a charge.
func TestReconcile_UnpairedProductIsNotCalledACharge(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-MATEO", Products: []POAProduct{
			product("Mateo Sofa Dusky Natural Large", 1, 900),
			product("Mateo Sofa Dusky Natural Small", 1, 608),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Mateo Sofa Dusky Natural Large", 1, 900, 900),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if l := unmatchedOf(t, r, 1); l.Kind != UnmatchedProduct || !l.Blocks {
		t.Errorf("kind %q blocks %v, want a blocking product", l.Kind, l.Blocks)
	}
	if r.WriteOK {
		t.Errorf("WriteOK=true with an unpaired product")
	}
}

// TestReconcile_MisreadLineIsNotCalledACharge: PF131507's Rowen Footstool came
// through at qty 0 with 246.00 printed. Its figures disagree; it is a product.
func TestReconcile_MisreadLineIsNotCalledACharge(t *testing.T) {
	stool, bcStool := barstool()
	poa, bc := order(
		POAOrder{PF: "PF-ROWEN", Products: []POAProduct{
			stool,
			{Code: "Rowen Footstool", Qty: 0, UnitPrice: 246, LineTotal: Ptr(246.0)},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{bcStool}},
	)

	r := Reconcile(poa, bc, cfg())

	if l := unmatchedOf(t, r, 1); l.Kind != UnmatchedProduct || !l.Blocks {
		t.Errorf("kind %q blocks %v, want a blocking product", l.Kind, l.Blocks)
	}
}
