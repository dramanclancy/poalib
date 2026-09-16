package domain

import "testing"

// TestLearnability is the learnability table as an executable one. Whatever
// records a reviewer's verdict rejects it on the strength of this, so a change
// to any row changes what a reviewer is permitted to teach the engine.
func TestLearnability(t *testing.T) {
	tests := []struct {
		kind DiscrepancyKind
		want Learnability
		why  string
	}{
		// The control. If these could be pardoned into a rule, the engine
		// would eventually stop checking the only things it checks objectively.
		{DiscNetValue, NeverLearnable, "a price difference is never something a reviewer may teach away"},
		{DiscQuantity, NeverLearnable, "same for a count"},
		{DiscInternalMath, NeverLearnable, "the POA contradicting itself is an extraction bug, not a rule"},
		{DiscSeatCount, NeverLearnable, "a structural check like quantity; unlisted checks fail closed"},

		// Identity — the half a feedback loop exists to teach.
		{DiscDescription, LearnablePerLine, "vocabulary; the point"},
		{DiscMissingBCCode, LearnablePerLine, "becomes a Vendor_Item_No BC does not have"},
		{DiscAmbiguousPairing, LearnablePerLine, "a reviewer picking the right one resolves it forever"},
		{DiscCodeAmbiguous, LearnablePerLine, "same"},
		{DiscVariantAmbiguous, LearnablePerLine, "a reviewer naming the right grade resolves it forever"},

		// Genuinely learnable, but only as a supplier-wide convention.
		{DiscOrientation, LearnablePerVendor, "how this supplier writes handedness is a rule; an exemption is not"},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := tc.kind.Learnability(); got != tc.want {
				t.Errorf("%q.Learnability() = %d, want %d — %s", tc.kind, got, tc.want, tc.why)
			}
		})
	}
}

// TestLearnability_UnknownKindFailsClosed is the property that matters more
// than any individual row: a kind added later and not considered here must
// refuse to teach anything rather than inherit permission by default.
func TestLearnability_UnknownKindFailsClosed(t *testing.T) {
	var invented DiscrepancyKind = "something_added_later"
	if invented.Learnability() != NeverLearnable {
		t.Error("an unrecognised kind reports as learnable; it must fail closed")
	}
	if invented.Learnable() || DiscrepancyKind("").Learnable() {
		t.Error("Learnable() true for an unrecognised or empty kind")
	}
}

// TestDiscrepancies_OneEntryPerReason is the structural promise the review
// queue depends on: a line failing several checks yields several entries, each
// independently verdictable, rather than one sentence pardoning all of them.
func TestDiscrepancies_OneEntryPerReason(t *testing.T) {
	p := checkProduct(pairing{
		poa: POAProduct{Code: "NFX2S (A)", Qty: 2},
		bc:  bcItem("IT0188674", 3, 500, &ItemDetail{ItemNo: "IT0188674"}),
		ev:  MatchEvidence{Similarity: Ptr(0.33), MarginNA: true},
	}, EnrichmentApplied, cfg())

	got := Kinds(p.Discrepancies())
	want := []DiscrepancyKind{DiscNetValue, DiscQuantity, DiscMissingBCCode}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The money reasons must be unpardonable and the identity one teachable,
	// on the same line, at the same time. That separation is the entire reason
	// these are separate rows.
	for _, d := range p.Discrepancies() {
		switch d.Kind {
		case DiscNetValue, DiscQuantity:
			if d.Kind.Learnable() {
				t.Errorf("%q is learnable; it is the control", d.Kind)
			}
		case DiscMissingBCCode:
			if !d.Kind.Learnable() {
				t.Errorf("%q is not learnable; it is what a feedback loop collects", d.Kind)
			}
		}
	}
}

// TestDiscrepancies_MessagesComeFromTheChecks: the prose a reviewer reads is
// the check's own message, not a second wording maintained alongside it.
func TestDiscrepancies_MessagesComeFromTheChecks(t *testing.T) {
	p := checkProduct(pairing{
		poa: POAProduct{Code: "Widget", Qty: 1, UnitPrice: 541},
		bc:  BCProduct{ItemNo: "ITX", Qty: 1, Net: 565},
		ev:  MatchEvidence{Similarity: Ptr(0.9), MarginNA: true},
	}, EnrichmentApplied, cfg())

	ds := p.Discrepancies()
	if len(ds) != 1 || ds[0].Kind != DiscNetValue {
		t.Fatalf("kinds = %v, want just [%q]", Kinds(ds), DiscNetValue)
	}
	if ds[0].Message != p.Price.Message {
		t.Errorf("discrepancy %q does not match the price check's own %q", ds[0].Message, p.Price.Message)
	}
}
