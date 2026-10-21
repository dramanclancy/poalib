package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/dramanclancy/poalib/domain"
)

// Fixture is a complete, replayable snapshot of one reconciliation.
//
// It is captured AFTER translation, so replaying it needs no Business
// Central, no SharePoint, no Document Intelligence, no embeddings key and no
// credentials of any kind: POAOrder and BCOrder carry their embeddings, and
// Reconcile is a pure function of the two plus Config.
//
// Expected is the result the capturing run produced. The replay test asserts
// against it, which is how a rewrite proves it still agrees with the engine
// that produced the numbers everybody has already reviewed.
//
// From schema 2 the file does not repeat what the fixture already holds:
// expected.poa and expected.bc are omitted (Reconcile copies its inputs there
// verbatim) and so is every embedding inside expected's lines. Schema 1 stored
// each 1536-float vector three times over. LoadFixture puts the two orders
// back into Expected; the per-line embeddings stay absent there, and nothing
// compares them — the vectors a replay needs are in POA and BC.
type Fixture struct {
	// Schema is bumped when the shape changes so an old fixture fails loudly
	// rather than replaying as something subtly different.
	Schema     int             `json:"schema"`
	CapturedAt time.Time       `json:"capturedAt"`
	PF         string          `json:"pf"`
	Run        RunRecord       `json:"run"`
	Config     domain.Config   `json:"config"`
	POA        domain.POAOrder `json:"poaOrder"`
	BC         domain.BCOrder  `json:"bcOrder"`
	Expected   domain.Result   `json:"expected"`
}

// FixtureSchema is the layout new fixtures are written in. Schema 1 files
// (expected repeating both orders and every embedding) still load, and
// -update rewrites each file in the schema it already has.
const FixtureSchema = 2

// storedResult is domain.Result as a schema-2 fixture stores it. The nil POA
// and BC fields shadow Result's own, so encoding/json omits them; every other
// field of Result is written as is, so a field added to Result later is
// captured without touching this.
type storedResult struct {
	domain.Result
	POA *struct{} `json:"poa,omitempty"`
	BC  *struct{} `json:"bc,omitempty"`
}

// storedFixture is a schema-2 file: Fixture with Expected shadowed by its
// stored form. Expected is Fixture's last field, so the key order is unchanged.
type storedFixture struct {
	Fixture
	Expected storedResult `json:"expected"`
}

// store strips from res what a schema-2 fixture holds elsewhere. It copies
// every slice it changes: res shares its lines' embeddings with the orders the
// caller still holds.
func store(res domain.Result) storedResult {
	products := make([]domain.ProductResult, len(res.Products))
	for i, p := range res.Products {
		p.POA.Embedding, p.BC.Embedding = nil, nil
		products[i] = p
	}
	res.Products = products
	res.UnmatchedPOA = append([]domain.POAProduct(nil), res.UnmatchedPOA...)
	for i := range res.UnmatchedPOA {
		res.UnmatchedPOA[i].Embedding = nil
	}
	res.UnmatchedBC = append([]domain.BCProduct(nil), res.UnmatchedBC...)
	for i := range res.UnmatchedBC {
		res.UnmatchedBC[i].Embedding = nil
	}
	return storedResult{Result: res}
}

// encodeFixture is the one encoding capture and -update share, so a fixture
// whose content did not change re-encodes byte-identically. It writes f in the
// schema f already has: regenerating a schema-1 file's verdicts does not
// migrate it as a side effect.
func encodeFixture(f Fixture) ([]byte, error) {
	if f.Schema == 1 {
		return json.MarshalIndent(f, "", "  ")
	}
	// Loading restores Expected's orders from POA and BC, so an Expected built
	// from any other orders would come back as something it never was.
	if !reflect.DeepEqual(f.Expected.POA, f.POA) || !reflect.DeepEqual(f.Expected.BC, f.BC) {
		return nil, fmt.Errorf("the expected result for %s was not produced from the captured orders", f.PF)
	}
	return json.MarshalIndent(storedFixture{Fixture: f, Expected: store(f.Expected)}, "", "  ")
}

// decodeFixture parses a fixture of either schema, with Expected's orders in
// place.
func decodeFixture(body []byte) (Fixture, error) {
	var f Fixture
	if err := json.Unmarshal(body, &f); err != nil {
		return Fixture{}, err
	}
	switch f.Schema {
	case 1:
	case FixtureSchema:
		f.Expected.POA, f.Expected.BC = f.POA, f.BC
	default:
		return Fixture{}, fmt.Errorf("schema %d, this build reads schemas 1 and %d", f.Schema, FixtureSchema)
	}
	return f, nil
}

// contentHash identifies what a fixture is evidence of — both orders and the
// result — and nothing about the run that captured it, so two captures of an
// unchanged order hash alike whichever schema each was written in.
func contentHash(f Fixture) ([32]byte, error) {
	body, err := json.Marshal(struct {
		POA      domain.POAOrder `json:"poaOrder"`
		BC       domain.BCOrder  `json:"bcOrder"`
		Expected storedResult    `json:"expected"`
	}{f.POA, f.BC, store(f.Expected)})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(body), nil
}

// FixtureWriter saves fixtures to a directory. A nil *FixtureWriter is the
// disabled state and writes nothing, so production behaviour is unchanged
// when capture is off.
type FixtureWriter struct {
	Dir string
	// Log receives one line per fixture written or skipped. nil is fine.
	Log func(format string, args ...any)
}

