// Package embeddings is a minimal client for one text-embedding model,
// against either Azure OpenAI or the OpenAI API.
//
// Two providers, one client: the request and response bodies are the same
// shape, and only the URL, the auth header and whether the model is named in
// the body differ. That is not enough difference to justify two packages, and
// keeping it in one guarantees both providers go through identical batching,
// timeout and result-ordering code — so a result measured against one is
// comparable with the other.
//
// Deliberately knows nothing about POAs — the same separation docintel keeps
// from the domain that calls it.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// azureAPIVersion is the Azure OpenAI REST version. 2023-05-15 covers the
// text-embedding-3 models; the `dimensions` parameter (shrinking 3-large to
// cut vector size) would need 2024-02-01 or later, which nothing currently
// asks for.
const azureAPIVersion = "2023-05-15"

const openAIEndpoint = "https://api.openai.com/v1/embeddings"

// Client calls one embedding model.
type Client struct {
	url string
	// authHeader is the header name the provider authenticates by: Azure uses
	// "api-key" with the bare key, OpenAI uses "Authorization: Bearer ...".
	authHeader string
	authValue  string
	// model is sent in the request body. OpenAI requires it; Azure selects the
	// model by deployment name in the URL instead, and it is left empty there.
	model string
	// describe is what the startup log prints, so a run is never ambiguous
	// about which provider produced its scores.
	describe   string
	httpClient *http.Client
}

// NewAzure builds a client for one Azure OpenAI embedding deployment.
//
// deployment is the name given to the deployment in Azure, which is not
// necessarily the model name — a deployment called "poa-embeddings" may serve
// text-embedding-3-small. All three arguments are required: there is no
// useful degraded mode for a client whose only job is producing the vectors
// description matching depends on.
func NewAzure(endpoint, apiKey, deployment string) (*Client, error) {
	if endpoint == "" || apiKey == "" || deployment == "" {
		return nil, fmt.Errorf("embeddings: Azure endpoint, API key and deployment are all required")
	}
	return &Client{
		url: fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
			strings.TrimRight(endpoint, "/"), deployment, azureAPIVersion),
		authHeader: "api-key",
		authValue:  apiKey,
		describe:   fmt.Sprintf("Azure OpenAI, deployment %q", deployment),
		httpClient: defaultHTTPClient(),
	}, nil
}

// NewOpenAI builds a client for the OpenAI API — api.openai.com, billed to an
// OpenAI account rather than the Azure subscription.
//
// Intended for development and for measuring the engine before an Azure
// deployment exists. The vectors are not interchangeable with Azure's in any
// stored sense, but nothing here stores them: every similarity is computed
// between two vectors from the same request, so switching provider between
// runs is safe.
func NewOpenAI(apiKey, model string) (*Client, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("embeddings: OpenAI API key and model are both required")
	}
	return &Client{
		url:        openAIEndpoint,
		authHeader: "Authorization",
		authValue:  "Bearer " + apiKey,
		model:      model,
		describe:   fmt.Sprintf("OpenAI API, model %q", model),
		httpClient: defaultHTTPClient(),
	}, nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

// Describe names the provider and model in use, for the startup log. Never
// includes the key.
func (c *Client) Describe() string { return c.describe }

type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed returns one vector per text in texts, in the same order, via a single
// batched call — the caller decides batch size, this client just makes one
// request per call.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embeddingRequest{Input: texts, Model: c.model})
	if err != nil {
		return nil, fmt.Errorf("marshalling embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(c.authHeader, c.authValue)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", c.describe, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading embedding response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %d: %s", c.describe, resp.StatusCode, string(respBody))
	}

	var parsed embeddingResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parsing embedding response: %w", err)
	}
	// Placed by the response's own index rather than arrival order: both APIs
	// document that vectors may come back out of order.
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			continue
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
