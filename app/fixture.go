package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// FixtureSchema is the current fixture layout version.
const FixtureSchema = 1

// FixtureWriter saves fixtures to a directory. A nil *FixtureWriter is the
// disabled state and writes nothing, so production behaviour is unchanged
// when capture is off.
type FixtureWriter struct {
	Dir string
	// Log receives one line per fixture written. nil is fine.
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

// Write saves one reconciliation as a fixture and returns the path written.
//
// The filename leads with the PF so fixtures for one order sort together, and
// carries the run's timestamp so a re-run never silently replaces the
// snapshot an earlier review was based on. If a name somehow collides, a
// counter is appended rather than overwriting: a fixture is evidence, and
// evidence is not overwritten.
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
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding fixture for %s: %w", res.POA.PF, err)
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

// LoadFixture reads one fixture from disk.
func LoadFixture(path string) (Fixture, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var f Fixture
	if err := json.Unmarshal(body, &f); err != nil {
		return Fixture{}, fmt.Errorf("parsing fixture %s: %w", path, err)
	}
	if f.Schema != FixtureSchema {
		return Fixture{}, fmt.Errorf("fixture %s is schema %d, this build reads schema %d", path, f.Schema, FixtureSchema)
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
