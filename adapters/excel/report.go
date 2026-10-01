// Package excel renders a reconciliation as an xlsx workbook: Summary,
// Matches, Unmatched POA, Review, BC Lines and Definitions.
//
// Presentation only. Every verdict on every sheet is read off the check
// results — nothing here re-compares anything, and no sheet shows a
// description other than the one the engine actually compared.
package excel

import (
	"fmt"

	"github.com/dramanclancy/poalib/app"
	"github.com/dramanclancy/poalib/domain"
	"github.com/xuri/excelize/v2"
)

// Build renders one reconciliation, with the record of how it was produced,
// as an xlsx workbook in memory.
func Build(res domain.Result, run app.RunRecord) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	st, err := newStyles(f)
	if err != nil {
		return nil, err
	}
	cols := matchColumns(res.Config)
	reviewLines := res.ReviewLines()

	_ = f.SetSheetName("Sheet1", sheetSummary)
	for _, name := range []string{sheetMatches, sheetUnmatchedPOA} {
		if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
	}
	if len(reviewLines) > 0 {
		if _, err := f.NewSheet(sheetReview); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{sheetBCLines, sheetDefinitions} {
		if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
	}

	writeSummary(f, st, res, run, len(reviewLines))
	writeMatches(f, st, res, cols)
	writeUnmatchedPOA(f, st, res)
	if len(reviewLines) > 0 {
		if err := writeReview(f, st, reviewLines); err != nil {
			return nil, err
		}
	}
	writeBCLines(f, st, res)
	writeDefinitions(f, st, res.Config, cols)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("build excel: %w", err)
	}
	return buf.Bytes(), nil
}

const (
	sheetSummary      = "Summary"
	sheetMatches      = "Matches"
	sheetUnmatchedPOA = "Unmatched POA"
	sheetReview       = "Review"
	sheetBCLines      = "BC Lines"
	sheetDefinitions  = "Definitions"
)

type styles struct {
	bold    int
	title   int
	warnBox int
	okBox   int
	red     int
	wrap    int
	label   int
}

func newStyles(f *excelize.File) (styles, error) {
	var st styles
	var err error
	if st.bold, err = f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}}); err != nil {
		return st, err
	}
	if st.title, err = f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 13}}); err != nil {
		return st, err
	}
	if st.warnBox, err = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFF2CC"}},
		Font: &excelize.Font{Bold: true, Color: "7F6000"},
	}); err != nil {
		return st, err
	}
	if st.okBox, err = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E2EFDA"}},
		Font: &excelize.Font{Color: "375623"},
	}); err != nil {
		return st, err
	}
	if st.red, err = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	}); err != nil {
		return st, err
	}
	if st.wrap, err = f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}}); err != nil {
		return st, err
	}
	if st.label, err = f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Vertical: "top"}}); err != nil {
		return st, err
	}
	return st, nil
}

// header writes a bold, frozen header row.
func header(f *excelize.File, st styles, sheet string, cols ...interface{}) {
	_ = f.SetSheetRow(sheet, "A1", &cols)
	last, _ := excelize.ColumnNumberToName(len(cols))
	_ = f.SetCellStyle(sheet, "A1", last+"1", st.bold)
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
}

func cell(col, row int) string {
	name, _ := excelize.ColumnNumberToName(col)
	return fmt.Sprintf("%s%d", name, row)
}

// ---------------------------------------------------------------------------
// Summary — the order, the verdict, and the conditions the run happened under
// ---------------------------------------------------------------------------

