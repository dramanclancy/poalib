package app

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dramanclancy/poalib/domain"
	"github.com/xuri/excelize/v2"
)

const (
	goldSetControlRows = 60
	goldSetVendorCap   = 15
	// goldSetSeed fixes the control sample, so one corpus always draws the same
	// rows and a labelled workbook can be traced back to how it was chosen.
	goldSetSeed = 20261109

	goldRowFlagged   = "Flagged"
	goldRowControl   = "Control"
	goldRowUnmatched = "Unmatched"

	goldSheetLabel        = "Label"
	goldSheetInstructions = "Instructions"
	goldSheetSummary      = "Summary"
)

// goldSetLabels is the Label dropdown. Excel stores the list in the workbook,
// so changing it does not change a workbook already out with a buyer.
var goldSetLabels = []string{"Right pair", "Wrong pair", "Not a product line", "Extraction error", "Not sure"}

// goldSetColumns is the Label sheet, in order. A goldRow fills the columns up
// to Alternative 3; the last three are the buyer's and stay empty.
var goldSetColumns = []struct {
	name   string
	width  float64
	wrap   bool
	money  bool // two decimal places
	buyers bool // filled in by the labeller
}{
	{name: "RowType", width: 11},
	{name: "PF", width: 13},
	{name: "Vendor", width: 22, wrap: true},
	{name: "POA line #", width: 7},
	{name: "POA code", width: 20, wrap: true},
	{name: "POA description", width: 48, wrap: true},
	{name: "POA qty", width: 7},
	{name: "POA unit price", width: 10, money: true},
	{name: "BC item no", width: 12},
	{name: "BC description", width: 48, wrap: true},
	{name: "BC qty", width: 7},
	{name: "BC unit cost", width: 10, money: true},
	{name: "Similarity", width: 10, money: true},
	{name: "Margin", width: 8, money: true},
	{name: "Runner-up", width: 12},
	{name: "Code match source", width: 18, wrap: true},
	{name: "Engine description verdict", width: 16},
	{name: "Engine message", width: 40, wrap: true},
	{name: "Price status", width: 10},
	{name: "Qty status", width: 10},
	{name: "Alternative 1", width: 38, wrap: true},
	{name: "Alternative 2", width: 38, wrap: true},
	{name: "Alternative 3", width: 38, wrap: true},
	{name: "Label", width: 18, buyers: true},
	{name: "Correct BC item no", width: 14, buyers: true},
	{name: "Notes", width: 30, wrap: true, buyers: true},
}

// goldRow is one row of the Label sheet.
type goldRow struct {
	kind, pf, vendor, vendorKey, verdict string
	poaIndex, bcIndex                    int
	// cells holds one value per column up to Alternative 3. nil or "" leaves
	// the cell empty: a similarity nobody calculated is absent, not zero.
	cells []any
}

// TestCorpus_ExportGoldSet writes the workbook a buyer marks up to say whether
// each pairing the engine made is really the same product. It becomes the
// evaluation set for a future identity judge.
//
// It is evaluation data only. Nothing in the engine may read it: every run
// decides from its own two orders (CLAUDE.md rule 11).
//
// Each order is replayed rather than read from its stored expected block, so
// the workbook describes the engine as it is now. It skips unless
// POA_EXPORT_GOLDSET=1, because it writes real supplier data to disk.
func TestCorpus_ExportGoldSet(t *testing.T) {
	if os.Getenv("POA_EXPORT_GOLDSET") != "1" {
		t.Skip("writes the labelling workbook; set POA_EXPORT_GOLDSET=1 to run it")
	}
	fixtures := latestFixtures(t)
	rows := goldSetRows(t, fixtures)

	// A filled-in workbook has no other copy, so a same-day re-run must not
	// replace a buyer's labels.
	path := filepath.Join("..", "testdata", "goldset", "goldset_"+time.Now().Format("20060102")+".xlsx")
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s already exists and may hold a buyer's labels; move it away to export again", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeGoldSet(path, rows, len(fixtures)); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	byType, flaggedByVendor := map[string]int{}, map[string]int{}
	for _, r := range rows {
		byType[r.kind]++
		if r.kind == goldRowFlagged {
			flaggedByVendor[r.vendor]++
		}
	}
	t.Logf("wrote %s from %d purchase orders", path, len(fixtures))
	t.Logf("rows: %d Flagged, %d Control, %d Unmatched", byType[goldRowFlagged], byType[goldRowControl], byType[goldRowUnmatched])
	for _, v := range sortedByCount(flaggedByVendor) {
		t.Logf("flagged %4d  %s", flaggedByVendor[v], v)
	}
}

