package poa

import (
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

// TestVerifyOrder_UsesDocumentTolerance guards against comparing the
// document-level (multi-line, rounding-accumulated) total with the
// half-a-penny per-line tolerance (moneyTol) instead of the documented
// document-level tolerance (docTol = 0.02). A stated POA total that is 1.5p
// off BC's total — well within rounding noise for a multi-line order — must
// still reconcile.
//
// NOTE: this test currently fails — VerifyOrder (verify.go) compares against
// moneyTol, not docTol, so the "beyond docTol does not reconcile" case below
// does not reconcile at moneyTol=0.05 either... it incorrectly does. This
// predates the package reorg (confirmed against graph-lib/orderverify.go on
// main before the split) and was left unchanged rather than silently fixed
// as part of a restructuring change. See CODEBASE_REORGANIZATION_PROPOSAL.md.
func TestVerifyOrder_UsesDocumentTolerance(t *testing.T) {
	tests := []struct {
		name     string
		poaTotal float64
		bcTotal  float64
		wantOK   bool
	}{
		{"exact match", 100.00, 100.00, true},
		{"within moneyTol", 100.00, 100.004, true},
		{"between moneyTol and docTol reconciles", 100.00, 100.015, true},
		{"beyond docTol does not reconcile", 100.00, 100.03, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := Products{TotalAmountexVAT: Ptr(tt.poaTotal)}
			po := &businesscentral.PurchaseOrder{TotalAmountExclTax: tt.bcTotal}

			ov := VerifyOrder(doc, po, nil, nil)
			if ov.TotalExVATOK != tt.wantOK {
				t.Errorf("TotalExVATOK = %v, want %v (poa=%.3f bc=%.3f)",
					ov.TotalExVATOK, tt.wantOK, tt.poaTotal, tt.bcTotal)
			}
		})
	}
}
