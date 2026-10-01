// Package domain is the POA (Purchase Order Acknowledgement) comparison.
//
// It imports nothing outside the standard library. No HTTP client, no SDK, no
// ERP payload shape reaches in here — adapters translate at the edge, so a
// Business Central API change touches one file and the comparison is testable
// with no credentials and no network.
//
// Four models carry the whole comparison:
//
//	POAProduct   one product line as the supplier printed it
//	BCProduct    one product as Business Central holds it
//	POAOrder     one acknowledgement — order fields plus its POAProducts
//	BCOrder      one purchase order — order fields plus its BCProducts
//
// Each side is built independently and the two are then compared by
// Reconcile, which is a pure function of (POAOrder, BCOrder, Config) and
// returns a Result: one ProductResult per paired line, plus what could not be
// paired, the order totals and the write gate.
//
// # How lines are matched
//
// Product identity and line layout are separate questions. Identity — which BC
// product a supplier line is — is decided by description similarity, exact
// supplier codes and quantity, and never by price. Layout — how many lines
// each side spends on that product — is allowed to differ: a supplier may
// print one line per chair where BC holds one line of 2, or a divan base as
// its two halves. Reconcile first takes the best one-to-one assignment, then
// repairs what that could not express with a LineGroup, whose quantity and
// price are checked on the combined figures.
//
// # How results are verified
//
// Money, quantity, the POA's own arithmetic, seat count and handedness are
// checked on each pair (or group) once it exists. Each check returns a
// CheckStatus; only StatusMismatch blocks, and a check with nothing to compare
// returns StatusUnknown and says why. Result.WriteOK is true only when every
// acknowledged line was paired and every pair passed. Anything less is
// flagged for a human in the report; no verdict a human gives is fed back into
// a later reconciliation.
package domain

import "strings"

// ---------------------------------------------------------------------------
// POA side
// ---------------------------------------------------------------------------

// POAProduct is one product line as read off the supplier's acknowledgement.
type POAProduct struct {
	// Code is the supplier's own product reference, where it prints one. Some
	// suppliers print a SKU here (NFX2S (A)), others print prose.
	Code            string  `json:"code"`
	Description     string  `json:"description"`
	Description2    string  `json:"description2"`
	Description3    string  `json:"description3"`
	Qty             int     `json:"qty"`
	UnitPrice       float64 `json:"unitPrice"`
	DiscountPercent float64 `json:"discountPercent"`
	DiscountAmount  float64 `json:"discountAmount"`

	// LineTotal is the total the supplier printed for the line, or nil when
	// the supplier prints no such column at all (Ashwood). Absent is not
	// zero: treating a missing total as 0.00 previously failed every Ashwood
	// line's arithmetic check, so no Ashwood order could pass the write gate.
	LineTotal *float64 `json:"lineTotal"`

	// Embedding is the vector for EmbeddingText(Text()), attached before
	// Reconcile runs so the comparison itself performs no I/O. Empty means
	// not embedded, which fails the description check closed rather than
	// quietly scoring it zero.
	Embedding []float32 `json:"embedding,omitempty"`
}

// Text is everything the supplier printed about the product, as one string.
func (p POAProduct) Text() string {
	return strings.Join([]string{p.Code, p.Description, p.Description2, p.Description3}, " ")
}

// Net is the line's post-discount value from the POA's own numbers, however
// the supplier expressed the discount.
//
// DiscountPercent and DiscountAmount are two REPRESENTATIONS of one discount,
// mirroring BC's Line Discount % / Line Discount Amount pair — they are never
// additive. The cash figure wins: it is what posts, and it survives rounding
// that a recomputed percentage would not.
func (p POAProduct) Net() float64 {
	gross := float64(p.Qty) * p.UnitPrice
	switch {
	case p.DiscountAmount != 0:
		return gross - p.DiscountAmount
	case p.DiscountPercent != 0:
		return gross * (1 - p.DiscountPercent/100)
	default:
		return gross
	}
}

// UnitNet is the per-unit equivalent of Net, applying the same discount rules
// so the two cannot disagree about what a discount is.
func (p POAProduct) UnitNet() float64 {
	switch {
	case p.DiscountAmount != 0 && p.Qty > 0:
		return p.UnitPrice - p.DiscountAmount/float64(p.Qty)
	case p.DiscountPercent != 0:
		return p.UnitPrice * (1 - p.DiscountPercent/100)
	default:
		return p.UnitPrice
	}
}

// POAOrder is one extracted acknowledgement: one PF worth of lines.
type POAOrder struct {
	// PF is the purchase order number the acknowledgement answers.
	PF                   string       `json:"pf"`
	TotalExVAT           *float64     `json:"totalExVAT"`
	ProposedDeliveryDate string       `json:"proposedDeliveryDate"`
	ReferenceNumber      string       `json:"referenceNumber"`
	Products             []POAProduct `json:"products"`
}

// NetFromLines sums the net of every extracted line — the correct order total
// for a multi-page POA, independent of any per-page printed total.
func (o POAOrder) NetFromLines() float64 {
	var t float64
	for _, p := range o.Products {
		t += p.Net()
	}
	return t
}

// Total is the order's ex-VAT total and where the figure came from.
func (o POAOrder) Total() (float64, string) {
	if o.TotalExVAT != nil {
		return *o.TotalExVAT, "stated on document"
	}
	return o.NetFromLines(), "derived from lines"
}

