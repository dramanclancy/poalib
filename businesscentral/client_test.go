package businesscentral

import (
	"context"
	"errors"
	"net/http"
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
