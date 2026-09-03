package poa

import (
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

func TestProductCodeIn(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The real POA lines from the batch run in phase2-feedback-loop.md
		// section 2, which is where this whole problem was measured.
		{"ashwood range code", "NFX2S (A)", "NFX2S"},
		{"ashwood handed unit", "HAN2HFR (A)", "HAN2HFR"},
		{"storage stool with grade suffix", "STGTS1 (C)", "STGTS1"},
		{"hyphenated code", "C191-TAN-P", "C191TANP"},
		{"code buried in prose", "CA24 Natural Comfort 5000 (QJ)", "CA24"},

		// BC-side prose must never read as a code, or every line would get
		// the wrong explanation.
		{"generic type name", "2 Seater Sofa", ""},
		{"handed prose", "Chaise End RHF/LHF", ""},
		{"plain prose", "Mattress Only", ""},

		// A measurement leads with its number; a code leads with its family.
		// Without this the "150CM" in a POA line would be reported as a
		// product code.
		{"dimension is not a code", "Width 150CM Depth 90CM", ""},
		{"bare number is not a code", "Natural Comfort 5000", ""},

		// Below minProductCodeLen there is not enough there to be sure.
		{"too short", "K2 Foot", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := productCodeIn(tc.in); got != tc.want {
				t.Errorf("productCodeIn(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBCLineGroup_HasCodeOnFile(t *testing.T) {
	tests := []struct {
		name   string
		detail *businesscentral.CaseysItem
		want   bool
	}{
		{"no enrichment at all", nil, false},
		{"enriched but both code fields empty", &businesscentral.CaseysItem{No: "IT0188674"}, false},
		{"vendor item no on file", &businesscentral.CaseysItem{VendorItemNo: "NFX2S"}, true},
		{"model no on file", &businesscentral.CaseysItem{ModelNo: "NFX2S"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := BCLineGroup{Detail: tc.detail}
			if got := g.hasCodeOnFile(); got != tc.want {
				t.Errorf("hasCodeOnFile() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIdentityGapReason_MissingCodeNamesTheFix is section 4 point 3 of
// phase2-feedback-loop.md: when the POA prints a SKU and BC holds no code to
// compare it against, the discrepancy must name the missing master data
// rather than report a similarity score that measures nothing.
func TestIdentityGapReason_MissingCodeNamesTheFix(t *testing.T) {
	m := LineMatch{
		POA: OrderDetail{Product: "NFX2S (A)"},
		BC: BCLineGroup{
			Item:         businesscentral.PurchaseOrderLine{LineObjectNumber: "IT0188674"},
			CombinedDesc: "2 Seater Sofa",
			Detail:       &businesscentral.CaseysItem{No: "IT0188674"}, // enriched, but no codes
		},
		DescScore:    0.33,
		DescScoreRaw: 0.33,
	}

	kind, got := m.identityGapReason()
	if kind != DiscMissingBCCode {
		t.Errorf("identityGapReason() kind = %q, want %q", kind, DiscMissingBCCode)
	}
	for _, want := range []string{"NFX2S", "IT0188674", "Vendor_Item_No"} {
		if !strings.Contains(got, want) {
			t.Errorf("identityGapReason() = %q, want it to mention %q", got, want)
		}
	}
	// The gap is a master-data problem a reviewer can close for good, so it
	// has to be recorded as teachable.
	if !kind.Learnable() {
		t.Errorf("%q reports as not learnable; a confirmed code mapping is the whole point of Phase 2", kind)
	}
}

// TestIdentityGapReason_FallsBackWhenCodeIsOnFile guards against the message
// over-reaching. If BC does hold a code and it still did not match, the
// obstacle is not missing master data — blaming it would send someone to
// populate a field that is already populated.
func TestIdentityGapReason_FallsBackWhenCodeIsOnFile(t *testing.T) {
	m := LineMatch{
		POA: OrderDetail{Product: "NFX2S (A)"},
		BC: BCLineGroup{
			Item:   businesscentral.PurchaseOrderLine{LineObjectNumber: "IT0188674"},
			Detail: &businesscentral.CaseysItem{No: "IT0188674", VendorItemNo: "SOMETHINGELSE"},
		},
		DescScore:    0.33,
		DescScoreRaw: 0.41,
	}

	if kind, got := m.identityGapReason(); kind != DiscDescription {
		t.Errorf("identityGapReason() = (%q, %q), want kind %q when BC has a code on file", kind, got, DiscDescription)
	}
}

// TestIdentityGapReason_FallsBackOnProseBothSides covers the Whitemeadow
// shape, where both sides print prose. There is no SKU to explain, so the
// similarity number is the honest thing to report — it is comparing like with
// like.
func TestIdentityGapReason_FallsBackOnProseBothSides(t *testing.T) {
	m := LineMatch{
		POA:          OrderDetail{Product: "Metro 1 Arm Sofa Unit"},
		BC:           BCLineGroup{Item: businesscentral.PurchaseOrderLine{LineObjectNumber: "IT0163911"}},
		DescScore:    0.42,
		DescScoreRaw: 0.55,
	}

	if kind, got := m.identityGapReason(); kind != DiscDescription {
		t.Errorf("identityGapReason() = (%q, %q), want kind %q when neither side prints a code", kind, got, DiscDescription)
	}
}

// TestDiscrepancies_UsesIdentityGapReason wires the above through the surface
// reviewers actually read.
func TestDiscrepancies_UsesIdentityGapReason(t *testing.T) {
	m := LineMatch{
		POA: OrderDetail{Product: "NFX2S (A)", Qty: 1},
		BC: BCLineGroup{
			Item:   businesscentral.PurchaseOrderLine{LineObjectNumber: "IT0188674", Quantity: 1},
			Detail: &businesscentral.CaseysItem{No: "IT0188674"},
		},
		DescScore: 0.33, DescScoreRaw: 0.33, DescMarginNA: true,
		NetOK: true, QtyOK: true, InternalOK: true, OrientationOK: true, SeatsOK: true,
	}

	d := m.Discrepancies()
	if len(d) != 1 {
		t.Fatalf("Discrepancies() = %v, want exactly one (the identity gap)", Kinds(d))
	}
	if d[0].Kind != DiscMissingBCCode {
		t.Errorf("Discrepancies()[0].Kind = %q, want %q", d[0].Kind, DiscMissingBCCode)
	}
	if !strings.Contains(d[0].Message, "no Vendor_Item_No") {
		t.Errorf("Discrepancies()[0].Message = %q, want the missing-code explanation", d[0].Message)
	}
}
