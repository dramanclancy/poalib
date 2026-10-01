package domain

import "testing"

// TestDiscrepancies_OneEntryPerReason: a line failing several checks yields
// several entries, one per reason, rather than one sentence covering all of
// them — the Review sheet prints one row per entry.
func TestDiscrepancies_OneEntryPerReason(t *testing.T) {
	p := checkProduct(pairing{
		poa: POAProduct{Code: "NFX2S (A)", Qty: 2},
		bc:  bcItem("IT0188674", 3, 500, &ItemDetail{ItemNo: "IT0188674"}),
		ev:  MatchEvidence{Similarity: Ptr(0.33), MarginNA: true},
	}, EnrichmentApplied, cfg())

	got := Kinds(p.Discrepancies())
	want := []DiscrepancyKind{DiscNetValue, DiscQuantity, DiscMissingBCCode}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDiscrepancies_MessagesComeFromTheChecks: the prose a reviewer reads is
// the check's own message, not a second wording maintained alongside it.
func TestDiscrepancies_MessagesComeFromTheChecks(t *testing.T) {
	p := checkProduct(pairing{
		poa: POAProduct{Code: "Widget", Qty: 1, UnitPrice: 541},
		bc:  BCProduct{ItemNo: "ITX", Qty: 1, Net: 565},
		ev:  MatchEvidence{Similarity: Ptr(0.9), MarginNA: true},
	}, EnrichmentApplied, cfg())

	ds := p.Discrepancies()
	if len(ds) != 1 || ds[0].Kind != DiscNetValue {
		t.Fatalf("kinds = %v, want just [%q]", Kinds(ds), DiscNetValue)
	}
	if ds[0].Message != p.Price.Message {
		t.Errorf("discrepancy %q does not match the price check's own %q", ds[0].Message, p.Price.Message)
	}
}
