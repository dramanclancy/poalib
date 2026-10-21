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

// TestExtractOrders_OrderDiscount: the order-level discount is read as an
// amount, or as a rate when it prints "%", and is absent when blank.
func TestExtractOrders_OrderDiscount(t *testing.T) {
	cases := []struct {
		content string
		want    *float64
		percent bool
	}{
		{"12.90", domain.Ptr(12.90), false},
		{"5%", domain.Ptr(5.0), true},
		{"", nil, false},
	}
	for _, tc := range cases {
		doc := Document{Fields: map[string]Field{
			"Purchase Order No.": {Content: "PF080317"},
			"Order Discount":     {Content: tc.content},
			"Order Details": {ValueArray: []Field{{ValueObject: map[string]Field{
				"Product": {Content: "LAW45"}, "Qty": {Content: "1.00"}, "Unit Product Price": {Content: "199.0000"},
			}}}},
		}}
		got := (&Scanner{Log: func(string, ...any) {}}).extractOrders([]Document{doc})
		if len(got) != 1 {
			t.Fatalf("%q: extracted %d orders", tc.content, len(got))
		}
		o := got[0]
		gv, gok := domain.Deref(o.OrderDiscount)
		wv, wok := domain.Deref(tc.want)
		if gok != wok || gv != wv || o.OrderDiscountIsPercent != tc.percent {
			t.Errorf("%q: discount %v (set %v) percent %v, want %v (set %v) percent %v",
				tc.content, gv, gok, o.OrderDiscountIsPercent, wv, wok, tc.percent)
		}
	}
}

// TestExtractOrders_FurtherDiscount: the model's further-discount columns
// reach the line, and the line's net compounds them on the first discount.
func TestExtractOrders_FurtherDiscount(t *testing.T) {
	doc := Document{Fields: map[string]Field{
		"Purchase Order No.": {Content: "PF080317"},
		"Order Details": {ValueArray: []Field{{ValueObject: map[string]Field{
			"Product":                 {Content: "LAW45"},
			"Qty":                     {Content: "1.00"},
			"Unit Product Price":      {Content: "200.0000"},
			"Discount%":               {Content: "10%"},
			"Further Discount %":      {Content: "5%"},
			"Further Discount Amount": {Content: ""},
		}}}},
	}}
	got := (&Scanner{Log: func(string, ...any) {}}).extractOrders([]Document{doc})
	if len(got) != 1 || len(got[0].Products) != 1 {
		t.Fatalf("extracted %+v", got)
	}
	p := got[0].Products[0]
	if p.FurtherDiscountPercent != 5 || p.FurtherDiscountAmount != 0 {
		t.Errorf("further discount = %v%% / %v, want 5%% / 0", p.FurtherDiscountPercent, p.FurtherDiscountAmount)
	}
	if net := p.Net(); net < 170.99 || net > 171.01 {
		t.Errorf("net = %.2f, want 171.00 (200 × 0.90 × 0.95)", net)
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

// TestExtractOrders_DropsBlankRowsAndSplitsSpilledCodes: Kaymed and Harrison
// Spinks layouts return "-" rows between real lines, and put a whole line's
// text in the Product column with Description empty.
func TestExtractOrders_DropsBlankRowsAndSplitsSpilledCodes(t *testing.T) {
	row := func(product, desc, qty, unit string) Field {
		return Field{ValueObject: map[string]Field{
			"Product":            {Content: product},
			"Description":        {Content: desc},
			"Qty":                {Content: qty},
			"Unit Product Price": {Content: unit},
		}}
	}
	doc := Document{Fields: map[string]Field{
		"Purchase Order No.": {Content: "PF130001"},
		"Order Details": {ValueArray: []Field{
			row("CA24 Natural Indulge", "", "1", "748.00"),
			row("-", "", "", ""),
			row("", "", "0", "0.00"),
			row("Eastbury (Soft) Large 2 Seater", "", "1", "1118.00"),
			row("NFX2S (A)", "", "1", "500.00"),
			row("HAN3S (A)", "HANSSON 3 seater sofa", "1", "663.00"),
		}},
	}}

	got := (&Scanner{Log: func(string, ...any) {}}).extractOrders([]Document{doc})[0].Products

	want := []struct{ code, desc string }{
		{"CA24", "Natural Indulge"},
		{"", "Eastbury (Soft) Large 2 Seater"},
		{"NFX2S (A)", ""},
		{"HAN3S (A)", "HANSSON 3 seater sofa"},
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %d lines, want %d: the blank rows must be dropped", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Code != w.code || got[i].Description != w.desc {
			t.Errorf("line %d = (%q, %q), want (%q, %q)", i, got[i].Code, got[i].Description, w.code, w.desc)
		}
	}
}
