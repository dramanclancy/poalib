package poa

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// BuildExcelBytes renders the review as an xlsx workbook in memory.
func (r *Review) BuildExcelBytes() ([]byte, error) {

	f := excelize.NewFile()
	defer f.Close()
	redFill, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})

	f.SetSheetName("Sheet1", "Summary")
	f.NewSheet("Matches")
	f.NewSheet("Unmatched POA")
	if len(r.ReviewLines) > 0 {
		f.NewSheet("Review")
	}
	f.NewSheet("BC Lines")

	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	header := func(sheet string, cols ...interface{}) {
		f.SetSheetRow(sheet, "A1", &cols)
		last, _ := excelize.ColumnNumberToName(len(cols))
		f.SetCellStyle(sheet, "A1", last+"1", bold)
		f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}

	// ---- Summary: review + OrderVerification fields, one row ----
	header("Summary",
		"PF", "Vendor", "Ack Date", "Reference No", "BCStatus", "WriteOK",
		"POATotal", "Verification Total", "BCTotal", "MatchedTotal", "TotalExVATOK",
		"LinesMatched", "LinesUnmatched", "FlaggedLines")
	f.SetSheetRow("Summary", "A2", &[]interface{}{
		r.Extraction.PF, r.BCOrder.VendorName, r.Extraction.ProposedDeliveryDate, r.Extraction.ReferenceNumber, r.BCOrder.Status, r.WriteOK,
		r.Verification.POATotal, r.Verification.TotalSource, r.Verification.BCTotal, r.Verification.MatchedTotal,
		r.Verification.TotalExVATOK,
		r.Verification.LinesMatched, r.Verification.LinesUnmatched, len(r.ReviewLines),
	})

	if r.Verification.TotalExVATOK == false {
		f.SetCellStyle("Summary", "K2", "K2", redFill)
	}

	// ---- Matches: every OrderDetail / BC field as its own column ----
	header("Matches",
		// POA side (OrderDetail)
		"POA Product", "POA Description", "POA Description 2", "POA Description 3",
		"POA Qty", "POA Unit Price", "POA Discount %", "POA Discount Amount", "POA Line Total",
		// BC side (PurchaseOrderLine)
		"BC ItemNo", "BC Description", "BC Qty", "BC Unit Cost", "BC Disc %", "BC Disc Amt", "BC NetAmount",
		// match metadata (LineMatch)
		"Score", "DescScore", "PackRatio", "NetOK", "QtyOK", "InternalOK", "FullyVerified")
	for i, m := range r.Matches {
		row := i + 2
		// Absent line total -> blank cell, not a pointer address, not 0.
		var lineTotal interface{}
		if total, ok := Deref(m.POA.TotalProductQtyPrice); ok {
			lineTotal = total
		}

		f.SetSheetRow("Matches", fmt.Sprintf("A%d", row), &[]interface{}{
			m.POA.Product, m.POA.Description, m.POA.Description2, m.POA.Description3,
			m.POA.Qty, m.POA.UnitProductPrice, m.POA.DiscountPercent, m.POA.DiscountFigure,
			lineTotal, // <- was m.POA.TotalProductQtyPrice (the pointer)

			m.BC.Item.LineObjectNumber, m.BC.Item.Description, m.BC.Item.Quantity,
			m.BC.Item.DirectUnitCost, m.BC.Item.DiscountPercent, m.BC.Item.DiscountAmount,
			m.BC.Item.NetAmount,

			m.Score, m.DescScore, m.PackRatio,
			m.NetOK, m.QtyOK, m.InternalOK, m.FullyVerified(),
		})

		cell := fmt.Sprintf("T%d", row)
		if !m.NetOK {
			f.SetCellStyle("Matches", cell, cell, redFill)
		}

		cell = fmt.Sprintf("U%d", row)
		if !m.QtyOK {
			f.SetCellStyle("Matches", cell, cell, redFill)
		}

		cell = fmt.Sprintf("V%d", row)
		if !m.InternalOK {
			f.SetCellStyle("Matches", cell, cell, redFill)
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
		f.SetSheetRow("Unmatched POA", fmt.Sprintf("A%d", i+2), &[]interface{}{
			d.Product, d.Description, d.Description2, d.Description3,
			d.Qty, d.UnitProductPrice, d.DiscountPercent, d.DiscountFigure, lineTotal,
		})
	}

	// ---- Review: ReviewLine fields ----
	if len(r.ReviewLines) > 0 {
		header("Review", "POADescription", "BCDescription", "Discrepancies")
		for i, rl := range r.ReviewLines {
			f.SetSheetRow("Review", fmt.Sprintf("A%d", i+2), &[]interface{}{
				rl.POADescription, rl.BCDescription, strings.Join(rl.Discrepancies, "; "),
			})
		}
	}

	// ---- BC Lines: raw PurchaseOrderLine fields ----
	header("BC Lines",
		"Sequence", "LineType", "ItemNo", "Description",
		"Qty", "UnitCost", "Disc %", "DiscAmt", "NetAmount")
	for i, l := range r.UnmatchedBC {
		f.SetSheetRow("BC Lines", fmt.Sprintf("A%d", i+2), &[]interface{}{
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