// ---------------------------------------------------------------------------
// BC side
// ---------------------------------------------------------------------------

// EnrichmentState records what happened to the item-detail lookup for an
// order, so a check that had nothing to compare can say WHY.
//
// This distinction is the whole point: "BC holds no seat count for this item"
// and "we never asked BC about this item" are different sentences, and only
// one of them is a master-data problem a human should act on. Reporting the
// first when the second is true sends someone to inspect a field that may be
// perfectly populated.
type EnrichmentState string

const (
	// EnrichmentNotRequested — no enrichment client was configured, so the
	// lookup never ran. Nothing is known about item detail either way.
	EnrichmentNotRequested EnrichmentState = "not_requested"
	// EnrichmentFailed — the lookup ran and errored. Some products may still
	// carry Detail from a partial batch.
	EnrichmentFailed EnrichmentState = "failed"
	// EnrichmentApplied — the lookup ran and its results were applied. A
	// product with no Detail is one the service holds no record for.
	EnrichmentApplied EnrichmentState = "applied"
)

// Ran reports whether the lookup executed at all.
func (e EnrichmentState) Ran() bool { return e == EnrichmentFailed || e == EnrichmentApplied }

// ItemDetail is the item master data behind a BC line — supplier codes,
// range, seat count.
type ItemDetail struct {
	ItemNo       string `json:"itemNo"`
	ModelNo      string `json:"modelNo"`
	VendorItemNo string `json:"vendorItemNo"`
	VendorNo     string `json:"vendorNo"`
	RangeCode    string `json:"rangeCode"`
	Description3 string `json:"description3"`

	// Seats is the item's seat count. nil means the record carries no seat
	// value at all, which is different from a record that says 0 — and
	// different again from having no record. All three are reported apart.
	Seats *float64 `json:"seats"`
}

// BCProduct is one complete Business Central product: the value-bearing
// purchase order line, the comment lines that qualify it, the description
// built from both, and the item's detail.
type BCProduct struct {
	LineID   string `json:"lineId"`
	ItemNo   string `json:"itemNo"`
	LineType string `json:"lineType"`
	Sequence int    `json:"sequence"`

	// Description is the merged text: item name, item detail, and the comment
	// lines. This is what the comparison actually reads, and therefore the
	// only description a report may present as "compared".
	Description string `json:"description"`
	// DisplayName / DisplayName2 are the item's own names, which read better
	// than the purchase order line's free text and so lead the merge.
	DisplayName  string `json:"displayName"`
	DisplayName2 string `json:"displayName2"`
	// LineDescription is the purchase order line's own free text, kept for
	// the BC Lines sheet. It is NOT what was compared.
	LineDescription string `json:"lineDescription"`
	// Comments are the qualifying comment lines, in sequence order.
	Comments []string `json:"comments"`

	Qty             float64 `json:"qty"`
	UnitCost        float64 `json:"unitCost"`
	DiscountPercent float64 `json:"discountPercent"`
	DiscountAmount  float64 `json:"discountAmount"`
	Net             float64 `json:"net"`

	// Detail is the item's master data, nil when the line has no item
	// (Account/G-L/Fixed Asset/Resource lines), or the lookup returned no
	// record, or it never ran. Read BCOrder.Enrichment to tell those apart.
	Detail *ItemDetail `json:"detail"`

	Embedding []float32 `json:"embedding,omitempty"`
}

// UnitNet is the per-unit net cost, applying BC's line discount percentage.
func (p BCProduct) UnitNet() float64 {
	if p.DiscountPercent != 0 {
		return p.UnitCost * (1 - p.DiscountPercent/100)
	}
	return p.UnitCost
}

// Label names the product for a discrepancy message or a review row,
// preferring the item number since a full Description can be long.
func (p BCProduct) Label() string {
	if p.ItemNo != "" {
		return p.ItemNo
	}
	if len(p.Description) > 40 {
		return p.Description[:40] + "..."
	}
	return p.Description
}

// hasCodeOnFile reports whether BC holds any code for this product that a POA
// product code could have been matched against.
//
// Only meaningful once enrichment has actually run — see codeStatus.
func (p BCProduct) hasCodeOnFile() bool {
	return p.Detail != nil && (p.Detail.VendorItemNo != "" || p.Detail.ModelNo != "")
}

// BCOrder is one Business Central purchase order.
type BCOrder struct {
	PF         string  `json:"pf"`
	ID         string  `json:"id"`
	VendorNo   string  `json:"vendorNo"`
	VendorName string  `json:"vendorName"`
	Status     string  `json:"status"`
	TotalExVAT float64 `json:"totalExVAT"`

	Products []BCProduct `json:"products"`

	// Enrichment says whether item detail was looked up, and EnrichmentNote
	// carries the error when it failed. Checks consult these before claiming
	// anything about master data.
	Enrichment     EnrichmentState `json:"enrichment"`
	EnrichmentNote string          `json:"enrichmentNote,omitempty"`
}

// ---------------------------------------------------------------------------
// Optional values
// ---------------------------------------------------------------------------

// Deref reads an optional float without panicking on nil. ok == false means
// the value is absent, which is not the same as zero and must not be verified
// as though it were.
func Deref(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// Ptr returns the address of v. Each call allocates, so results never alias.
func Ptr[T any](v T) *T { return &v }
