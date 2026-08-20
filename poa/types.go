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
}

// LineMatch pairs one POA line with one BC line group and records both why
// they were paired (identity) and whether the supplier's figures agree with
// BC's (verification, see FullyVerified).
type LineMatch struct {
	POA OrderDetail
	BC  BCLineGroup

	// Identity — why these two were paired.
	Score     float64
	DescScore float64
	PackRatio int // 1 = same unit; >1 = supplier sells packs of this size

	// Verification — whether the pairing agrees.
	NetOK         bool
	QtyOK         bool
	InternalOK    bool
	OrientationOK bool

	NetDelta     float64 // POA net minus BC net; signed, for the review sheet
	POAOrient    Orientation
	BCOrient     Orientation
	OrientReason string
	InternalNA   bool // supplier prints no line total; nothing to check

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
	POADescription string   `json:"poaDescription"`
	BCDescription  string   `json:"bcDescription"`
	Discrepancies  []string `json:"discrepancies"`
}
