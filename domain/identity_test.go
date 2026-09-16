package domain

import (
	"strings"
	"testing"
)

func TestProductCodeIn(t *testing.T) {
	tests := []struct{ name, in, want string }{
		// Real POA lines from the batch this rule was measured on.
		{"ashwood range code", "NFX2S (A)", "NFX2S"},
		{"ashwood handed unit", "HAN2HFR (A)", "HAN2HFR"},
		{"storage stool with grade suffix", "STGTS1 (C)", "STGTS1"},
		{"hyphenated code", "C191-TAN-P", "C191TANP"},
		{"code buried in prose", "CA24 Natural Comfort 5000 (QJ)", "CA24"},

		// BC-side prose must never read as a code, or every line gets the
		// wrong explanation.
		{"generic type name", "2 Seater Sofa", ""},
		{"handed prose", "Chaise End RHF/LHF", ""},
		{"plain prose", "Mattress Only", ""},

		// A measurement leads with its number; a code leads with its family.
		{"dimension is not a code", "Width 150CM Depth 90CM", ""},
		{"bare number is not a code", "Natural Comfort 5000", ""},
		{"too short", "K2 Foot", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := productCodeIn(tc.in, cfg()); got != tc.want {
				t.Errorf("productCodeIn(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCodeMatch_VendorScoping(t *testing.T) {
	// Vendor_Item_No is this vendor's code for the item. Matching it against
	// an item belonging to a different supplier would be a coincidence dressed
	// up as authoritative.
	bc := BCProduct{ItemNo: "IT1", Detail: &ItemDetail{
		ItemNo: "IT1", VendorItemNo: "CODE123456", VendorNo: "VX01568",
	}}
	text := squash("Widget CODE123456 Extra")

	if ok, src := codeMatch(text, bc, "VX01568", cfg()); !ok || src != "Vendor_Item_No" {
		t.Errorf("same vendor: matched=%v source=%q, want true/Vendor_Item_No", ok, src)
	}
	if ok, _ := codeMatch(text, bc, "VX99999", cfg()); ok {
		t.Error("matched a Vendor_Item_No belonging to a different supplier")
	}
	// Model_No carries no vendor constraint.
	bc.Detail = &ItemDetail{ItemNo: "IT1", ModelNo: "CODE123456"}
	if ok, src := codeMatch(text, bc, "VX99999", cfg()); !ok || src != "Model_No" {
		t.Errorf("Model_No: matched=%v source=%q, want true/Model_No", ok, src)
	}
}

func TestSplitVariantSuffix(t *testing.T) {
	tests := []struct{ name, in, base, suffix string }{
		{"ashwood grade suffix", "STGTS1 (A)", "STGTS1", "A"},
		{"other grade of the same code", "STGTS1 (C)", "STGTS1", "C"},
		{"no space before the suffix", "NFX2S(C)", "NFX2S", "C"},
		{"multi-character suffix", "HANCLR (QJ)", "HANCLR", "QJ"},
		{"lowercase input still reads", "stgts1 (a)", "STGTS1", "A"},
		// A bare number before a parenthesis is a quantity or a price.
		{"number is not a code", "CA24 Natural Comfort 5000 (QJ)", "", ""},
		{"base too short", "K2 (A)", "", ""},
		{"no suffix at all", "STGTS1", "", ""},
		{"plain prose", "2 Seater Sofa", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, suffix := splitVariantSuffix(tc.in, cfg())
			if base != tc.base || suffix != tc.suffix {
				t.Errorf("splitVariantSuffix(%q) = (%q,%q), want (%q,%q)", tc.in, base, suffix, tc.base, tc.suffix)
			}
		})
	}
}

// variantOrder builds the situation the variant rule exists for: two POA lines
// whose codes differ only by the parenthesised grade, against BC items holding
// the bare family code, which cannot tell the grades apart.
func variantOrder(bcCode string) []pairing {
	item := func(itemNo string) BCProduct {
		return BCProduct{
			LineID: "bc-" + itemNo, ItemNo: itemNo, Description: "Small Storage Stool", Qty: 1,
			Detail: &ItemDetail{ItemNo: itemNo, VendorItemNo: bcCode, VendorNo: "VX00099"},
		}
	}
	matched := MatchEvidence{CodeMatch: true, CodeMatchSource: "Vendor_Item_No"}
	return []pairing{
		{poaIndex: 0, bcIndex: 0, poa: POAProduct{Code: "STGTS1 (A)", Qty: 1}, bc: item("IT0233832"), ev: matched},
		{poaIndex: 1, bcIndex: 1, poa: POAProduct{Code: "STGTS1 (C)", Qty: 1}, bc: item("IT0233835"), ev: matched},
	}
}

// TestResolveVariantAmbiguity_FamilyCodeCannotClaimAVariant: both POA lines
// contain the family code, so both claim an exact match. A code match settles
// identity outright, so without this one variant is auto-approved against the
// other's item.
func TestResolveVariantAmbiguity_FamilyCodeCannotClaimAVariant(t *testing.T) {
	all := variantOrder("STGTS1")
	resolveVariantAmbiguity(all, cfg())

	for i, p := range all {
		if p.ev.CodeMatch {
			t.Errorf("pairing %d: CodeMatch still true — STGTS1 cannot say which grade this line is", i)
		}
		if p.ev.VariantAmbiguity == "" {
			t.Errorf("pairing %d: VariantAmbiguity empty, want the competing variants named", i)
		}
	}
	if got := all[0].ev.VariantAmbiguity; !strings.Contains(got, "STGTS1 (A)") || !strings.Contains(got, "STGTS1 (C)") {
		t.Errorf("VariantAmbiguity = %q, want both variants named", got)
	}
}

// TestResolveVariantAmbiguity_LeavesAVariantSpelledOutInBC: when BC's code does
// carry the grade, it really does identify the line and keeps its match.
func TestResolveVariantAmbiguity_LeavesAVariantSpelledOutInBC(t *testing.T) {
	all := variantOrder("STGTS1")
	all[1].bc.Detail = &ItemDetail{ItemNo: "IT0233835", VendorItemNo: "STGTS1C", VendorNo: "VX00099"}
	resolveVariantAmbiguity(all, cfg())

	if !all[1].ev.CodeMatch {
		t.Error("pairing 1: match withdrawn even though BC's code spells out the C grade")
	}
	if all[0].ev.CodeMatch {
		t.Error("pairing 0: match kept even though BC holds only the family code for it")
	}
}

// TestResolveVariantAmbiguity_LoneVariantIsUnambiguous guards against
// over-reach. Ambiguity is a property of the candidate set: with one STGTS1
// line on the order there is nothing to confuse it with.
func TestResolveVariantAmbiguity_LoneVariantIsUnambiguous(t *testing.T) {
	all := variantOrder("STGTS1")[:1]
	resolveVariantAmbiguity(all, cfg())
	if !all[0].ev.CodeMatch {
		t.Error("match withdrawn from the only STGTS1 line on the order")
	}
	if all[0].ev.VariantAmbiguity != "" {
		t.Errorf("VariantAmbiguity = %q, want empty", all[0].ev.VariantAmbiguity)
	}
}

func TestResolveVariantAmbiguity_DifferentBasesDoNotCompete(t *testing.T) {
	all := variantOrder("STGTS1")
	all[1].poa = POAProduct{Code: "HANCLR (C)", Qty: 1}
	resolveVariantAmbiguity(all, cfg())
	for i, p := range all {
		if !p.ev.CodeMatch {
			t.Errorf("pairing %d: match withdrawn, but STGTS1 and HANCLR are different products", i)
		}
	}
}

// TestResolveCodeAmbiguity_OneCodeOnTwoItems: the same code on two BC items is
// a data error CaseysItems cannot rule out. Neither may claim identity.
func TestResolveCodeAmbiguity_OneCodeOnTwoItems(t *testing.T) {
	item := func(no string) BCProduct {
		return BCProduct{LineID: "bc-" + no, ItemNo: no, Description: "Something",
			Detail: &ItemDetail{ItemNo: no, ModelNo: "CODE123456"}}
	}
	all := []pairing{
		{poaIndex: 0, bcIndex: 0, poa: POAProduct{Code: "Widget CODE123456"}, bc: item("ITEM-A"),
			ev: MatchEvidence{CodeMatch: true, CodeMatchSource: "Model_No"}},
		{poaIndex: 0, bcIndex: 1, poa: POAProduct{Code: "Widget CODE123456"}, bc: item("ITEM-B"),
			ev: MatchEvidence{CodeMatch: true, CodeMatchSource: "Model_No"}},
	}
	resolveCodeAmbiguity(all)

	for i, p := range all {
		if p.ev.CodeMatch {
			t.Errorf("pairing %d kept its code match; a code on two items identifies neither", i)
		}
		if p.ev.CodeAmbiguousWith == "" {
			t.Errorf("pairing %d: CodeAmbiguousWith empty, want the collision named", i)
		}
	}
}
