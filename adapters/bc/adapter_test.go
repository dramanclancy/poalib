package bc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// TestGroupLines pins how purchase order lines become products: sorted by
// sequence, a product per value-bearing line (Account included — surcharges
// went unmatched when only Item counted), comment lines attached to the line
// before them, and a comment with no owner dropped.
func TestGroupLines(t *testing.T) {
	lines := []PurchaseOrderLine{
		{Sequence: 40, LineType: "Account", LineObjectNumber: "718000", Description: "Vendor Surcharges", Quantity: 1, NetAmount: 25},
		{Sequence: 30, LineType: "Comment", Description: "Right Hand Facing"},
		{Sequence: 5, LineType: "Comment", Description: "Delivery to store 12"},
		{Sequence: 10, LineType: "Item", LineObjectNumber: "IT0188674", Description: "Hansson 2.5 Seater End", Quantity: 1, NetAmount: 541},
		{Sequence: 20, LineType: "Comment", Description: "LEFT/RIGHT OPTION"},
	}

	got := groupLines(lines)

	if len(got) != 2 {
		t.Fatalf("built %d products, want 2 (the item and the surcharge)", len(got))
	}
	item, surcharge := got[0], got[1]
	if item.ItemNo != "IT0188674" || item.Net != 541 {
		t.Errorf("first product = %s / %g, want IT0188674 / 541", item.ItemNo, item.Net)
	}
	if len(item.Comments) != 2 || item.Comments[0] != "LEFT/RIGHT OPTION" || item.Comments[1] != "Right Hand Facing" {
		t.Errorf("item comments = %q, want the two that follow it, in sequence order", item.Comments)
	}
	if surcharge.LineType != "Account" || surcharge.Net != 25 || len(surcharge.Comments) != 0 {
		t.Errorf("surcharge = %+v, want an Account product with no comments", surcharge)
	}
}

// TestGroupLines_KeepsEveryValueLine: whatever its type, a line that is not
// text becomes a product, so the products always carry every unit of money
// BC's header total does. "Charge" (an item charge: freight, delivery, duty)
// used to fall through the switch and vanish, leaving the total short by its
// amount with no BC line to name; an unknown type would have gone the same way.
func TestGroupLines_KeepsEveryValueLine(t *testing.T) {
	lines := []PurchaseOrderLine{
		{Sequence: 10000, LineType: "Item", LineObjectNumber: "IT0225813", Description: "Armchair", Quantity: 1, DirectUnitCost: 552, NetAmount: 552},
		{Sequence: 20000, LineType: "Comment", Description: "2547 VERONA IRIS"},
		{Sequence: 30000, LineType: "Charge", LineObjectNumber: "DELIVERY", Description: "Delivery charge", Quantity: 1, DirectUnitCost: 45, NetAmount: 45},
		{Sequence: 40000, LineType: "Allocation Account", LineObjectNumber: "AA01", Description: "Allocated freight", Quantity: 1, DirectUnitCost: 12.5, NetAmount: 12.5},
		// An empty type that carries nothing is text; one carrying money is not.
		{Sequence: 50000, LineType: "", Description: "Deliver to store 12"},
		{Sequence: 60000, LineType: "", LineObjectNumber: "X1", Description: "Untyped value line", Quantity: 1, NetAmount: 3},
	}
	const headerTotal = 552 + 45 + 12.5 + 3

	got := groupLines(lines)

	var sum float64
	types := make([]string, len(got))
	for i, p := range got {
		sum += p.Net
		types[i] = p.LineType
	}
	if sum != headerTotal {
		t.Errorf("products carry %.2f, want the header's %.2f: a value line was dropped (types %q)", sum, headerTotal, types)
	}
	if len(got) != 4 {
		t.Fatalf("built %d products %q, want 4: Item, Charge, Allocation Account, untyped value line", len(got), types)
	}
	if c := got[1]; c.LineType != "Charge" || c.ItemNo != "DELIVERY" || c.LineDescription != "Delivery charge" {
		t.Errorf("charge product = %+v, want the item charge line with its own number and text", c)
	}
	if got[0].Comments == nil || got[0].Comments[0] != "2547 VERONA IRIS" {
		t.Errorf("item comments = %q, want its comment line", got[0].Comments)
	}
	if allocation := got[2]; len(allocation.Comments) != 1 || allocation.Comments[0] != "Deliver to store 12" {
		t.Errorf("an empty-type text line should qualify the line above it; comments = %q", allocation.Comments)
	}
}

