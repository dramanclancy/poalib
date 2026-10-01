package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/dramanclancy/poalib/domain"
)

// RunRecord is what a reconciliation can say about itself: which engine and
// model produced it, what optional data was actually available, and under
// which thresholds.
//
// It exists because of the batch of 2026-09-04. Thirty lines produced no code
// match and ran no seat check, and nothing in the output could say whether
// that was a master-data gap or a lookup that never executed. A result that
// cannot report the conditions it ran under cannot be trusted or compared
// with another run.
type RunRecord struct {
	PF        string    `json:"pf"`
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`

	EngineVersion  string `json:"engineVersion"`
	EmbeddingModel string `json:"embeddingModel"`

	// BC side.
	// OrderNumberUsed is set when the acknowledgement printed an order number
	// Business Central does not hold, and a shorter form of it was matched
	// instead. Empty when the printed number matched directly.
	OrderNumberUsed string `json:"orderNumberUsed,omitempty"`
	BCLinesFetched  int    `json:"bcLinesFetched"`
	BCProductsBuilt int    `json:"bcProductsBuilt"`

	ItemNamesRequested int    `json:"itemNamesRequested"`
	ItemNamesReturned  int    `json:"itemNamesReturned"`
	ItemNamesError     string `json:"itemNamesError,omitempty"`

	EnrichmentState string `json:"enrichmentState"`
	EnrichmentAsked bool   `json:"enrichmentAsked"`
	ItemsRequested  int    `json:"itemsRequested"`
	ItemsReturned   int    `json:"itemsReturned"`
	ItemsWithSeats  int    `json:"itemsWithSeats"`
	ItemsWithCodes  int    `json:"itemsWithCodes"`
	EnrichmentError string `json:"enrichmentError,omitempty"`

	// POA side.
	POALinesExtracted int `json:"poaLinesExtracted"`
	POAPagesMerged    int `json:"poaPagesMerged"`

	// Outcome.
	LinesMatched int `json:"linesMatched"`
	LinesFlagged int `json:"linesFlagged"`

	Config domain.Config `json:"config"`

	// Fixture capture.
	FixturesEnabled bool   `json:"fixturesEnabled"`
	FixturePath     string `json:"fixturePath,omitempty"`
	FixtureError    string `json:"fixtureError,omitempty"`
}

// Degraded reports whether the comparison ran with less than the full set of
// inputs. A degraded run may still be correct, but its silences mean less: a
// seat check that did not run is not evidence that seat counts agree.
func (r RunRecord) Degraded() bool { return len(r.Degradations()) > 0 }

// Degradations lists, in plain words, everything that was missing or failed.
// The Summary sheet prints these so a reader never has to infer it from a
// column of blanks.
func (r RunRecord) Degradations() []string {
	var out []string
	switch {
	case !r.EnrichmentAsked:
		out = append(out, "Item enrichment was not requested: no supplier codes and no seat counts were available, so those checks could not run.")
	case r.EnrichmentError != "":
		out = append(out, fmt.Sprintf("Item enrichment failed (%s): codes and seat counts are incomplete or missing.", r.EnrichmentError))
	case r.ItemsReturned == 0 && r.ItemsRequested > 0:
		out = append(out, fmt.Sprintf("Item enrichment returned no records for %d requested items: codes and seat counts were unavailable.", r.ItemsRequested))
	case r.ItemsReturned < r.ItemsRequested:
		out = append(out, fmt.Sprintf("Item enrichment returned %d of %d requested items: some lines have no codes or seat count.", r.ItemsReturned, r.ItemsRequested))
	}
	if r.EnrichmentAsked && r.ItemsReturned > 0 {
		if r.ItemsWithSeats == 0 {
			out = append(out, "No returned item carried a seat count, so seat checks fell back to the seat count in BC's own line description wherever one was stated.")
		}
		if r.ItemsWithCodes == 0 {
			out = append(out, "No returned item carried a Vendor_Item_No or Model_No, so exact-code identity was unavailable on every line.")
		}
	}
	// Only a failed lookup counts. An item number BC holds no record for is
	// reported on the Summary sheet but is a data fact, not a degraded run.
	if r.ItemNamesError != "" {
		out = append(out, fmt.Sprintf("Item names could not be fetched for every line (%s): those lines were compared on the purchase order line's own text instead of the item name.", r.ItemNamesError))
	}
	if r.EmbeddingModel == "" {
		out = append(out, "No embedding provider was recorded for this run; description similarity may not be comparable with other runs.")
	}
	return out
}

// Summary is a one-line health verdict for the top of the report.
func (r RunRecord) Summary() string {
	if !r.Degraded() {
		return "Full data: item enrichment and embeddings were both available."
	}
	return fmt.Sprintf("DEGRADED — %s", strings.Join(r.Degradations(), " "))
}
