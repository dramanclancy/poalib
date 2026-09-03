package poa

import "testing"

// TestReviewLine_POACodeCandidate exercises the exact expression Reconcile
// uses to populate ReviewLine.POACodeCandidate — the Stage 3 rules lookup
// key (see docs/NEXT_PHASE.md Task 1). A line whose POA text carries a
// product code should surface it; a line that is plain description should
// come back empty rather than picking up an arbitrary word.
func TestReviewLine_POACodeCandidate(t *testing.T) {
	withCode := OrderDetail{Product: "STGTS1 (A) Sofa", Description: "Storage Footstool"}
	if got := productCodeIn(combinedPOADesc(withCode)); got != "STGTS1" {
		t.Errorf("POACodeCandidate = %q, want %q", got, "STGTS1")
	}

	noCode := pf128964POALines()[0] // "Metro 1 Arm Sofa Unit LHF Digby Beige" — no code, just description
	if got := productCodeIn(combinedPOADesc(noCode)); got != "" {
		t.Errorf("POACodeCandidate = %q, want empty for a plain-description line", got)
	}
}
