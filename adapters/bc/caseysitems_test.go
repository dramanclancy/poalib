package bc

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

// TestCaseysItem_DecodesTheWireFormat pins the JSON tags against a literal
// body rather than against the struct's own output.
//
// TestCaseysClient_GetItemsByNumbers_Chunks builds its response with
// json.Marshal(caseysItemsResponse{...}) — the same struct it then decodes
// into — so every tag round-trips perfectly no matter what it says, and a tag
// matching nothing the service actually sends would be invisible. That is not
// a hypothetical worry: the seat check returned "no data" on every line of
// twenty consecutive orders, and a wrong tag was the first suspect. It turned
// out No_of_Seats decodes correctly and BC's master data is simply almost
// empty — 2 of 48 item records carry it — but nothing in the suite could tell
// those two explanations apart, which is the gap this closes.
//
// The property names below are still worth confirming against live $metadata
// (tools/testCaseysItems.ps1, section 4) whenever one is added or changed.
func TestCaseysItem_DecodesTheWireFormat(t *testing.T) {
	const body = `{"value":[{
		"No":"IT0226778",
		"Model_No":"NOV4S",
		"Vendor_Item_No":"NOVA-4S",
		"Description_3":"Fabric Grade A",
		"Range_Code":"NOVA",
		"No_of_Seats":4.0,
		"Vendor_No":"VX00006",
		"Supplier_Name":"Ashwood"
	}]}`

	var parsed caseysItemsResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decoding a CaseysItems body: %v", err)
	}
	if len(parsed.Value) != 1 {
		t.Fatalf("decoded %d items, want 1", len(parsed.Value))
	}
	got := parsed.Value[0]

	want := CaseysItem{
		No:           "IT0226778",
		ModelNo:      "NOV4S",
		VendorItemNo: "NOVA-4S",
		Description3: "Fabric Grade A",
		RangeCode:    "NOVA",
		NoOfSeats:    4,
		VendorNo:     "VX00006",
		SupplierName: "Ashwood",
	}
	if got != want {
		t.Errorf("decoded\n  %+v\nwant\n  %+v", got, want)
	}

	// Called out separately because this is the field whose silence started
	// the investigation: a zero here means toItemDetail maps Seats to nil and
	// every seat check on the order reports "no data" instead of a verdict,
	// and there would be no way to tell a bad tag from empty master data.
	if got.NoOfSeats == 0 {
		t.Error("NoOfSeats decoded as 0 from a body that states 4: the JSON tag does not match the property name")
	}
}
