package bc

// Client for "CaseysItems", a custom OData V4 web service (a published BC
// page/query, not the standard /api/v2.0 REST surface) that exposes item
// detail beyond what businesscentral.Item carries — supplier model/catalogue
// codes, range, seat count. See testdata/poa-samples/bc-odata-metadata-*.xml
// for the full 83-property declared schema; CaseysItem models only the
// subset phase1b-item-enrichment.md section 1 identifies as useful.
//
// This is a DIFFERENT endpoint shape from BCClient: the company segment is a
// name literal (Company('Caseys Furniture')), not the GUID the v2.0 API
// addresses companies by, so it cannot reuse BCClient's URL builder. Same
// client-credential token and scope.

import (
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

// CaseysItem carries the CaseysItems fields that matter to POA matching —
// see phase1b-item-enrichment.md section 1 for why each one is here and why
// Search_Field/Search_Description are deliberately absent.
type CaseysItem struct {
	No           string  `json:"No"`
	ModelNo      string  `json:"Model_No"`
	VendorItemNo string  `json:"Vendor_Item_No"`
	Description3 string  `json:"Description_3"`
	RangeCode    string  `json:"Range_Code"`
	NoOfSeats    float64 `json:"No_of_Seats"`
	VendorNo     string  `json:"Vendor_No"`
	SupplierName string  `json:"Supplier_Name"`
}

type caseysItemsResponse struct {
	Value []CaseysItem `json:"value"`
}

// caseysChunkSize caps how many item numbers go into one $filter — OData URL
// length limits bite on a long PO's worth of "No eq 'X' or ..." clauses
// well before this.
const caseysChunkSize = 20

// CaseysClient calls the CaseysItems OData V4 web service for a single
// tenant/environment/company.
type CaseysClient struct {
	cred       azcore.TokenCredential
	baseURL    string
	httpClient *http.Client
}

// NewCaseysClient constructs a client bound to one tenant, environment and
// company. companyName is the company's NAME as BC stores it ("Caseys
// Furniture"), not its GUID — unlike the v2.0 API's companies(GUID), the
// ODataV4 surface addresses companies by name literal.
func NewCaseysClient(cred azcore.TokenCredential, tenantID, environment, companyName string) (*CaseysClient, error) {
	if cred == nil {
		return nil, errors.New("businesscentral: nil credential")
	}
	if tenantID == "" || environment == "" || companyName == "" {
		return nil, errors.New("businesscentral: tenantID, environment and companyName are all required")
	}
	// The company literal's surrounding quotes are not percent-encoded, only
	// the name inside them — PathEscape gives the same %20-for-space
	// behaviour proven out in tools/testCaseysItems.ps1.
	base := fmt.Sprintf(
		"https://api.businesscentral.dynamics.com/v2.0/%s/%s/ODataV4/Company('%s')/CaseysItems",
		url.PathEscape(tenantID),
		url.PathEscape(environment),
		url.PathEscape(escapeODataString(companyName)),
	)
	return &CaseysClient{cred: cred, baseURL: base, httpClient: http.DefaultClient}, nil
}

func (c *CaseysClient) doRequest(ctx context.Context, pathAndQuery string) ([]byte, int, error) {
	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{bcScope}})
	if err != nil {
		return nil, 0, fmt.Errorf("acquiring BC token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+pathAndQuery, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("CaseysItems request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// GetItemsByNumbers looks up CaseysItems detail for a batch of item numbers,
// one HTTP request per ~20 numbers rather than one per line. A number the
// service has no row for is simply absent from the returned map — callers
// treat a missing entry exactly like a failed lookup (BCLineGroup.Detail
// stays nil), never as an error.
//
// On a mid-batch failure this returns whatever chunks already succeeded
// alongside the error, so a caller that chooses to degrade rather than fail
// the whole order (see poa.Reconcile) still gets partial enrichment.
func (c *CaseysClient) GetItemsByNumbers(ctx context.Context, numbers []string) (map[string]CaseysItem, error) {
	out := make(map[string]CaseysItem, len(numbers))

	seen := make(map[string]bool, len(numbers))
	unique := make([]string, 0, len(numbers))
	for _, n := range numbers {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		unique = append(unique, n)
	}

	for start := 0; start < len(unique); start += caseysChunkSize {
		end := min(start+caseysChunkSize, len(unique))
		chunk := unique[start:end]

		clauses := make([]string, len(chunk))
		for i, n := range chunk {
			clauses[i] = fmt.Sprintf("No eq '%s'", escapeODataString(n))
		}
		params := url.Values{}
		params.Set("$filter", strings.Join(clauses, " or "))

		body, status, err := c.doRequest(ctx, "?"+params.Encode())
		if err != nil {
			return out, fmt.Errorf("fetching CaseysItems batch %d-%d: %w", start, end, err)
		}
		if status != http.StatusOK {
			return out, fmt.Errorf("CaseysItems API returned %d: %s", status, string(body))
		}
		var parsed caseysItemsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return out, fmt.Errorf("parsing CaseysItems response: %w", err)
		}
		for _, item := range parsed.Value {
			out[item.No] = item
		}
	}
	return out, nil
}
