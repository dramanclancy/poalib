package poa

import (
	"bytes"
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
	"github.com/xuri/excelize/v2"
)

// TestBuildExcelBytes_SheetNamesMatch guards against header()/SetSheetRow()
// calls targeting a sheet name that was never created by NewSheet (or vice
// versa). Before the fix, "Unmatched POA" was created but written to under
// the name "Unmatched" (so the created sheet stayed blank and the writes
// silently failed with ErrSheetNotExist, discarded by header()'s ignored
// return value), and "BC Lines" was created and populated with data but its
// header was written to a phantom "BC Unmatched Lines" sheet — plus an
// entirely unused "Unmatched BC" sheet was created and never touched.
func TestBuildExcelBytes_SheetNamesMatch(t *testing.T) {
	r := &Review{
		Extraction: Products{PF: "PO-TEST"},
		BCOrder:    &businesscentral.PurchaseOrder{VendorName: "Acme", Status: "Released"},
		Unmatched: []OrderDetail{
			{Product: "SKU1", Description: "Widget", Qty: 2, UnitProductPrice: 10, TotalProductQtyPrice: Ptr(20.0)},
			{Product: "SKU2", Description: "Gadget", Qty: 1, UnitProductPrice: 5}, // no printed line total
		},
		UnmatchedBC: []BCLineGroup{
			{Item: businesscentral.PurchaseOrderLine{Sequence: 10000, LineType: "Item", LineObjectNumber: "BC1", Description: "Gizmo", Quantity: 1, DirectUnitCost: 7, NetAmount: 7}},
		},
	}

	data, err := r.BuildExcelBytes()
	if err != nil {
		t.Fatalf("BuildExcelBytes: %v", err)
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("reopen generated workbook: %v", err)
	}
	defer f.Close()

	sheets := map[string]bool{}
	for _, s := range f.GetSheetList() {
		sheets[s] = true
	}

	for _, want := range []string{"Summary", "Matches", "Unmatched POA", "BC Lines"} {
		if !sheets[want] {
			t.Errorf("expected sheet %q to exist, got sheets: %v", want, f.GetSheetList())
		}
	}
	for _, unwanted := range []string{"Unmatched", "BC Unmatched Lines", "Unmatched BC"} {
		if sheets[unwanted] {
			t.Errorf("unexpected orphan/phantom sheet %q present", unwanted)
		}
	}

	// Header row must have landed on the sheet that actually holds the data.
	cell, err := f.GetCellValue("Unmatched POA", "A1")
	if err != nil || cell != "Product" {
		t.Errorf("Unmatched POA A1 = %q, err=%v; want header row present", cell, err)
	}
	cell, err = f.GetCellValue("BC Lines", "A1")
	if err != nil || cell != "Sequence" {
		t.Errorf("BC Lines A1 = %q, err=%v; want header row present", cell, err)
	}

	// Data rows must have actually been written (previously lost on "Unmatched POA").
	cell, err = f.GetCellValue("Unmatched POA", "A2")
	if err != nil || cell != "SKU1" {
		t.Errorf("Unmatched POA A2 = %q, err=%v; want %q", cell, err, "SKU1")
	}
	cell, err = f.GetCellValue("BC Lines", "C2")
	if err != nil || cell != "BC1" {
		t.Errorf("BC Lines C2 = %q, err=%v; want %q", cell, err, "BC1")
	}
}

// TestBuildExcelBytes_LineTotalPointerNotLeaked guards against writing a
// *float64 straight into an excelize row: present -> numeric cell, absent ->
// blank cell, never "0" and never a formatted pointer address.
func TestBuildExcelBytes_LineTotalPointerNotLeaked(t *testing.T) {
	r := &Review{
		Extraction: Products{PF: "PO-TEST"},
		BCOrder:    &businesscentral.PurchaseOrder{VendorName: "Acme"},
		Unmatched: []OrderDetail{
			{Product: "SKU1", TotalProductQtyPrice: Ptr(123.45)},
			{Product: "SKU2", TotalProductQtyPrice: nil},
		},
	}

	data, err := r.BuildExcelBytes()
	if err != nil {
		t.Fatalf("BuildExcelBytes: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("reopen generated workbook: %v", err)
	}
	defer f.Close()

	present, err := f.GetCellValue("Unmatched POA", "I2")
	if err != nil {
		t.Fatalf("GetCellValue I2: %v", err)
	}
	if present != "123.45" {
		t.Errorf("present line total = %q, want %q", present, "123.45")
	}

	absent, err := f.GetCellValue("Unmatched POA", "I3")
	if err != nil {
		t.Fatalf("GetCellValue I3: %v", err)
	}
	if absent != "" {
		t.Errorf("absent line total = %q, want blank (not 0, not a pointer address)", absent)
	}
}
