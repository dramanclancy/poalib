package excel

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/app"
	"github.com/dramanclancy/poalib/domain"
	"github.com/xuri/excelize/v2"
)

// sample builds a reconciliation with one clean line and one that fails on
// price, plus a BC line the supplier never acknowledged.
func sample() (domain.Result, app.RunRecord) {
	poa := domain.POAOrder{PF: "PF130368", ReferenceNumber: "602055", Products: []domain.POAProduct{
		{Code: "NFX3S (A)", Description: "NEW FELIX 3 seater sofa", Qty: 1, UnitPrice: 657,
			Embedding: []float32{1, 0}},
		{Code: "NFXCH (A)", Description: "NEW FELIX Chair", Qty: 1, UnitPrice: 376,
			Embedding: []float32{0, 1}},
	}}
	bc := domain.BCOrder{
		PF: "PF130368", VendorNo: "VX1", VendorName: "Ashwood Designs", Status: "Open",
		TotalExVAT: 1055, Enrichment: domain.EnrichmentApplied,
		Products: []domain.BCProduct{
			{LineID: "bc-1", ItemNo: "IT0188673", LineType: "Item",
				LineDescription: "3 Seater Sofa",
				Description:     "3 Seater Sofa Standard Fabric MARNI OLIVE",
				Qty:             1, UnitCost: 657, Net: 657, Embedding: []float32{1, 0},
				Detail: &domain.ItemDetail{ItemNo: "IT0188673", Seats: domain.Ptr(3.0)}},
			{LineID: "bc-2", ItemNo: "IT0188675", LineType: "Item",
				LineDescription: "Chair",
				Description:     "Chair Standard Fabric MARNI OLIVE",
				Qty:             1, UnitCost: 400, Net: 400, Embedding: []float32{0, 1}},
			{LineID: "bc-3", ItemNo: "718000", LineType: "Account", Sequence: 300,
				LineDescription: "Vendor Surcharges", Description: "Vendor Surcharges",
				Qty: 1, UnitCost: 22, Net: 22, Embedding: []float32{0.1, 0.1}},
		},
	}
	res := domain.Reconcile(poa, bc, domain.DefaultConfig())
	run := app.RunRecord{
		PF: "PF130368", RunID: "PF130368-1", EngineVersion: domain.EngineVersion,
		EmbeddingModel: "OpenAI API, model \"text-embedding-3-small\"",
		BCLinesFetched: 8, BCProductsBuilt: 3,
		EnrichmentAsked: true, EnrichmentState: string(domain.EnrichmentApplied),
		ItemsRequested: 2, ItemsReturned: 2, ItemsWithSeats: 1, ItemsWithCodes: 0,
		POALinesExtracted: 2, POAPagesMerged: 1,
		LinesMatched: len(res.Products), LinesFlagged: len(res.Flagged()),
		Config: res.Config,
	}
	return res, run
}

func open(t *testing.T, data []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("reopen generated workbook: %v", err)
	}
	return f
}

// TestBuild_EveryMatchesColumnIsDocumented is the promise the Definitions
// sheet makes: a reader who did not build the engine can look up any column.
// Both sides come from one spec, so this cannot drift.
func TestBuild_EveryMatchesColumnIsDocumented(t *testing.T) {
	res, run := sample()
	data, err := Build(res, run)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := open(t, data)
	defer f.Close()

	headers, err := f.GetRows(sheetMatches)
	if err != nil || len(headers) == 0 {
		t.Fatalf("GetRows(%s): %v", sheetMatches, err)
	}
	defs, err := f.GetRows(sheetDefinitions)
	if err != nil {
		t.Fatalf("GetRows(%s): %v", sheetDefinitions, err)
	}

	documented := map[string][]string{}
	for _, row := range defs {
		if len(row) >= 6 && row[0] == sheetMatches {
			documented[row[1]] = row
		}
	}
	for _, h := range headers[0] {
		row, ok := documented[h]
		if !ok {
			t.Errorf("column %q has no entry on the Definitions sheet", h)
			continue
		}
		for i, what := range []string{"meaning", "data used", "calculation", "effect"} {
			if strings.TrimSpace(row[i+2]) == "" {
				t.Errorf("column %q has no %s", h, what)
			}
		}
	}
}

