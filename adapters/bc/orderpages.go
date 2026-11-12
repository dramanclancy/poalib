package bc

// A second way to read a purchase order: the header and line pages the
// company publishes as OData V4 web services, addressed by company name like
// CaseysItems.
//
// It exists because the standard v2.0 purchaseOrders endpoint does not return
// every order Business Central holds. On 2026-11-12 it answered 200 with an
// empty list for PF119012 (Bontempi, raised May 2025, Released, fully
// received and invoiced) while Purcahse_Header_SP_Report returned the order
// and CaseysPurchaseLines its three lines, in the same company. The request
// was right; the endpoint had no row for the order. Source.fetchOrder asks
// here only after v2.0 has said "no such order".
//
// For orders both surfaces hold (PF132195, PF132266 compared the same day)
// the pages agree with v2.0 line for line, with three differences this file
// translates: the line type is BC's own caption ("G/L Account", blank for a
// text line) where v2.0 says "Account" and "Comment"; the status of a
// released order reads "Released" where v2.0 says "Open"; and the line page
// has no discount columns, so the discount is derived from Line_Amount.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// The page names are as published in Business Central, misspelling included.
const (
	orderHeaderPage = "Purcahse_Header_SP_Report"
	orderLinesPage  = "CaseysPurchaseLines"
)

// OrderPagesClient reads purchase orders from the OData V4 pages of a single
// tenant/environment/company.
type OrderPagesClient struct {
	cred       azcore.TokenCredential
	baseURL    string // ends at Company('<name>')
	httpClient *http.Client
}

// NewOrderPagesClient constructs a client bound to one tenant, environment and
// company. companyName is the company's NAME ("Caseys Furniture"), as for
// NewCaseysClient.
func NewOrderPagesClient(cred azcore.TokenCredential, tenantID, environment, companyName string) (*OrderPagesClient, error) {
	if cred == nil {
		return nil, errors.New("businesscentral: nil credential")
	}
	if tenantID == "" || environment == "" || companyName == "" {
		return nil, errors.New("businesscentral: tenantID, environment and companyName are all required")
	}
	base := fmt.Sprintf(
		"https://api.businesscentral.dynamics.com/v2.0/%s/%s/ODataV4/Company('%s')",
		url.PathEscape(tenantID),
		url.PathEscape(environment),
		url.PathEscape(escapeODataString(companyName)),
	)
	return &OrderPagesClient{cred: cred, baseURL: base, httpClient: newHTTPClient()}, nil
}

type pageOrderHeader struct {
	No         string  `json:"No"`
	VendorNo   string  `json:"Buy_from_Vendor_No"`
	VendorName string  `json:"Buy_from_Vendor_Name"`
	Status     string  `json:"Status"`
	Amount     float64 `json:"Amount"`
}

type pageOrderLine struct {
	LineNo         int     `json:"Line_No"`
	Type           string  `json:"Type"`
	No             string  `json:"No"`
	Description    string  `json:"Description"`
	Quantity       float64 `json:"Quantity"`
	DirectUnitCost float64 `json:"Direct_Unit_Cost"`
	LineAmount     float64 `json:"Line_Amount"`
}

// orderFilter selects purchase ORDERS by number. Both pages sit on tables
// that also hold quotes, invoices and return orders, which have number
// series of their own but nothing that stops one colliding.
func orderFilter(field, poNumber string) string {
	p := url.Values{}
	p.Set("$filter", fmt.Sprintf("%s eq '%s' and Document_Type eq 'Order'", field, escapeODataString(poNumber)))
	return "?" + p.Encode()
}

// HeaderURL is the request GetPurchaseOrder makes for poNumber's header, for
// the log. The token travels in a header, so the URL holds no secret.
func (c *OrderPagesClient) HeaderURL(poNumber string) string {
	return c.baseURL + "/" + orderHeaderPage + orderFilter("No", poNumber)
}

func (c *OrderPagesClient) linesURL(poNumber string) string {
	return c.baseURL + "/" + orderLinesPage + orderFilter("Document_No", poNumber)
}

