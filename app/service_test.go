package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// The pipeline's own decisions — which scans become one order, where each
// vector lands, and that one PF's failure stays its own — tested with fake
// ports, so no service and no credential is involved.

type fakeFiles struct{}

func (fakeFiles) Get(context.Context, string) ([]byte, error) { return []byte("%PDF"), nil }

type fakeScanner struct{ scans []domain.POAOrder }

func (s fakeScanner) Scan(context.Context, []byte, string) ([]domain.POAOrder, error) {
	return s.scans, nil
}

// fakeOrders returns one BC product per PF, described as "<PF> sofa", and
// fails for the PF named in fail.
type fakeOrders struct{ fail string }

func (o fakeOrders) Order(_ context.Context, pf string) (domain.BCOrder, OrderStats, error) {
	if pf == o.fail {
		return domain.BCOrder{}, OrderStats{}, errors.New("BC unavailable")
	}
	return domain.BCOrder{
		PF:         pf,
		Enrichment: domain.EnrichmentApplied,
		Products:   []domain.BCProduct{{ItemNo: "IT1", LineType: "Item", Description: pf + " sofa", Qty: 1, UnitCost: 100, Net: 100}},
	}, OrderStats{EnrichmentAsked: true}, nil
}

// oneHotEmbedder gives every distinct text its own axis, so two texts score 1
// when identical and 0 otherwise, and records what it was asked to embed.
type oneHotEmbedder struct {
	axis  map[string]int
	asked []string
	short bool // return one vector too few
}

func (e *oneHotEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.asked = append(e.asked, texts...)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if _, ok := e.axis[t]; !ok {
			e.axis[t] = len(e.axis)
		}
		v := make([]float32, 64)
		v[e.axis[t]%64] = 1
		out[i] = v
	}
	if e.short {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (e *oneHotEmbedder) Describe() string { return "fake one-hot" }

func newService(scans []domain.POAOrder, orders fakeOrders, emb *oneHotEmbedder) *Service {
	return &Service{
		Files:    fakeFiles{},
		Scanner:  fakeScanner{scans: scans},
		Orders:   orders,
		Embedder: emb,
		Config:   domain.DefaultConfig(),
		Log:      func(string, ...any) {},
	}
}

func line(desc string, qty int, unit float64) domain.POAProduct {
	return domain.POAProduct{Description: desc, Qty: qty, UnitPrice: unit}
}

// TestService_MergesThePagesOfOnePurchaseOrder: two pages of PF1 become one
// order whose total is the sum of its lines — each page prints its own page
// total, and neither is the order total — and results come back in the order
// the PFs first appeared.
func TestService_MergesThePagesOfOnePurchaseOrder(t *testing.T) {
	scans := []domain.POAOrder{
		{PF: "PF1", TotalExVAT: domain.Ptr(999.0), Products: []domain.POAProduct{line("PF1 sofa", 1, 100)}},
		{PF: "PF2", TotalExVAT: domain.Ptr(100.0), Products: []domain.POAProduct{line("PF2 sofa", 1, 100)}},
		{PF: "PF1", TotalExVAT: domain.Ptr(888.0), Products: []domain.POAProduct{line("PF1 footstool", 2, 40)}},
	}
	out, err := newService(scans, fakeOrders{}, &oneHotEmbedder{axis: map[string]int{}}).
		Analyze(context.Background(), "POA/x.pdf", "Model")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(out) != 2 || out[0].PF != "PF1" || out[1].PF != "PF2" {
		t.Fatalf("outcomes = %v, want PF1 then PF2", pfs(out))
	}
	pf1 := out[0]
	if got := len(pf1.Result.POA.Products); got != 2 {
		t.Errorf("PF1 has %d lines, want both pages' lines (2)", got)
	}
	if got, _ := domain.Deref(pf1.Result.POA.TotalExVAT); got != 180 {
		t.Errorf("PF1 total = %g, want 180 (100 + 2x40), not either page's printed total", got)
	}
	if pf1.Run.POAPagesMerged != 2 {
		t.Errorf("POAPagesMerged = %d, want 2", pf1.Run.POAPagesMerged)
	}
	// A single-page order keeps the total it printed.
	if got, _ := domain.Deref(out[1].Result.POA.TotalExVAT); got != 100 {
		t.Errorf("PF2 total = %g, want its printed 100", got)
	}
}

// TestService_OneFailingOrderDoesNotFailTheOthers is business rule 8.
func TestService_OneFailingOrderDoesNotFailTheOthers(t *testing.T) {
	scans := []domain.POAOrder{
		{PF: "PF1", Products: []domain.POAProduct{line("PF1 sofa", 1, 100)}},
		{PF: "PF2", Products: []domain.POAProduct{line("PF2 sofa", 1, 100)}},
	}
	out, err := newService(scans, fakeOrders{fail: "PF1"}, &oneHotEmbedder{axis: map[string]int{}}).
		Analyze(context.Background(), "POA/x.pdf", "Model")
	if err != nil {
		t.Fatalf("Analyze failed the whole request for one PF: %v", err)
	}
	if out[0].Err == nil {
		t.Error("PF1's failure was not reported on its outcome")
	}
	if out[1].Err != nil || !out[1].Result.WriteOK {
		t.Errorf("PF2 = err %v, writeOK %v; want a clean result unaffected by PF1", out[1].Err, out[1].Result.WriteOK)
	}
}

// TestService_EachVectorLandsOnItsOwnProduct: POA and BC texts go out in one
// batch, and the vectors come back by position. Shifting by one would pair
// every line on its neighbour's description.
func TestService_EachVectorLandsOnItsOwnProduct(t *testing.T) {
	emb := &oneHotEmbedder{axis: map[string]int{}}
	scans := []domain.POAOrder{{PF: "PF1", Products: []domain.POAProduct{
		line("Metro footstool", 1, 40),
		line("PF1 sofa", 1, 100),
	}}}
	out, err := newService(scans, fakeOrders{}, emb).Analyze(context.Background(), "POA/x.pdf", "Model")
	if err != nil || out[0].Err != nil {
		t.Fatalf("Analyze: %v / %v", err, out[0].Err)
	}

	if len(emb.asked) != 3 {
		t.Fatalf("embedded %d texts in total, want 3 (2 POA + 1 BC, one batch)", len(emb.asked))
	}
	r := out[0].Result
	if len(r.Products) != 1 || r.Products[0].POAIndex != 1 {
		t.Fatalf("paired %+v, want the BC sofa paired with POA line 1 (the sofa)", r.Products)
	}
	if s := r.Products[0].Description.SimilarityScore; s == nil || *s < 0.99 {
		t.Errorf("sofa against sofa scored %v, want 1: the vectors landed on the wrong products", s)
	}
}

// TestService_ShortEmbeddingResponseFailsTheOrder: a provider returning fewer
// vectors than asked must fail that order, not misalign the rest silently.
func TestService_ShortEmbeddingResponseFailsTheOrder(t *testing.T) {
	scans := []domain.POAOrder{{PF: "PF1", Products: []domain.POAProduct{line("PF1 sofa", 1, 100)}}}
	out, _ := newService(scans, fakeOrders{}, &oneHotEmbedder{axis: map[string]int{}, short: true}).
		Analyze(context.Background(), "POA/x.pdf", "Model")
	if out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "asked for 2 vectors, got 1") {
		t.Errorf("err = %v, want the vector-count mismatch reported", out[0].Err)
	}
}

