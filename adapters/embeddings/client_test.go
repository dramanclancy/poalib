package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbed_PlacesVectorsByIndex: both APIs document that vectors may come
// back out of order. Placing them by arrival would hand every description its
// neighbour's vector, and every pairing would be scored against the wrong text.
func TestEmbed_PlacesVectorsByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		var req embeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "m" || len(req.Input) != 3 {
			t.Errorf("request = %+v", req)
		}
		// Reversed, with each vector's first value naming its index.
		_, _ = w.Write([]byte(`{"data":[
			{"index":2,"embedding":[2]},
			{"index":0,"embedding":[0]},
			{"index":1,"embedding":[1]}]}`))
	}))
	defer srv.Close()

	c, err := NewOpenAI("key", "m")
	if err != nil {
		t.Fatal(err)
	}
	c.url = srv.URL

	got, err := c.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for i, v := range got {
		if len(v) != 1 || v[0] != float32(i) {
			t.Errorf("vector %d = %v, want [%d]", i, v, i)
		}
	}
}

// TestEmbed_ErrorStatusIsAnError: a provider error must fail the order rather
// than produce empty vectors that read as "no resemblance".
func TestEmbed_ErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c, _ := NewAzure(srv.URL, "key", "deploy")
	c.url = srv.URL
	if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
		t.Error("a 429 returned no error")
	}
}
