package domain

// EngineVersion identifies the comparison logic that produced a result. It is
// recorded on every review row and in the run record, so a later change in
// scoring is never silently attributed to an older run.
const EngineVersion = "v8-layered"

// Config is every number the comparison can be tuned by, in one struct.
//
// These were constants until the twelve-order batch of 2026-09-04 showed that
// nobody knew where they belonged: 53% of correct pairings were flagged, and
// answering that needs a sweep over a corpus rather than a rebuild per value.
// Reconcile takes a Config so a calibration test can vary it in a loop, and
// the workbook prints the one that was actually used.
type Config struct {
	// DescThreshold is the description-similarity floor a pairing must clear
	// to be trusted on description alone. Measured against real orders,
	// similarity clusters in a narrow band, so this separates "has comparable
	// description text" from "does not" more than it separates right from
	// wrong.
	DescThreshold float64 `json:"descThreshold"`

	// MarginThreshold is the minimum lead the chosen BC product must have
	// over the best alternative still available. Catches the "resembles two
	// lines equally" failure an absolute score cannot see.
	MarginThreshold float64 `json:"marginThreshold"`

	// MoneyTolerance is the per-line money tolerance, in currency units.
	MoneyTolerance float64 `json:"moneyTolerance"`

	// OrderTolerance is the document-level tolerance, deliberately looser per
	// line but tighter overall: a multi-line order accumulates rounding, but
	// the total is one figure both sides printed. Kept separate from
	// MoneyTolerance, which is what the long-standing docTol test was about.
	OrderTolerance float64 `json:"orderTolerance"`

	// MinSeats is the floor a BC seat count must clear to be treated as a
	// real seat count. CaseysItems returned 0.5 on a parasol, while a genuine
	// "2.5 Seater" sofa is a valid reading — so the floor sits at 1, not 0.
	MinSeats float64 `json:"minSeats"`

	// MinCodeLen is the squashed length a Model_No/Vendor_Item_No must clear
	// before it is eligible for exact-code matching. Below it, a code is
	// short enough to appear inside unrelated text by chance.
	MinCodeLen int `json:"minCodeLen"`

	// MinProductCodeLen is the floor for RECOGNISING a POA token as a product
	// code, a lower bar than matching one: the result only chooses the
	// wording of a discrepancy, never a pairing.
	MinProductCodeLen int `json:"minProductCodeLen"`

	// CodeMatchBonus is added to a code-matched pairing's score, large enough
	// that the assignment prefers it over any description-only candidate.
	// Additive rather than absolute: an overwhelming description match
	// elsewhere can still outscore a wrong code hit.
	CodeMatchBonus float64 `json:"codeMatchBonus"`

	// DescWeight and QtyBonus split the identity score. Money and orientation
	// are deliberately absent — they are checked on the pair, never used to
	// form it.
	DescWeight float64 `json:"descWeight"`
	QtyBonus   float64 `json:"qtyBonus"`

	// ExhaustiveLimit is the largest order the assignment solves exhaustively
	// before falling back to greedy.
	ExhaustiveLimit int `json:"exhaustiveLimit"`
}

// DefaultConfig is the shipped tuning.
//
// DescThreshold and MarginThreshold carry over from the embeddings engine and
// have not been recalibrated against a corpus — that is what the replay test
// exists for. Everything else is settled.
func DefaultConfig() Config {
	return Config{
		DescThreshold:   0.60,
		MarginThreshold: 0.10,

		MoneyTolerance: 0.05,
		// 0.02 rather than MoneyTolerance: the order total is a single figure
		// both sides printed, so it deserves a tighter bar than a line that
		// accumulates rounding. This decides a question that sat open for
		// months; it affects only the reported total flag, never the write
		// gate, which does not consult the order total at all.
		OrderTolerance: 0.02,

		MinSeats:          1.0,
		MinCodeLen:        6,
		MinProductCodeLen: 4,
		CodeMatchBonus:    2.0,

		DescWeight: 0.85,
		QtyBonus:   0.15,

		ExhaustiveLimit: 8,
	}
}
