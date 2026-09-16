// The review-queue view of a result: one flat row per flagged reason,
// carrying everything a reviewer and the endpoint recording their verdict
// need, with no reference back to the objects it came from.
//
// The Excel Review sheet and the webhook are both this shape, so the two
// surfaces stay a direct translation of each other.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ReviewLine is one flagged pair, flattened for the review queue.
type ReviewLine struct {
	POADescription string `json:"poaDescription"`
	// BCDescription is the merged text the engine actually compared, never
	// the raw purchase order line.
	BCDescription string `json:"bcDescription"`
	// Discrepancies is every reason this line needs a human, as separate
	// typed entries, so a verdict applies to exactly one reason.
	Discrepancies []Discrepancy `json:"discrepancies"`

	// Identifiers — without these a row cannot be traced back to the line it
	// came from, so feedback recorded against it is unattributable.
	PF            string `json:"pf"`
	VendorNo      string `json:"vendorNo"`
	VendorName    string `json:"vendorName"`
	BCLineID      string `json:"bcLineId"`
	BCItemNo      string `json:"bcItemNo"`
	POALineIndex  int    `json:"poaLineIndex"`
	EngineVersion string `json:"engineVersion"`
	RowHash       string `json:"rowHash"`

	// Carried for display only — never fold these back into identity scoring.
	DescScore  float64 `json:"descScore"`
	DescMargin float64 `json:"descMargin"`
	// CodeMatchSource audits which code matched; CodeDataState says what was
	// actually known about BC's codes, so a reviewer is never told master data
	// is missing when the lookup never ran.
	CodeMatchSource  string `json:"codeMatchSource"`
	CodeDataState    string `json:"codeDataState"`
	POACodeCandidate string `json:"poaCodeCandidate"`
	// SeatDataState says why a seat comparison did or did not happen.
	SeatDataState string `json:"seatDataState"`

	// The figures a reviewer should see even when the discrepancy is about
	// something else.
	POAQty float64 `json:"poaQty"`
	BCQty  float64 `json:"bcQty"`
	POANet float64 `json:"poaNet"`
	BCNet  float64 `json:"bcNet"`
}

// ReviewLines is the flagged half of the result, as review rows.
func (r Result) ReviewLines() []ReviewLine {
	var lines []ReviewLine
	for _, p := range r.Flagged() {
		lines = append(lines, r.reviewLine(p))
	}
	return lines
}

func (r Result) reviewLine(p ProductResult) ReviewLine {
	return ReviewLine{
		POADescription:   p.Description.POADescription,
		BCDescription:    p.Description.BCDescription,
		Discrepancies:    p.Discrepancies(),
		PF:               r.POA.PF,
		VendorNo:         r.BC.VendorNo,
		VendorName:       r.BC.VendorName,
		BCLineID:         p.BC.LineID,
		BCItemNo:         p.BC.ItemNo,
		POALineIndex:     p.POAIndex,
		EngineVersion:    EngineVersion,
		RowHash:          rowHash(r.POA.PF, p.BC.LineID, fmt.Sprint(p.POAIndex)),
		DescScore:        similarityValue(p.Description.SimilarityScore),
		DescMargin:       optional(p.Description.Margin),
		CodeMatchSource:  p.Description.CodeMatchSource,
		CodeDataState:    p.Description.CodeDataState,
		POACodeCandidate: p.Description.POACode,
		SeatDataState:    p.Seats.DataState,
		POAQty:           p.Quantity.POAQty,
		BCQty:            p.Quantity.BCQty,
		POANet:           p.Price.POAPrice,
		BCNet:            p.Price.BCPrice,
	}
}

// optional flattens an absent number to 0 for a queue row, where a numeric
// column has to hold something. The typed result keeps the nil.
func optional(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// rowHash is a stable identifier for a review row, derived from the fields
// that identify what it refers to. It lets a feedback endpoint detect a row
// whose underlying line no longer exists (order re-run, line renumbered)
// without depending on description text, which can legitimately change.
func rowHash(parts ...string) string {
	h := sha256.Sum256([]byte(fmt.Sprint(parts)))
	return hex.EncodeToString(h[:])[:16]
}
