package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// changed is sample() with BC's quantity moved, so its content differs.
func changed() (domain.POAOrder, domain.BCOrder, domain.Result) {
	poa, bc, _ := sample()
	bc.Products = append([]domain.BCProduct(nil), bc.Products...)
	bc.Products[0].Qty = 2
	return poa, bc, domain.Reconcile(poa, bc, domain.DefaultConfig())
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
	poa, bc, res = changed()
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

// TestFixtureWriter_SkipsUnchangedContent: re-running an order that nothing
// has changed adds no evidence, only weight in every sweep. Only the latest
// fixture is compared, so content that changes and changes back is kept.
func TestFixtureWriter_SkipsUnchangedContent(t *testing.T) {
	dir := t.TempDir()
	var logged []string
	w := NewFixtureWriter(true, dir, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	at := func(day int) RunRecord {
		return RunRecord{PF: "PF130368", RunID: fmt.Sprint(day), StartedAt: time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC)}
	}

	poa, bc, res := sample()
	first, err := w.Write(at(4), poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	again, err := w.Write(at(5), poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("unchanged re-run returned %s, want the existing %s", again, first)
	}
	if !strings.Contains(logged[len(logged)-1], "not written") {
		t.Errorf("skip not logged; last line %q", logged[len(logged)-1])
	}

	cpoa, cbc, cres := changed()
	if _, err := w.Write(at(6), cpoa, cbc, cres); err != nil {
		t.Fatal(err)
	}
	back, err := w.Write(at(7), poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	if back == first {
		t.Error("content that changed back was compared against an older fixture, not the latest")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Errorf("%d files on disk, want 3", len(entries))
	}
}

// TestFixtureWriter_SkipsAcrossSchemas: a schema-1 capture of the same
// content counts as unchanged, so the first run after this change does not
// duplicate every order already in the corpus.
func TestFixtureWriter_SkipsAcrossSchemas(t *testing.T) {
	dir := t.TempDir()
	poa, bc, res := sample()
	old := filepath.Join(dir, "PF130368_20260904T100000Z.json")
	writeSchemaOne(t, old, poa, bc, res)

	w := NewFixtureWriter(true, dir, nil)
	got, err := w.Write(RunRecord{StartedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}, poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	if got != old {
		t.Errorf("Write returned %s, want the schema-1 fixture %s", got, old)
	}
}

// TestFixtureWriter_LatestPath orders by the run timestamp and counter in the
// name, not by string order, and never picks up another PF's file.
func TestFixtureWriter_LatestPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"PF1_20260904T101500Z.json",
		"PF1_20260904T101500Z_2.json",
		"PF1_20260904T101500Z_10.json",
		"PF1_A_20270101T000000Z.json",
		"PF1_notes.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := &FixtureWriter{Dir: dir}
	if got := filepath.Base(w.latestPath("PF1")); got != "PF1_20260904T101500Z_10.json" {
		t.Errorf("latest = %s, want PF1_20260904T101500Z_10.json", got)
	}
	if got := w.latestPath("PF2"); got != "" {
		t.Errorf("latest for an order with no fixture = %q", got)
	}
}

// TestFixture_StoresEachEmbeddingOnce: a schema-2 file keeps the vectors in
// poaOrder and bcOrder only. expected repeating both orders and every line's
// embedding was two thirds of the corpus.
func TestFixture_StoresEachEmbeddingOnce(t *testing.T) {
	dir := t.TempDir()
	w := NewFixtureWriter(true, dir, nil)
	poa, bc, res := sample()
	path, err := w.Write(RunRecord{StartedAt: time.Now()}, poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if n := strings.Count(string(body), `"embedding"`); n != 2 {
		t.Errorf("%d embeddings in the file, want 2 (one per order line)", n)
	}
	var raw struct {
		Expected map[string]json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Expected["poa"]; ok {
		t.Error("expected.poa written; it repeats poaOrder")
	}
	if _, ok := raw.Expected["bc"]; ok {
		t.Error("expected.bc written; it repeats bcOrder")
	}
	// Stripping works on copies: the caller's result keeps its vectors.
	if len(res.Products[0].POA.Embedding) == 0 || len(poa.Products[0].Embedding) == 0 {
		t.Error("writing the fixture cleared the caller's embeddings")
	}

	f, err := LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Expected.POA, f.POA) || !reflect.DeepEqual(f.Expected.BC, f.BC) {
		t.Error("LoadFixture did not restore expected's orders")
	}
}

// TestFixture_ReencodesByteIdentically is the -update promise: a fixture whose
// verdicts did not move comes back byte for byte, in either schema.
func TestFixture_ReencodesByteIdentically(t *testing.T) {
	dir := t.TempDir()
	poa, bc, res := sample()
	w := NewFixtureWriter(true, dir, nil)
	two, err := w.Write(RunRecord{StartedAt: time.Now()}, poa, bc, res)
	if err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(dir, "schema1.json")
	writeSchemaOne(t, one, poa, bc, res)

	for _, path := range []string{one, two} {
		before, _ := os.ReadFile(path)
		f, err := LoadFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		f.Expected = domain.Reconcile(f.POA, f.BC, f.Config)
		after, err := encodeFixture(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("%s: re-encoding an unchanged fixture changed it (%d bytes -> %d)", filepath.Base(path), len(before), len(after))
		}
	}
}

// TestLoadFixture_ReadsSchemaOne: every fixture captured before schema 2
// still loads and replays.
func TestLoadFixture_ReadsSchemaOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	poa, bc, res := sample()
	writeSchemaOne(t, path, poa, bc, res)

	f, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(f.Expected.Products[0].POA.Embedding) == 0 {
		t.Error("schema-1 expected lost its embeddings on load")
	}
	if diff := compareResults(f.Expected, domain.Reconcile(f.POA, f.BC, f.Config)); diff != "" {
		t.Errorf("schema-1 replay differs: %s", diff)
	}
}

// writeSchemaOne writes a fixture the way capture did before schema 2.
func writeSchemaOne(t *testing.T, path string, poa domain.POAOrder, bc domain.BCOrder, res domain.Result) {
	t.Helper()
	body, err := json.MarshalIndent(Fixture{
		Schema: 1, CapturedAt: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), PF: res.POA.PF,
		Config: res.Config, POA: poa, BC: bc, Expected: res,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
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
