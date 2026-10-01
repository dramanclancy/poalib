// Package app is the pipeline: it decides the order of operations and owns
// every piece of I/O, so that domain.Reconcile stays a pure function of two
// built orders.
//
// The interfaces below are the only ones in the codebase. Each exists because
// it has a real second implementation or a real need to be faked in a test —
// there is no interface per struct, no container to resolve them, and no
// registry. main.go constructs the concrete adapters and passes them in.
package app

import (
	"context"

	"github.com/dramanclancy/poalib/domain"
)

// FileStore fetches the acknowledgement document by path.
type FileStore interface {
	Get(ctx context.Context, path string) ([]byte, error)
}

// DocScanner turns a supplier PDF into one POAOrder per purchase order the
// file acknowledges — a single document may carry several.
type DocScanner interface {
	Scan(ctx context.Context, pdf []byte, modelID string) ([]domain.POAOrder, error)
}

// OrderSource builds the BC side of the comparison for one PF.
//
// Enrichment is part of building the order rather than a separate port: the
// adapter is the only thing that knows whether the lookup ran, and
// domain.BCOrder.Enrichment has to carry that answer regardless. Splitting it
// would mean two calls that must agree about one fact.
type OrderSource interface {
	Order(ctx context.Context, pf string) (domain.BCOrder, OrderStats, error)
}

// OrderStats is what the fetch can report about itself, for the run record.
// It is diagnostics, never input to the comparison.
type OrderStats struct {
	// OrderNumberUsed is the number Business Central actually matched, set
	// only when it differs from the one printed on the acknowledgement.
	OrderNumberUsed string `json:"orderNumberUsed,omitempty"`

	LinesFetched    int    `json:"linesFetched"`
	EnrichmentAsked bool   `json:"enrichmentAsked"`
	ItemsRequested  int    `json:"itemsRequested"`
	ItemsReturned   int    `json:"itemsReturned"`
	EnrichmentError string `json:"enrichmentError,omitempty"`
	ItemsWithSeats  int    `json:"itemsWithSeats"`
	ItemsWithCodes  int    `json:"itemsWithCodes"`
}

// Embedder computes a vector per text. Required, unlike enrichment: there is
// no fallback description-identity signal, and an unembedded line scores zero
// against everything — which reads as "definitely not this product" rather
// than "we could not tell".
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Describe names the provider and model for the run record, so a result
	// is never ambiguous about which numbers produced its scores.
	Describe() string
}
