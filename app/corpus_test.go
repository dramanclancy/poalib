package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// corpusDir is where captured fixtures live. Override with POA_FIXTURE_DIR to
// replay a batch kept somewhere else.
func corpusDir() string {
	if d := os.Getenv("POA_FIXTURE_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "testdata", "orders")
}

// updateCorpus rewrites each fixture's expected result from the current
// engine:
//
//	go test ./app/ -run TestCorpus -update
//
// Use it only when a behaviour change is intended AND has been measured. The
// captured result is the record of what the engine told a person who then
// acted on it; regenerating it silently turns the corpus from evidence into a
// mirror that agrees with whatever the code now does. Run TestCorpus first and
// read what moved — it names the first difference in each order — and say what
// was regenerated, and why, in the commit that does it.
var updateCorpus = flag.Bool("update", false, "rewrite fixture expectations from the current engine")

// TestCorpus replays every captured order through the comparison, offline.
//
// This is what makes the engine measurable. A fixture holds both built orders
// with their embeddings, so a replay needs no Business Central, no SharePoint,
// no Document Intelligence, no embeddings key and no credentials — which means
// a threshold can be changed and its effect on real orders seen in under a
// second, and a regression is caught by a test rather than by someone reading
// a workbook.
//
// It skips when the corpus is empty. To fill it, run the service once with
// CAPTURE_FIXTURES=true and copy the JSON files into testdata/orders/.
func TestCorpus(t *testing.T) {
	if *updateCorpus {
		rewriteCorpus(t)
	}
	fixtures := loadCorpus(t)

	var lines, flagged, degraded int
	for _, f := range fixtures {
		t.Run(f.PF, func(t *testing.T) {
			got := domain.Reconcile(f.POA, f.BC, f.Config)
			if diff := compareResults(f.Expected, got); diff != "" {
				t.Errorf("replay differs from the captured result: %s", diff)
			}
			lines += len(got.Products)
			flagged += len(got.Flagged())
			if f.Run.Degraded() {
				degraded++
			}
		})
	}

	// The numbers a calibration decision is actually made on.
	if lines > 0 {
		t.Logf("corpus: %d orders, %d matched lines, %d flagged (%.0f%%), %d orders ran degraded",
			len(fixtures), lines, flagged, 100*float64(flagged)/float64(lines), degraded)
	}
}

// TestCorpus_ThresholdSweep reports what the flag rate would be at other
// thresholds. It asserts nothing — it exists so the question "where should
// DescThreshold sit?" is answered by running one test rather than by
// re-running a batch against live services.
func TestCorpus_ThresholdSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("sweep is a calibration aid, not a check")
	}
	fixtures := loadCorpus(t)

	t.Logf("%-8s %-8s %8s %8s %8s", "desc", "margin", "lines", "flagged", "rate")
	for _, desc := range []float64{0.40, 0.50, 0.60, 0.70} {
		for _, margin := range []float64{0.02, 0.05, 0.10, 0.15} {
			var lines, flagged int
			for _, f := range fixtures {
				cfg := f.Config
				cfg.DescThreshold, cfg.MarginThreshold = desc, margin
				res := domain.Reconcile(f.POA, f.BC, cfg)
				lines += len(res.Products)
				flagged += len(res.Flagged())
			}
			if lines == 0 {
				continue
			}
			t.Logf("%-8.2f %-8.2f %8d %8d %7.0f%%", desc, margin, lines, flagged, 100*float64(flagged)/float64(lines))
		}
	}
}

// TestCorpus_SplitAndAggregatedLines pins the intended outcome on the real
// orders that exposed the one-to-one assumption — supplier and BC laying the
// same goods out as different numbers of lines. TestCorpus only asserts that a
// replay agrees with the stored expectation, and -update rewrites that; these
// assertions are what stop a later regeneration quietly turning them back
// into false negatives. Each case skips when its fixture is not on disk.
func TestCorpus_SplitAndAggregatedLines(t *testing.T) {
	cases := []struct {
		pf           string
		writeOK      bool
		groups       int
		unmatchedPOA int
		why          string
	}{
		{"PF131114", true, 1, 0, "two wing-chair lines are BC's one line of 2"},
		{"PF131048", false, 2, 5, "Eastbury and arm caps aggregate; '-' lines and free scatter packs stay unmatched"},
		{"PF130794", true, 0, 0, "a Zip & Link mattress acknowledged as 2 parts at half BC's unit price"},
		{"PF130999", true, 0, 0, "the same Zip & Link mattress on a later order"},
		{"PF131012", false, 1, 0, "two divan halves are one base; descriptions too weak to approve"},
		{"PF130543", false, 1, 0, "one acknowledged qty 2 is BC's same item on two lines"},
		{"PF130884", false, 0, 1, "chaise + sofa unit, but the money differs: no structure is corroborated"},
	}
	for _, c := range cases {
		t.Run(c.pf, func(t *testing.T) {
			paths, _ := filepath.Glob(filepath.Join(corpusDir(), c.pf+"_*.json"))
			if len(paths) == 0 {
				t.Skipf("no fixture for %s", c.pf)
			}
			f, err := LoadFixture(paths[len(paths)-1])
			if err != nil {
				t.Fatal(err)
			}
			got := domain.Reconcile(f.POA, f.BC, f.Config)
			groups := map[*domain.LineGroup]bool{}
			for _, p := range got.Products {
				if p.Group != nil {
					groups[p.Group] = true
				}
			}
			if got.WriteOK != c.writeOK || len(groups) != c.groups || len(got.UnmatchedPOA) != c.unmatchedPOA {
				t.Errorf("%s: WriteOK %v, %d groups, %d unmatched POA lines; want %v, %d, %d",
					c.why, got.WriteOK, len(groups), len(got.UnmatchedPOA), c.writeOK, c.groups, c.unmatchedPOA)
			}
		})
	}
}

