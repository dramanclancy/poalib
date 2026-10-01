package domain

import (
	"strings"
	"testing"
)

func cfg() Config { return DefaultConfig() }

// bcItem is a BC product with an item record attached.
func bcItem(itemNo string, qty, net float64, detail *ItemDetail) BCProduct {
	return BCProduct{
		LineID: "bc-" + itemNo, ItemNo: itemNo, LineType: "Item",
		Description: "2 Seater Sofa", Qty: qty, Net: net, Detail: detail,
	}
}

// bcItemSilentOnSeats is bcItem with a description that states no seat count.
// bcItem's default ("2 Seater Sofa") would be read by the description
// fallback, so a case that means to exercise an ITEM-RECORD gap has to use a
// product whose text cannot answer either.
func bcItemSilentOnSeats(itemNo string, detail *ItemDetail) BCProduct {
	p := bcItem(itemNo, 1, 100, detail)
	p.Description = "Display Cabinet"
	return p
}

// ---------------------------------------------------------------------------
// Seat count — the check that never ran across thirty real lines
// ---------------------------------------------------------------------------

// TestSeatsCheck_DistinguishesWhyItCannotCompare is the fix. Every one of
// these comes back "unknown" and passes, but a reviewer is told a different
// thing in each case, because only one of them is a master-data problem
// somebody should act on.
func TestSeatsCheck_DistinguishesWhyItCannotCompare(t *testing.T) {
	poa := POAProduct{Code: "Metro 3 Seater", Qty: 1}

	tests := []struct {
		name  string
		bc    BCProduct
		state EnrichmentState
		want  string
	}{
		{
			name:  "enrichment never ran",
			bc:    bcItemSilentOnSeats("IT1", nil),
			state: EnrichmentNotRequested,
			want:  SeatsEnrichmentOff,
		},
		{
			name:  "enrichment failed",
			bc:    bcItemSilentOnSeats("IT1", nil),
			state: EnrichmentFailed,
			want:  SeatsEnrichmentFailed,
		},
		{
			name:  "enrichment ran but holds no record for this item",
			bc:    bcItemSilentOnSeats("IT1", nil),
			state: EnrichmentApplied,
			want:  SeatsNoItemRecord,
		},
		{
			name:  "item record carries no seat count",
			bc:    bcItemSilentOnSeats("IT1", &ItemDetail{ItemNo: "IT1"}),
			state: EnrichmentApplied,
			want:  SeatsNotOnItem,
		},
		{
			name:  "item seat count is not a real seat count",
			bc:    bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(0.5)}),
			state: EnrichmentApplied,
			want:  SeatsImplausible,
		},
		{
			name:  "the POA states no seat count",
			bc:    bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(3.0)}),
			state: EnrichmentApplied,
			want:  SeatsNotOnPOA,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := poa
			if tc.want == SeatsNotOnPOA {
				p = POAProduct{Code: "Metro Storage Footstool", Qty: 1}
			}
			got := seatsCheck(p, tc.bc, tc.state, cfg())
			if got.DataState != tc.want {
				t.Errorf("DataState = %q, want %q (%s)", got.DataState, tc.want, got.Message)
			}
			if got.Status != StatusUnknown {
				t.Errorf("status = %q, want unknown", got.Status)
			}
			if got.Failed() {
				t.Error("check failed; one that cannot be performed must pass")
			}
			if got.Message == "" {
				t.Error("no message: a reviewer is told nothing about why")
			}
		})
	}
}

// TestSeatsCheck_NeverBlamesBCForAMissingLookup is the specific wrong sentence
// this rewrite removes. When enrichment did not run, the report must not claim
// BC holds no seat count — that sends someone to inspect a field that may be
// perfectly populated.
func TestSeatsCheck_NeverBlamesBCForAMissingLookup(t *testing.T) {
	got := seatsCheck(POAProduct{Code: "Metro 3 Seater"}, bcItemSilentOnSeats("IT0188674", nil), EnrichmentNotRequested, cfg())
	if got.Message != "item enrichment was not run for this order, so BC's seat count is unknown" {
		t.Errorf("message = %q; it must name the lookup, not BC's data", got.Message)
	}
	if got.BCSeats != nil {
		t.Error("BCSeats is set; nothing is known about BC's seat count here")
	}
}