// latestFixtures returns the most recent capture of each PF, chosen by the
// rule the capture path itself uses. A re-run that changed something gets its
// own fixture, so counting every capture would weight a re-run order twice.
func latestFixtures(t *testing.T) []Fixture {
	t.Helper()
	w := &FixtureWriter{Dir: corpusDir()}
	seen := map[string]bool{}
	var out []Fixture
	for _, f := range loadCorpus(t) {
		if seen[f.PF] {
			continue
		}
		seen[f.PF] = true
		latest, err := LoadFixture(w.latestPath(f.PF))
		if err != nil {
			t.Fatalf("latest fixture for %s: %v", f.PF, err)
		}
		out = append(out, latest)
	}
	return out
}

// goldSetRows replays each order and selects the rows: every flagged pair, a
// control sample of pairs the engine trusted on description alone, and every
// line left unmatched. A code-matched pair is not a control: identity there
// came from the code, so it says nothing about how far the text can be trusted.
func goldSetRows(t *testing.T, fixtures []Fixture) []goldRow {
	t.Helper()
	var rows, pool []goldRow
	for _, f := range fixtures {
		res := domain.Reconcile(f.POA, f.BC, f.Config)
		for _, p := range res.Products {
			d := p.Description
			if local := goldSimilarity(p.POA.Embedding, p.BC.Embedding); !sameScore(local, d.SimilarityScore) {
				t.Fatalf("%s POA line %d: this test's similarity disagrees with the engine's, so its alternatives would be ranked on a different measure", f.PF, p.POAIndex)
			}
			switch {
			case d.Status == domain.StatusMismatch || d.Status == domain.StatusWarning:
				rows = append(rows, pairedGoldRow(goldRowFlagged, f, p))
			case d.Status == domain.StatusMatch && !d.CodeMatch:
				pool = append(pool, pairedGoldRow(goldRowControl, f, p))
			}
		}
		for i, line := range res.UnmatchedLines {
			rows = append(rows, unmatchedPOAGoldRow(f, res.UnmatchedPOA[i], line))
		}
		for i, b := range res.UnmatchedBC {
			rows = append(rows, unmatchedBCGoldRow(f, b, i))
		}
	}

	control := sampleControl(pool)
	if len(control) != goldSetControlRows {
		t.Fatalf("drew %d control rows from %d eligible, want %d with at most %d per vendor", len(control), len(pool), goldSetControlRows, goldSetVendorCap)
	}
	rows = append(rows, control...)
	sortGoldRows(rows, true)
	return rows
}

// sampleControl draws the control rows: sorted first so the shuffle starts from
// an order that does not depend on how the fixtures were read, then taken in
// shuffled order, skipping any vendor already at its cap. Without the cap the
// largest suppliers would be most of the sample.
func sampleControl(pool []goldRow) []goldRow {
	sortGoldRows(pool, false)
	r := rand.New(rand.NewSource(goldSetSeed))
	r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

	perVendor := map[string]int{}
	var out []goldRow
	for _, row := range pool {
		if len(out) == goldSetControlRows {
			break
		}
		if perVendor[row.vendorKey] >= goldSetVendorCap {
			continue
		}
		perVendor[row.vendorKey]++
		out = append(out, row)
	}
	return out
}

