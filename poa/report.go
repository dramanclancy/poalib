package poa

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// BuildExcelBytes renders the review as an xlsx workbook in memory.
func (r *Review) BuildExcelBytes() ([]byte, error) {

	f := excelize.NewFile()
	defer func(f *excelize.File) {
		err := f.Close()
		if err != nil {

		}
	}(f)
	redFill, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})

	_ = f.SetSheetName("Sheet1", "Summary")
	_, _ = f.NewSheet("Matches")
	_, _ = f.NewSheet("Unmatched POA")
	if len(r.ReviewLines) > 0 {
		_, _ = f.NewSheet("Review")
	}
	_, _ = f.NewSheet("BC Lines")

	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	header := func(sheet string, cols ...interface{}) {
		_= f.SetSheetRow(sheet, "A1", &cols)
		last, _ := excelize.ColumnNumberToName(len(cols))
		_= f.SetCellStyle(sheet, "A1", last+"1", bold)
		_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}

	// ---- Summary: review + OrderVerification fields, one row ----
	header("Summary",
		"PF", "Vendor", "Ack Date", "Reference No", "BCStatus", "WriteOK",
		"POATotal", "Verification Total", "BCTotal", "MatchedTotal", "TotalExVATOK",
		"LinesMatched", "LinesUnmatched", "FlaggedLines")
	_ = f.SetSheetRow("Summary", "A2", &[]interface{}{
		r.Extraction.PF, r.BCOrder.VendorName, r.Extraction.ProposedDeliveryDate, r.Extraction.ReferenceNumber, r.BCOrder.Status, r.WriteOK,
		r.Verification.POATotal, r.Verification.TotalSource, r.Verification.BCTotal, r.Verification.MatchedTotal,
		r.Verification.TotalExVATOK,
		r.Verification.LinesMatched, r.Verification.LinesUnmatched, len(r.ReviewLines),
	})

	if r.Verification.TotalExVATOK == false {
		_= f.SetCellStyle("Summary", "K2", "K2", redFill)
	}

	// ---- Matches: every OrderDetail / BC field as its own column ----
	header("Matches",
		// POA side (OrderDetail)
		"POA Product", "POA Description", "POA Description 2", "POA Description 3",
		"POA Qty", "POA Unit Price", "POA Discount %", "POA Discount Amount", "POA Line Total",
		// BC side (PurchaseOrderLine)
		"BC ItemNo", "BC Description", "BC Qty", "BC Unit Cost", "BC Disc %", "BC Disc Amt", "BC NetAmount",
		// match metadata (LineMatch)
		"Score", "DescScore", "DescScoreRaw", "DescMargin", "IdentityConfident", "PackRatio", "NetOK", "QtyOK", "InternalOK", "FullyVerified",
		"CodeMatch", "CodeMatchSource", "SeatsOK")
	for i, m := range r.Matches {
		row := i + 2
		// Absent line total -> blank cell, not a pointer address, not 0.
		var lineTotal interface{}
		if total, ok := Deref(m.POA.TotalProductQtyPrice); ok {
			lineTotal = total
		}

		_ = f.SetSheetRow("Matches", fmt.Sprintf("A%d", row), &[]interface{}{
			m.POA.Product, m.POA.Description, m.POA.Description2, m.POA.Description3,
			m.POA.Qty, m.POA.UnitProductPrice, m.POA.DiscountPercent, m.POA.DiscountFigure,
			lineTotal, // <- was m.POA.TotalProductQtyPrice (the pointer)

			m.BC.Item.LineObjectNumber, m.BC.Item.Description, m.BC.Item.Quantity,
			m.BC.Item.DirectUnitCost, m.BC.Item.DiscountPercent, m.BC.Item.DiscountAmount,
			m.BC.Item.NetAmount,

			m.Score, m.DescScore, m.DescScoreRaw, m.DescMargin, m.IdentityConfident(), m.PackRatio,
			m.NetOK, m.QtyOK, m.InternalOK, m.FullyVerified(),
			m.CodeMatch, m.CodeMatchSource, m.SeatsOK,
		})

		cell := fmt.Sprintf("W%d", row)
		if !m.NetOK {
			_ = f.SetCellStyle("Matches", cell, cell, redFill)
		}

		cell = fmt.Sprintf("X%d", row)
		if !m.QtyOK {
			_ = f.SetCellStyle("Matches", cell, cell, redFill)
		}

		cell = fmt.Sprintf("Y%d", row)
		if !m.InternalOK {
			_ = f.SetCellStyle("Matches", cell, cell, redFill)
		}
	}

	// ---- Unmatched: raw OrderDetail fields ----
	header("Unmatched POA",
		"Product", "Description", "Description 2", "Description 3",
		"Qty", "Unit Price", "Discount %", "Discount Amount", "Line Total")
	for i, d := range r.Unmatched {
		// Absent line total -> blank cell, not a pointer address, not 0.
		var lineTotal interface{}
		if total, ok := Deref(d.TotalProductQtyPrice); ok {
			lineTotal = total
		}
		_ = f.SetSheetRow("Unmatched POA", fmt.Sprintf("A%d", i+2), &[]interface{}{
			d.Product, d.Description, d.Description2, d.Description3,
			d.Qty, d.UnitProductPrice, d.DiscountPercent, d.DiscountFigure, lineTotal,
		})
	}

	// ---- Review: ReviewLine fields ----
	// Registered as a real Excel Table (not just a header + rows): Graph's
	// "list rows present in a table" action, which the Phase 2 feedback
	// endpoint reads this sheet through, requires one.
	//
	// One row per DISCREPANCY, not per line. A line flagged for two reasons
	// gets two rows, differing only in DiscrepancyType and Discrepancy, so a
	// verdict recorded against a row applies to exactly one reason. Joining
	// them into a cell — as v6.2 did — makes "approved" ambiguous on any line
	// carrying both an identity doubt and a price difference, and the second
	// of those must never be pardonable. This mirrors the SharePoint List
	// schema Phase 2 stage 1 writes, so the two surfaces stay a direct
	// translation of each other.
	if len(r.ReviewLines) > 0 {
		reviewCols := []interface{}{
			"PF", "VendorNo", "VendorName", "POADescription", "BCDescription",
			"DiscrepancyType", "Discrepancy",
			"BCLineID", "BCItemNo", "POALineIndex", "EngineVersion", "RowHash",
		}
		header("Review", reviewCols...)

		row := 2
		for _, rl := range r.ReviewLines {
			// A ReviewLine is only built for a line that failed something, so
			// an empty list should not occur. Emit a row with a blank reason
			// rather than dropping it: a silently missing line is worse than a
			// visibly unexplained one.
			reasons := rl.Discrepancies
			if len(reasons) == 0 {
				reasons = []Discrepancy{{}}
			}
			for _, d := range reasons {
				_ = f.SetSheetRow("Review", fmt.Sprintf("A%d", row), &[]interface{}{
					rl.PF, rl.VendorNo, rl.VendorName, rl.POADescription, rl.BCDescription,
					string(d.Kind), d.Message,
					rl.BCLineID, rl.BCItemNo, rl.POALineIndex, rl.EngineVersion, rl.RowHash,
				})
				row++
			}
		}

		lastCol, _ := excelize.ColumnNumberToName(len(reviewCols))
		if err := f.AddTable("Review", &excelize.Table{
			Range: fmt.Sprintf("A1:%s%d", lastCol, row-1),
			Name:  "ReviewTable",
		}); err != nil {
			return nil, fmt.Errorf("add review table: %w", err)
		}
	}

	// ---- BC Lines: raw PurchaseOrderLine fields ----
	header("BC Lines",
		"Sequence", "LineType", "ItemNo", "Description",
		"Qty", "UnitCost", "Disc %", "DiscAmt", "NetAmount")
	for i, l := range r.UnmatchedBC {
		_ = f.SetSheetRow("BC Lines", fmt.Sprintf("A%d", i+2), &[]interface{}{
			l.Item.Sequence, l.Item.LineType, l.Item.LineObjectNumber, l.Item.Description,
			l.Item.Quantity, l.Item.DirectUnitCost, l.Item.DiscountPercent, l.Item.DiscountAmount, l.Item.NetAmount,
		})
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("build excel: %w", err)
	}
	return buf.Bytes(), nil
}
