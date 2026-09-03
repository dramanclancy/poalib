package poa

import (
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

// TestLearnability is phase2-feedback-loop.md section 5.2 as an executable
// table. The feedback endpoint rejects verdicts on the strength of this, so a
// change to any row here is a change to what a reviewer is permitted to teach
// the engine — it should be hard to make by accident.
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
		{DiscInternalMath, NeverLearnable, "the POA contradicting itself is an extraction bug; it needs the model retrained, not a rule"},
		{DiscSeatCount, NeverLearnable, "a structural check like quantity, and unlisted checks fail closed"},

		// Identity — the half Phase 2 exists to teach.
		{DiscDescription, LearnablePerLine, "vocabulary; the point"},
		{DiscMissingBCCode, LearnablePerLine, "becomes a Vendor_Item_No BC does not have — the prize"},
		{DiscAmbiguousPairing, LearnablePerLine, "a reviewer picking the right one resolves it forever"},
		{DiscCodeAmbiguous, LearnablePerLine, "same"},
		{DiscVariantAmbiguous, LearnablePerLine, "a reviewer naming the right grade resolves it forever"},

		// Genuinely learnable, but only as a supplier-wide convention.
		{DiscOrientation, LearnablePerVendor, `"this supplier prints RH for our LHF" is a rule; "this sofa is exempt" is not`},
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
// refuse to teach anything, rather than inheriting permission by default.
func TestLearnability_UnknownKindFailsClosed(t *testing.T) {
	var invented DiscrepancyKind = "something_added_later"
	if invented.Learnability() != NeverLearnable {
		t.Error("an unrecognised kind reports as learnable; it must fail closed")
	}
	if invented.Learnable() {
		t.Error("Learnable() true for an unrecognised kind")
	}
	if DiscrepancyKind("").Learnable() {
		t.Error("Learnable() true for the zero value")
	}
}

func TestLearnable_AgreesWithLearnability(t *testing.T) {
	for _, k := range []DiscrepancyKind{
		DiscNetValue, DiscQuantity, DiscInternalMath, DiscSeatCount,
		DiscOrientation, DiscDescription, DiscMissingBCCode,
		DiscAmbiguousPairing, DiscCodeAmbiguous, DiscVariantAmbiguous,
	} {
		if k.Learnable() != (k.Learnability() != NeverLearnable) {
			t.Errorf("%q: Learnable() and Learnability() disagree", k)
		}
	}
}

// TestDiscrepancies_OneEntryPerReason is the structural promise Phase 2 stage
// 1 depends on: a line failing several checks yields several entries, each
// independently verdictable, rather than one sentence pardoning all of them.
func TestDiscrepancies_OneEntryPerReason(t *testing.T) {
	m := LineMatch{
		POA: OrderDetail{Product: "NFX2S (A)", Qty: 2},
		BC: BCLineGroup{
			Item: businesscentral.PurchaseOrderLine{
				LineObjectNumber: "IT0188674", Quantity: 3, NetAmount: 500,
			},
			Detail: &businesscentral.CaseysItem{No: "IT0188674"},
		},
		// Identity doubt AND a money difference on the same line — the exact
		// combination that made a joined string unsafe to approve.
		NetOK: false, NetDelta: -24,
		QtyOK:      false,
		InternalOK: true, OrientationOK: true, SeatsOK: true,
		DescScore: 0.33, DescScoreRaw: 0.33, DescMarginNA: true,
	}

	got := Kinds(m.Discrepancies())
	want := []DiscrepancyKind{DiscNetValue, DiscQuantity, DiscMissingBCCode}
	if len(got) != len(want) {
		t.Fatalf("Discrepancies() kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The money reasons must be unpardonable and the identity one teachable,
	// on the same line, at the same time. That separation is the entire
	// reason these are separate rows.
	for _, d := range m.Discrepancies() {
		switch d.Kind {
		case DiscNetValue, DiscQuantity:
			if d.Kind.Learnable() {
				t.Errorf("%q is learnable; it is the control", d.Kind)
			}
		case DiscMissingBCCode:
			if !d.Kind.Learnable() {
				t.Errorf("%q is not learnable; it is what Phase 2 collects", d.Kind)
			}
		}
	}
}

// TestDiscrepancies_EveryEntryCarriesBoth guards against a Kind being added
// without a message, or prose being added without a Kind — either would break
// a queue row.
func TestDiscrepancies_EveryEntryCarriesBoth(t *testing.T) {
	m := LineMatch{
		POA:           OrderDetail{Product: "Metro 1 Arm Sofa Unit LHF", Qty: 1},
		BC:            BCLineGroup{Item: businesscentral.PurchaseOrderLine{LineObjectNumber: "IT0163911", Quantity: 1}},
		OrientReason:  "POA states LHF, BC states RHF",
		NetOK:         true,
		QtyOK:         true,
		InternalOK:    true,
		OrientationOK: false,
		SeatsOK:       false,
		POASeats:      2, BCSeats: 3,
		DescScore: 0.42, DescScoreRaw: 0.55,
		DescMargin: 0.02, DescRunnerUp: "2 Seater Sofa",
	}

	ds := m.Discrepancies()
	if len(ds) == 0 {
		t.Fatal("no discrepancies produced by a line failing four checks")
	}
	for i, d := range ds {
		if d.Kind == "" {
			t.Errorf("discrepancy %d has no Kind (message %q)", i, d.Message)
		}
		if d.Message == "" {
			t.Errorf("discrepancy %d (%q) has no Message", i, d.Kind)
		}
	}
}