// TestSeatsCheck_ComparesWhenBothSidesHaveOne is the case that should have
// been firing all along.
func TestSeatsCheck_ComparesWhenBothSidesHaveOne(t *testing.T) {
	poa := POAProduct{Code: "Metro 3 Seater Digby Beige", Qty: 1}

	agree := seatsCheck(poa, bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(3.0)}), EnrichmentApplied, cfg())
	if agree.Status != StatusMatch || agree.DataState != SeatsComparable {
		t.Errorf("3 vs 3: status %q state %q, want match/comparable", agree.Status, agree.DataState)
	}

	differ := seatsCheck(poa, bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(2.0)}), EnrichmentApplied, cfg())
	if !differ.Failed() {
		t.Errorf("3 vs 2: status %q, want a mismatch", differ.Status)
	}
	if differ.POASeats == nil || *differ.POASeats != 3 || differ.BCSeats == nil || *differ.BCSeats != 2 {
		t.Error("both compared figures must be reported, so a reviewer sees the working")
	}
}

// TestSeatsCheck_FallsBackToBCDescription is why the check now does anything
// at all. Across the captured corpus the item record carried a seat count on
// 2 of 48 enriched lines and on neither of the 17 lines that actually stated
// one, so every seat check returned "unknown" and passed — including where
// BC's own description said "4 Seater Sofa" and the POA said the same. The
// figure was in hand the whole time; only No_of_Seats was empty.
func TestSeatsCheck_FallsBackToBCDescription(t *testing.T) {
	poa := POAProduct{Code: "NOV4S", Description: "NOVA 4 seater sofa in CAMPA ARCTIC"}

	// The corpus's actual shape: enrichment ran, the item came back, and its
	// seat count was empty.
	bc := bcItem("IT0226778", 1, 100, &ItemDetail{ItemNo: "IT0226778"})
	bc.Description = "4 Seater Sofa"

	got := seatsCheck(poa, bc, EnrichmentApplied, cfg())
	if got.Status != StatusMatch {
		t.Errorf("Status = %s, want %s: BC's description states the seat count even though its item record does not", got.Status, StatusMatch)
	}
	if got.DataState != SeatsFromDescription {
		t.Errorf("DataState = %q, want %q: a comparison made from the description must not be reported as though the item record supplied it", got.DataState, SeatsFromDescription)
	}
	if got.POASeats == nil || *got.POASeats != 4 || got.BCSeats == nil || *got.BCSeats != 4 {
		t.Errorf("POASeats/BCSeats = %v/%v, want 4/4", got.POASeats, got.BCSeats)
	}
}

// TestSeatsCheck_DescriptionFallbackCanBlock guards the point of the fallback.
// It has to be able to fail, not merely to agree: a supplier sending a 2
// seater against an ordered 3 seater is the substitution this check exists for,
// and it is invisible to the price and quantity checks.
func TestSeatsCheck_DescriptionFallbackCanBlock(t *testing.T) {
	poa := POAProduct{Code: "MET2S", Description: "METRO 2 seater sofa"}
	bc := bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1"})
	bc.Description = "3 Seater Sofa"

	got := seatsCheck(poa, bc, EnrichmentApplied, cfg())
	if got.Status != StatusMismatch {
		t.Fatalf("Status = %s, want %s", got.Status, StatusMismatch)
	}
	if !got.Failed() {
		t.Error("a seat mismatch read from the description must block, exactly as one read from the item record does")
	}
}

// TestSeatsCheck_ItemRecordWinsOverTheDescription pins the precedence. The
// item record is master data and the description is a fallback; a fallback
// that overrode the authoritative source would make DataState a lie.
func TestSeatsCheck_ItemRecordWinsOverTheDescription(t *testing.T) {
	poa := POAProduct{Code: "X", Description: "3 seater"}
	bc := bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(3.0)})
	bc.Description = "2 Seater Sofa" // disagrees with the item record on purpose

	got := seatsCheck(poa, bc, EnrichmentApplied, cfg())
	if got.DataState != SeatsComparable {
		t.Errorf("DataState = %q, want %q", got.DataState, SeatsComparable)
	}
	if got.Status != StatusMatch || got.BCSeats == nil || *got.BCSeats != 3 {
		t.Errorf("Status/BCSeats = %s/%v, want %s/3 taken from the item record", got.Status, got.BCSeats, StatusMatch)
	}
}