func writeSummary(f *excelize.File, st styles, res domain.Result, run app.RunRecord, flagged int) {
	_ = f.SetColWidth(sheetSummary, "A", "A", 34)
	_ = f.SetColWidth(sheetSummary, "B", "B", 62)
	_ = f.SetColWidth(sheetSummary, "C", "C", 70)

	row := 1
	put := func(label string, value any, note string) {
		_ = f.SetCellValue(sheetSummary, cell(1, row), label)
		_ = f.SetCellValue(sheetSummary, cell(2, row), value)
		if note != "" {
			_ = f.SetCellValue(sheetSummary, cell(3, row), note)
		}
		_ = f.SetCellStyle(sheetSummary, cell(1, row), cell(1, row), st.label)
		row++
	}
	section := func(title string) {
		row++
		_ = f.SetCellValue(sheetSummary, cell(1, row), title)
		_ = f.SetCellStyle(sheetSummary, cell(1, row), cell(1, row), st.title)
		row++
	}

	_ = f.SetCellValue(sheetSummary, "A1", "Purchase Order Acknowledgement — reconciliation")
	_ = f.SetCellStyle(sheetSummary, "A1", "A1", st.title)
	row = 3

	put("Purchase order", res.POA.PF, "The PO number printed on the acknowledgement.")
	if run.OrderNumberUsed != "" {
		usedRow := row
		put("Reconciled against", run.OrderNumberUsed,
			"Business Central holds no order under the printed number, so the part before the \"/\" was matched instead. Check this is the right order.")
		_ = f.SetCellStyle(sheetSummary, cell(2, usedRow), cell(2, usedRow), st.warnBox)
	}
	put("Supplier", res.BC.VendorName, "")
	put("BC status", res.BC.Status, "")
	put("Acknowledgement date", res.POA.ProposedDeliveryDate, "Proposed delivery date as printed.")
	put("Supplier reference", res.POA.ReferenceNumber, "")

	section("Verdict")
	verdictRow := row
	put("Accepted without review", res.WriteOK,
		"TRUE only when every acknowledged line was paired and every check passed. Unacknowledged BC lines and the order total are deliberately NOT part of this gate.")
	_ = f.SetCellStyle(sheetSummary, cell(2, verdictRow), cell(2, verdictRow), map[bool]int{true: st.okBox, false: st.warnBox}[res.WriteOK])
	put("Lines matched", run.LinesMatched, "Pairs of an acknowledgement line with a purchase order line. A line in a split or aggregated group is counted once per line on the other side.")
	put("Lines in split/aggregated groups", groupedRows(res), "Pairs where the supplier and the order lay one product out as different numbers of lines (see the Line Group column on Matches). Quantity and price are checked on each group's combined figures.")
	put("Lines needing review", flagged, "Lines where at least one check failed. See the Review sheet.")
	put("POA lines not matched", len(res.UnmatchedPOA), "Acknowledged lines with no counterpart on the order. Any of these blocks acceptance.")
	put("BC lines not acknowledged", len(res.UnmatchedBC), "Order lines the supplier never mentioned. Reported, but does not block acceptance.")

	section("Order totals")
	put("POA total (ex VAT)", res.Totals.POATotal, "")
	put("Where that total came from", res.Totals.TotalSource, "\"stated on document\" or, when the supplier prints no total, summed from the lines.")
	put("BC total (ex VAT)", res.Totals.BCTotal, "")
	put("Difference", res.Totals.Difference, "POA total minus BC total.")
	totalsRow := row
	put("Totals agree", !res.Totals.Failed(), fmt.Sprintf("Compared within %.2f. This does not affect acceptance.", res.Totals.Tolerance))
	if res.Totals.Failed() {
		_ = f.SetCellStyle(sheetSummary, cell(2, totalsRow), cell(2, totalsRow), st.red)
	}
	put("Value of matched BC lines", res.Totals.MatchedTotal, "Short of the BC total means part of the order was never acknowledged.")

	section("How this run was produced")
	healthRow := row
	put("Data completeness", run.Summary(), "")
	_ = f.SetCellStyle(sheetSummary, cell(2, healthRow), cell(2, healthRow), map[bool]int{true: st.warnBox, false: st.okBox}[run.Degraded()])
	_ = f.SetCellStyle(sheetSummary, cell(2, healthRow), cell(2, healthRow+40), st.wrap)

	put("Engine version", run.EngineVersion, "Identifies the comparison logic, so results from different runs are never confused.")
	put("Embedding provider and model", run.EmbeddingModel, "Which model produced the description similarity. Scores from different models are not directly comparable.")
	put("Run ID", run.RunID, "")
	put("Started at (UTC)", run.StartedAt.Format("2006-01-02 15:04:05"), "")

	put("BC lines fetched", run.BCLinesFetched, "Purchase order lines returned by Business Central, before grouping.")
	put("BC products built", run.BCProductsBuilt, "Value-bearing lines, each with its comment lines attached.")
	put("POA lines extracted", run.POALinesExtracted, "")
	put("Document pages merged", run.POAPagesMerged, "More than 1 means a multi-page acknowledgement was folded into one order.")

	put("Item enrichment requested", run.EnrichmentAsked, "Whether the item lookup (codes, seat counts) was attempted at all.")
	put("Item enrichment state", run.EnrichmentState, "not_requested, applied, or failed.")
	put("Items requested", run.ItemsRequested, "Distinct BC item numbers on this order.")
	put("Items returned", run.ItemsReturned, "How many of those the lookup found.")
	put("Items with a seat count", run.ItemsWithSeats, "Items whose CaseysItems record carried No_of_Seats. 0 does NOT mean the seat counts agreed — it means the item records are empty, and any seat verdict came from BC's line description instead (Seat Data State says which).")
	put("Items with a supplier code", run.ItemsWithCodes, "0 here means exact-code identity was unavailable on every line.")
	if run.EnrichmentError != "" {
		errRow := row
		put("Item enrichment error", run.EnrichmentError, "")
		_ = f.SetCellStyle(sheetSummary, cell(2, errRow), cell(2, errRow), st.red)
	}

	put("Fixture capture enabled", run.FixturesEnabled, "When on, this comparison was saved so it can be replayed offline.")
	if run.FixturePath != "" {
		put("Fixture written to", run.FixturePath, "")
	}
	if run.FixtureError != "" {
		put("Fixture capture error", run.FixtureError, "Capture failed; the reconciliation itself was unaffected.")
	}

	section("Thresholds used for this run")
	c := res.Config
	put("Description similarity threshold", c.DescThreshold, "Below this, the engine reports that it cannot identify a line from its description.")
	put("Ambiguity margin threshold", c.MarginThreshold, "Minimum lead over the runner-up before a pairing counts as identified.")
	put("Line money tolerance", c.MoneyTolerance, "Price differences smaller than this are treated as rounding.")
	put("Order total tolerance", c.OrderTolerance, "Applied to the order total only.")
	put("Minimum plausible seat count", c.MinSeats, "A BC seat count below this is not treated as a real seat count.")
	put("Minimum code length to match", c.MinCodeLen, "Shorter codes are ignored: they turn up inside unrelated text by chance.")
	put("Code match bonus", c.CodeMatchBonus, "Added to the pairing score when a supplier code matches exactly.")

	if run.Degraded() {
		section("What was missing")
		for _, d := range run.Degradations() {
			_ = f.SetCellValue(sheetSummary, cell(1, row), "•")
			_ = f.SetCellValue(sheetSummary, cell(2, row), d)
			_ = f.SetCellStyle(sheetSummary, cell(2, row), cell(2, row), st.wrap)
			row++
		}
	}
}