// sortGoldRows orders rows by PF and POA line, keeping one order's lines
// together so a labeller reads them as the order they came from. byKind puts
// the row types in the order they are worth a buyer's time.
func sortGoldRows(rows []goldRow, byKind bool) {
	rank := map[string]int{goldRowFlagged: 0, goldRowControl: 1, goldRowUnmatched: 2}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if byKind && a.kind != b.kind {
			return rank[a.kind] < rank[b.kind]
		}
		if a.pf != b.pf {
			return a.pf < b.pf
		}
		if a.poaIndex != b.poaIndex {
			return a.poaIndex < b.poaIndex
		}
		return a.bcIndex < b.bcIndex
	})
}

func pairedGoldRow(kind string, f Fixture, p domain.ProductResult) goldRow {
	d := p.Description
	alt := goldAlternatives(p.POA, f.BC.Products, p.BCIndex)
	return goldRow{
		kind: kind, pf: f.PF, vendor: goldVendor(f), vendorKey: goldVendorKey(f), verdict: string(d.Status),
		poaIndex: p.POAIndex, bcIndex: p.BCIndex,
		cells: []any{
			kind, f.PF, goldVendor(f),
			p.POAIndex, flattenLines(p.POA.Code), goldPOADescription(p.POA), p.POA.Qty, p.POA.UnitPrice,
			p.BC.ItemNo, flattenLines(p.BC.Description), p.BC.Qty, p.BC.UnitCost,
			goldOptional(d.SimilarityScore), goldOptional(d.Margin), d.RunnerUp, d.CodeMatchSource,
			string(d.Status), flattenLines(d.Message), string(p.Price.Status), string(p.Quantity.Status),
			alt[0], alt[1], alt[2],
		},
	}
}

func unmatchedPOAGoldRow(f Fixture, p domain.POAProduct, line domain.UnmatchedLine) goldRow {
	verdict := "unmatched: " + string(line.Kind)
	alt := goldAlternatives(p, f.BC.Products, -1)
	return goldRow{
		kind: goldRowUnmatched, pf: f.PF, vendor: goldVendor(f), vendorKey: goldVendorKey(f), verdict: verdict,
		poaIndex: line.POAIndex, bcIndex: -1,
		cells: []any{
			goldRowUnmatched, f.PF, goldVendor(f),
			line.POAIndex, flattenLines(p.Code), goldPOADescription(p), p.Qty, p.UnitPrice,
			nil, nil, nil, nil,
			nil, nil, nil, nil,
			verdict, flattenLines(line.Message), nil, nil,
			alt[0], alt[1], alt[2],
		},
	}
}

// unmatchedBCGoldRow has no alternatives: they are ranked against a POA line,
// and this row has none.
func unmatchedBCGoldRow(f Fixture, b domain.BCProduct, order int) goldRow {
	const verdict = "unmatched: BC line"
	return goldRow{
		kind: goldRowUnmatched, pf: f.PF, vendor: goldVendor(f), vendorKey: goldVendorKey(f), verdict: verdict,
		poaIndex: math.MaxInt, bcIndex: order, // after the POA lines of the same order
		cells: []any{
			goldRowUnmatched, f.PF, goldVendor(f),
			nil, nil, nil, nil, nil,
			b.ItemNo, flattenLines(b.Description), b.Qty, b.UnitCost,
			nil, nil, nil, nil,
			verdict, "no acknowledgement line was paired with this BC line", nil, nil,
			nil, nil, nil,
		},
	}
}

// goldVendor is the name a buyer recognises. BC stores some with a trailing
// space ("Baker Ltd. "), which would otherwise count as a separate vendor.
func goldVendor(f Fixture) string { return strings.TrimSpace(f.BC.VendorName) }

// goldVendorKey is what the per-vendor cap counts: the vendor number is stable,
// as vendors.go notes, and the name is only a fallback.
func goldVendorKey(f Fixture) string {
	if no := strings.TrimSpace(f.BC.VendorNo); no != "" {
		return no
	}
	return goldVendor(f)
}