// TestService_NothingExtractedIsAnError: an empty scan is a request-level
// failure, not an empty success.
func TestService_NothingExtractedIsAnError(t *testing.T) {
	_, err := newService(nil, fakeOrders{}, &oneHotEmbedder{axis: map[string]int{}}).
		Analyze(context.Background(), "POA/x.pdf", "Model")
	if err == nil {
		t.Error("a document with no purchase orders returned no error")
	}
}

func pfs(out []Outcome) []string {
	s := make([]string, len(out))
	for i, o := range out {
		s[i] = o.PF
	}
	return s
}

// TestRunRecord_Degradations: each missing input produces its own sentence,
// and a complete run produces none — the Summary sheet and the response's
// degraded flag both read this.
func TestRunRecord_Degradations(t *testing.T) {
	full := RunRecord{EmbeddingModel: "m", EnrichmentAsked: true, ItemsRequested: 3, ItemsReturned: 3, ItemsWithSeats: 1, ItemsWithCodes: 1}
	tests := []struct {
		name string
		edit func(*RunRecord)
		want string // substring of the one expected degradation, "" for none
	}{
		{"complete", func(*RunRecord) {}, ""},
		{"enrichment not asked", func(r *RunRecord) { r.EnrichmentAsked = false }, "not requested"},
		{"enrichment failed", func(r *RunRecord) { r.EnrichmentError = "timeout" }, "failed (timeout)"},
		{"enrichment partial", func(r *RunRecord) { r.ItemsReturned = 2 }, "returned 2 of 3"},
		{"item names failed", func(r *RunRecord) { r.ItemNamesError = "503" }, "Item names could not be fetched"},
		{"no embedding model", func(r *RunRecord) { r.EmbeddingModel = "" }, "No embedding provider"},
		// An item number with no record is a data fact, not a degraded run.
		{"item name missing, no error", func(r *RunRecord) { r.ItemNamesRequested, r.ItemNamesReturned = 3, 2 }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := full
			tc.edit(&r)
			got := r.Degradations()
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("degradations = %q, want none", got)
			case tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)):
				t.Errorf("degradations = %q, want exactly one containing %q", got, tc.want)
			}
			if r.Degraded() != (tc.want != "") {
				t.Errorf("Degraded() = %v", r.Degraded())
			}
		})
	}
}