// get fetches one page of an OData list into value, reporting whether
// Business Central cut it short.
func (c *OrderPagesClient) get(ctx context.Context, fullURL string, value any) error {
	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{bcScope}})
	if err != nil {
		return fmt.Errorf("acquiring BC token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("BC request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("BC OData returned %d: %s", resp.StatusCode, string(body))
	}
	var list struct {
		Value    json.RawMessage `json:"value"`
		NextLink string          `json:"@odata.nextLink"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	if err := json.Unmarshal(list.Value, value); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	if list.NextLink != "" {
		return errTruncated
	}
	return nil
}

// GetPurchaseOrder fetches a purchase order and its lines by document number,
// in the shape BCClient.GetPurchaseOrder returns. It wraps
// ErrPurchaseOrderNotFound only when the header query succeeded and matched
// nothing.
//
// ID stays empty: the header page does not expose the order's GUID.
func (c *OrderPagesClient) GetPurchaseOrder(ctx context.Context, poNumber string) (*PurchaseOrder, error) {
	if poNumber == "" {
		return nil, errors.New("businesscentral: empty purchase order number")
	}
	var headers []pageOrderHeader
	if err := c.get(ctx, c.HeaderURL(poNumber), &headers); err != nil {
		return nil, fmt.Errorf("purchase order %q via %s: %w", poNumber, orderHeaderPage, err)
	}
	if len(headers) == 0 {
		return nil, fmt.Errorf("purchase order %q: %w", poNumber, ErrPurchaseOrderNotFound)
	}
	if len(headers) > 1 {
		return nil, fmt.Errorf("purchase order %q matched %d records on %s; expected exactly 1", poNumber, len(headers), orderHeaderPage)
	}

	var lines []pageOrderLine
	if err := c.get(ctx, c.linesURL(poNumber), &lines); err != nil {
		// Includes a paged answer: an order missing lines must not be compared.
		return nil, fmt.Errorf("purchase order %q lines via %s: %w", poNumber, orderLinesPage, err)
	}

	h := headers[0]
	po := &PurchaseOrder{
		Number:             h.No,
		VendorNumber:       h.VendorNo,
		VendorName:         h.VendorName,
		Status:             h.Status,
		TotalAmountExclTax: h.Amount,
		PurchaseOrderLines: make([]PurchaseOrderLine, len(lines)),
	}
	for i, l := range lines {
		po.PurchaseOrderLines[i] = l.toLine()
	}
	return po, nil
}

// toLine translates a page row into the v2.0 line the rest of the adapter
// reads.
//
// The page carries no discount: it has the quantity, the list cost and the
// amount BC charges for the line. A line of 8 at 250.00 with a Line_Amount of
// 925.00 carries a 53.75% discount that appears nowhere as a number, and
// PF119012's lines were all like that. So the discount is what is left between the two, and the percentage is
// rounded to two places, since Line_Amount is itself rounded to the cent.
func (l pageOrderLine) toLine() PurchaseOrderLine {
	out := PurchaseOrderLine{
		Sequence:         l.LineNo,
		LineType:         pageLineType(l.Type),
		LineObjectNumber: l.No,
		Description:      l.Description,
		Quantity:         l.Quantity,
		DirectUnitCost:   l.DirectUnitCost,
		NetAmount:        l.LineAmount,
	}
	if gross := l.Quantity * l.DirectUnitCost; gross != 0 {
		if discount := gross - l.LineAmount; math.Abs(discount) >= 0.005 {
			out.DiscountAmount = math.Round(discount*100) / 100
			out.DiscountPercent = math.Round(discount/gross*10000) / 100
		}
	}
	return out
}

// pageLineType maps the page's line type, which is the caption of BC's own
// enum, onto the names the v2.0 API uses and groupLines was written against.
// Anything unrecognised passes through: groupLines treats every type that is
// not text as a value line, so a new one is kept rather than dropped.
func pageLineType(t string) string {
	switch t = strings.TrimSpace(t); t {
	case "":
		return "Comment"
	case "G/L Account":
		return "Account"
	case "Charge (Item)":
		return "Charge"
	default:
		return t
	}
}
