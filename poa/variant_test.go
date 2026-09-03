package poa

import (
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

func TestSplitVariantSuffix(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		base, suffix string
	}{
		{"ashwood grade suffix", "STGTS1 (A)", "STGTS1", "A"},
		{"other grade of the same code", "STGTS1 (C)", "STGTS1", "C"},
		{"no space before the suffix", "NFX2S(C)", "NFX2S", "C"},
		{"multi-character suffix", "HANCLR (QJ)", "HANCLR", "QJ"},
		{"lowercase input still reads", "stgts1 (a)", "STGTS1", "A"},

		// A bare number before a parenthesis is a quantity or a price, not a
		// product code — the base has to carry a letter.
		{"number is not a code", "CA24 Natural Comfort 5000 (QJ)", "", ""},
		// Below minProductCodeLen there is not enough there to be sure.
		{"base too short", "K2 (A)", "", ""},
		{"no suffix at all", "STGTS1", "", ""},
		{"plain prose", "2 Seater Sofa", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, suffix := splitVariantSuffix(tc.in)
			if base != tc.base || suffix != tc.suffix {
				t.Errorf("splitVariantSuffix(%q) = (%q, %q), want (%q, %q)", tc.in, base, suffix, tc.base, tc.suffix)
			}
		})
	}
}

func TestBCDistinguishesVariant(t *testing.T) {
	tests := []struct {
		name   string
		detail *businesscentral.CaseysItem
		want   bool
	}{
		{"no enrichment", nil, false},
		{"family code only", &businesscentral.CaseysItem{VendorItemNo: "STGTS1"}, false},
		{"vendor item no spells the variant", &businesscentral.CaseysItem{VendorItemNo: "STGTS1C"}, true},
		{"model no spells the variant", &businesscentral.CaseysItem{ModelNo: "STGTS1C"}, true},
		{"spells a different variant", &businesscentral.CaseysItem{VendorItemNo: "STGTS1A"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := BCLineGroup{Detail: tc.detail}
			if got := bcDistinguishesVariant(g, "STGTS1", "C"); got != tc.want {
				t.Errorf("bcDistinguishesVariant(STGTS1, C) = %v, want %v", got, tc.want)
			}
		})
	}
}

// variantOrder builds the situation phase2-feedback-loop.md section 3
// describes: two POA lines whose codes differ only by the parenthesised grade
// suffix, against BC items that hold the bare family code and cannot tell the
// grades apart.
func variantOrder(bcCode string) []LineMatch {
	group := func(id, itemNo string) BCLineGroup {
		return BCLineGroup{
			Item:         businesscentral.PurchaseOrderLine{ID: id, LineObjectNumber: itemNo},
			CombinedDesc: "Small Storage Stool",
			Detail: &businesscentral.CaseysItem{
				No: itemNo, VendorItemNo: bcCode, VendorNo: "VX00099",
			},
		}
	}
	return []LineMatch{
		{
			POA: OrderDetail{Product: "STGTS1 (A)"}, poaIndex: 0,
			BC: group("bc-0", "IT0233832"), CodeMatch: true, CodeMatchSource: "Vendor_Item_No",
		},
		{
			POA: OrderDetail{Product: "STGTS1 (C)"}, poaIndex: 1,
			BC: group("bc-1", "IT0233835"), CodeMatch: true, CodeMatchSource: "Vendor_Item_No",
		},
	}
}

// TestResolveVariantAmbiguity_FamilyCodeCannotClaimAVariant is the bug this
// change closes. Both POA lines contain the family code, so both claim an
// exact match; CodeMatch settles identity outright, so without this one
// variant is auto-approved against the other's item.
func TestResolveVariantAmbiguity_FamilyCodeCannotClaimAVariant(t *testing.T) {
	all := variantOrder("STGTS1")
	resolveVariantAmbiguity(all)

	for i, m := range all {
		if m.CodeMatch {
			t.Errorf("match %d: CodeMatch still true — the family code STGTS1 cannot say which grade this line is", i)
		}
		if m.CodeVariantAmbiguous == "" {
			t.Errorf("match %d: CodeVariantAmbiguous empty, want the competing variants named", i)
		}
		if m.IdentityConfident() {
			t.Errorf("match %d: IdentityConfident despite an unresolved variant", i)
		}
	}
	if got := all[0].CodeVariantAmbiguous; !strings.Contains(got, "STGTS1 (A)") || !strings.Contains(got, "STGTS1 (C)") {
		t.Errorf("CodeVariantAmbiguous = %q, want both competing variants named", got)
	}
}