// TestBuild_ShowsTheDescriptionThatWasCompared: the report must never present
// a raw BC line description as the text the engine matched on. Those differ,
// and confusing them makes a correct score look wrong.
func TestBuild_ShowsTheDescriptionThatWasCompared(t *testing.T) {
	res, run := sample()
	data, err := Build(res, run)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := open(t, data)
	defer f.Close()

	rows, err := f.GetRows(sheetMatches)
	if err != nil || len(rows) < 2 {
		t.Fatalf("GetRows: %v", err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	idx, ok := col["BC Description (compared)"]
	if !ok {
		t.Fatalf("no compared-description column: %v", rows[0])
	}
	got := rows[1][idx]
	if got != "3 Seater Sofa Standard Fabric MARNI OLIVE" {
		t.Errorf("BC description = %q, want the merged text the engine compared", got)
	}
	if got == "3 Seater Sofa" {
		t.Error("the raw purchase order line description was shown as if it had been compared")
	}
}

// TestBuild_SummaryReportsHowTheRunWent: a reader must be able to see that a
// comparison ran without item enrichment, rather than infer it from a column
// of blanks.
func TestBuild_SummaryReportsHowTheRunWent(t *testing.T) {
	res, run := sample()
	run.EnrichmentAsked = false
	run.EnrichmentState = string(domain.EnrichmentNotRequested)
	run.ItemsRequested, run.ItemsReturned, run.ItemsWithSeats = 0, 0, 0

	if !run.Degraded() {
		t.Fatal("a run without enrichment should report as degraded")
	}
	data, err := Build(res, run)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := open(t, data)
	defer f.Close()

	rows, err := f.GetRows(sheetSummary)
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	var text strings.Builder
	for _, r := range rows {
		text.WriteString(strings.Join(r, "\x1f"))
		text.WriteString("\n")
	}
	body := text.String()

	for _, want := range []string{
		"Engine version", domain.EngineVersion,
		"Embedding provider and model", "text-embedding-3-small",
		"Item enrichment requested", "Items requested", "Items with a seat count",
		"Description similarity threshold", "Fixture capture enabled",
		"DEGRADED",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Summary does not mention %q", want)
		}
	}
}

// TestBuild_SheetsAndBlanks guards the sheet set, and that an absent value
// stays blank rather than becoming a zero a reader would believe.
func TestBuild_SheetsAndBlanks(t *testing.T) {
	res, run := sample()
	data, err := Build(res, run)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f := open(t, data)
	defer f.Close()

	sheets := map[string]bool{}
	for _, s := range f.GetSheetList() {
		sheets[s] = true
	}
	for _, want := range []string{sheetSummary, sheetMatches, sheetUnmatchedPOA, sheetReview, sheetBCLines, sheetDefinitions} {
		if !sheets[want] {
			t.Errorf("missing sheet %q; got %v", want, f.GetSheetList())
		}
	}

	rows, _ := f.GetRows(sheetMatches)
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	// Neither sample line prints a line total, and neither BC item carries a
	// seat count on the second line: both must be blank, never 0.
	for _, name := range []string{"POA Line Total (printed)", "BC Seats"} {
		i := col[name]
		if i < len(rows[2]) && rows[2][i] == "0" {
			t.Errorf("%q rendered as 0; absent must be blank", name)
		}
	}

	// The unacknowledged surcharge line appears with both descriptions, and
	// only one of them is labelled as compared.
	bcRows, _ := f.GetRows(sheetBCLines)
	if len(bcRows) < 2 {
		t.Fatal("the unacknowledged BC line is missing from the BC Lines sheet")
	}
	if !strings.Contains(strings.Join(bcRows[0], " "), "not compared") {
		t.Error("the raw BC description column is not labelled as not compared")
	}
}
