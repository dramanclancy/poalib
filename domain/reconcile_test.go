package domain

import (
	"hash/fnv"
	"strings"
	"testing"
)

// embed is a deterministic, network-free stand-in for a real embedding: each
// text becomes a small bag-of-words vector, so identical text scores 1 and
// text sharing no words scores ~0. Real embeddings behave the same way at the
// extremes, which is all these tests need.
func embed(s string) []float32 {
	v := make([]float32, 64)
	for _, tok := range strings.Fields(normalize(EmbeddingText(s))) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		v[h.Sum32()%64]++
	}
	return v
}

// order builds both sides with embeddings attached, the way the pipeline does
// before calling Reconcile.
func order(poa POAOrder, bc BCOrder) (POAOrder, BCOrder) {
	for i := range poa.Products {
		poa.Products[i].Embedding = embed(poa.Products[i].Text())
	}
	for i := range bc.Products {
		bc.Products[i].Embedding = embed(bc.Products[i].Description)
	}
	return poa, bc
}

func product(code string, qty int, unit float64) POAProduct {
	return POAProduct{Code: code, Qty: qty, UnitPrice: unit}
}

func bcProduct(id, itemNo, desc string, qty, unit, net float64) BCProduct {
	return BCProduct{
		LineID: id, ItemNo: itemNo, LineType: "Item", Description: desc,
		DisplayName: desc, Qty: qty, UnitCost: unit, Net: net,
	}
}

// TestReconcile_PairsEveryLineAndGatesTheWrite: two lines, one clean and one
// with a price difference. Both pair; the order does not pass the gate.
func TestReconcile_PairsEveryLineAndGatesTheWrite(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-GATE", Products: []POAProduct{
			product("Metro 3 Seater Digby Beige", 1, 500),
			product("Metro Storage Footstool Digby Beige", 1, 200),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Metro 3 Seater Digby Beige", 1, 500, 500),
			bcProduct("bc-2", "IT2", "Metro Storage Footstool Digby Beige", 1, 220, 220),
		}},
	)

	r := Reconcile(poa, bc, cfg())

	if len(r.Products) != 2 || len(r.UnmatchedPOA) != 0 || len(r.UnmatchedBC) != 0 {
		t.Fatalf("matched=%d unmatchedPOA=%d unmatchedBC=%d, want 2/0/0",
			len(r.Products), len(r.UnmatchedPOA), len(r.UnmatchedBC))
	}
	// Results come back in POA line order, so a report row and a POA line line
	// up without the reader checking an index.
	if r.Products[0].POAIndex != 0 || r.Products[1].POAIndex != 1 {
		t.Errorf("POA indexes = %d,%d, want 0,1", r.Products[0].POAIndex, r.Products[1].POAIndex)
	}
	if r.Products[0].BC.ItemNo != "IT1" || r.Products[1].BC.ItemNo != "IT2" {
		t.Errorf("paired with %q and %q, want IT1 and IT2", r.Products[0].BC.ItemNo, r.Products[1].BC.ItemNo)
	}
	if !r.Products[0].OK() {
		t.Errorf("first line should be clean, got %v", Kinds(r.Products[0].Discrepancies()))
	}
	if !r.Products[1].Price.Failed() {
		t.Errorf("second line: price = %q, want mismatch (POA 200 vs BC 220)", r.Products[1].Price.Status)
	}
	if r.WriteOK {
		t.Error("WriteOK = true with a £20 price difference on the order")
	}

	lines := r.ReviewLines()
	if len(lines) != 1 {
		t.Fatalf("ReviewLines() = %d, want 1", len(lines))
	}
	if lines[0].PF != "PF-GATE" || lines[0].BCItemNo != "IT2" || lines[0].BCLineID != "bc-2" {
		t.Errorf("review row = %+v, want it traceable to PF-GATE / IT2", lines[0])
	}
	if lines[0].POANet != 200 || lines[0].BCNet != 220 {
		t.Errorf("nets = %g/%g, want 200/220 — a consumer must never recompute these", lines[0].POANet, lines[0].BCNet)
	}
}

