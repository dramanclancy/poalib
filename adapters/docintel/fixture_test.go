package docintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// A model fixture is what Document Intelligence returned for one real
// document, plus what the parser must make of it. The files hold real order
// data, so testdata/*.json is gitignored and these tests skip without them,
// as TestCorpus does; POA_REQUIRE_CORPUS=1 turns the skip into a failure.
//
// To add one: save the analysis (the body of the operation once it reads
// "succeeded") as testdata/<model>_<document>.json. Bounding regions, spans
// and page text can be dropped; field names, types and content must stay as
// returned. Then add an "expected" block beside "analyzeResult" with the
// orders a person reads off the PDF. After a model is retrained, save its
// answer for the same PDF over the fixture, keep "expected", and run these.
type modelFixture struct {
	analyzeOperation
	Expected []expectedOrder `json:"expected"`
}

type expectedOrder struct {
	PF            string         `json:"pf"`
	Reference     string         `json:"reference"`
	DeliveryDate  string         `json:"deliveryDate"`
	TotalExVAT    *float64       `json:"totalExVAT"`
	OrderDiscount *float64       `json:"orderDiscount"`
	Lines         []expectedLine `json:"lines"`
}

type expectedLine struct {
	Code            string   `json:"code"`
	Description     string   `json:"description"`
	Qty             int      `json:"qty"`
	UnitPrice       float64  `json:"unitPrice"`
	DiscountPercent float64  `json:"discountPercent"`
	LineTotal       *float64 `json:"lineTotal"`
}

func modelFixtures(t *testing.T) map[string]modelFixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		if os.Getenv("POA_REQUIRE_CORPUS") != "" {
			t.Fatal("no model fixtures in testdata/, and POA_REQUIRE_CORPUS is set")
		}
		t.Skip("no model fixtures in testdata/ (gitignored: real order data)")
	}
	out := make(map[string]modelFixture, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Decoded through the client's own wire shape, so the test reads the
		// analysis exactly as pollOperation does.
		var f modelFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		if f.Status != "succeeded" || f.AnalyzeResult == nil || len(f.Expected) == 0 {
			t.Fatalf("%s: want a succeeded analysis and an expected block", path)
		}
		out[filepath.Base(path)] = f
	}
	return out
}

func sameMoney(got, want *float64) bool {
	g, gok := domain.Deref(got)
	w, wok := domain.Deref(want)
	return gok == wok && g == w
}

// TestExtractOrders_ModelFixtures replays each stored analysis through the
// parser. The first fixture (2026-11-12) is a retrained model's answer for a
// one-line order: every "- Page 2" / "- Page 3" slot comes back present and
// empty, and every optional cell of the line — both discounts, the further
// discounts, Descriptions 2 and 3 — is present with no content. None of that
// may become an order, a line, a zero-valued total or a logged warning.
func TestExtractOrders_ModelFixtures(t *testing.T) {
	for name, f := range modelFixtures(t) {
		t.Run(name, func(t *testing.T) {
			var logged []string
			s := &Scanner{Log: func(_ context.Context, format string, _ ...any) { logged = append(logged, format) }}
			got := s.extractOrders(context.Background(), f.AnalyzeResult.Documents)

			if len(got) != len(f.Expected) {
				t.Fatalf("extracted %d orders, want %d: an empty page slot is not an order", len(got), len(f.Expected))
			}
			for i, want := range f.Expected {
				o := got[i]
				if o.PF != want.PF || o.ReferenceNumber != want.Reference || o.ProposedDeliveryDate != want.DeliveryDate {
					t.Errorf("order %d: PF %q, reference %q, delivery %q; want %q, %q, %q",
						i+1, o.PF, o.ReferenceNumber, o.ProposedDeliveryDate, want.PF, want.Reference, want.DeliveryDate)
				}
				if !sameMoney(o.TotalExVAT, want.TotalExVAT) {
					t.Errorf("order %d: total exVAT read differently from the document; a blank must stay absent", i+1)
				}
				if !sameMoney(o.OrderDiscount, want.OrderDiscount) {
					t.Errorf("order %d: order discount read differently from the document; a blank must stay absent", i+1)
				}
				if len(o.Products) != len(want.Lines) {
					t.Fatalf("order %d: %d lines, want %d", i+1, len(o.Products), len(want.Lines))
				}
				for n, wl := range want.Lines {
					p := o.Products[n]
					if p.Code != wl.Code || p.Description != wl.Description || p.Qty != wl.Qty ||
						p.UnitPrice != wl.UnitPrice || p.DiscountPercent != wl.DiscountPercent || !sameMoney(p.LineTotal, wl.LineTotal) {
						t.Errorf("order %d line %d does not match the fixture's expected line", i+1, n+1)
					}
				}
			}
			if len(logged) != 0 {
				t.Errorf("a cleanly read document logged extraction warnings: %q", logged)
			}
		})
	}
}

// TestModelFixtures_SchemaNamesTheParserReads pins the labels extractOrderAt
// looks up against the labels the model returns. Lookups are exact and
// case-sensitive, and a miss yields an empty Field rather than an error, so a
// label renamed in a retrain ("Discount%" to "Discount %") would otherwise
// read as a blank on every order without a word in the log.
func TestModelFixtures_SchemaNamesTheParserReads(t *testing.T) {
	for name, f := range modelFixtures(t) {
		t.Run(name, func(t *testing.T) {
			fields := f.AnalyzeResult.Documents[0].Fields
			for _, label := range []string{
				"Purchase Order No.", "Reference Number", "Proposed Delivery Date",
				"Total Order Amount - exVAT", "Order Details", "Order Discount",
			} {
				if _, ok := fields[label]; !ok {
					t.Errorf("model returns no field %q, which extractOrderAt reads", label)
				}
			}
			rows := fields["Order Details"].ValueArray
			if len(rows) == 0 {
				t.Fatal("fixture has no Order Details row")
			}
			for _, col := range []string{
				"Product", "Description", "Description 2", "Description 3", "Qty",
				"Unit Product Price", "Discount%", "Discount Amount",
				"Further Discount %", "Further Discount Amount", "Total Product Qty Price",
			} {
				if _, ok := rows[0].ValueObject[col]; !ok {
					t.Errorf("model returns no column %q, which extractOrderAt reads", col)
				}
			}
		})
	}
}
