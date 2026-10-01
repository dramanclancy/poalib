// Package bc calls the Business Central API v2.0 and the CaseysItems OData v4
// page, and translates both into the domain.
package bc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const bcScope = "https://api.businesscentral.dynamics.com/.default"

// requestTimeout bounds one Business Central call. http.DefaultClient has no
// timeout at all, so a stalled connection held a Functions worker until the
// host killed the whole invocation. A purchase order with its lines expanded
// is the slowest query made, and comfortably inside this.
const requestTimeout = 60 * time.Second

func newHTTPClient() *http.Client { return &http.Client{Timeout: requestTimeout} }

// ErrPurchaseOrderNotFound reports that the query succeeded and matched
// nothing. Callers branch on it to retry with a different order number; every
// other failure means the request itself did not work and must not be retried
// as though the number were wrong.
var ErrPurchaseOrderNotFound = errors.New("purchase order not found")

// BCClient calls the Business Central API v2.0 for a single
// tenant / environment / company.
type BCClient struct {
	cred       azcore.TokenCredential
	baseURL    string
	httpClient *http.Client
}

// NewBCClient constructs a client bound to one tenant, environment and company.
// cred is typically the same ClientSecretCredential held by GraphAuth.
func NewBCClient(cred azcore.TokenCredential, tenantID, environment, companyID string) (*BCClient, error) {
	if cred == nil {
		return nil, errors.New("businesscentral: nil credential")
	}
	if tenantID == "" || environment == "" || companyID == "" {
		return nil, errors.New("businesscentral: tenantID, environment and companyID are all required")
	}
	base := fmt.Sprintf(
		"https://api.businesscentral.dynamics.com/v2.0/%s/%s/api/v2.0/companies(%s)",
		url.PathEscape(tenantID),
		url.PathEscape(environment),
		url.PathEscape(companyID),
	)
	return &BCClient{
		cred:       cred,
		baseURL:    base,
		httpClient: newHTTPClient(),
	}, nil

}

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

type purchaseOrderListResponse struct {
	Value []PurchaseOrder `json:"value"`
}

// PurchaseOrder mirrors the BC API v2.0 purchaseOrders entity (subset).
type PurchaseOrder struct {
	ETag               string  `json:"@odata.etag"`
	ID                 string  `json:"id"`
	Number             string  `json:"number"`
	VendorNumber       string  `json:"vendorNumber"`
	VendorName         string  `json:"vendorName"`
	Status             string  `json:"status"`
	TotalAmountExclTax float64 `json:"totalAmountExcludingTax"`

	PurchaseOrderLines []PurchaseOrderLine `json:"purchaseOrderLines"`
}

// PurchaseOrderLine mirrors the BC API v2.0 purchaseOrderLines entity (subset).
//
// Note: ItemID is the item GUID; LineObjectNumber is the item "No." shown in
// the BC UI — use the latter when matching against vendor model numbers.
type PurchaseOrderLine struct {
	ETag             string  `json:"@odata.etag"`
	ID               string  `json:"id"`
	Sequence         int     `json:"sequence"`
	LineType         string  `json:"lineType"`
	ItemID           string  `json:"itemId"`
	LineObjectNumber string  `json:"lineObjectNumber"`
	Description      string  `json:"description"`
	Quantity         float64 `json:"quantity"`
	DirectUnitCost   float64 `json:"directUnitCost"`
	DiscountPercent  float64 `json:"discountPercent"`
	DiscountAmount   float64 `json:"discountAmount"`
	NetAmount        float64 `json:"netAmount"`

	// DisplayName / DisplayName2 are not part of the purchaseOrderLines
	// payload itself; EnhancePurchaseOrderLines fills them in from the
	// item's own record after the fact.
	DisplayName  string `json:"-"`
	DisplayName2 string `json:"-"`
}

// Item mirrors the BC API v2.0 items entity (subset) — see
// tools/testBCGetItem.ps1 for the full schema returned by the endpoint.
type Item struct {
	ID           string `json:"id"`
	Number       string `json:"number"`
	DisplayName  string `json:"displayName"`
	DisplayName2 string `json:"displayName2"`
}

type itemListResponse struct {
	Value []Item `json:"value"`
}

// ---------------------------------------------------------------------------
// Core request plumbing
// ---------------------------------------------------------------------------