// goldPOADescription is everything the supplier printed about the line besides
// its code. Fixtures captured before 9ebea4f can hold a row's whole text in
// Product with the description columns empty (Harrison Spinks, Kaymed, Parker
// Knoll). There the Product text is the description: an empty cell would look
// to a labeller like an extraction error when the text was read perfectly well.
func goldPOADescription(p domain.POAProduct) string {
	var parts []string
	for _, s := range []string{p.Description, p.Description2, p.Description3} {
		if s = flattenLines(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return flattenLines(p.Code)
	}
	return strings.Join(parts, " | ")
}

// flattenLines puts multi-line supplier text on one line, so a filtered or
// sorted sheet keeps one row per pairing.
func flattenLines(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " | ")
}

func goldOptional(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// goldAlternatives names the three other items on the order that most resemble
// this POA line, so a labeller can pick the right one without opening BC.
// paired is the BC line it was paired with, or -1. An item BC holds on two
// lines is listed once, and not at all if it is the paired item: repeating it
// would use up a slot without offering anything different to choose.
func goldAlternatives(poa domain.POAProduct, bc []domain.BCProduct, paired int) [3]any {
	sims := make([]*float64, len(bc))
	order := make([]int, 0, len(bc))
	for i, b := range bc {
		sims[i] = goldSimilarity(poa.Embedding, b.Embedding)
		if i != paired {
			order = append(order, i)
		}
	}
	rank := func(i int) float64 {
		if sims[i] == nil {
			return math.Inf(-1)
		}
		return *sims[i]
	}
	sort.SliceStable(order, func(a, b int) bool { return rank(order[a]) > rank(order[b]) })

	seen := map[string]bool{}
	if paired >= 0 {
		seen[bc[paired].ItemNo] = true
	}
	var out [3]any
	n := 0
	for _, i := range order {
		if n == len(out) {
			break
		}
		no := bc[i].ItemNo
		if no != "" && seen[no] {
			continue
		}
		seen[no] = true
		score := "n/a"
		if sims[i] != nil {
			score = fmt.Sprintf("%.2f", *sims[i])
		}
		out[n] = fmt.Sprintf("%s | %s | sim %s", no, firstRunes(flattenLines(bc[i].Description), 80), score)
		n++
	}
	return out
}

// firstRunes cuts by character, not byte: supplier and BC text is not all ASCII
// (X8 Soluções), and a cut through a character would put invalid text in a cell.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// goldSimilarity is domain's similarityOf, which the engine does not export.
// Exporting engine internals for a test that only prints them would be the
// wrong way round, so this repeats the sum, and goldSetRows checks it against
// the engine's score on every pair so the two cannot drift apart unnoticed.
func goldSimilarity(a, b []float32) *float64 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	if len(a) != len(b) {
		return domain.Ptr(0.0)
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return domain.Ptr(0.0)
	}
	return domain.Ptr(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}

func sameScore(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(*a-*b) < 1e-12
}

// sortedByCount returns the keys of counts, largest first, then by name.
func sortedByCount(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// ---------------------------------------------------------------------------
// The workbook
// ---------------------------------------------------------------------------

func writeGoldSet(path string, rows []goldRow, orders int) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	if err := f.SetSheetName("Sheet1", goldSheetLabel); err != nil {
		return err
	}
	for _, name := range []string{goldSheetInstructions, goldSheetSummary} {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Vertical: "top"}})
	if err != nil {
		return err
	}
	if err := writeGoldLabelSheet(f, rows); err != nil {
		return err
	}
	if err := writeGoldInstructions(f, bold); err != nil {
		return err
	}
	if err := writeGoldSummary(f, bold, rows, orders); err != nil {
		return err
	}
	return f.SaveAs(path)
}