func groupLabel(g *domain.LineGroup) string {
	if g == nil {
		return ""
	}
	return g.String()
}

func loadCorpus(t *testing.T) []Fixture {
	t.Helper()
	dir := corpusDir()
	fixtures, err := LoadFixtures(dir)
	if err != nil {
		t.Fatalf("loading fixtures from %s: %v", dir, err)
	}
	if len(fixtures) == 0 {
		t.Skipf("no fixtures in %s — run the service with CAPTURE_FIXTURES=true and copy the JSON files there", dir)
	}
	return fixtures
}

// compareResults reports the first meaningful difference between a captured
// result and a replayed one, or "" when they agree.
//
// It compares the decisions, not every float: which BC line each POA line was
// paired with, what each check concluded, which discrepancies were raised, and
// whether the order passed the gate. Those are what a person acted on.
func compareResults(want, got domain.Result) string {
	if want.WriteOK != got.WriteOK {
		return fmt.Sprintf("WriteOK %v, want %v", got.WriteOK, want.WriteOK)
	}
	if len(want.Products) != len(got.Products) {
		return fmt.Sprintf("%d matched lines, want %d", len(got.Products), len(want.Products))
	}
	if len(want.UnmatchedPOA) != len(got.UnmatchedPOA) {
		return fmt.Sprintf("%d unmatched POA lines, want %d", len(got.UnmatchedPOA), len(want.UnmatchedPOA))
	}
	if len(want.UnmatchedBC) != len(got.UnmatchedBC) {
		return fmt.Sprintf("%d unmatched BC lines, want %d", len(got.UnmatchedBC), len(want.UnmatchedBC))
	}
	if want.Totals.Status != got.Totals.Status {
		return fmt.Sprintf("totals %q, want %q", got.Totals.Status, want.Totals.Status)
	}

	for i := range want.Products {
		w, g := want.Products[i], got.Products[i]
		if w.POAIndex != g.POAIndex || w.BC.LineID != g.BC.LineID {
			return fmt.Sprintf("line %d paired POA %d with %s, want POA %d with %s",
				i, g.POAIndex, g.BC.LineID, w.POAIndex, w.BC.LineID)
		}
		if a, b := groupLabel(w.Group), groupLabel(g.Group); a != b {
			return fmt.Sprintf("line %d (POA %d) grouped as %q, want %q", i, w.POAIndex, b, a)
		}
		wc, gc := w.Checks(), g.Checks()
		for j := range wc {
			if wc[j].Status != gc[j].Status {
				return fmt.Sprintf("line %d (POA %d): %s check %q, want %q",
					i, w.POAIndex, gc[j].CheckType, gc[j].Status, wc[j].Status)
			}
		}
		if a, b := kindList(w.Discrepancies()), kindList(g.Discrepancies()); a != b {
			return fmt.Sprintf("line %d (POA %d) raised [%s], want [%s]", i, w.POAIndex, b, a)
		}
	}
	return ""
}

func kindList(ds []domain.Discrepancy) string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, string(d.Kind))
	}
	return strings.Join(out, " ")
}

// rewriteCorpus replays every fixture and writes the new result back into it,
// leaving the captured inputs — both orders, the embeddings, the run record
// and the thresholds — untouched. Only Expected changes. It re-encodes with
// the same MarshalIndent the capture path uses, so a fixture whose verdicts
// did not move comes back byte-identical and the diff shows exactly what did.
func rewriteCorpus(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(corpusDir(), "*.json"))
	if err != nil {
		t.Fatalf("listing fixtures: %v", err)
	}
	for _, p := range paths {
		f, err := LoadFixture(p)
		if err != nil {
			t.Fatalf("loading %s: %v", p, err)
		}
		f.Expected = domain.Reconcile(f.POA, f.BC, f.Config)
		body, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatalf("encoding %s: %v", p, err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	t.Logf("rewrote expectations in %d fixtures", len(paths))
}
