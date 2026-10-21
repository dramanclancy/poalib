// Vendors whose printed product code is trusted to match BC's Vendor_Item_No.
//
// THIS IS THE LIST TO EDIT. To add a vendor, add a line to VendorItemNoVendors
// below with its BC vendor number and name. Nothing else needs to change.
//
// This is a deliberate, owner-approved exception to "no vendor-keyed tables"
// (CLAUDE.md rule 6). The general rule ignores codes shorter than MinCodeLen
// (6), so short catalogue codes such as Baker's MV07, AS11 or KD29 never count
// as identity, and those lines are judged on description alone — which is
// where most of Baker's false description flags came from. For a vendor on
// this list, the code the supplier prints in its Product column counts as
// identity when it is EXACTLY BC's Vendor_Item_No for an item of the same
// vendor, at any length from MinProductCodeLen (4) up.
//
// Exact only. A prefix ("C288" inside "C288-R-MISTY") is never enough: on the
// captured corpus a family code prefixing a handed variant pointed at the
// wrong hand. Ambiguity rules still apply: a code matching two BC lines claims
// neither.
//
// Corpus evidence when the list was introduced (2026-10-20, 281 orders): 85
// new exact matches, 32 description flags cleared, 11 more orders accepted
// without review, no false pairings. The two pairings it changed (PF131596,
// MR01/MR03 bedsteads) corrected a 135cm/180cm swap.

package domain

import "strings"

// VendorEntry names one vendor on the list. VendorNo is what matches: it is
// BC's vendor number and is stable. Name is for the person reading this file,
// and is used to match only when VendorNo is left blank.
type VendorEntry struct {
	VendorNo string
	Name     string
}

// VendorItemNoVendors is the list. Add vendors here.
var VendorItemNoVendors = []VendorEntry{
	{VendorNo: "VX00006", Name: "Baker Ltd."},
	{VendorNo: "VX00019", Name: "Tempur"},
	{VendorNo: "VX01568", Name: "Egoitaliano"},
	{VendorNo: "VX01614", Name: "X8 Soluções, Lda"},
}

// trustsVendorItemNo reports whether an order's vendor is on the list.
func trustsVendorItemNo(vendorNo, vendorName string) bool {
	for _, v := range VendorItemNoVendors {
		switch {
		case v.VendorNo != "":
			if strings.EqualFold(strings.TrimSpace(v.VendorNo), strings.TrimSpace(vendorNo)) {
				return true
			}
		case v.Name != "":
			if strings.EqualFold(strings.TrimSpace(v.Name), strings.TrimSpace(vendorName)) {
				return true
			}
		}
	}
	return false
}

// listedVendorItemNoMatch reports whether, for a listed vendor, the POA
// Product column is exactly this BC item's Vendor_Item_No and the item
// belongs to the order's vendor.
func listedVendorItemNoMatch(poa POAProduct, bc BCProduct, vendorNo string, cfg Config) bool {
	if bc.Detail == nil || vendorNo == "" || bc.Detail.VendorNo != vendorNo {
		return false
	}
	return vendorItemNoMatch(poa.Code, bc, cfg) == "exact"
}