// TestReconcile_MarginIgnoresCandidatesTakenByAnotherLine is the fix for what
// the 2026-09-04 batch showed on PF130365: a line reported as ambiguous
// against a BC product that a different POA line had already been paired with,
// producing a NEGATIVE margin. That contest never happened.
func TestReconcile_MarginIgnoresCandidatesTakenByAnotherLine(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-MARGIN", Products: []POAProduct{
			product("NOVA 3 seater sofa MARNI LATTE", 1, 659),
			product("NOVA Cuddler MARNI LATTE", 1, 497),
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT3SEAT", "NOVA 3 seater sofa MARNI LATTE", 1, 659, 659),
			bcProduct("bc-2", "ITCUDDLE", "Cuddler", 1, 497, 497),
		}},
	)

	r := Reconcile(poa, bc, cfg())
	if len(r.Products) != 2 {
		t.Fatalf("matched %d lines, want 2", len(r.Products))
	}

	// The cuddler line is the weak one: it resembles the 3-seater's text more
	// than its own BC line. That product is taken, so it is not an
	// alternative, and there is nothing else left.
	cuddler := r.Products[1]
	if cuddler.BC.ItemNo != "ITCUDDLE" {
		t.Fatalf("second line paired with %q, want ITCUDDLE", cuddler.BC.ItemNo)
	}
	if cuddler.Description.Margin != nil {
		t.Errorf("margin = %g against runner-up %q; every alternative was already assigned, so there was no contest to report",
			*cuddler.Description.Margin, cuddler.Description.RunnerUp)
	}
	for _, d := range cuddler.Discrepancies() {
		if d.Kind == DiscAmbiguousPairing {
			t.Errorf("reported as ambiguous: %q", d.Message)
		}
	}
}

// TestReconcile_UnacknowledgedBCLineIsReportedNotGated pins the write gate's
// documented blind spot: a BC line the supplier never acknowledged is
// reported, but does not by itself stop the order auto-approving. Changing
// this changes which orders auto-approve and is a deliberate decision, not a
// tidy-up — so it must fail loudly if changed by accident.
func TestReconcile_UnacknowledgedBCLineIsReportedNotGated(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-PARTIAL", Products: []POAProduct{
			{Code: "Metro 3 Seater Digby Beige", Qty: 1, UnitPrice: 500, LineTotal: Ptr(500.0)},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			bcProduct("bc-1", "IT1", "Metro 3 Seater Digby Beige", 1, 500, 500),
			{LineID: "bc-2", ItemNo: "718000", LineType: "Account", Sequence: 200,
				Description: "Vendor Surcharges", LineDescription: "Vendor Surcharges",
				Qty: 1, UnitCost: 21.85, Net: 21.85},
		}},
	)

	r := Reconcile(poa, bc, cfg())
	if len(r.UnmatchedBC) != 1 || r.UnmatchedBC[0].ItemNo != "718000" {
		t.Fatalf("unmatchedBC = %d, want the surcharge line reported", len(r.UnmatchedBC))
	}
	if !r.WriteOK {
		t.Error("WriteOK = false; the gate does not consider unacknowledged BC lines — if that changed deliberately, update this test and the architecture doc")
	}
}

// TestReconcile_CodeMatchDoesNotSuppressAPriceDifference is the most important
// test in the package. A code match settles IDENTITY only. If this fails
// because OK() went true, the identity/verification separation is broken.
func TestReconcile_CodeMatchDoesNotSuppressAPriceDifference(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-CODE", Products: []POAProduct{
			{Code: "Sofa CODEXYZ9001 Something", Qty: 1, UnitPrice: 541, LineTotal: Ptr(541.0)},
		}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			{LineID: "bc-1", ItemNo: "ITX", LineType: "Item",
				Description: "Completely unrelated BC description text",
				Qty:         1, UnitCost: 565, Net: 565,
				Detail: &ItemDetail{ItemNo: "ITX", ModelNo: "CODEXYZ9001"}},
		}},
	)

	r := Reconcile(poa, bc, cfg())
	p := r.Products[0]
	if !p.Description.CodeMatch {
		t.Fatalf("test setup invalid: no code match")
	}
	if p.Description.Failed() {
		t.Error("description check failed; a code match settles identity regardless of the price delta")
	}
	if !p.Price.Failed() {
		t.Fatalf("test setup invalid: price check passed on a £24 difference")
	}
	if p.OK() {
		t.Error("OK() = true; a code match must NOT suppress a real price discrepancy")
	}
}