func writeGoldLabelSheet(f *excelize.File, rows []goldRow) error {
	const sheet = goldSheetLabel
	wrap, err := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	if err != nil {
		return err
	}
	plain, err := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Vertical: "top"}})
	if err != nil {
		return err
	}
	money, err := f.NewStyle(&excelize.Style{NumFmt: 2, Alignment: &excelize.Alignment{Vertical: "top"}})
	if err != nil {
		return err
	}
	head, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	if err != nil {
		return err
	}
	// The three columns the buyer fills in are marked, among twenty-three they
	// only read.
	headBuyer, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFF2CC"}},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	if err != nil {
		return err
	}

	last := len(rows) + 1
	for c, col := range goldSetColumns {
		name, err := excelize.ColumnNumberToName(c + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth(sheet, name, name, col.width); err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, name+"1", col.name); err != nil {
			return err
		}
		headStyle, bodyStyle := head, plain
		if col.buyers {
			headStyle = headBuyer
		}
		switch {
		case col.wrap:
			bodyStyle = wrap
		case col.money:
			bodyStyle = money
		}
		if err := f.SetCellStyle(sheet, name+"1", name+"1", headStyle); err != nil {
			return err
		}
		if err := f.SetCellStyle(sheet, name+"2", fmt.Sprintf("%s%d", name, last), bodyStyle); err != nil {
			return err
		}
	}

	for i, r := range rows {
		for c, v := range r.cells {
			if v == nil || v == "" {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(c+1, i+2)
			if err != nil {
				return err
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return err
			}
		}
	}

	lastCol, err := excelize.ColumnNumberToName(len(goldSetColumns))
	if err != nil {
		return err
	}
	if err := f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	if err := f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, last), nil); err != nil {
		return err
	}

	labelCol, err := goldColumnName("Label")
	if err != nil {
		return err
	}
	dv := excelize.NewDataValidation(true)
	dv.Sqref = fmt.Sprintf("%s2:%s%d", labelCol, labelCol, last)
	if err := dv.SetDropList(goldSetLabels); err != nil {
		return err
	}
	dv.SetError(excelize.DataValidationErrorStyleStop, "Label", "Please pick one of the options in the list.")
	return f.AddDataValidation(sheet, dv)
}

func goldColumnName(name string) (string, error) {
	for c, col := range goldSetColumns {
		if col.name == name {
			return excelize.ColumnNumberToName(c + 1)
		}
	}
	return "", fmt.Errorf("no column %q", name)
}

// goldInstructions is the Instructions sheet: a row with no text is a heading.
var goldInstructions = []struct{ term, text string }{
	{term: "How to label this workbook"},
	{text: "Each row on the Label sheet shows one line from a supplier's acknowledgement (POA) and the Business Central (BC) line the system paired it with. Your answer tells us whether the system picked the right product. Choose one Label for every row."},
	{text: "Price and quantity differences do NOT matter for this label. Only judge whether it is the same product."},
	{text: "The columns headed Engine, and Similarity and Margin, show what the system concluded. They can be wrong, so judge for yourself."},
	{},
	{term: "Labels"},
	{"Right pair", "This POA line and this BC line are the same product, even if size notation, codes or wording differ (e.g. \"5-0\" = 5ft = 150cm)."},
	{"Wrong pair", "They are different products. Put the correct BC item in \"Correct BC item no\" if it is one of the alternatives or on the order."},
	{"Not a product line", "A surcharge, carriage, note or similar, on either side."},
	{"Extraction error", "The POA text is not the product description (e.g. tariff codes), or it is missing. Document Intelligence read the wrong field, or has not been trained on that document."},
	{"Not sure", "You cannot tell from what is shown. Say why in Notes if you can."},
	{},
	{term: "Other columns"},
	{"Alternative 1 to 3", "The other items on the same order that look most like this POA line, best first: item number | description | similarity. Use them to find the right item without opening BC."},
	{"Correct BC item no", "Only fill this in when the Label is Wrong pair."},
	{"Notes", "Anything else that would help us."},
	{"POA line #", "The line's position on the acknowledgement, counting from 0, as on the reconciliation workbook."},
	{},
	{term: "Types of row"},
	{"Flagged", "The system was not confident these are the same product."},
	{"Control", "The system was confident. This is a sample, to check how often it is right."},
	{"Unmatched", "The system found no partner for this line, so only one side is filled in. There is no pair to judge, so do not choose Right pair."},
	{"  POA side only", "If this is really one of the items on the order, choose Wrong pair and put that item in Correct BC item no. Otherwise choose Not a product line or Extraction error if one fits, or Not sure."},
	{"  BC side only", "If this is a surcharge, carriage or similar, choose Not a product line. If it is a real product the acknowledgement does not show, choose Not sure and write \"not on POA\" in Notes."},
	{"The same line twice", "When the system treats several lines as one product (say two divan halves against one base), each pairing has its own row. Choose Right pair if this POA line is part of this BC product."},
}

