package domain

// Discrepancy is one reason a matched line needs a human to look at it.
//
// These travel separately rather than joined into one cell: a reviewer approving a row that reads "descriptions look different;
// net differs by £24" has pardoned both, and one of those must never be
// pardonable. So each reason travels separately, carrying a Kind the code can
// reason about rather than prose the code would have to match on.
//
// The Kind is what a queue column, a card action and the weekly flag-rate
// metric all key on. Message is for the human only — reword
// it freely; nothing branches on it.
type Discrepancy struct {
	Kind    DiscrepancyKind `json:"kind"`
	Message string          `json:"message"`
}

// DiscrepancyKind identifies what sort of disagreement was found. Values are
// stable identifiers written to the review queue and quoted back by the
// feedback endpoint, so they must not be renamed once rows exist.
type DiscrepancyKind string

const (
	// Verification — the supplier and BC disagree about money, count or
	// structure. These are the control (see Learnability).
	DiscNetValue     DiscrepancyKind = "net_line_value"
	DiscQuantity     DiscrepancyKind = "quantity"
	DiscInternalMath DiscrepancyKind = "poa_internal_math"
	DiscSeatCount    DiscrepancyKind = "seat_count"

	// Orientation — handedness. Learnable, but only as a vendor-wide
	// convention.
	DiscOrientation DiscrepancyKind = "orientation"

	// Identity — which BC line this POA line is. This is the half Phase 2
	// exists to teach.
	DiscDescription      DiscrepancyKind = "description_mismatch"
	DiscMissingBCCode    DiscrepancyKind = "bc_code_missing"
	DiscAmbiguousPairing DiscrepancyKind = "ambiguous_pairing"
	DiscCodeAmbiguous    DiscrepancyKind = "code_ambiguous"
	DiscVariantAmbiguous DiscrepancyKind = "variant_ambiguous"
)

// Learnability says whether a reviewer's verdict on a discrepancy may be
// recorded as a rule, and at what scope.
//
// This is the table in phase2-feedback-loop.md section 5.2, expressed once so
// the feedback endpoint, the card and any future consumer all consult the same
// answer. It is deliberately not a bool: orientation is genuinely learnable,
// but only as "this supplier prints RH for our LHF" — never per line — and
// collapsing that into yes/no would either lose a real rule or licence a
// dangerous one.
type Learnability int

const (
	// NeverLearnable is the control. A reviewer may not teach the system that
	// a price or a quantity disagreement is acceptable; if these could be
	// pardoned into a rule, the engine would eventually stop checking the only
	// things it can check objectively. The feedback endpoint must reject a
	// verdict against one of these outright — server-side, not merely by
	// omitting the button.
	NeverLearnable Learnability = iota

	// LearnablePerLine means a confirmation teaches a fact about this specific
	// product: that NFX2S (A) is IT0188674. This is the prize — every such
	// confirmation is a Vendor_Item_No row BC does not have.
	LearnablePerLine

	// LearnablePerVendor means a confirmation teaches a convention about the
	// supplier as a whole, and must never be applied to a single line. A
	// reviewer confirming one handed unit is telling us how this supplier
	// writes handedness, not that this particular sofa is exempt from the
	// check.
	LearnablePerVendor
)

// Learnability reports whether a verdict on this kind may become a rule.
//
// Unknown kinds return NeverLearnable. That default matters: a kind added
// later without being considered here should fail closed, refusing to teach
// anything, rather than silently becoming pardonable.
func (k DiscrepancyKind) Learnability() Learnability {
	switch k {
	case DiscDescription, DiscMissingBCCode, DiscAmbiguousPairing,
		DiscCodeAmbiguous, DiscVariantAmbiguous:
		return LearnablePerLine
	case DiscOrientation:
		return LearnablePerVendor
	default:
		// Money, quantity, the POA's own arithmetic, and seat count.
		//
		// Seat count is a judgement not covered by section 5.2's table. It
		// sits here because it is a structural verification check like
		// quantity — BC's No_of_Seats against what the POA states — and the
		// safe reading of an unlisted check is that it is a control. If a
		// reviewer's confirmation should teach something here, that is a
		// deliberate decision to make, not a default to inherit.
		return NeverLearnable
	}
}

// Learnable is the coarse question — may a verdict on this become a rule at
// all? — for callers that do not care about scope.
func (k DiscrepancyKind) Learnable() bool {
	return k.Learnability() != NeverLearnable
}

// String is the stable wire value for Learnability — consumed by the review
// queue webhook payload and, downstream, by whatever enforces the
// never-pardon-money-or-quantity guardrail outside this process (a Power
// Automate flow condition, today). Renaming these strings has the same
// consequence as renaming a DiscrepancyKind: it orphans whatever the
// consumer already matches against.
func (l Learnability) String() string {
	switch l {
	case LearnablePerLine:
		return "per_line"
	case LearnablePerVendor:
		return "per_vendor"
	default:
		return "never"
	}
}

// Kinds returns just the kinds of a discrepancy list, in order. Useful for
// tests and for grouping the weekly flag-rate metric.
func Kinds(ds []Discrepancy) []DiscrepancyKind {
	ks := make([]DiscrepancyKind, 0, len(ds))
	for _, d := range ds {
		ks = append(ks, d.Kind)
	}
	return ks
}
