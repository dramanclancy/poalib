package docintel

import (
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// TestExtractOrders_SeveralPurchaseOrdersInOneFile: WhiteMeadow batch several
// acknowledgements into one PDF. The model labels the first instance's fields
// plainly and later ones "<field> - Page N"; an instance slot with no order
// number is unused and skipped.
func TestExtractOrders_SeveralPurchaseOrdersInOneFile(t *testing.T) {
	row := func(product, qty, unit, total string) Field {
		return Field{ValueObject: map[string]Field{
			"Product":                 {Content: product},
			"Qty":                     {Content: qty},
			"Unit Product Price":      {Content: unit},
			"Total Product Qty Price": {Content: total},
		}}
	}
	doc := Document{Fields: map[string]Field{
		"Purchase Order No.":         {Content: "PF130001"},
		"Reference Number":           {Content: "Ack No:\n2332206"},
		"Total Order Amount - exVAT": {Content: "1,377.00"},
		"Order Details":              {ValueArray: []Field{row("NFX2S (A)", "2", "500.00", "1,000.00"), row("Footstool", "1", "377.00", "")}},

		"Purchase Order No. - Page 2":         {Content: "PF130002"},
		"Total Order Amount - exVAT - Page 2": {Content: ""},
		"Order Details - Page 2":              {ValueArray: []Field{row("Lamp Table", "1", "111,00", "111,00")}},

		// Slot 3 exists in the model but this file has no third order.
		"Purchase Order No. - Page 3": {Content: ""},
		"Order Details - Page 3":      {ValueArray: []Field{row("ghost", "1", "1", "1")}},
	}}

	got := (&Scanner{Log: func(string, ...any) {}}).extractOrders([]Document{doc})

	if len(got) != 2 {
		t.Fatalf("extracted %d orders, want 2 (slot 3 has no order number)", len(got))
	}
	first, second := got[0], got[1]
	if first.PF != "PF130001" || second.PF != "PF130002" {
		t.Errorf("PFs = %s, %s", first.PF, second.PF)
	}
	if first.ReferenceNumber != "2332206" {
		t.Errorf("reference = %q, want the value without its captured label", first.ReferenceNumber)
	}
	if v, ok := domain.Deref(first.TotalExVAT); !ok || v != 1377 {
		t.Errorf("first total = %v, want 1377", first.TotalExVAT)
	}
	if second.TotalExVAT != nil {
		t.Errorf("second total = %v, want nil: a blank total is absent, not zero", *second.TotalExVAT)
	}
	if len(first.Products) != 2 || first.Products[0].Qty != 2 || first.Products[0].UnitPrice != 500 {
		t.Errorf("first order lines = %+v", first.Products)
	}
	if first.Products[1].LineTotal != nil {
		t.Error("a blank line total became a number; it must stay absent")
	}
	if len(second.Products) != 1 || second.Products[0].UnitPrice != 111 {
		t.Errorf("second order lines = %+v, want one line at 111 (continental decimal)", second.Products)
	}
}

func TestParseMoney(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		// UK convention, as every supplier printed before Ego Italiano.
		{"1,377.00", 1377, true},
		{"£1,377.00", 1377, true},
		{"GBP 1,377.00", 1377, true},
		{"1,234,567.89", 1234567.89, true},
		{"1,377", 1377, true},
		{"608", 608, true},
		{"608.50", 608.5, true},

		// Continental convention. PF130853 (Ego Italiano 419790) printed these
		// and the old parser read them one hundred times too large.
		{"672,00", 672, true},
		{"15,00", 15, true},
		{"15,00%", 15, true},
		{"571,20", 571.2, true},
		{"EUR 571,20", 571.2, true},
		{"€1.377,00", 1377, true},
		{"1.234.567,89", 1234567.89, true},
		{"1.234.567", 1234567, true},
		{"0,84", 0.84, true},
		{"1,5", 1.5, true},

		{"", 0, false},
		{"  ", 0, false},
		{"n/a", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMoney(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("parseMoney(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