func (c *BCClient) doRequest(ctx context.Context, method, pathAndQuery string, body []byte, extraHeaders map[string]string) ([]byte, int, error) {

	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{bcScope},
	})

	if err != nil {
		return nil, 0, fmt.Errorf("acquiring BC token: %w", err)
	}

	var reader io.Reader

	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+pathAndQuery, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("BC request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// escapeODataString doubles single quotes per OData literal rules.
func escapeODataString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// GetPurchaseOrder fetches a purchase order by its document number ("PO-001234"),
// with lines expanded. Returns an error if no PO matches.

func (c *BCClient) GetPurchaseOrder(ctx context.Context, poNumber string) (*PurchaseOrder, error) {
	if poNumber == "" {
		return nil, errors.New("businesscentral: empty purchase order number")
	}
	params := url.Values{}
	params.Set("$filter", fmt.Sprintf("number eq '%s'", escapeODataString(poNumber)))
	params.Set("$expand", "purchaseOrderLines")

	body, status, err := c.doRequest(ctx, http.MethodGet, "/purchaseOrders?"+params.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("BC API returned %d: %s", status, string(body))
	}
	var parsed purchaseOrderListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if len(parsed.Value) == 0 {
		return nil, fmt.Errorf("purchase order %q: %w", poNumber, ErrPurchaseOrderNotFound)
	}
	if len(parsed.Value) > 1 {
		return nil, fmt.Errorf("purchase order %q matched %d records; expected exactly 1", poNumber, len(parsed.Value))
	}

	return &parsed.Value[0], nil
}

// itemChunkSize caps how many item numbers go into one $filter, for the same
// URL-length reason as caseysChunkSize.
const itemChunkSize = 20

// GetItems looks up the item records for a set of item numbers, one request
// per itemChunkSize numbers rather than one per number. The result is keyed
// by upper-cased number; a number BC holds no item for is simply absent.
//
// A failing chunk does not stop the others: whatever arrived is returned
// alongside the first error, so one bad request costs at most its own chunk.
func (c *BCClient) GetItems(ctx context.Context, numbers []string) (map[string]Item, error) {
	out := make(map[string]Item, len(numbers))
	var firstErr error
	for start := 0; start < len(numbers); start += itemChunkSize {
		chunk := numbers[start:min(start+itemChunkSize, len(numbers))]
		clauses := make([]string, len(chunk))
		for i, n := range chunk {
			clauses[i] = fmt.Sprintf("number eq '%s'", escapeODataString(n))
		}
		params := url.Values{}
		params.Set("$filter", strings.Join(clauses, " or "))

		items, err := c.getItemPage(ctx, "/items?"+params.Encode())
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetching items %d-%d: %w", start, start+len(chunk), err)
			}
			continue
		}
		for _, item := range items {
			out[strings.ToUpper(item.Number)] = item
		}
	}
	return out, firstErr
}

func (c *BCClient) getItemPage(ctx context.Context, pathAndQuery string) ([]Item, error) {
	body, status, err := c.doRequest(ctx, http.MethodGet, pathAndQuery, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("BC API returned %d: %s", status, string(body))
	}
	var parsed itemListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return parsed.Value, nil
}

// ItemNameLookup is what EnhancePurchaseOrderLines can say about itself, for
// the run record.
type ItemNameLookup struct {
	Requested int   // distinct item numbers asked for
	Returned  int   // how many of those BC returned a record for
	Err       error // the first failure; nil when every request succeeded
}

// EnhancePurchaseOrderLines looks up the Item record behind each "Item" line
// and copies its displayName/displayName2 onto the line, so downstream
// description matching can prefer the item's own name over the PO line's
// free-text Description.
//
// A line whose item could not be looked up keeps the PO line's own text
// rather than failing the order. That changes what the comparison reads,
// which is why the lookup reports how it went instead of swallowing errors:
// it used to discard every failure, so a run that compared free text where it
// normally compares item names looked exactly like one that did not.
func (c *BCClient) EnhancePurchaseOrderLines(ctx context.Context, lines []PurchaseOrderLine) ([]PurchaseOrderLine, ItemNameLookup) {
	out := make([]PurchaseOrderLine, len(lines))
	copy(out, lines)

	var numbers []string
	seen := map[string]bool{}
	for _, l := range out {
		key := strings.ToUpper(l.LineObjectNumber)
		if l.LineType != "Item" || key == "" || seen[key] {
			continue
		}
		seen[key] = true
		numbers = append(numbers, l.LineObjectNumber)
	}
	lookup := ItemNameLookup{Requested: len(numbers)}
	if len(numbers) == 0 {
		return out, lookup
	}

	items, err := c.GetItems(ctx, numbers)
	lookup.Returned, lookup.Err = len(items), err
	for i := range out {
		if out[i].LineType != "Item" {
			continue
		}
		item, ok := items[strings.ToUpper(out[i].LineObjectNumber)]
		if !ok {
			continue
		}
		out[i].DisplayName = item.DisplayName
		out[i].DisplayName2 = item.DisplayName2
	}
	return out, lookup
}
