package poa

import (
	"context"
	"fmt"

	"github.com/dramanclancy/poalib/businesscentral"
)

// Result is one reconciled purchase order acknowledgement: either a completed
// Review, or Err if the matching Business Central order couldn't be fetched.
type Result struct {
	PF     string
	Review Review
	Err    error
}

// Reconcile groups multi-page POA scans by PF number (a multi-page scan of
// the same order arrives as several Products, one per page), fetches the
// matching purchase order from Business Central for each distinct PF, and
// runs line matching and verification. One Result is returned per distinct
// PF number found in scans, in first-seen order.
func Reconcile(ctx context.Context, bc *businesscentral.BCClient, scans []Products) []Result {
	byPF := make(map[string]Products)
	pageCount := make(map[string]int)
	order := make([]string, 0, len(scans)) // preserve first-seen order, avoid map-range randomness
	for _, scan := range scans {
		existing, ok := byPF[scan.PF]
		pageCount[scan.PF]++
		if !ok {
			byPF[scan.PF] = scan
			order = append(order, scan.PF)
			continue
		}
		// Same PO seen again on another scan: fold its products into the first.
		existing.Products = append(existing.Products, scan.Products...)
		byPF[scan.PF] = existing
	}

	results := make([]Result, 0, len(order))
	for _, pf := range order {
		scan := byPF[pf]
		if pageCount[pf] > 1 {
			scan.TotalAmountexVAT = POATotalFromLines(scan)
		}

		po, err := bc.GetPurchaseOrder(ctx, pf)
		if err != nil {
			results = append(results, Result{PF: pf, Err: fmt.Errorf("fetching purchase order from BC: %w", err)})
			continue
		}

		matches, unmatched, unmatchedBC := MatchPOA(scan, po.PurchaseOrderLines)
		ov := VerifyOrder(scan, po, matches, unmatched)

		var reviewLines []ReviewLine
		writeOK := len(unmatched) == 0
		for _, m := range matches {
			if !m.FullyVerified() {
				reviewLines = append(reviewLines, ReviewLine{
					POADescription: m.POA.Product + " " + m.POA.Description + " " + m.POA.Description2 + " " + m.POA.Description3,
					BCDescription:  m.BC.CombinedDesc,
					Discrepancies:  m.Discrepancies(),
				})
			}
			writeOK = writeOK && m.FullyVerified()
		}

		results = append(results, Result{
			PF: pf,
			Review: Review{
				Extraction:   scan,
				BCOrder:      po,
				Matches:      matches,
				Unmatched:    unmatched,
				UnmatchedBC:  unmatchedBC,
				Verification: ov,
				WriteOK:      writeOK,
				ReviewLines:  reviewLines,
			},
		})
	}
	return results
}