// ---------------------------------------------------------------------------
// Matches
// ---------------------------------------------------------------------------

func writeMatches(f *excelize.File, st styles, res domain.Result, cols []column) {
	headers := make([]interface{}, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	header(f, st, sheetMatches, headers...)
	_ = f.SetColWidth(sheetMatches, "C", "C", 46)
	_ = f.SetColWidth(sheetMatches, "K", "K", 46)

	// Which columns get the red fill when their check failed.
	failCol := map[string]func(domain.ProductResult) bool{
		"Price Check":       func(m domain.ProductResult) bool { return m.Price.Failed() },
		"Quantity Check":    func(m domain.ProductResult) bool { return m.Quantity.Failed() },
		"Arithmetic Check":  func(m domain.ProductResult) bool { return m.Arithmetic.Failed() },
		"Orientation Check": func(m domain.ProductResult) bool { return m.Orientation.Failed() },
		"Seat Check":        func(m domain.ProductResult) bool { return m.Seats.Failed() },
		"Description Check": func(m domain.ProductResult) bool { return m.Description.Failed() },
		"Accepted":          func(m domain.ProductResult) bool { return !m.OK() },
	}

	for i, m := range res.Products {
		row := i + 2
		values := make([]interface{}, len(cols))
		for j, c := range cols {
			values[j] = c.Value(m)
		}
		_ = f.SetSheetRow(sheetMatches, fmt.Sprintf("A%d", row), &values)

		for j, c := range cols {
			if fail, ok := failCol[c.Header]; ok && fail(m) {
				ref := cell(j+1, row)
				_ = f.SetCellStyle(sheetMatches, ref, ref, st.red)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Unmatched POA
// ---------------------------------------------------------------------------

func writeUnmatchedPOA(f *excelize.File, st styles, res domain.Result) {
	header(f, st, sheetUnmatchedPOA,
		"Product Code", "Description (as extracted)", "Qty", "Unit Price",
		"Discount %", "Discount Amount", "Line Total (printed)", "Net")
	for i, d := range res.UnmatchedPOA {
		var lineTotal interface{}
		if total, ok := domain.Deref(d.LineTotal); ok {
			lineTotal = total
		}
		_ = f.SetSheetRow(sheetUnmatchedPOA, fmt.Sprintf("A%d", i+2), &[]interface{}{
			d.Code, d.Text(), d.Qty, d.UnitPrice, d.DiscountPercent, d.DiscountAmount, lineTotal, d.Net(),
		})
	}
}

// ---------------------------------------------------------------------------
// Review — one row per discrepancy
// ---------------------------------------------------------------------------

func writeReview(f *excelize.File, st styles, lines []domain.ReviewLine) error {
	cols := []interface{}{
		"PF", "VendorNo", "VendorName", "POADescription", "BCDescription",
		"DiscrepancyType", "Discrepancy",
		"BCLineID", "BCItemNo", "POALineIndex", "LineGroup", "EngineVersion",
		"DescScore", "DescMargin", "CodeDataState", "SeatDataState",
	}
	header(f, st, sheetReview, cols...)

	row := 2
	for _, rl := range lines {
		// A review line is only built for a line that failed something, so an
		// empty list should not occur. Emit a row with a blank reason rather
		// than dropping it: a silently missing line is worse than a visibly
		// unexplained one.
		reasons := rl.Discrepancies
		if len(reasons) == 0 {
			reasons = []domain.Discrepancy{{}}
		}
		for _, d := range reasons {
			_ = f.SetSheetRow(sheetReview, fmt.Sprintf("A%d", row), &[]interface{}{
				rl.PF, rl.VendorNo, rl.VendorName, rl.POADescription, rl.BCDescription,
				string(d.Kind), d.Message,
				rl.BCLineID, rl.BCItemNo, rl.POALineIndex, rl.LineGroup, rl.EngineVersion,
				rl.DescScore, rl.DescMargin, rl.CodeDataState, rl.SeatDataState,
			})
			row++
		}
	}

	// Registered as an Excel Table so a reader can sort and filter it by
	// discrepancy type, supplier or line.
	lastCol, _ := excelize.ColumnNumberToName(len(cols))
	if err := f.AddTable(sheetReview, &excelize.Table{
		Range: fmt.Sprintf("A1:%s%d", lastCol, row-1),
		Name:  "ReviewTable",
	}); err != nil {
		return fmt.Errorf("add review table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// BC Lines — what the order asked for that the POA never mentioned
// ---------------------------------------------------------------------------

func writeBCLines(f *excelize.File, st styles, res domain.Result) {
	header(f, st, sheetBCLines,
		"Sequence", "Line Type", "Item No", "Line Description (raw, not compared)",
		"Merged Description (what would have been compared)", "Qty", "Unit Cost", "Net")
	for i, b := range res.UnmatchedBC {
		_ = f.SetSheetRow(sheetBCLines, fmt.Sprintf("A%d", i+2), &[]interface{}{
			b.Sequence, b.LineType, b.ItemNo, b.LineDescription, b.Description,
			b.Qty, b.UnitCost, b.Net,
		})
	}
}

// groupedRows counts the Matches rows that belong to a line group.
func groupedRows(res domain.Result) int {
	n := 0
	for _, p := range res.Products {
		if p.Group != nil {
			n++
		}
	}
	return n
}
