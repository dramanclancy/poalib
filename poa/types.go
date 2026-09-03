// Package poa is the POA (Purchase Order Acknowledgement) reconciliation
// domain: extracting order lines from a Document Intelligence analysis,
// matching them against a Business Central purchase order, verifying price,
// quantity and handedness, and rendering the result.
package poa

import "github.com/dramanclancy/poalib/businesscentral"

// Products is one extracted purchase order acknowledgement (one PF/PO number
// worth of lines), as read off a supplier's document.
type Products struct {
	// PF is the "Purchase Order No." field DocIntel extracts — the PO number
	// used to look the order up in Business Central.
	PF                   string        `json:"PF"`
	TotalAmountexVAT     *float64      `json:"total amount exVAT"`
	ProposedDeliveryDate string        `json:"proposed delivery date"`
	ReferenceNumber      string        `json:"reference number"`
	Products             []OrderDetail `json:"products"`
}

type OrderDetail struct {
	Product              string   `json:"Product"`
	Description          string   `json:"Description"`
	Description2         string   `json:"Description 2"`
	Description3         string   `json:"Description 3"`
	Qty                  int      `json:"Qty"`
	UnitProductPrice     float64  `json:"Unit Price"`
	DiscountPercent      float64  `json:"Discount %"`
	DiscountFigure       float64  `json:"Discount Amount"`
	TotalProductQtyPrice *float64 `json:"Total Product Qty Price"`
}

// BCLineGroup is a BC "Item"/"Account" line plus the "Comment" lines that
// qualify it (fabric, bolster, legs, orientation, ...), produced by
// GroupBCLines.
type BCLineGroup struct {
	Item         businesscentral.PurchaseOrderLine
	Comments     []businesscentral.PurchaseOrderLine
	CombinedDesc string

	// Detail is the item's CaseysItems enrichment (Model_No, Vendor_Item_No,
	// Range_Code, ...), nil when the line has no item (Account/G-L/Fixed
	// Asset/Resource lines) or the lookup failed. Every read must nil-check —
	// a failed enrichment call degrades to Phase 1 behaviour, it never fails
	// the order.
	Detail *businesscentral.CaseysItem
}

// LineMatch pairs one POA line with one BC line group and records both why
// they were paired (identity) and whether the supplier's figures agree with
// BC's (verification, see FullyVerified).
type LineMatch struct {
	POA OrderDetail
	BC  BCLineGroup

	// Identity — why these two were paired.
	Score        float64
	DescScore    float64 // cleaned overlap: identity signal, drives assignment
	DescScoreRaw float64 // uncleaned overlap, kept for audit / tuning
	DescMargin   float64 // DescScore minus the best DescScore this POA line
	// achieves against any OTHER BC group
	DescMarginNA bool   // only one candidate group; margin is meaningless
	DescRunnerUp string // label of the BC group DescMargin was measured against
	PackRatio    int    // 1 = same unit; >1 = supplier sells packs of this size

	// CodeMatch is exact-code identity: squash(POA text) contains
	// squash(item.Model_No) or squash(item.Vendor_Item_No), with the guards
	// in vocab.go's codeMatch (minimum length, vendor scoping, ambiguity).
	// It beats fuzzy description scoring outright in IdentityConfident, so a
	// vendor's own catalogue code printed on the acknowledgement identifies
	// the line even when the surrounding text reads nothing like BC's.
	CodeMatch       bool
	CodeMatchSource string // "Model_No" / "Vendor_Item_No" / "both"; empty if no code match
	// CodeAmbiguousWith is set when a code match was found but suppressed
	// because another BC group in the same POA also code-matched — names
	// that other group for the review sheet. CodeMatch is false whenever
	// this is non-empty.
	CodeAmbiguousWith string
	// CodeVariantAmbiguous is set when a code match was withdrawn because the
	// code names a product family while the order carries more than one
	// variant of it — Ashwood's STGTS1 (A) and STGTS1 (C), which resolve to
	// different items — and BC holds no code that tells the variants apart.
	// Holds a ready phrase naming the competing variants. CodeMatch is false
	// whenever this is non-empty. See resolveVariantAmbiguity.
	CodeVariantAmbiguous string

	// Verification — whether the pairing agrees.
	NetOK         bool
	QtyOK         bool
	InternalOK    bool
	OrientationOK bool
	// SeatsOK is true whenever SeatsNA is true — "a check that cannot be
	// performed passes", same convention as InternalOK/InternalNA.
	SeatsOK bool
	SeatsNA bool

	NetDelta     float64 // POA net minus BC net; signed, for the review sheet
	POAOrient    Orientation
	BCOrient     Orientation
	OrientReason string
	InternalNA   bool // supplier prints no line total; nothing to check
	// POASeats/BCSeats are only meaningful when !SeatsNA — the seat counts
	// SeatsOK actually compared, kept for the review sheet.
	POASeats float64
	BCSeats  float64

	poaIndex int
}

