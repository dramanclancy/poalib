package excel

import (
	"fmt"

	"github.com/dramanclancy/poalib/domain"
	"github.com/xuri/excelize/v2"
)

// definition is one row of the data dictionary.
type definition struct {
	Sheet    string
	Column   string
	Meaning  string
	Source   string
	Calc     string
	Decision string
}

// writeDefinitions builds the data dictionary.
//
// The Matches rows are generated from the very column specs the sheet is
// written with, so a column cannot exist without an explanation and an
// explanation cannot survive its column being renamed. The rest are written
// out here because those sheets carry plain values rather than derived ones.
func writeDefinitions(f *excelize.File, st styles, cfg domain.Config, cols []column) {
	_ = f.SetCellValue(sheetDefinitions, "A1", "What every column in this workbook means")
	_ = f.SetCellStyle(sheetDefinitions, "A1", "A1", st.title)
	_ = f.SetCellValue(sheetDefinitions, "A2",
		"Written for somebody who did not build the engine. Every calculated column says where its data comes from, how the number is produced, and what it changes about the decision.")

	headers := []interface{}{"Sheet", "Column", "What it means", "What data it uses", "How it is calculated", "What it changes"}
	_ = f.SetSheetRow(sheetDefinitions, "A4", &headers)
	last, _ := excelize.ColumnNumberToName(len(headers))
	_ = f.SetCellStyle(sheetDefinitions, "A4", last+"4", st.bold)
	_ = f.SetPanes(sheetDefinitions, &excelize.Panes{Freeze: true, YSplit: 4, TopLeftCell: "A5", ActivePane: "bottomLeft"})

	_ = f.SetColWidth(sheetDefinitions, "A", "A", 16)
	_ = f.SetColWidth(sheetDefinitions, "B", "B", 30)
	_ = f.SetColWidth(sheetDefinitions, "C", "F", 62)

	defs := make([]definition, 0, len(cols)+40)
	for _, c := range cols {
		defs = append(defs, definition{
			Sheet: sheetMatches, Column: c.Header,
			Meaning: c.Meaning, Source: c.Source, Calc: c.Calc, Decision: c.Decision,
		})
	}
	defs = append(defs, otherDefinitions(cfg)...)

	row := 5
	for _, d := range defs {
		_ = f.SetSheetRow(sheetDefinitions, fmt.Sprintf("A%d", row), &[]interface{}{
			d.Sheet, d.Column, d.Meaning, d.Source, d.Calc, d.Decision,
		})
		_ = f.SetCellStyle(sheetDefinitions, cell(3, row), cell(6, row), st.wrap)
		_ = f.SetRowHeight(sheetDefinitions, row, 58)
		row++
	}
}