// TestResolveVariantAmbiguity_LeavesAVariantSpelledOutInBC is the other half:
// when BC's code does carry the grade, the code really does identify the line
// and must keep its match.
func TestResolveVariantAmbiguity_LeavesAVariantSpelledOutInBC(t *testing.T) {
	all := variantOrder("STGTS1")
	all[1].BC.Detail = &businesscentral.CaseysItem{
		No: "IT0233835", VendorItemNo: "STGTS1C", VendorNo: "VX00099",
	}
	resolveVariantAmbiguity(all)

	if !all[1].CodeMatch {
		t.Error("match 1: CodeMatch withdrawn even though BC's code spells out the C grade")
	}
	if all[0].CodeMatch {
		t.Error("match 0: CodeMatch kept even though BC holds only the family code for it")
	}
}

// TestResolveVariantAmbiguity_LoneVariantIsUnambiguous guards against
// over-reach. Ambiguity is a property of the candidate set: with only one
// STGTS1 line on the order there is no second variant to confuse it with, and
// withdrawing the match would flag a line that is correctly identified.
func TestResolveVariantAmbiguity_LoneVariantIsUnambiguous(t *testing.T) {
	all := variantOrder("STGTS1")[:1]
	resolveVariantAmbiguity(all)

	if !all[0].CodeMatch {
		t.Error("CodeMatch withdrawn from the only STGTS1 line on the order; nothing competes with it")
	}
	if all[0].CodeVariantAmbiguous != "" {
		t.Errorf("CodeVariantAmbiguous = %q, want empty", all[0].CodeVariantAmbiguous)
	}
}

// TestResolveVariantAmbiguity_DifferentBasesDoNotCompete checks the grouping
// is by base code and not merely by the presence of a suffix.
func TestResolveVariantAmbiguity_DifferentBasesDoNotCompete(t *testing.T) {
	all := variantOrder("STGTS1")
	all[1].POA = OrderDetail{Product: "HANCLR (C)"}
	resolveVariantAmbiguity(all)

	for i, m := range all {
		if !m.CodeMatch {
			t.Errorf("match %d: CodeMatch withdrawn, but STGTS1 and HANCLR are different products", i)
		}
	}
}

// TestDiscrepancies_NamesTheUnresolvedVariant is the message a reviewer reads.
func TestDiscrepancies_NamesTheUnresolvedVariant(t *testing.T) {
	all := variantOrder("STGTS1")
	resolveVariantAmbiguity(all)

	m := all[0]
	m.NetOK, m.QtyOK, m.InternalOK, m.OrientationOK, m.SeatsOK = true, true, true, true, true
	m.DescScore, m.DescMarginNA = 1.0, true

	var found Discrepancy
	for _, s := range m.Discrepancies() {
		if s.Kind == DiscVariantAmbiguous {
			found = s
		}
	}
	if found.Kind == "" {
		t.Fatalf("Discrepancies() kinds = %v, want one of kind %q", Kinds(m.Discrepancies()), DiscVariantAmbiguous)
	}
	if !strings.Contains(found.Message, "IT0233832") {
		t.Errorf("discrepancy = %q, want it to name the BC item", found.Message)
	}
	// A reviewer picking the right variant teaches a real, permanent fact
	// about this product, so the verdict has to be recordable.
	if found.Kind.Learnability() != LearnablePerLine {
		t.Errorf("%q is not learnable per line; a reviewer resolving the variant is exactly what Phase 2 records", found.Kind)
	}
}
