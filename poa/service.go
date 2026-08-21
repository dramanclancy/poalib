package poa

import (
	"context"
	"fmt"
	"log"

	"github.com/dramanclancy/poalib/businesscentral"
)

// Result is one reconciled purchase order acknowledgement: either a completed
// Review, or Err if the matching Business Central order couldn't be fetched.
type Result struct {
	PF     string
	Review Review
	Err    error
}

// itemNumbers collects the distinct item numbers off "Item" lines, for the
// one batched CaseysItems lookup per PO (not one per line).
func itemNumbers(lines []businesscentral.PurchaseOrderLine) []string {
	seen := map[string]bool{}
	var nos []string
	for _, l := range lines {
		if l.LineType != "Item" || l.LineObjectNumber == "" || seen[l.LineObjectNumber] {
			continue
		}
		seen[l.LineObjectNumber] = true
		nos = append(nos, l.LineObjectNumber)
	}
	return nos
}

// Reconcile groups multi-page POA scans by PF number (a multi-page scan of
// the same order arrives as several Products, one per page), fetches the
// matching purchase order from Business Central for each distinct PF, and
// runs line matching and verification. One Result is returned per distinct
// PF number found in scans, in first-seen order.
//
// caseys is CaseysItems enrichment (Model_No, Range_Code, No_of_Seats, ...)
// — additive on top of the v2.0 API bc already calls. It may be nil (not
// configured) and GetItemsByNumbers may fail per-order; either way matching
// degrades to Phase 1 behaviour for that order rather than failing it — a
// broken enrichment endpoint must never take down reconciliation.
func Reconcile(ctx context.Context, bc *businesscentral.BCClient, caseys *businesscentral.CaseysClient, scans []Products) []Result {
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
		po.PurchaseOrderLines = bc.EnhancePurchaseOrderLines(ctx, po.PurchaseOrderLines)

		items := map[string]businesscentral.CaseysItem{}
		if caseys != nil {
			itemNos := itemNumbers(po.PurchaseOrderLines)
			if len(itemNos) > 0 {
				fetched, err := caseys.GetItemsByNumbers(ctx, itemNos)
				if err != nil {
					// Degrade, don't fail the order: enrichment is additive.
					log.Printf("CaseysItems enrichment failed for %s, continuing at Phase 1 quality: %s", pf, err)
				}
				items = fetched // non-nil even on error: partial batches, or an empty map
			}
		}

		mc := MatchContext{VendorNo: po.VendorNumber, Items: items}
		matches, unmatched, unmatchedBC := MatchPOA(scan, po.PurchaseOrderLines, mc)
		ov := VerifyOrder(scan, po, matches, unmatched)

		var reviewLines []ReviewLine
		writeOK := len(unmatched) == 0
		for _, m := range matches {
			if !m.FullyVerified() {
				bcLineID := m.BC.Item.ID
				poaIdx := m.poaIndex
				reviewLines = append(reviewLines, ReviewLine{
					POADescription: m.POA.Product + " " + m.POA.Description + " " + m.POA.Description2 + " " + m.POA.Description3,
					BCDescription:  m.BC.CombinedDesc,
					Discrepancies:  m.Discrepancies(),
					PF:             pf,
					VendorNo:       po.VendorNumber,
					VendorName:     po.VendorName,
					BCLineID:       bcLineID,
					BCItemNo:       m.BC.Item.LineObjectNumber,
					POALineIndex:   poaIdx,
					EngineVersion:  EngineVersion,
					RowHash:        rowHash(pf, bcLineID, fmt.Sprint(poaIdx)),
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
