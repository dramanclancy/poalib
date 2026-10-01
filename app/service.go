package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dramanclancy/poalib/domain"
)

// Service is the pipeline. It holds the ports and the tuning, and nothing else.
type Service struct {
	Files    FileStore
	Scanner  DocScanner
	Orders   OrderSource
	Embedder Embedder
	// Fixtures is optional: nil means capture is disabled and nothing is
	// written.
	Fixtures *FixtureWriter

	Config domain.Config
	Log    func(format string, args ...any)
}

// Outcome is one reconciled purchase order: either a completed result and the
// record of how it was produced, or Err.
//
// One failing purchase order must never fail the whole request, so the error
// is carried here per PO rather than returned.
type Outcome struct {
	PF     string
	Result domain.Result
	Run    RunRecord
	Err    error
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Analyze runs the whole pipeline for one document: fetch, scan, then
// reconcile every purchase order the document acknowledges.
func (s *Service) Analyze(ctx context.Context, path, modelID string) ([]Outcome, error) {
	pdf, err := s.Files.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", path, err)
	}
	scans, err := s.Scanner.Scan(ctx, pdf, modelID)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}
	if len(scans) == 0 {
		return nil, fmt.Errorf("no purchase orders extracted from %s", path)
	}
	return s.Reconcile(ctx, scans), nil
}

// Reconcile builds the BC side for every distinct PF in scans, embeds both
// sides, and compares them. One Outcome per PF, in first-seen order.
func (s *Service) Reconcile(ctx context.Context, scans []domain.POAOrder) []Outcome {
	orders, pages, pfs := groupScansByPF(scans)

	out := make([]Outcome, 0, len(pfs))
	for _, pf := range pfs {
		out = append(out, s.reconcileOne(ctx, orders[pf], pages[pf]))
	}
	return out
}

func (s *Service) reconcileOne(ctx context.Context, poaOrder domain.POAOrder, pages int) Outcome {
	rec := RunRecord{
		PF:                poaOrder.PF,
		RunID:             fmt.Sprintf("%s-%d", poaOrder.PF, time.Now().UnixNano()),
		StartedAt:         time.Now().UTC(),
		EngineVersion:     domain.EngineVersion,
		Config:            s.Config,
		POALinesExtracted: len(poaOrder.Products),
		POAPagesMerged:    pages,
		FixturesEnabled:   s.Fixtures.Enabled(),
	}
	if s.Embedder != nil {
		rec.EmbeddingModel = s.Embedder.Describe()
	}

	bcOrder, stats, err := s.Orders.Order(ctx, poaOrder.PF)
	if err != nil {
		return Outcome{PF: poaOrder.PF, Run: rec, Err: err}
	}
	rec.OrderNumberUsed = stats.OrderNumberUsed
	rec.BCLinesFetched = stats.LinesFetched
	rec.BCProductsBuilt = len(bcOrder.Products)
	rec.EnrichmentState = string(bcOrder.Enrichment)
	rec.EnrichmentAsked = stats.EnrichmentAsked
	rec.ItemsRequested = stats.ItemsRequested
	rec.ItemsReturned = stats.ItemsReturned
	rec.ItemsWithSeats = stats.ItemsWithSeats
	rec.ItemsWithCodes = stats.ItemsWithCodes
	rec.EnrichmentError = stats.EnrichmentError

	if err := s.attachEmbeddings(ctx, &poaOrder, &bcOrder); err != nil {
		return Outcome{PF: poaOrder.PF, Run: rec, Err: err}
	}

	result := domain.Reconcile(poaOrder, bcOrder, s.Config)
	rec.LinesMatched = len(result.Products)
	rec.LinesFlagged = len(result.Flagged())

	if rec.OrderNumberUsed != "" {
		s.logf("%s: no such purchase order in BC; reconciled against %s instead",
			poaOrder.PF, rec.OrderNumberUsed)
	}
	if rec.Degraded() {
		s.logf("%s reconciled in a degraded state: %s", poaOrder.PF, rec.Summary())
	}

	// Capture after the comparison, so the fixture carries the result to
	// replay against.
	if s.Fixtures.Enabled() {
		path, err := s.Fixtures.Write(rec, poaOrder, bcOrder, result)
		if err != nil {
			// Capture is a development aid. Losing it must never fail an
			// order that reconciled correctly.
			rec.FixtureError = err.Error()
			s.logf("capturing fixture for %s: %s", poaOrder.PF, err)
		} else {
			rec.FixturePath = path
		}
	}

	return Outcome{PF: poaOrder.PF, Result: result, Run: rec}
}

// attachEmbeddings fills Embedding on every product of both orders, in one
// batched call for the pair rather than one per product or per candidate.
//
// This is the only I/O the comparison needs, and it happens before Reconcile
// so Reconcile stays pure. There is no degraded mode: an error here fails the
// order, because an unembedded line scores zero against everything, which
// reads as "definitely not this product" rather than "we could not tell".
func (s *Service) attachEmbeddings(ctx context.Context, poa *domain.POAOrder, bc *domain.BCOrder) error {
	if s.Embedder == nil {
		return fmt.Errorf("no embedder configured")
	}
	texts := make([]string, 0, len(poa.Products)+len(bc.Products))
	for _, p := range poa.Products {
		texts = append(texts, domain.EmbeddingText(p.Text()))
	}
	for _, b := range bc.Products {
		texts = append(texts, domain.EmbeddingText(b.Description))
	}
	if len(texts) == 0 {
		return nil
	}

	vectors, err := s.Embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embedding descriptions: %w", err)
	}
	if len(vectors) != len(texts) {
		return fmt.Errorf("embedding descriptions: asked for %d vectors, got %d", len(texts), len(vectors))
	}
	for i := range poa.Products {
		poa.Products[i].Embedding = vectors[i]
	}
	for i := range bc.Products {
		bc.Products[i].Embedding = vectors[len(poa.Products)+i]
	}
	return nil
}

// groupScansByPF folds the pages of a multi-page scan back into one order per
// PF, and returns the PF numbers in first-seen order — map iteration must not
// decide the order results come back in.
//
// A merged order's printed total is replaced by the sum of its lines: each
// page prints its own page total, and no one of those is the order total.
func groupScansByPF(scans []domain.POAOrder) (map[string]domain.POAOrder, map[string]int, []string) {
	byPF := make(map[string]domain.POAOrder, len(scans))
	pages := make(map[string]int, len(scans))
	order := make([]string, 0, len(scans))

	for _, scan := range scans {
		pages[scan.PF]++
		existing, ok := byPF[scan.PF]
		if !ok {
			byPF[scan.PF] = scan
			order = append(order, scan.PF)
			continue
		}
		existing.Products = append(existing.Products, scan.Products...)
		byPF[scan.PF] = existing
	}
	for pf, n := range pages {
		if n > 1 {
			merged := byPF[pf]
			merged.TotalExVAT = domain.Ptr(merged.NetFromLines())
			byPF[pf] = merged
		}
	}
	return byPF, pages, order
}
