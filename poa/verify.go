package poa

import (
	"math"

	"github.com/dramanclancy/poalib/businesscentral"
)

// OrderVerification is the document-level result that sits above line
// matching in the write-gate.
type OrderVerification struct {
	POATotal       float64 // ex-VAT total extracted from the POA
	TotalSource    string
	BCTotal        float64 // BC totalAmountExcludingTax
	MatchedTotal   float64 // sum of NetAmount over matched BC lines
	TotalExVATOK   bool    // POATotal == BCTotal (within tolerance)
	LinesMatched   int
	LinesUnmatched int
}

func POATotalCalc(doc Products) (float64, string) {
	if doc.TotalAmountexVAT != nil {
		return *doc.TotalAmountexVAT, "stated on document"
	}

	var total float64
	for _, n := range doc.Products {
		total += effectiveNetLine(n)
	}
	return total, "derived from lines"
}

// VerifyOrder performs the document-level check after MatchPOA.
//
// Two distinct comparisons, catching two distinct failure modes:
//
//   - TotalOK: the supplier's order total agrees with BC's. Catches
//     price/discount drift that somehow survived line checks, VAT included
//     where it shouldn't be, or a DocIntel misread of the total field.
//   - CoverageOK: the matched BC lines account for the whole BC order value.
//     Catches BC item lines the POA never mentioned (supplier dropped a
//     product) and POA lines DocIntel failed to extract — both invisible to
//     per-line verification because an absent line has nothing to compare.
//
// Write-gate: proceed only if TotalOK && CoverageOK && len(unmatched) == 0
// && every match FullyVerified().
func VerifyOrder(doc Products, po *businesscentral.PurchaseOrder, matches []LineMatch, unmatched []OrderDetail) OrderVerification {
	matchedTotal := 0.0
	for _, m := range matches {
		matchedTotal += m.BC.Item.NetAmount
	}

	total, source := POATotalCalc(doc)

	return OrderVerification{
		POATotal:       total,
		TotalSource:    source,
		BCTotal:        po.TotalAmountExclTax,
		MatchedTotal:   matchedTotal,
		TotalExVATOK:   math.Abs(total-po.TotalAmountExclTax) < moneyTol,
		LinesMatched:   len(matches),
		LinesUnmatched: len(unmatched),
	}
}