// Review is the full result of reconciling one acknowledgement against one
// BC purchase order.
type Review struct {
	Extraction   Products                       `json:"extraction"`
	BCOrder      *businesscentral.PurchaseOrder `json:"bcOrder"`
	Matches      []LineMatch                    `json:"matches"`
	Unmatched    []OrderDetail                  `json:"unmatched"`
	UnmatchedBC  []BCLineGroup
	Verification OrderVerification `json:"verification"`
	WriteOK      bool              `json:"writeOK"`
	ReviewLines  []ReviewLine      `json:"reviewLines,omitempty"`
}

type ReviewLine struct {
	POADescription string `json:"poaDescription"`
	BCDescription  string `json:"bcDescription"`
	// Discrepancies is every reason this line needs a human, kept as separate
	// typed entries rather than joined prose. Phase 2 turns each one into its
	// own review-queue row so a verdict applies to exactly one reason: a
	// reviewer approving "descriptions look different; net differs by £24"
	// would otherwise pardon both, and the second must never be pardonable.
	Discrepancies []Discrepancy `json:"discrepancies"`

	// Identifiers — without these a row on the Review sheet cannot be traced
	// back to the order/line it came from, so feedback recorded against it is
	// unattributable. Prerequisite for the Phase 2 feedback endpoint.
	PF            string `json:"pf"`
	VendorNo      string `json:"vendorNo"`
	VendorName    string `json:"vendorName"`
	BCLineID      string `json:"bcLineId"`     // PurchaseOrderLine.ID
	BCItemNo      string `json:"bcItemNo"`     // PurchaseOrderLine.LineObjectNumber
	POALineIndex  int    `json:"poaLineIndex"` // index into the extracted POA product list
	EngineVersion string `json:"engineVersion"`
	RowHash       string `json:"rowHash"` // stable hash of the identifying fields

	// Carried for display only — never fold back into identity scoring.
	DescScore  float64 `json:"descScore"`  // so a reviewer sees when the engine leaned on memory vs. evidence
	DescMargin float64 `json:"descMargin"`
	// CodeMatchSource audits which code matched ("Model_No" / "Vendor_Item_No" / "both" / "").
	CodeMatchSource string `json:"codeMatchSource"`
	// POACodeCandidate is the rules lookup key for Stage 3: the product code
	// found in the POA's own text, independent of whether it matched anything.
	POACodeCandidate string `json:"poaCodeCandidate"`
	// POAQty/BCQty/POANet/BCNet must show on the card even when the
	// discrepancy is not about money or quantity — a reviewer confirming
	// identity should still see the figures they are not being asked about.
	POAQty float64 `json:"poaQty"`
	BCQty  float64 `json:"bcQty"`
	POANet float64 `json:"poaNet"`
	BCNet  float64 `json:"bcNet"`
}
