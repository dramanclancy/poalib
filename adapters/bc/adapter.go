// Translation from Business Central into the domain.
//
// This is the only file that knows a purchase order line has a Sequence, that
// comment lines follow the line they qualify, or that CaseysItems addresses
// companies by name. Everything downstream sees domain.BCProduct.
package bc

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/dramanclancy/poalib/app"
	"github.com/dramanclancy/poalib/domain"
)

// Source builds domain BC orders from the Business Central APIs.
//
// Items may be nil: item enrichment is additive, and its absence degrades
// matching rather than failing an order. The resulting BCOrder records which
// of those happened, so a check never claims BC holds no code when nobody
// asked BC.
type Source struct {
	Orders *BCClient
	Items  *CaseysClient
	Log    func(format string, args ...any)
}

// Order fetches purchase order pf and builds the BC side of the comparison.
func (s *Source) Order(ctx context.Context, pf string) (domain.BCOrder, app.OrderStats, error) {
	var stats app.OrderStats

	po, err := s.fetchOrder(ctx, pf, &stats)
	if err != nil {
		return domain.BCOrder{}, stats, err
	}
	lines := s.Orders.EnhancePurchaseOrderLines(ctx, po.PurchaseOrderLines)
	stats.LinesFetched = len(lines)

	order := domain.BCOrder{
		PF:         po.Number,
		ID:         po.ID,
		VendorNo:   po.VendorNumber,
		VendorName: po.VendorName,
		Status:     po.Status,
		TotalExVAT: po.TotalAmountExclTax,
		Products:   groupLines(lines),
		Enrichment: domain.EnrichmentNotRequested,
	}

	s.enrich(ctx, pf, &order, &stats)
	for i := range order.Products {
		order.Products[i].Description = mergedDescription(order.Products[i])
	}
	return order, stats, nil
}

// fetchOrder looks the purchase order up by the number printed on the
// acknowledgement, falling back to the part before a "/" when Business
// Central holds no such order.
//
// Suppliers annotate the number they print: an acknowledgement reading
// "PF126282/EXC" answers purchase order PF126282, and BC has never heard of
// the suffixed form. The exact number is always tried first, so a genuine
// order whose number contains a slash is never bypassed, and the fallback
// runs only on a real "no such order" answer — never on a transport failure,
// which would otherwise be retried as though the number were wrong.
//
// When the fallback succeeds, the number actually used is recorded in
// OrderStats and printed on the report. Reconciling against a different order
// than the one printed is a fact a reader must be told, not a convenience to
// perform silently.
func (s *Source) fetchOrder(ctx context.Context, pf string, stats *app.OrderStats) (*PurchaseOrder, error) {
	po, err := s.Orders.GetPurchaseOrder(ctx, pf)
	if err == nil {
		return po, nil
	}
	base := baseOrderNumber(pf)
	if base == "" || !errors.Is(err, ErrPurchaseOrderNotFound) {
		return nil, err
	}

	fallback, ferr := s.Orders.GetPurchaseOrder(ctx, base)
	if ferr != nil {
		// Report the original number's failure: that is the one printed on
		// the document, and the one a reader will be looking for.
		return nil, err
	}
	stats.OrderNumberUsed = base
	if s.Log != nil {
		s.Log("purchase order %q not found in BC; matched %q instead", pf, base)
	}
	return fallback, nil
}

// baseOrderNumber returns the part of an order number before the first "/",
// or "" when there is nothing to strip.
func baseOrderNumber(pf string) string {
	i := strings.Index(pf, "/")
	if i <= 0 || i == len(pf)-1 {
		return ""
	}
	return pf[:i]
}

// enrich attaches item detail, in one batched lookup per order rather than one
// per line, and records exactly what happened for the run record.
//
// Enrichment degrades, never fails: a broken lookup leaves Detail nil and the
// comparison falls back to description evidence, rather than failing an order
// the service could still usefully reconcile.
func (s *Source) enrich(ctx context.Context, pf string, order *domain.BCOrder, stats *app.OrderStats) {
	if s.Items == nil {
		return // EnrichmentNotRequested, already set
	}
	numbers := itemNumbers(order.Products)
	stats.EnrichmentAsked = true
	stats.ItemsRequested = len(numbers)
	if len(numbers) == 0 {
		order.Enrichment = domain.EnrichmentApplied
		return
	}

	items, err := s.Items.GetItemsByNumbers(ctx, numbers)
	stats.ItemsReturned = len(items)
	order.Enrichment = domain.EnrichmentApplied
	if err != nil {
		order.Enrichment = domain.EnrichmentFailed
		order.EnrichmentNote = err.Error()
		stats.EnrichmentError = err.Error()
		if s.Log != nil {
			s.Log("CaseysItems enrichment failed for %s, continuing without it: %s", pf, err)
		}
		// Partial batches still come back alongside the error, so carry on
		// and attach whatever arrived.
	}

	for i := range order.Products {
		item, ok := items[order.Products[i].ItemNo]
		if !ok || order.Products[i].ItemNo == "" {
			continue
		}
		detail := toItemDetail(item)
		order.Products[i].Detail = &detail
		if detail.Seats != nil {
			stats.ItemsWithSeats++
		}
		if detail.VendorItemNo != "" || detail.ModelNo != "" {
			stats.ItemsWithCodes++
		}
	}
}

