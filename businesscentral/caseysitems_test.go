package businesscentral

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCaseysClient_GetItemsByNumbers_TransportFailureReturnsError guards the
// same failure mode client_test.go covers for BCClient: a network failure
// (DNS, timeout, connection refused) must come back as an error, never a
// panic, so poa.Reconcile's degrade-to-Phase-1 path actually gets a chance
// to run instead of the whole request blowing up.
func TestCaseysClient_GetItemsByNumbers_TransportFailureReturnsError(t *testing.T) {
	c := &CaseysClient{
		cred:       fakeCredential{},
		baseURL:    "https://example.invalid/ODataV4/Company('Test')/CaseysItems",
		httpClient: &http.Client{Transport: erroringTransport{}},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GetItemsByNumbers panicked instead of returning an error: %v", r)
		}
	}()

	items, err := c.GetItemsByNumbers(context.Background(), []string{"IT0001"})
	if err == nil {
		t.Fatal("expected an error when the HTTP client fails, got nil")
	}
	if items == nil {
		t.Error("items map is nil on failure; want a non-nil (possibly empty) map so callers can range over it unconditionally")
	}
	if len(items) != 0 {
		t.Errorf("items = %v, want empty on a total transport failure", items)
	}
}

// roundTripFunc lets a test supply a RoundTrip as a plain function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestCaseysClient_GetItemsByNumbers_Chunks guards the batching contract:
// more than caseysChunkSize numbers must go out as more than one request,
// each filtering on "No eq 'X' or No eq 'Y' ..." for its own slice, and the
// results from every chunk must end up merged into one map.
func TestCaseysClient_GetItemsByNumbers_Chunks(t *testing.T) {
	var requestFilters []string

	c := &CaseysClient{
		cred:    fakeCredential{},
		baseURL: "https://example.invalid/ODataV4/Company('Test')/CaseysItems",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			filter := r.URL.Query().Get("$filter")
			requestFilters = append(requestFilters, filter)

			// Echo back one CaseysItem per "No eq 'X'" clause in this chunk's filter.
			var value []CaseysItem
			for _, clause := range strings.Split(filter, " or ") {
				no := strings.Trim(strings.TrimPrefix(clause, "No eq "), "'")
				value = append(value, CaseysItem{No: no, ModelNo: "MODEL-" + no})
			}
			body, _ := json.Marshal(caseysItemsResponse{Value: value})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		})},
	}

	numbers := make([]string, 45) // 45 numbers -> 3 chunks of 20/20/5
	for i := range numbers {
		numbers[i] = itemNoAt(i)
	}

	items, err := c.GetItemsByNumbers(context.Background(), numbers)
	if err != nil {
		t.Fatalf("GetItemsByNumbers: %v", err)
	}

	if len(requestFilters) != 3 {
		t.Fatalf("issued %d requests, want 3 (45 numbers at %d per chunk)", len(requestFilters), caseysChunkSize)
	}
	if len(items) != len(numbers) {
		t.Fatalf("items has %d entries, want %d (results from every chunk must be merged)", len(items), len(numbers))
	}
	for _, n := range numbers {
		if items[n].ModelNo != "MODEL-"+n {
			t.Errorf("items[%q].ModelNo = %q, want %q", n, items[n].ModelNo, "MODEL-"+n)
		}
	}
}

func itemNoAt(i int) string { return "IT" + string(rune('A'+i%26)) + string(rune('0'+i/26)) }
