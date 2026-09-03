package poa

import (
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

// ashwoodGroups is the shape phase2-feedback-loop.md section 3 measured: BC
// prints generic type names and a populated Range_Code, and holds no
// Vendor_Item_No (Ashwood's live fill rate for that field is 3%). Two
// different ranges on one order, which is what makes the range worth anything
// — a range every candidate shares cannot tell them apart.
func ashwoodGroups() []BCLineGroup {
	return []BCLineGroup{
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-0", LineObjectNumber: "IT0233832"},
			CombinedDesc: "Chaise End RHF/LHF HANSSON",
			Detail:       &businesscentral.CaseysItem{No: "IT0233832", RangeCode: "HANSSON"},
		},
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-1", LineObjectNumber: "IT0233835"},
			CombinedDesc: "2 Seater Sofa OLLSON",
			Detail:       &businesscentral.CaseysItem{No: "IT0233835", RangeCode: "OLLSON"},
		},
	}
}

func TestCommonPrefixLen(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"HANCLR", "HANSSON", 3},
		{"HAN2HFR", "HANSSON", 3},
		{"NFX2S", "HANSSON", 0},
		{"HANSSON", "HANSSON", 7},
		{"", "HANSSON", 0},
	}
	for _, tc := range tests {
		if got := commonPrefixLen(tc.a, tc.b); got != tc.want {
			t.Errorf("commonPrefixLen(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestMatchedRangeCode_AshwoodAbbreviation is the case section 3 identified:
// the POA code is range-prefixed, so HAN reaches HANSSON even though the two
// tokens share nothing a string metric can see.
func TestMatchedRangeCode_AshwoodAbbreviation(t *testing.T) {
	groups := ashwoodGroups()
	bcTokens := bcTokensOf(groups)

	if got := matchedRangeCode("HANCLR (A)", groups, bcTokens); got != "HANSSON" {
		t.Errorf("matchedRangeCode(HANCLR) = %q, want HANSSON", got)
	}
	if got := matchedRangeCode("HAN2HFR (A)", groups, bcTokens); got != "HANSSON" {
		t.Errorf("matchedRangeCode(HAN2HFR) = %q, want HANSSON", got)
	}
	// A code belonging to neither range on the order abbreviates nothing.
	if got := matchedRangeCode("NFX2S (A)", groups, bcTokens); got != "" {
		t.Errorf("matchedRangeCode(NFX2S) = %q, want \"\" (no range on this order starts NFX)", got)
	}
}

// TestMatchedRangeCode_ProseIsNotAnAbbreviation is the guard that keeps this
// from inventing evidence. "SOFAS" shares SOF with a SOFTLINE range, but BC
// prints "Sofa" as a word — the two sides already agree on it, and the shared
// prefix is coincidence between two unrelated words.
func TestMatchedRangeCode_ProseIsNotAnAbbreviation(t *testing.T) {
	groups := []BCLineGroup{{
		Item:         businesscentral.PurchaseOrderLine{ID: "bc-0"},
		CombinedDesc: "2 Seater Sofa SOFTLINE",
		Detail:       &businesscentral.CaseysItem{RangeCode: "SOFTLINE"},
	}}

	if got := matchedRangeCode("2 Seater Sofas", groups, bcTokensOf(groups)); got != "" {
		t.Errorf("matchedRangeCode = %q, want \"\" — SOFAS is prose BC also prints, not an abbreviation of SOFTLINE", got)
	}
}

// TestMatchedRangeCode_AmbiguousRangesYieldNothing mirrors
// resolveCodeAmbiguity: two ranges abbreviating to the same prefix is a
// confusion, and inventing a shared token with both groups would manufacture
// exactly what MarginThreshold exists to catch.
func TestMatchedRangeCode_AmbiguousRangesYieldNothing(t *testing.T) {
	groups := []BCLineGroup{
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-0"},
			CombinedDesc: "Chaise End HANSSON",
			Detail:       &businesscentral.CaseysItem{RangeCode: "HANSSON"},
		},
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-1"},
			CombinedDesc: "Corner Unit HANBURY",
			Detail:       &businesscentral.CaseysItem{RangeCode: "HANBURY"},
		},
	}

	if got := matchedRangeCode("HANCLR (A)", groups, bcTokensOf(groups)); got != "" {
		t.Errorf("matchedRangeCode = %q, want \"\" — HAN abbreviates both HANSSON and HANBURY", got)
	}
}

func TestMatchedRangeCode_NoEnrichmentYieldsNothing(t *testing.T) {
	groups := []BCLineGroup{{
		Item:         businesscentral.PurchaseOrderLine{ID: "bc-0"},
		CombinedDesc: "Chaise End",
	}}
	if got := matchedRangeCode("HANCLR (A)", groups, bcTokensOf(groups)); got != "" {
		t.Errorf("matchedRangeCode = %q, want \"\" — no Detail means no Range_Code to abbreviate", got)
	}
}

// TestPrepareMatch_AppendsRangeCodeToPOAText is the wiring: the matched range
// has to reach the POA text, or nothing downstream can score it.
func TestPrepareMatch_AppendsRangeCodeToPOAText(t *testing.T) {
	groups := ashwoodGroups()
	products := []OrderDetail{{Product: "HANCLR (A)"}, {Product: "OLL2S (A)"}}

	poaTexts, _ := prepareMatch(products, groups)
	if !strings.Contains(poaTexts[0], "HANSSON") {
		t.Errorf("poaTexts[0] = %q, want it to carry HANSSON", poaTexts[0])
	}
	if !strings.Contains(poaTexts[1], "OLLSON") {
		t.Errorf("poaTexts[1] = %q, want it to carry OLLSON", poaTexts[1])
	}
}

// TestPrepareMatch_RangeCodeLiftsTheCorrectPairing is the point of the whole
// change, stated as the outcome rather than the mechanism: an Ashwood line
// whose SKU shares nothing with BC's type name must still score higher against
// its own range than against the other one.
func TestPrepareMatch_RangeCodeLiftsTheCorrectPairing(t *testing.T) {
	groups := ashwoodGroups()
	products := []OrderDetail{{Product: "HANCLR (A)"}}
	poaTexts, vocab := prepareMatch(products, groups)

	right := scorePair(products[0], poaTexts[0], groups[0], vocab, MatchContext{})
	wrong := scorePair(products[0], poaTexts[0], groups[1], vocab, MatchContext{})

	if right.DescScore <= wrong.DescScore {
		t.Errorf("HANCLR scored %.3f against its own range (HANSSON) and %.3f against OLLSON; want the former higher",
			right.DescScore, wrong.DescScore)
	}
	// Without the range there is nothing shared at all — this is what the
	// batch run was reporting as "similarity 0.33" and calling evidence.
	if bare := overlapSimilarity("HANCLR (A)", groups[0].CombinedDesc); bare >= right.DescScore {
		t.Errorf("range augmentation did not improve on the bare overlap (%.3f vs %.3f)", bare, right.DescScore)
	}
}

// TestPrepareMatch_SharedRangeCarriesNoSignal is the self-limiting property:
// when every candidate is in the same range, the range cannot discriminate,
// and the weighting must reduce it to nothing without being told to.
func TestPrepareMatch_SharedRangeCarriesNoSignal(t *testing.T) {
	groups := []BCLineGroup{
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-0"},
			CombinedDesc: "Chaise End HANSSON",
			Detail:       &businesscentral.CaseysItem{RangeCode: "HANSSON"},
		},
		{
			Item:         businesscentral.PurchaseOrderLine{ID: "bc-1"},
			CombinedDesc: "2 Seater Sofa HANSSON",
			Detail:       &businesscentral.CaseysItem{RangeCode: "HANSSON"},
		},
	}
	products := []OrderDetail{{Product: "HANCLR (A)"}, {Product: "HAN2S (A)"}}

	_, vocab := prepareMatch(products, groups)
	if w := vocab.weightOf("hansson"); w != 0 {
		t.Errorf("weightOf(hansson) = %.3f, want 0 — every candidate is in this range, so it tells them nothing apart", w)
	}
}

func bcTokensOf(groups []BCLineGroup) map[string]bool {
	toks := map[string]bool{}
	for _, g := range groups {
		for t := range cleanTokenSet(g.CombinedDesc) {
			toks[t] = true
		}
	}
	return toks
}