func writeGoldInstructions(f *excelize.File, bold int) error {
	const sheet = goldSheetInstructions
	if err := f.SetColWidth(sheet, "A", "A", 22); err != nil {
		return err
	}
	if err := f.SetColWidth(sheet, "B", "B", 110); err != nil {
		return err
	}
	wrap, err := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	if err != nil {
		return err
	}
	for i, line := range goldInstructions {
		row := i + 1
		if line.term != "" {
			if err := f.SetCellValue(sheet, fmt.Sprintf("A%d", row), line.term); err != nil {
				return err
			}
			if err := f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), bold); err != nil {
				return err
			}
		}
		if line.text != "" {
			if err := f.SetCellValue(sheet, fmt.Sprintf("B%d", row), line.text); err != nil {
				return err
			}
			if err := f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), wrap); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeGoldSummary(f *excelize.File, bold int, rows []goldRow, orders int) error {
	const sheet = goldSheetSummary
	if err := f.SetColWidth(sheet, "A", "A", 44); err != nil {
		return err
	}
	if err := f.SetColWidth(sheet, "B", "E", 12); err != nil {
		return err
	}

	next := 1
	put := func(heading bool, values ...any) error {
		cell := fmt.Sprintf("A%d", next)
		if err := f.SetSheetRow(sheet, cell, &values); err != nil {
			return err
		}
		if heading {
			last, err := excelize.CoordinatesToCellName(len(values), next)
			if err != nil {
				return err
			}
			if err := f.SetCellStyle(sheet, cell, last, bold); err != nil {
				return err
			}
		}
		next++
		return nil
	}

	byType, byVerdict := map[string]int{}, map[string]int{}
	byVendor := map[string]map[string]int{}
	vendorTotal := map[string]int{}
	for _, r := range rows {
		byType[r.kind]++
		byVerdict[r.verdict]++
		vendorTotal[r.vendor]++
		if byVendor[r.vendor] == nil {
			byVendor[r.vendor] = map[string]int{}
		}
		byVendor[r.vendor][r.kind]++
	}

	sections := []func() error{
		func() error {
			if err := put(true, "Gold set summary"); err != nil {
				return err
			}
			if err := put(false, "Generated", time.Now().Format("2006-01-02")); err != nil {
				return err
			}
			if err := put(false, "Engine version", domain.EngineVersion); err != nil {
				return err
			}
			if err := put(false, "Purchase orders (latest fixture per PF)", orders); err != nil {
				return err
			}
			return put(false, "Control sample seed", goldSetSeed)
		},
		func() error {
			if err := put(true, "By RowType", "Rows"); err != nil {
				return err
			}
			for _, k := range []string{goldRowFlagged, goldRowControl, goldRowUnmatched} {
				if err := put(false, k, byType[k]); err != nil {
					return err
				}
			}
			return put(true, "Total", len(rows))
		},
		func() error {
			if err := put(true, "By vendor", goldRowFlagged, goldRowControl, goldRowUnmatched, "Total"); err != nil {
				return err
			}
			for _, v := range sortedByCount(vendorTotal) {
				c := byVendor[v]
				if err := put(false, v, c[goldRowFlagged], c[goldRowControl], c[goldRowUnmatched], vendorTotal[v]); err != nil {
					return err
				}
			}
			return nil
		},
		func() error {
			if err := put(true, "By engine verdict", "Rows"); err != nil {
				return err
			}
			for _, v := range sortedByCount(byVerdict) {
				if err := put(false, v, byVerdict[v]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	for _, section := range sections {
		if err := section(); err != nil {
			return err
		}
		next++
	}
	return nil
}
