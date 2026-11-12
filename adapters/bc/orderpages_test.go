package bc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

// emptyV2 is a v2.0 client that answers every purchase order query the way
// Business Central answered for PF119012: 200, and no order.
func emptyV2(asked *[]string) *BCClient {
	return &BCClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/api/v2.0/companies(guid)",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			*asked = append(*asked, "v2.0 "+r.URL.Query().Get("$filter"))
			return jsonResponse(http.StatusOK, `{"value":[]}`), nil
		})},
	}
}

// pagesHolding is an OData pages client that holds one order, PF100001, with
// a discounted item, its text line, and a surcharge.
func pagesHolding(asked *[]string) *OrderPagesClient {
	return &OrderPagesClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/ODataV4/Company('Test%20Co')",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			filter := r.URL.Query().Get("$filter")
			*asked = append(*asked, "pages "+filter)
			switch {
			case strings.HasSuffix(r.URL.Path, "/"+orderHeaderPage) && filter == "No eq 'PF100001' and Document_Type eq 'Order'":
				return jsonResponse(http.StatusOK, `{"value":[{"Document_Type":"Order","No":"PF100001","Buy_from_Vendor_No":"VX1","Buy_from_Vendor_Name":"Vendor One","Status":"Released","Amount":930}]}`), nil
			case strings.HasSuffix(r.URL.Path, "/"+orderLinesPage) && filter == "Document_No eq 'PF100001' and Document_Type eq 'Order'":
				return jsonResponse(http.StatusOK, `{"value":[
					{"Line_No":10000,"Type":"Item","No":"IT1","Description":"Shell only","Quantity":8,"Direct_Unit_Cost":250,"Line_Amount":925},
					{"Line_No":20000,"Type":" ","No":"","Description":"Yellow","Quantity":0,"Direct_Unit_Cost":0,"Line_Amount":0},
					{"Line_No":30000,"Type":"G/L Account","No":"718000","Description":"Vendor Surcharges","Quantity":1,"Direct_Unit_Cost":5,"Line_Amount":5}]}`), nil
			}
			return jsonResponse(http.StatusOK, `{"value":[]}`), nil
		})},
	}
}

// TestSourceOrder_ReadsFromPagesWhenV2HasNoSuchOrder replays PF119012: the
// v2.0 API answered 200 with no order, for an order the header and line pages
// both held. The order must be built from the pages, in v2.0's vocabulary,
// and the run must say where it came from.
func TestSourceOrder_ReadsFromPagesWhenV2HasNoSuchOrder(t *testing.T) {
	var asked, logged []string
	src := &Source{
		Orders: emptyV2(&asked),
		Pages:  pagesHolding(&asked),
		Log:    func(_ context.Context, format string, _ ...any) { logged = append(logged, format) },
	}

	order, stats, err := src.Order(context.Background(), "PF100001")
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if order.PF != "PF100001" || order.VendorNo != "VX1" || order.Status != "Released" || order.TotalExVAT != 930 {
		t.Errorf("order = %q / %q / %q / %g; want the header page's values", order.PF, order.VendorNo, order.Status, order.TotalExVAT)
	}
	if stats.OrderReadFrom == "" || stats.OrderNumberUsed != "" {
		t.Errorf("OrderReadFrom %q, OrderNumberUsed %q; want the pages named and no substitute number", stats.OrderReadFrom, stats.OrderNumberUsed)
	}
	if len(order.Products) != 2 {
		t.Fatalf("built %d products, want 2: the blank-type line is text, the G/L line is a product", len(order.Products))
	}
	item, surcharge := order.Products[0], order.Products[1]
	if item.Net != 925 || item.DiscountPercent != 53.75 || item.DiscountAmount != 1075 {
		t.Errorf("item net %g, discount %g%% / %g; want 925, 53.75%% and 1075 worked out from the line amount", item.Net, item.DiscountPercent, item.DiscountAmount)
	}
	if len(item.Comments) != 1 || item.Comments[0] != "Yellow" {
		t.Errorf("item comments = %q, want the text line below it", item.Comments)
	}
	if surcharge.LineType != "Account" || surcharge.DiscountPercent != 0 {
		t.Errorf("surcharge type %q, discount %g; want Account (v2.0's name for G/L Account) and no discount", surcharge.LineType, surcharge.DiscountPercent)
	}
	if len(asked) < 2 || !strings.HasPrefix(asked[0], "v2.0 ") || !strings.HasPrefix(asked[1], "pages ") {
		t.Errorf("asked %q; want v2.0 first and the pages only after it found nothing", asked)
	}
	if len(logged) < 2 {
		t.Errorf("logged %q; want the v2.0 miss with its URL and the switch to the pages", logged)
	}
}

// TestSourceOrder_NotFoundOnEitherSurface: an order neither surface holds is
// still "not found", naming the printed number.
func TestSourceOrder_NotFoundOnEitherSurface(t *testing.T) {
	var asked []string
	src := &Source{Orders: emptyV2(&asked), Pages: pagesHolding(&asked)}

	_, stats, err := src.Order(context.Background(), "PF999999")
	if !errors.Is(err, ErrPurchaseOrderNotFound) || !strings.Contains(err.Error(), "PF999999") {
		t.Errorf("err = %v; want not found, naming PF999999", err)
	}
	if stats.OrderReadFrom != "" {
		t.Errorf("OrderReadFrom = %q for an order nothing held", stats.OrderReadFrom)
	}
}

// TestSourceOrder_PagesFailureIsNotNotFound: when the pages cannot be read the
// order is not known to be missing, and the error must not say it is.
func TestSourceOrder_PagesFailureIsNotNotFound(t *testing.T) {
	var asked []string
	pages := &OrderPagesClient{cred: fakeCredential{}, baseURL: "https://example.invalid/ODataV4/Company('Test')",
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{"error":{"message":"no permission"}}`), nil
		})}}
	src := &Source{Orders: emptyV2(&asked), Pages: pages}

	_, _, err := src.Order(context.Background(), "PF100001")
	if err == nil || errors.Is(err, ErrPurchaseOrderNotFound) || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v; want the 403 reported, not a missing order", err)
	}
}

// TestOrderPagesURLs pins the requests against the one that was shown to
// work: company by name with the space encoded, the misspelt page, and a
// filter on No (the order's own number), quoted.
func TestOrderPagesURLs(t *testing.T) {
	c, err := NewOrderPagesClient(fakeCredential{}, "tenant", "Production", "Test Co")
	if err != nil {
		t.Fatal(err)
	}
	got := c.HeaderURL("PF100001")
	const prefix = "https://api.businesscentral.dynamics.com/v2.0/tenant/Production/ODataV4/Company('Test%20Co')/Purcahse_Header_SP_Report?"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("HeaderURL = %s\n      want prefix %s", got, prefix)
	}
	req, _ := http.NewRequest(http.MethodGet, got, nil)
	if f := req.URL.Query().Get("$filter"); f != "No eq 'PF100001' and Document_Type eq 'Order'" {
		t.Errorf("$filter = %q", f)
	}
}

func TestPageLineType(t *testing.T) {
	for in, want := range map[string]string{
		" ": "Comment", "": "Comment", "Item": "Item", "G/L Account": "Account",
		"Charge (Item)": "Charge", "Fixed Asset": "Fixed Asset",
	} {
		if got := pageLineType(in); got != want {
			t.Errorf("pageLineType(%q) = %q, want %q", in, got, want)
		}
	}
}
