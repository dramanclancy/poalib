// Package docintel is a generic Azure Document Intelligence client: submit a
// document, poll until analysis completes, get back the raw field/value
// shapes. It has no knowledge of POA (or any other) document layout — that
// parsing lives in adapter.go.
package docintel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const docIntelAPIVersion = "2024-11-30"

// ---- Exported result shapes (consumers need to reference these types) ----

type AnalyzeResult struct {
	ModelID   string     `json:"modelId"`
	Documents []Document `json:"documents"`
}

type Document struct {
	DocType    string           `json:"docType"`
	Confidence float64          `json:"confidence"`
	Fields     map[string]Field `json:"fields"`
}

type Field struct {
	Type        string           `json:"type"`
	Content     string           `json:"content"`
	Confidence  float64          `json:"confidence"`
	ValueArray  []Field          `json:"valueArray"`  // table rows
	ValueObject map[string]Field `json:"valueObject"` // row cells by column name
}

// ---- Internal wire shapes ----

type analyzeOperation struct {
	Status        string         `json:"status"` // notStarted | running | succeeded | failed
	AnalyzeResult *AnalyzeResult `json:"analyzeResult"`
	Error         *docIntelError `json:"error"`
}

type docIntelError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ---- Client ----

type DocIntelClient struct {
	endpoint string // e.g. https://<resource>.cognitiveservices.azure.com
	key      string
	http     *http.Client
}

func NewDocIntelClient(endpoint, key string) (*DocIntelClient, error) {
	if endpoint == "" || key == "" {
		return nil, fmt.Errorf("docintel: endpoint and key are required")
	}
	return &DocIntelClient{
		endpoint: endpoint,
		key:      key,
		http:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// AnalyzeDocumentFromBytes analyzes raw document bytes (e.g. a PDF downloaded
// from SharePoint via DownloadContentByPath).
//
// ctx bounds the whole analysis, polling included: a caller that has gone
// away stops it rather than leaving it running for up to two minutes.
func (c *DocIntelClient) AnalyzeDocumentFromBytes(ctx context.Context, modelID string, data []byte) (*AnalyzeResult, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("docintel: empty document")
	}
	return c.analyze(ctx, modelID, map[string]string{
		"base64Source": base64.StdEncoding.EncodeToString(data),
	})
}

// analyze submits the job, then polls the Operation-Location until completion.
func (c *DocIntelClient) analyze(ctx context.Context, modelID string, source map[string]string) (*AnalyzeResult, error) {
	submitURL := fmt.Sprintf(
		"%s/documentintelligence/documentModels/%s:analyze?api-version=%s",
		c.endpoint, modelID, docIntelAPIVersion,
	)

	body, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("docintel: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("docintel: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ocp-Apim-Subscription-Key", c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docintel: submit analyze: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var e struct {
			Error docIntelError `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return nil, fmt.Errorf("docintel: submit failed (%d): %s %s",
			resp.StatusCode, e.Error.Code, e.Error.Message)
	}

	opURL := resp.Header.Get("Operation-Location")
	if opURL == "" {
		return nil, fmt.Errorf("docintel: no Operation-Location header in response")
	}

	return c.pollOperation(ctx, opURL)
}

func (c *DocIntelClient) pollOperation(ctx context.Context, opURL string) (*AnalyzeResult, error) {
	const (
		pollInterval = 2 * time.Second
		maxAttempts  = 60 // ~2 minutes
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("docintel: analysis abandoned: %w", ctx.Err())
		case <-time.After(pollInterval):
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opURL, nil)
		if err != nil {
			return nil, fmt.Errorf("docintel: build poll request: %w", err)
		}
		req.Header.Set("Ocp-Apim-Subscription-Key", c.key)

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("docintel: poll: %w", err)
		}

		// Throttling and server errors are worth another poll; any other
		// failure status will not fix itself, and decoding its body as an
		// operation would only read as "still running" until the attempts ran
		// out.
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			resp.Body.Close()
			return nil, fmt.Errorf("docintel: poll returned %d", resp.StatusCode)
		}

		var op analyzeOperation
		decodeErr := json.NewDecoder(resp.Body).Decode(&op)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("docintel: decode poll response: %w", decodeErr)
		}

		switch op.Status {
		case "succeeded":
			if op.AnalyzeResult == nil {
				return nil, fmt.Errorf("docintel: succeeded but analyzeResult is nil")
			}
			return op.AnalyzeResult, nil
		case "failed":
			if op.Error != nil {
				return nil, fmt.Errorf("docintel: analysis failed: %s %s", op.Error.Code, op.Error.Message)
			}
			return nil, fmt.Errorf("docintel: analysis failed with no error detail")
		}
		// notStarted / running → keep polling
	}
	return nil, fmt.Errorf("docintel: analysis timed out after %d attempts", maxAttempts)
}