// TestSourceOrder_EmptyOrder pins what an order BC holds no lines for builds
// to: no products, a zero line count for the run record, and no lookups. Four
// captured Draft orders came back like this; the order is a fact to report,
// not an error to fail on.
func TestSourceOrder_EmptyOrder(t *testing.T) {
	var requests []string
	orders := &BCClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/v2.0",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.URL.Path)
			body := `{"value":[{"id":"po-1","number":"PF131737","vendorNumber":"VX1","status":"Draft","totalAmountExcludingTax":0,"purchaseOrderLines":[]}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
	}
	items := &CaseysClient{cred: fakeCredential{}, baseURL: "https://example.invalid/caseys",
		httpClient: &http.Client{Transport: erroringTransport{}}}
	src := &Source{Orders: orders, Items: items}

	order, stats, err := src.Order(context.Background(), "PF131737")
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(order.Products) != 0 || stats.LinesFetched != 0 || order.Status != "Draft" {
		t.Errorf("order = %d products, %d lines fetched, status %q; want 0, 0, Draft", len(order.Products), stats.LinesFetched, order.Status)
	}
	if len(requests) != 1 {
		t.Errorf("issued %d requests %q, want only the order itself: there is no item to look up", len(requests), requests)
	}
	if order.Enrichment != domain.EnrichmentApplied || stats.EnrichmentError != "" {
		t.Errorf("enrichment = %s / %q, want applied with no error: nothing was asked for", order.Enrichment, stats.EnrichmentError)
	}
}

// TestMergedDescription pins the text the comparison actually reads.
func TestMergedDescription(t *testing.T) {
	tests := []struct {
		name string
		p    domain.BCProduct
		want string
	}{
		{
			name: "display names lead, then item detail, then comments",
			p: domain.BCProduct{
				DisplayName: "Hansson 2.5 Seater End", DisplayName2: "Grey",
				LineDescription: "HANSSON END (ignored: a display name exists)",
				Detail:          &domain.ItemDetail{Description3: "Fabric Grade A", RangeCode: "HANSSON"},
				Comments:        []string{"Right Hand Facing"},
			},
			want: "Hansson 2.5 Seater End Grey Fabric Grade A HANSSON Right Hand Facing",
		},
		{
			name: "no display name falls back to the line's own text",
			p:    domain.BCProduct{LineDescription: "Vendor Surcharges"},
			want: "Vendor Surcharges",
		},
		{
			// A purchase order often repeats the item name as its first
			// comment; counting it twice would double its weight.
			name: "a comment restating the name is dropped, whatever its case or punctuation",
			p: domain.BCProduct{
				DisplayName: "Metro Footstool",
				Comments:    []string{"METRO  FOOTSTOOL.", "WM"},
			},
			// Joined with spaces: "" once glued words into "FootstoolWM".
			want: "Metro Footstool WM",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergedDescription(tc.p); got != tc.want {
				t.Errorf("mergedDescription = %q\n                 want %q", got, tc.want)
			}
		})
	}
}

// TestToItemDetail_ZeroSeatsMeansNotRecorded: CaseysItems sends 0 both for
// "no seats" and "never filled in", so 0 must become nil, never a seat count
// every sofa would then disagree with.
func TestToItemDetail_ZeroSeatsMeansNotRecorded(t *testing.T) {
	if d := toItemDetail(CaseysItem{No: "IT1", NoOfSeats: 0}); d.Seats != nil {
		t.Errorf("Seats = %v for No_of_Seats 0, want nil", *d.Seats)
	}
	if d := toItemDetail(CaseysItem{No: "IT1", NoOfSeats: 3, ModelNo: "  NOV4S "}); d.Seats == nil || *d.Seats != 3 || d.ModelNo != "NOV4S" {
		t.Errorf("detail = %+v, want 3 seats and a trimmed Model_No", d)
	}
}

// TestBaseOrderNumber covers the supplier annotation an acknowledgement can
// carry on the purchase order number. The fallback only ever runs after
// Business Central has said the printed number does not exist, so this needs
// only to identify what to try next — and to decline when there is nothing
// sensible to try.
// TestFallbackOrderNumbers: what is tried, and in what order, after BC has
// said the printed number does not exist.
func TestFallbackOrderNumbers(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"PF^PF130191-CC34", []string{"PF130191"}}, // the order that failed as not found
		{"PF126282/EXC", []string{"PF126282"}},     // both rules agree: tried once
		{"PF^PF130191/CC34", []string{"PF^PF130191", "PF130191"}},
		{"SF130191-A", []string{"SF130191"}},
		{"PF130368", nil},    // the printed number itself is never retried
		{"PF1301-CC34", nil}, // too short to be an order number
		{"PFABC", nil},
	}
	for _, tc := range tests {
		if got := fallbackOrderNumbers(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("fallbackOrderNumbers(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSourceOrder_FallsBackToEmbeddedNumber replays PF^PF130191-CC34: BC
// holds PF130191, and the result must say that is what was reconciled.
func TestSourceOrder_FallsBackToEmbeddedNumber(t *testing.T) {
	var asked []string
	orders := &BCClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/v2.0",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			filter := r.URL.Query().Get("$filter")
			asked = append(asked, filter)
			body := `{"value":[]}`
			if filter == "number eq 'PF130191'" {
				body = `{"value":[{"id":"po-1","number":"PF130191","status":"Open","purchaseOrderLines":[]}]}`
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
	}
	src := &Source{Orders: orders}

	order, stats, err := src.Order(context.Background(), "PF^PF130191-CC34")
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if order.PF != "PF130191" || stats.OrderNumberUsed != "PF130191" {
		t.Errorf("order %q, OrderNumberUsed %q; want PF130191 for both", order.PF, stats.OrderNumberUsed)
	}
	if len(asked) != 2 || asked[0] != "number eq 'PF^PF130191-CC34'" {
		t.Errorf("asked %q; want the printed number first, then PF130191", asked)
	}

	// No order under either number: the error names the printed one.
	_, _, err = src.Order(context.Background(), "PF^PF999999-CC34")
	if !errors.Is(err, ErrPurchaseOrderNotFound) || !strings.Contains(err.Error(), "PF^PF999999-CC34") {
		t.Errorf("err = %v; want not found, naming the printed number", err)
	}
}

func TestBaseOrderNumber(t *testing.T) {
	tests := []struct{ in, want string }{
		{"PF126282/EXC", "PF126282"},
		{"PF126282/EXC/2", "PF126282"}, // first separator wins
		{"PF130368", ""},               // nothing to strip
		{"PF126282/", ""},              // a trailing slash leaves no annotation
		{"/EXC", ""},                   // no order number before the separator
		{"", ""},
	}
	for _, tc := range tests {
		if got := baseOrderNumber(tc.in); got != tc.want {
			t.Errorf("baseOrderNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