// TestReconcile_SeatMismatchFailsVerificationNotIdentity mirrors the price
// guard for the seat check, and only works now that enrichment state and seat
// counts survive into the domain.
func TestReconcile_SeatMismatchFailsVerificationNotIdentity(t *testing.T) {
	poa, bc := order(
		POAOrder{PF: "PF-SEATS", Products: []POAProduct{product("Metro 3 Seater Digby Beige", 1, 500)}},
		BCOrder{VendorNo: "VX1", Enrichment: EnrichmentApplied, Products: []BCProduct{
			{LineID: "bc-1", ItemNo: "ITSEAT", LineType: "Item",
				Description: "Metro 3 Seater Digby Beige", DisplayName: "Metro 3 Seater Digby Beige",
				Qty: 1, UnitCost: 500, Net: 500,
				Detail: &ItemDetail{ItemNo: "ITSEAT", Seats: Ptr(2.0)}},
		}},
	)

	r := Reconcile(poa, bc, cfg())
	p := r.Products[0]
	if p.Description.Failed() {
		t.Fatalf("test setup invalid: description check failed (%s)", p.Description.Message)
	}
	if p.Seats.DataState != SeatsComparable {
		t.Fatalf("seat check did not run: %s (%s)", p.Seats.DataState, p.Seats.Message)
	}
	if !p.Seats.Failed() {
		t.Error("seat check passed: POA states 3 seats, BC records 2")
	}
	if p.OK() {
		t.Error("OK() = true; a seat mismatch must block even when identity is confident")
	}
}

// TestReconcile_ConfigTravelsWithTheResult: a workbook or fixture is never
// ambiguous about which thresholds produced it.
func TestReconcile_ConfigTravelsWithTheResult(t *testing.T) {
	c := cfg()
	c.DescThreshold = 0.42
	poa, bc := order(
		POAOrder{PF: "PF-CFG", Products: []POAProduct{product("Widget", 1, 10)}},
		BCOrder{Enrichment: EnrichmentApplied, Products: []BCProduct{bcProduct("bc-1", "IT1", "Widget", 1, 10, 10)}},
	)
	r := Reconcile(poa, bc, c)
	if r.Config.DescThreshold != 0.42 {
		t.Errorf("Config.DescThreshold = %g, want the value the run used", r.Config.DescThreshold)
	}
	if r.Products[0].Description.Threshold != 0.42 {
		t.Errorf("the check reported threshold %g, want 0.42", r.Products[0].Description.Threshold)
	}
}

// TestReconcile_PriceNeverDecidesPairingOnLargeOrders: above ExhaustiveLimit
// the greedy assignment used to take pairs that passed every check first, so a
// weaker description match whose price happened to agree beat the right line
// — and "matched, but the price differs" became an unexpressible verdict on
// exactly the orders big enough to need it. Both assignment paths must choose
// the same pair, by identity alone.
func TestReconcile_PriceNeverDecidesPairingOnLargeOrders(t *testing.T) {
	poa := POAOrder{PF: "PF-GREEDY", Products: []POAProduct{product("Metro 3 Seater", 1, 500)}}
	poa.Products[0].Embedding = []float32{1, 0}
	bc := BCOrder{Enrichment: EnrichmentApplied, Products: []BCProduct{
		bcProduct("bc-1", "IT1", "Metro 3 Seater", 1, 450, 450), // identical text, wrong price
		bcProduct("bc-2", "IT2", "Metro 3 Seater", 1, 500, 500), // weaker text, right price
	}}
	bc.Products[0].Embedding = []float32{1, 0}     // similarity 1.0
	bc.Products[1].Embedding = []float32{0.8, 0.6} // similarity 0.8

	for _, limit := range []int{8, 1} { // exhaustive, then greedy
		c := cfg()
		c.ExhaustiveLimit = limit
		// Low enough that the weaker line passes the description check on its
		// own, which is what let the old greedy order prefer it.
		c.MarginThreshold = -1

		r := Reconcile(poa, bc, c)
		if len(r.Products) != 1 {
			t.Fatalf("limit %d: %d pairs, want 1", limit, len(r.Products))
		}
		if got := r.Products[0].BCIndex; got != 0 {
			t.Errorf("limit %d: paired with BC line %d, want 0 — the price chose the pair", limit, got)
		}
		if !r.Products[0].Price.Failed() {
			t.Errorf("limit %d: the price difference was not reported", limit)
		}
	}
}