// TestSeatsCheck_ImplausibleItemRecordIsNotPaperedOver: an item record holding
// a figure that cannot be a seat count is a data-quality signal, and reading
// past it to the description would hide it. That case reports and stops.
func TestSeatsCheck_ImplausibleItemRecordIsNotPaperedOver(t *testing.T) {
	poa := POAProduct{Code: "X", Description: "3 seater"}
	bc := bcItem("IT1", 1, 100, &ItemDetail{ItemNo: "IT1", Seats: Ptr(0.5)})
	bc.Description = "3 Seater Sofa"

	got := seatsCheck(poa, bc, EnrichmentApplied, cfg())
	if got.DataState != SeatsImplausible {
		t.Errorf("DataState = %q, want %q", got.DataState, SeatsImplausible)
	}
}

// ---------------------------------------------------------------------------
// Code data state — the same distinction, for identity
// ---------------------------------------------------------------------------

func TestCodeStatus_SeparatesMissingDataFromMissingLookup(t *testing.T) {
	withCodes := &ItemDetail{ItemNo: "IT1", VendorItemNo: "NFX2S"}
	bare := &ItemDetail{ItemNo: "IT1"}

	tests := []struct {
		name      string
		bc        BCProduct
		state     EnrichmentState
		wantKnown bool
		wantNote  bool
	}{
		{"lookup never ran", bcItem("IT1", 1, 1, nil), EnrichmentNotRequested, false, true},
		{"lookup failed", bcItem("IT1", 1, 1, nil), EnrichmentFailed, false, true},
		{"no record for the item", bcItem("IT1", 1, 1, nil), EnrichmentApplied, false, true},
		{"record with no codes", bcItem("IT1", 1, 1, bare), EnrichmentApplied, true, true},
		{"record with a code", bcItem("IT1", 1, 1, withCodes), EnrichmentApplied, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			known, note := codeStatus(tc.bc, tc.state)
			if known != tc.wantKnown {
				t.Errorf("known = %v, want %v (%s)", known, tc.wantKnown, note)
			}
			if (note != "") != tc.wantNote {
				t.Errorf("note = %q, want present=%v", note, tc.wantNote)
			}
		})
	}
}

// TestIdentityGap_DoesNotClaimBCIsMissingDataWhenNobodyAsked: the message that
// was confidently wrong on every degraded run.
func TestIdentityGap_DoesNotClaimBCIsMissingDataWhenNobodyAsked(t *testing.T) {
	poa := POAProduct{Code: "NFX2S (A)", Qty: 1}
	bc := bcItem("IT0188674", 1, 0, nil)
	ev := MatchEvidence{Similarity: Ptr(0.33), MarginNA: true}

	notRun := descriptionCheck(poa, bc, ev, EnrichmentNotRequested, cfg())
	if notRun.CodeDataState != "unknown" {
		t.Errorf("CodeDataState = %q, want unknown", notRun.CodeDataState)
	}
	if strings.Contains(notRun.Message, "has no Vendor_Item_No") {
		t.Errorf("message = %q; it blames BC's data for a lookup that never ran", notRun.Message)
	}

	ran := descriptionCheck(poa, bcItem("IT0188674", 1, 0, &ItemDetail{ItemNo: "IT0188674"}), ev, EnrichmentApplied, cfg())
	if ran.CodeDataState != "no_code_on_file" {
		t.Errorf("CodeDataState = %q, want no_code_on_file", ran.CodeDataState)
	}
	if !strings.Contains(ran.Message, "no Vendor_Item_No") {
		t.Errorf("message = %q; when the lookup did run, the master-data gap is the point", ran.Message)
	}
}

// ---------------------------------------------------------------------------
// The controls
// ---------------------------------------------------------------------------

func TestPriceCheck(t *testing.T) {
	poa := POAProduct{Qty: 1, UnitPrice: 541}
	if got := priceCheck(poa, BCProduct{Net: 541}, cfg()); got.Failed() {
		t.Errorf("equal nets reported as %q", got.Status)
	}
	got := priceCheck(poa, BCProduct{Net: 565}, cfg())
	if !got.Failed() {
		t.Fatalf("541 vs 565 reported as %q", got.Status)
	}
	if got.Difference != -24 {
		t.Errorf("difference = %g, want -24 (signed, POA minus BC)", got.Difference)
	}
}

func TestQuantityCheck_PackRatio(t *testing.T) {
	poa := POAProduct{Qty: 2, UnitPrice: 100}
	bc := BCProduct{Qty: 4, UnitCost: 50}
	if got := quantityCheck(poa, bc); got.Failed() || got.PackRatio != 2 {
		t.Errorf("2 packs of 2 at 100 vs 4 singles at 50: %q ratio %d, want match at 2", got.Status, got.PackRatio)
	}
	bc.UnitCost = 80 // the prices no longer confirm the ratio
	if got := quantityCheck(poa, bc); !got.Failed() {
		t.Errorf("status = %q, want mismatch: a real quantity change must not pass as a pack size", got.Status)
	}
}

