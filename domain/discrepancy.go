package domain

// Discrepancy is one reason a matched line needs a human to look at it.
//
// Reasons travel separately rather than joined into one cell: a row reading
// "descriptions look different; net differs by £24" is two problems with two
// different fixes, and a reader answering one has not answered the other. Each
// carries a Kind the code can reason about rather than prose it would have to
// match on.
//
// The Kind is what the Review sheet's DiscrepancyType column and any flag-rate
// metric key on. Message is for the human only — reword it freely; nothing
// branches on it.
type Discrepancy struct {
	Kind    DiscrepancyKind `json:"kind"`
	Message string          `json:"message"`
}

// DiscrepancyKind identifies what sort of disagreement was found. Values are
// stable identifiers printed in the workbook, so renaming one breaks any filter
// or metric a reader has built on them.
type DiscrepancyKind string

const (
	// Verification — the supplier and BC disagree about money, count or
	// structure. These are the control: no reviewer verdict ever makes one
	// acceptable.
	DiscNetValue     DiscrepancyKind = "net_line_value"
	DiscQuantity     DiscrepancyKind = "quantity"
	DiscInternalMath DiscrepancyKind = "poa_internal_math"
	DiscSeatCount    DiscrepancyKind = "seat_count"

	// Orientation — handedness.
	DiscOrientation DiscrepancyKind = "orientation"

	// Identity — which BC line this POA line is.
	DiscDescription      DiscrepancyKind = "description_mismatch"
	DiscMissingBCCode    DiscrepancyKind = "bc_code_missing"
	DiscAmbiguousPairing DiscrepancyKind = "ambiguous_pairing"
	DiscCodeAmbiguous    DiscrepancyKind = "code_ambiguous"
	DiscVariantAmbiguous DiscrepancyKind = "variant_ambiguous"
)

// Kinds returns just the kinds of a discrepancy list, in order. Useful for
// tests and for grouping a flag-rate metric.
func Kinds(ds []Discrepancy) []DiscrepancyKind {
	ks := make([]DiscrepancyKind, 0, len(ds))
	for _, d := range ds {
		ks = append(ks, d.Kind)
	}
	return ks
}
