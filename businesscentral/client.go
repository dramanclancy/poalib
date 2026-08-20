// Package businesscentral calls the Business Central API v2.0.
package businesscentral

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
	ETag               string	`json:"@odata.etag"`
	ID                 string	`json:"id"`
	Number             string	`json:"number"`
	VendorNumber       string	`json:"vendorNumber"`
	VendorName         string	`json:"vendorName"`
	Status             string	`json:"status"`
	TotalAmountExclTax float64	`json:"totalAmountExcludingTax"`

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
	DiscountAmount  float64 `json:"discountAmount"`
	NetAmount        float64 `json:"netAmount"`
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
		return nil, fmt.Errorf("purchase order %q not found", poNumber)
	}
	if len(parsed.Value) > 1 {
		return nil, fmt.Errorf("purchase order %q matched %d records; expected exactly 1", poNumber, len(parsed.Value))
	}

	return &parsed.Value[0], nil
}

// GetPurchaseOrderID resolves a PO document number to its BC record GUID.
func (c *BCClient) GetPurchaseOrderID(ctx context.Context, poNumber string) (string, error) {
	po, err := c.GetPurchaseOrder(ctx, poNumber)
	if err != nil {
		return "", err
	}
	return po.ID, nil
}

func (c *BCClient) GetitemRange(ctx context.Context, IT string) (*PurchaseOrder, error) {
	if IT == "" {
		return nil, errors.New("businesscentral: empty purchase order number")
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
	var parsed purchaseOrderListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if len(parsed.Value) == 0 {
		return nil, fmt.Errorf("Item Number %q not found", IT)
	}
	if len(parsed.Value) > 1 {
		return nil, fmt.Errorf("Item Number %q matched %d records; expected exactly 1", IT, len(parsed.Value))
	}

	return &parsed.Value[0], nil
}