// TestQuantityCheck_PartsPerUnit is the pack ratio the other way round: the
// supplier counts the two mattresses of a Zip & Link, BC counts the one item.
// PF130794 and PF130999 failed on quantity alone before this.
func TestQuantityCheck_PartsPerUnit(t *testing.T) {
	poa := POAProduct{Qty: 2, UnitPrice: 466}
	bc := BCProduct{Qty: 1, UnitCost: 932}
	if got := quantityCheck(poa, bc); got.Failed() || got.PartsPerUnit != 2 {
		t.Errorf("2 parts at 466 vs 1 unit at 932: %q parts %d, want match at 2", got.Status, got.PartsPerUnit)
	}
	poa.UnitPrice = 932 // two whole units: the supplier is sending twice the goods
	if got := quantityCheck(poa, bc); !got.Failed() {
		t.Errorf("status = %q, want mismatch: a real quantity change must not pass as parts", got.Status)
	}
}

func TestArithmeticCheck_AbsentTotalIsUnknownNotZero(t *testing.T) {
	got := arithmeticCheck(POAProduct{Qty: 1, UnitPrice: 541}, cfg())
	if got.Status != StatusUnknown || got.Failed() {
		t.Errorf("status = %q, want unknown and passing when no line total is printed", got.Status)
	}
	bad := arithmeticCheck(POAProduct{Qty: 1, UnitPrice: 541, LineTotal: Ptr(500.0)}, cfg())
	if !bad.Failed() {
		t.Errorf("status = %q, want mismatch when the POA contradicts itself", bad.Status)
	}
}

func TestTotalsCheck_UsesTheOrderTolerance(t *testing.T) {
	// The order total is one figure both sides printed, so it is held to a
	// tighter bar than a line that accumulates rounding. This was an open
	// question for months; Config now states the answer.
	c := cfg()
	tests := []struct {
		poaTotal, bcTotal float64
		wantOK            bool
	}{
		{100.00, 100.00, true},
		{100.00, 100.015, true}, // inside OrderTolerance
		{100.00, 100.03, false}, // outside it, though inside the per-line tolerance
	}
	for _, tc := range tests {
		got := totalsCheck(POAOrder{TotalExVAT: Ptr(tc.poaTotal)}, BCOrder{TotalExVAT: tc.bcTotal}, nil, nil, c)
		if !got.Failed() != tc.wantOK {
			t.Errorf("POA %.3f vs BC %.3f: status %q, want ok=%v at tolerance %.2f",
				tc.poaTotal, tc.bcTotal, got.Status, tc.wantOK, c.OrderTolerance)
		}
	}
}

func TestTotalsCheck_DerivesTotalFromLines(t *testing.T) {
	poa := POAOrder{Products: []POAProduct{
		{Qty: 2, UnitPrice: 100},
		{Qty: 1, UnitPrice: 50, DiscountPercent: 10},
	}}
	got := totalsCheck(poa, BCOrder{TotalExVAT: 245}, nil, nil, cfg())
	if got.TotalSource != "derived from lines" {
		t.Errorf("TotalSource = %q", got.TotalSource)
	}
	if got.Failed() {
		t.Errorf("status = %q (%s), want a match: 200 + 45 = 245", got.Status, got.Message)
	}
}

func TestOrientationCheck_HeaderIsNotASpecification(t *testing.T) {
	// BC states handedness as a heading offering both hands, followed by the
	// actual value on a comment line. Reading the heading as the value would
	// send every Ashwood corner unit to manual review.
	bc := BCProduct{
		LineDescription: "Chaise End RHF/LHF",
		Comments:        []string{"Fabric or Aqua Clean", "Left Hand Facing"},
	}
	got := orientationCheck(POAProduct{Code: "HANCLL (A)", Description: "HANSSON Chaise longue L/H"}, bc)
	if got.Status != StatusMatch {
		t.Errorf("status = %q (%s), want match — both sides say left", got.Status, got.Message)
	}
	if got.BCOrientation != "LHF" {
		t.Errorf("BC orientation = %q, want LHF from the comment line", got.BCOrientation)
	}
}