// otherDefinitions documents the sheets whose columns are not generated from a
// spec: the concepts on Summary, and the Review sheet's own fields.
func otherDefinitions(cfg domain.Config) []definition {
	return []definition{
		{
			Sheet: sheetSummary, Column: "Accepted without review",
			Meaning:  "Whether this acknowledgement can be accepted with no human involvement.",
			Source:   "Every check on every matched line, plus the count of acknowledged lines that could not be paired.",
			Calc:     "TRUE when no check returned mismatch on any line AND every acknowledged line was paired. Warnings and \"no data\" results do not block.",
			Decision: "The whole point of the report. Note what it deliberately ignores: order lines the supplier never acknowledged, and a disagreement about the order total. Both are reported but neither blocks.",
		},
		{
			Sheet: sheetSummary, Column: "Reconciled against",
			Meaning:  "A different purchase order number from the one printed on the acknowledgement.",
			Source:   "The run record, set when the printed number found nothing in Business Central.",
			Calc:     "The exact printed number is always tried first. Only when Business Central answers that no such order exists is the part before the first \"/\" tried, because suppliers annotate the number they print (\"PF126282/EXC\" answers PF126282).",
			Decision: "Appears only when the fallback was used. Everything in the workbook describes the order named here, not the printed one, so confirm it is the right order before acting on the verdict.",
		},
		{
			Sheet: sheetSummary, Column: "Data completeness",
			Meaning:  "Whether this comparison ran with everything it needs, or in a degraded state.",
			Source:   "The run record: whether item enrichment was requested, what it returned, and which embedding model was used.",
			Calc:     "Degraded when enrichment was skipped or failed, when it returned fewer items than requested, or when no returned item carried a seat count or a supplier code.",
			Decision: "A degraded run can still be correct, but its silences mean less: a seat check that never ran is not evidence that seat counts agree. Read this before trusting a clean result.",
		},
		{
			Sheet: sheetSummary, Column: "Items with a seat count",
			Meaning:  "How many enriched items carried a seat count.",
			Source:   "No_of_Seats on each item record returned by the lookup.",
			Calc:     "Counted after enrichment. A record with no value is not counted; zero and absent are treated as different.",
			Decision: "Normally zero — No_of_Seats is populated on a small minority of items — and that does NOT mean the seat checks did not run: they fall back to the seat count in BC's line description. Read Seat Data State per line. Zero here is a master-data question, not a matching failure.",
		},
		{
			Sheet: sheetSummary, Column: "Items with a supplier code",
			Meaning:  "How many enriched items carried a Model_No or Vendor_Item_No.",
			Source:   "The item records returned by the lookup.",
			Calc:     "Counted after enrichment.",
			Decision: "Zero here means identity rested entirely on description similarity for every line. Populating Vendor_Item_No in BC is the single change that most improves matching.",
		},
		{
			Sheet: sheetSummary, Column: "Totals agree",
			Meaning:  "Whether the supplier's order total matches the purchase order's.",
			Source:   "The POA total (printed, or summed from lines) and BC's total excluding tax.",
			Calc:     fmt.Sprintf("Compared within %.2f — tighter than the per-line tolerance, because the total is one figure both sides printed rather than a sum that accumulates rounding.", cfg.OrderTolerance),
			Decision: "Reported for information. It does NOT affect acceptance: a supplier that omits a surcharge line will fail this while every line still agrees.",
		},
		{
			Sheet: sheetSummary, Column: "Fixture capture enabled",
			Meaning:  "Whether this comparison was saved to disk so it can be replayed offline.",
			Source:   "Service configuration.",
			Calc:     "Set by the CAPTURE_FIXTURES setting. When on, the built orders and the result are written as a JSON snapshot after the comparison.",
			Decision: "Nothing about this order. Captured runs are what let thresholds be re-tuned and regressions caught without calling Business Central or the embedding service again.",
		},

		{
			Sheet: sheetReview, Column: "DiscrepancyType",
			Meaning:  "A stable identifier for the kind of problem found.",
			Source:   "Whichever check failed.",
			Calc:     "One row per reason, never several reasons joined into one cell — so a verdict recorded against a row applies to exactly one problem.",
			Decision: "What to sort and filter on. A line failing two checks appears twice, and each can be answered separately.",
		},
		{
			Sheet: sheetReview, Column: "Discrepancy",
			Meaning:  "The explanation a reviewer reads.",
			Source:   "The failing check's own message.",
			Calc:     "Generated by the check itself, so the Matches and Review sheets can never word the same problem differently.",
			Decision: "For humans. Nothing in the system branches on this text, so it can be reworded freely.",
		},
		{
			Sheet: sheetReview, Column: "LineGroup",
			Meaning:  "The split or aggregated group this pair belongs to, if any.",
			Source:   "The same grouping as the Line Group column on Matches.",
			Calc:     "Blank for an ordinary one-to-one pair.",
			Decision: "When set, the quantity and net figures on the row are the group's combined figures, not this line's alone.",
		},

		{
			Sheet: sheetBCLines, Column: "Line Description (raw, not compared)",
			Meaning:  "The purchase order line's own free text.",
			Source:   "Business Central.",
			Calc:     "Taken verbatim.",
			Decision: "Shown for context only. The engine compares the merged description in the next column — the two differ, and confusing them makes a score look wrong when it is not.",
		},
		{
			Sheet: sheetBCLines, Column: "Merged Description (what would have been compared)",
			Meaning:  "The text this line would have been matched on, had the supplier acknowledged it.",
			Source:   "Item display names, item enrichment, and the line's comment lines.",
			Calc:     "Built exactly as for matched lines.",
			Decision: "Lets a reviewer see why an order line went unacknowledged, or judge whether it should have matched something.",
		},
		{
			Sheet: sheetUnmatchedPOA, Column: "Description (as extracted)",
			Meaning:  "The full supplier text for a line that could not be paired with anything on the order.",
			Source:   "The acknowledgement.",
			Calc:     "Joined exactly as it would have been for comparison.",
			Decision: "Any row on this sheet blocks automatic acceptance: the supplier acknowledged something the order does not appear to contain.",
		},
	}
}
