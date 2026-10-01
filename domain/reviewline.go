// The Review sheet's view of a result: one flat row per flagged pair,
// carrying everything a person needs to act on it, with no reference back to
// the objects it came from.
//
// These rows are a report and nothing more. They are not queued, posted or
// stored anywhere, and no later reconciliation reads them: every run decides
// from the two orders in front of it alone.

package domain

// ReviewLine is one flagged pair, flattened for the workbook's Review sheet.
type ReviewLine struct {
	POADescription string `json:"poaDescription"`
	// BCDescription is the merged text the engine actually compared, never
	// the raw purchase order line.
	BCDescription string `json:"bcDescription"`
	// Discrepancies is every reason this line needs a human, as separate
	// typed entries, one Review sheet row each.
	Discrepancies []Discrepancy `json:"discrepancies"`

	// Identifiers — without these a row cannot be traced back to the line it
	// came from.
	PF            string `json:"pf"`
	VendorNo      string `json:"vendorNo"`
	VendorName    string `json:"vendorName"`
	BCLineID      string `json:"bcLineId"`
	BCItemNo      string `json:"bcItemNo"`
	POALineIndex  int    `json:"poaLineIndex"`
	EngineVersion string `json:"engineVersion"`
	// LineGroup describes the split or aggregated representation this pair
	// belongs to, empty for an ordinary pair. A reviewer needs it to read the
	// combined quantity and net figures on the row.
	LineGroup string `json:"lineGroup,omitempty"`

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
		LineGroup:        groupText(p.Group),
		DescScore:        orZero(p.Description.SimilarityScore),
		DescMargin:       orZero(p.Description.Margin),
		CodeMatchSource:  p.Description.CodeMatchSource,
		CodeDataState:    string(p.Description.CodeDataState),
		POACodeCandidate: p.Description.POACode,
		SeatDataState:    string(p.Seats.DataState),
		POAQty:           p.Quantity.POAQty,
		BCQty:            p.Quantity.BCQty,
		POANet:           p.Price.POAPrice,
		BCNet:            p.Price.BCPrice,
	}
}

func groupText(g *LineGroup) string {
	if g == nil {
		return ""
	}
	return g.String()
}
