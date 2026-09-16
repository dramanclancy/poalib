// Package businesscentral calls the Business Central API v2.0.
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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const bcScope = "https://api.businesscentral.dynamics.com/.default"

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
		httpClient: http.DefaultClient,
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
		return nil, 0, fmt.Errorf("acquirung BC token: %w", err)
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

func (c *BCClient) GetitemRange(ctx context.Context, IT string) (*Item, error) {
	if IT == "" {
		return nil, errors.New("businesscentral: empty item number")
	}
	params := url.Values{}
	params.Set("$filter", fmt.Sprintf("number eq '%s'", escapeODataString(IT)))

	body, status, err := c.doRequest(ctx, http.MethodGet, "/items?"+params.Encode(), nil, nil)
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
	if len(parsed.Value) == 0 {
		return nil, fmt.Errorf("item number %q not found", IT)
	}
	if len(parsed.Value) > 1 {
		return nil, fmt.Errorf("item number %q matched %d records; expected exactly 1", IT, len(parsed.Value))
	}

	return &parsed.Value[0], nil
}

// EnhancePurchaseOrderLines looks up the Item record behind each "Item" line
// and copies its displayName/displayName2 onto the line, so downstream
// description matching can prefer the item's own name over the PO line's
// free-text Description. Lookups are cached per item number since multiple
// lines on the same order commonly reference the same item. A line whose
// item lookup fails is left as-is rather than failing the whole order — the
// PO line's Description still stands in for it.
func (c *BCClient) EnhancePurchaseOrderLines(ctx context.Context, lines []PurchaseOrderLine) []PurchaseOrderLine {
	out := make([]PurchaseOrderLine, len(lines))
	copy(out, lines)

	cache := make(map[string]*Item)
	for i := range out {
		if out[i].LineType != "Item" || out[i].LineObjectNumber == "" {
			continue
		}
		number := out[i].LineObjectNumber
		item, looked := cache[number]
		if !looked {
			item, _ = c.GetitemRange(ctx, number)
			cache[number] = item
		}
		if item == nil {
			continue
		}
		out[i].DisplayName = item.DisplayName
		out[i].DisplayName2 = item.DisplayName2
	}
	return out
}