// toItemDetail maps one CaseysItems record into the domain.
//
// No_of_Seats arrives as a float that is 0 both when the item genuinely has
// no seats and when the field was never populated. The API cannot tell those
// apart, so the honest translation is: 0 means "no seat count recorded", and
// Seats stays nil. Reporting 0 as a real seat count would have every sofa
// disagree with BC; reporting nil says what is actually known.
func toItemDetail(item CaseysItem) domain.ItemDetail {
	d := domain.ItemDetail{
		ItemNo:       item.No,
		ModelNo:      strings.TrimSpace(item.ModelNo),
		VendorItemNo: strings.TrimSpace(item.VendorItemNo),
		VendorNo:     item.VendorNo,
		RangeCode:    strings.TrimSpace(item.RangeCode),
		Description3: strings.TrimSpace(item.Description3),
	}
	if item.NoOfSeats != 0 {
		d.Seats = domain.Ptr(item.NoOfSeats)
	}
	return d
}

// groupLines walks lines in sequence order, starting a new product on each
// value-bearing line and attaching the following Comment lines to it.
//
// Account lines start a product too. When only "Item" did, a surcharge line
// (G/L 718000, "Vendor Surcharges") never became a product: it could not be
// matched, and the matched total was short by exactly that amount on every
// Ashwood order.
func groupLines(lines []PurchaseOrderLine) []domain.BCProduct {
	sorted := make([]PurchaseOrderLine, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })

	var products []domain.BCProduct
	for _, l := range sorted {
		switch l.LineType {
		case "Item", "Account", "G/L Account", "Fixed Asset", "Resource":
			products = append(products, domain.BCProduct{
				LineID:          l.ID,
				ItemNo:          l.LineObjectNumber,
				LineType:        l.LineType,
				Sequence:        l.Sequence,
				LineDescription: l.Description,
				DisplayName:     l.DisplayName,
				DisplayName2:    l.DisplayName2,
				Qty:             l.Quantity,
				UnitCost:        l.DirectUnitCost,
				DiscountPercent: l.DiscountPercent,
				DiscountAmount:  l.DiscountAmount,
				Net:             l.NetAmount,
			})
		case "Comment":
			if len(products) > 0 {
				last := &products[len(products)-1]
				last.Comments = append(last.Comments, l.Description)
			}
			// A Comment before any value line has no owner; dropping it is
			// correct — it is a document header, not a line qualifier.
		}
	}
	return products
}

// mergedDescription builds the text the comparison actually reads: the item's
// own display name first, falling back to the line's free text when there is
// no item, then the enrichment, then the comment lines.
//
// A comment that merely restates the display name is skipped — a purchase
// order frequently repeats the item name as its own first comment, and
// counting it twice would double its weight.
func mergedDescription(p domain.BCProduct) string {
	var parts []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[domain.Normalize(s)] {
			return
		}
		parts = append(parts, s)
		seen[domain.Normalize(s)] = true
	}

	add(p.DisplayName)
	add(p.DisplayName2)
	if len(parts) == 0 {
		add(p.LineDescription)
	}
	// Description_3 is free extra text the display names do not carry;
	// Range_Code is a product range word ("METRO", "DUSK") that POAs print and
	// which had no BC-side equivalent to match against before enrichment.
	if p.Detail != nil {
		add(p.Detail.Description3)
		add(p.Detail.RangeCode)
	}
	for _, c := range p.Comments {
		add(c)
	}
	// Separator must be " " — joining with "" glues adjacent words into
	// unmatchable tokens ("FootstoolWM").
	return strings.Join(parts, " ")
}

// itemNumbers collects the distinct item numbers off item-bearing products,
// for the one batched lookup per order.
func itemNumbers(products []domain.BCProduct) []string {
	seen := map[string]bool{}
	var nos []string
	for _, p := range products {
		if p.LineType != "Item" || p.ItemNo == "" || seen[p.ItemNo] {
			continue
		}
		seen[p.ItemNo] = true
		nos = append(nos, p.ItemNo)
	}
	return nos
}
