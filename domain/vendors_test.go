package domain

import "testing"

// withVendorList replaces VendorItemNoVendors for one test.
func withVendorList(t *testing.T, list []VendorEntry) {
	t.Helper()
	saved := VendorItemNoVendors
	VendorItemNoVendors = list
	t.Cleanup(func() { VendorItemNoVendors = saved })
}

// bakerOrder is PF130699's shape: Baker prints MV07, BC's Vendor_Item_No is
// MV07, and the descriptions alone score below threshold.
func bakerOrder(code, vin, itemVendor string) (POAOrder, BCOrder) {
	bc := bcProduct("bc-1", "IT0218099", "TV Unit MARLO DINING", 1, 300, 300)
	bc.Detail = &ItemDetail{VendorItemNo: vin, VendorNo: itemVendor}
	return order(
		POAOrder{PF: "PF-BAKER", Products: []POAProduct{{Code: code, Description: "TV UNIT", Qty: 1, UnitPrice: 300}}},
		BCOrder{VendorNo: "VX00006", VendorName: "Baker Ltd.", Enrichment: EnrichmentApplied, Products: []BCProduct{bc}},
	)
}

func TestListedVendor_ShortVendorItemNoIsIdentity(t *testing.T) {
	withVendorList(t, []VendorEntry{{VendorNo: "VX00006", Name: "Baker Ltd."}})
	poa, bc := bakerOrder("MV07", "MV07", "VX00006")

	r := Reconcile(poa, bc, cfg())

	d := r.Products[0].Description
	if !d.CodeMatch || d.CodeMatchSource != "Vendor_Item_No (listed vendor)" || !r.WriteOK {
		t.Errorf("code match %v (%s), WriteOK %v: want MV07 = Vendor_Item_No to identify the line", d.CodeMatch, d.CodeMatchSource, r.WriteOK)
	}
}

func TestListedVendor_MatchesByNameWhenNumberIsBlank(t *testing.T) {
	withVendorList(t, []VendorEntry{{Name: "baker ltd."}})
	poa, bc := bakerOrder("MV07", "MV07", "VX00006")

	if d := Reconcile(poa, bc, cfg()).Products[0].Description; !d.CodeMatch {
		t.Errorf("a name-only entry should match the order's vendor name")
	}
}

func TestUnlistedVendor_ShortCodeStaysDescriptionOnly(t *testing.T) {
	withVendorList(t, nil)
	poa, bc := bakerOrder("MV07", "MV07", "VX00006")

	if d := Reconcile(poa, bc, cfg()).Products[0].Description; d.CodeMatch {
		t.Errorf("an unlisted vendor's 4-character code must not count as identity")
	}
}

func TestListedVendor_PrefixIsNotEnough(t *testing.T) {
	withVendorList(t, []VendorEntry{{VendorNo: "VX00006"}})
	poa, bc := bakerOrder("C288-R-MISTY", "C288", "VX00006")

	if d := Reconcile(poa, bc, cfg()).Products[0].Description; d.CodeMatch {
		t.Errorf("C288 only prefixes C288-R-MISTY; a family code must not identify a handed variant")
	}
}

func TestListedVendor_ItemOfAnotherVendorDoesNotMatch(t *testing.T) {
	withVendorList(t, []VendorEntry{{VendorNo: "VX00006"}})
	poa, bc := bakerOrder("MV07", "MV07", "VX09999")

	if d := Reconcile(poa, bc, cfg()).Products[0].Description; d.CodeMatch {
		t.Errorf("Vendor_Item_No of another vendor's item is that vendor's code, not this one's")
	}
}
