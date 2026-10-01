package bc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// fakeCredential always returns a fixed token; doRequest never inspects it.
type fakeCredential struct{}

func (fakeCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake"}, nil
}

// erroringTransport simulates a network failure (DNS, timeout, connection
// refused) so http.Client.Do returns a nil *http.Response alongside the error.
type erroringTransport struct{}

func (erroringTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("simulated network failure")
}

// TestDoRequest_HTTPClientErrorDoesNotPanic guards against the bug where
// doRequest's error branch was empty and execution fell through to
// `defer resp.Body.Close()` with resp == nil, panicking on every network
// failure (timeout, DNS failure, connection refused) instead of returning an
// error to the caller.
func TestDoRequest_HTTPClientErrorDoesNotPanic(t *testing.T) {
	c := &BCClient{
		cred:       fakeCredential{},
		baseURL:    "https://example.invalid/v2.0",
		httpClient: &http.Client{Transport: erroringTransport{}},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("doRequest panicked instead of returning an error: %v", r)
		}
	}()

	_, status, err := c.doRequest(context.Background(), http.MethodGet, "/purchaseOrders", nil, nil)
	if err == nil {
		t.Fatal("expected an error when the HTTP client fails, got nil")
	}
	if status != 0 {
		t.Fatalf("expected status 0 on transport failure, got %d", status)
	}
}

// itemServer answers /items?$filter=number eq 'A' or ... with one item per
// clause, named after its number, and fails any request whose filter mentions
// a number in failFor.
func itemServer(t *testing.T, requests *int, failFor string) *BCClient {
	t.Helper()
	return &BCClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/v2.0",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			*requests++
			filter := r.URL.Query().Get("$filter")
			if failFor != "" && strings.Contains(filter, "'"+failFor+"'") {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("busy")), Header: make(http.Header)}, nil
			}
			var value []Item
			for _, clause := range strings.Split(filter, " or ") {
				no := strings.Trim(strings.TrimPrefix(clause, "number eq "), "'")
				value = append(value, Item{Number: no, DisplayName: "Name " + no, DisplayName2: "Detail " + no})
			}
			body, _ := json.Marshal(itemListResponse{Value: value})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
		})},
	}
}

// TestEnhancePurchaseOrderLines_BatchesAndReports: one request per chunk
// rather than per item, every Item line named, and non-Item lines untouched.
func TestEnhancePurchaseOrderLines_BatchesAndReports(t *testing.T) {
	var requests int
	c := itemServer(t, &requests, "")

	var lines []PurchaseOrderLine
	for i := range 25 {
		n := itemNoAt(i)
		// Every item twice: duplicates must be asked for once.
		lines = append(lines,
			PurchaseOrderLine{LineType: "Item", LineObjectNumber: n},
			PurchaseOrderLine{LineType: "Item", LineObjectNumber: n})
	}
	lines = append(lines, PurchaseOrderLine{LineType: "Comment", Description: "Right Hand Facing"})

	out, lookup := c.EnhancePurchaseOrderLines(context.Background(), lines)

	if requests != 2 {
		t.Errorf("issued %d requests, want 2 (25 distinct items at %d per chunk)", requests, itemChunkSize)
	}
	if lookup.Requested != 25 || lookup.Returned != 25 || lookup.Err != nil {
		t.Errorf("lookup = %+v, want 25 requested, 25 returned, no error", lookup)
	}
	for _, l := range out[:50] {
		if l.DisplayName != "Name "+l.LineObjectNumber || l.DisplayName2 != "Detail "+l.LineObjectNumber {
			t.Errorf("line %s named %q / %q", l.LineObjectNumber, l.DisplayName, l.DisplayName2)
		}
	}
	if out[50].DisplayName != "" {
		t.Errorf("comment line was given a display name %q", out[50].DisplayName)
	}
}

// TestEnhancePurchaseOrderLines_FailureIsReportedNotSwallowed: a failed chunk
// leaves its lines on their own text, the other chunks still apply, and the
// error comes back. It used to be discarded, so a run comparing free text in
// place of item names was indistinguishable from a normal one.
func TestEnhancePurchaseOrderLines_FailureIsReportedNotSwallowed(t *testing.T) {
	var requests int
	failing := itemNoAt(0) // in the first chunk
	c := itemServer(t, &requests, failing)

	var lines []PurchaseOrderLine
	for i := range 25 {
		lines = append(lines, PurchaseOrderLine{LineType: "Item", LineObjectNumber: itemNoAt(i), Description: "line text"})
	}

	out, lookup := c.EnhancePurchaseOrderLines(context.Background(), lines)

	if lookup.Err == nil {
		t.Fatal("a failed chunk returned no error")
	}
	if lookup.Requested != 25 || lookup.Returned != 5 {
		t.Errorf("lookup = %d requested / %d returned, want 25 / 5 (only the second chunk succeeded)", lookup.Requested, lookup.Returned)
	}
	if out[0].DisplayName != "" {
		t.Errorf("line in the failed chunk was named %q", out[0].DisplayName)
	}
	if out[24].DisplayName == "" {
		t.Error("line in the successful chunk was not named: one failure cost more than its own chunk")
	}
}
