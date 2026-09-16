package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dramanclancy/poalib/domain"
)

// sample builds a small reconciliation with embeddings attached, the way the
// pipeline hands one to the fixture writer.
func sample() (domain.POAOrder, domain.BCOrder, domain.Result) {
	poa := domain.POAOrder{PF: "PF130368", Products: []domain.POAProduct{
		{Code: "NFX3S (A)", Description: "NEW FELIX 3 seater sofa", Qty: 1, UnitPrice: 657,
			Embedding: []float32{1, 0, 0}},
	}}
	bc := domain.BCOrder{
		PF: "PF130368", VendorNo: "VX1", VendorName: "Ashwood Designs",
		TotalExVAT: 657, Enrichment: domain.EnrichmentApplied,
		Products: []domain.BCProduct{{
			LineID: "bc-1", ItemNo: "IT0188673", LineType: "Item",
			Description: "3 Seater Sofa", Qty: 1, UnitCost: 657, Net: 657,
			Detail:    &domain.ItemDetail{ItemNo: "IT0188673", Seats: domain.Ptr(3.0)},
			Embedding: []float32{1, 0, 0},
		}},
	}
	return poa, bc, domain.Reconcile(poa, bc, domain.DefaultConfig())
}

// TestFixtureWriter_DisabledWritesNothing is the safety requirement: with
// capture off, production behaviour is untouched and no file appears.
func TestFixtureWriter_DisabledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	w := NewFixtureWriter(false, dir, nil)

	if w.Enabled() {
		t.Fatal("writer reports enabled when capture is off")
	}
	poa, bc, res := sample()
	path, err := w.Write(RunRecord{}, poa, bc, res)
	if err != nil || path != "" {
		t.Errorf("Write returned (%q, %v), want empty and no error", path, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("%d files written with capture disabled", len(entries))
	}
}

// TestFixtureWriter_CreatesDirectoryAndFile: enabled capture creates what it
// needs and names the file after the order.
func TestFixtureWriter_CreatesDirectoryAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "testdata", "orders")
	var logged string
	w := NewFixtureWriter(true, dir, func(format string, args ...any) { logged = format })

	poa, bc, res := sample()
	rec := RunRecord{PF: res.POA.PF, StartedAt: time.Date(2026, 9, 4, 10, 15, 0, 0, time.UTC)}

	path, err := w.Write(rec, poa, bc, res)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture not on disk: %v", err)
	}
	if want := "PF130368_20260904T101500Z.json"; filepath.Base(path) != want {
		t.Errorf("filename = %q, want %q — the PF leads so an order's fixtures sort together", filepath.Base(path), want)
	}
	if logged == "" {
		t.Error("nothing logged; a captured fixture must say where it went")
	}
}

// TestFixtureWriter_NeverOverwrites: a fixture is evidence. A second run at
// the same timestamp gets its own file rather than replacing the snapshot an
// earlier review was based on.
func TestFixtureWriter_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	w := NewFixtureWriter(true, dir, nil)
	poa, bc, res := sample()
	rec := RunRecord{PF: res.POA.PF, StartedAt: time.Date(2026, 9, 4, 10, 15, 0, 0, time.UTC)}

	first, err := w.Write(rec, poa, bc, res)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := w.Write(rec, poa, bc, res)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if first == second {
		t.Fatalf("both writes went to %s; the first fixture was overwritten", first)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d files on disk, want 2", len(entries))
	}
}

// TestFixture_ReplaysWithoutAnyService is the whole point of capture: a
// fixture read back off disk reproduces the same result with no Business
// Central, no Document Intelligence and no embeddings call.
func TestFixture_ReplaysWithoutAnyService(t *testing.T) {
	dir := t.TempDir()
	w := NewFixtureWriter(true, dir, nil)
	poa, bc, res := sample()

	path, err := w.Write(RunRecord{PF: res.POA.PF, StartedAt: time.Now()}, poa, bc, res)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	f, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if f.Schema != FixtureSchema {
		t.Errorf("schema = %d, want %d", f.Schema, FixtureSchema)
	}
	// Embeddings have to survive the round trip, or the replay scores
	// everything zero and proves nothing.
	if len(f.POA.Products[0].Embedding) == 0 || len(f.BC.Products[0].Embedding) == 0 {
		t.Fatal("embeddings did not survive capture; replay would not be deterministic")
	}

	replayed := domain.Reconcile(f.POA, f.BC, f.Config)
	if diff := compareResults(f.Expected, replayed); diff != "" {
		t.Errorf("replay differs from the captured result: %s", diff)
	}
}

// TestLoadFixture_RejectsAnUnknownSchema: an old snapshot must fail loudly
// rather than replay as something subtly different.
func TestLoadFixture_RejectsAnUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(path, []byte(`{"schema":0,"pf":"PF1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(path); err == nil {
		t.Error("a schema-0 fixture loaded without complaint")
	}
}