// NewFixtureWriter returns a writer for dir, or nil when capture is disabled.
// The nil return is the point: callers hold a *FixtureWriter and every method
// is nil-safe, so there is no flag to check at each call site.
func NewFixtureWriter(enabled bool, dir string, log func(string, ...any)) *FixtureWriter {
	if !enabled {
		return nil
	}
	if dir == "" {
		dir = filepath.Join("testdata", "orders")
	}
	return &FixtureWriter{Dir: dir, Log: log}
}

// Enabled reports whether capture is on.
func (w *FixtureWriter) Enabled() bool { return w != nil }

// Write saves one reconciliation as a fixture and returns its path.
//
// The filename leads with the PF so fixtures for one order sort together, and
// carries the run's timestamp so a re-run never silently replaces the
// snapshot an earlier review was based on. If a name somehow collides, a
// counter is appended rather than overwriting: a fixture is evidence, and
// evidence is not overwritten.
//
// A re-run that changed nothing is not new evidence. When both orders and the
// result hash the same as the latest fixture for this PF, nothing is written
// and that fixture's path is returned: re-running one order three times had
// put it in the corpus three times, weighting it triple in every threshold
// sweep. Only the latest is compared, so an order that changes and changes
// back is captured each time.
func (w *FixtureWriter) Write(rec RunRecord, poa domain.POAOrder, bc domain.BCOrder, res domain.Result) (string, error) {
	if !w.Enabled() {
		return "", nil
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return "", fmt.Errorf("creating fixture directory %s: %w", w.Dir, err)
	}

	f := Fixture{
		Schema:     FixtureSchema,
		CapturedAt: time.Now().UTC(),
		PF:         res.POA.PF,
		Run:        rec,
		Config:     res.Config,
		POA:        poa,
		BC:         bc,
		Expected:   res,
	}
	body, err := encodeFixture(f)
	if err != nil {
		return "", fmt.Errorf("encoding fixture for %s: %w", res.POA.PF, err)
	}
	if prev := w.unchangedSince(body); prev != "" {
		if w.Log != nil {
			w.Log("fixture for %s not written: content unchanged since %s", res.POA.PF, prev)
		}
		return prev, nil
	}

	path := w.freePath(res.POA.PF, rec.StartedAt)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("writing fixture %s: %w", path, err)
	}
	if w.Log != nil {
		w.Log("fixture captured for %s: %s (%d bytes)", res.POA.PF, path, len(body))
	}
	return path, nil
}

// unchangedSince returns the latest fixture for body's PF when it holds the
// same content, or "". The new fixture is hashed from its decoded body, as the
// one on disk is, so both sides pass through the same JSON round trip. Any
// failure to read the earlier file means "write anyway": skipping is an
// economy, and losing a capture to it would not be.
func (w *FixtureWriter) unchangedSince(body []byte) string {
	f, err := decodeFixture(body)
	if err != nil {
		return ""
	}
	prev := w.latestPath(f.PF)
	if prev == "" {
		return ""
	}
	old, err := LoadFixture(prev)
	if err != nil {
		return ""
	}
	a, errA := contentHash(f)
	b, errB := contentHash(old)
	if errA != nil || errB != nil || a != b {
		return ""
	}
	return prev
}

// latestPath returns the most recently captured fixture for pf, or "". It
// orders by the run timestamp and collision counter freePath put in the
// filename, and ignores any name freePath could not have built for this PF —
// including another PF that shares this one's prefix up to an underscore.
func (w *FixtureWriter) latestPath(pf string) string {
	prefix := safeName(pf) + "_"
	paths, err := filepath.Glob(filepath.Join(w.Dir, prefix+"*.json"))
	if err != nil {
		return ""
	}
	var (
		latest string
		at     time.Time
		n      int
	)
	for _, p := range paths {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), prefix), ".json")
		ts, counter, hasCounter := strings.Cut(stamp, "_")
		t, err := time.Parse("20060102T150405Z", ts)
		if err != nil {
			continue
		}
		c := 1
		if hasCounter {
			if c, err = strconv.Atoi(counter); err != nil || c < 2 {
				continue
			}
		}
		if latest == "" || t.After(at) || (t.Equal(at) && c > n) {
			latest, at, n = p, t, c
		}
	}
	return latest
}

// freePath builds a filename that does not already exist.
func (w *FixtureWriter) freePath(pf string, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	base := fmt.Sprintf("%s_%s", safeName(pf), at.UTC().Format("20060102T150405Z"))
	path := filepath.Join(w.Dir, base+".json")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		path = filepath.Join(w.Dir, fmt.Sprintf("%s_%d.json", base, i))
	}
}

// safeName keeps a PF usable as a filename on every platform.
func safeName(s string) string {
	if s == "" {
		return "unknown"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// LoadFixture reads one fixture from disk, in either schema.
func LoadFixture(path string) (Fixture, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	f, err := decodeFixture(body)
	if err != nil {
		return Fixture{}, fmt.Errorf("fixture %s: %w", path, err)
	}
	return f, nil
}

// LoadFixtures reads every fixture in dir, in filename order.
func LoadFixtures(dir string) ([]Fixture, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]Fixture, 0, len(paths))
	for _, p := range paths {
		f, err := LoadFixture(p)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
