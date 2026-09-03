package poa

import (
	"math"
	"testing"

	"github.com/dramanclancy/poalib/businesscentral"
)

func TestRarity(t *testing.T) {
	tests := []struct {
		name  string
		df, n int
		want  float64
	}{
		{"absent from this side entirely", 0, 5, 1},
		{"unique in a corpus of five", 1, 5, 0.8},
		{"on every document", 4, 4, 0},
		{"on all but one", 3, 4, 0.25},
		// A single document is not evidence that its every token is rare —
		// there is nothing to tell apart — so weighting must stand down and
		// let the plain overlap decide.
		{"corpus of one carries no evidence", 1, 1, 1},
		{"empty corpus carries no evidence", 0, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rarity(tc.df, tc.n); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("rarity(%d, %d) = %.4f, want %.4f", tc.df, tc.n, got, tc.want)
			}
		})
	}
}

// TestBuildVocab_BoilerplateOnEitherSideIsDiscounted is the property that
// replaces the hand-maintained stoplists: a token repeated across every
// document on one side must be discounted even when it is distinctive on the
// other, because it cannot discriminate between the candidates being told
// apart.
func TestBuildVocab_BoilerplateOnEitherSideIsDiscounted(t *testing.T) {
	// "fabric" is on every BC group (comment-label boilerplate, the case the
	// Whitemeadow stoplist existed for) but on only one POA line.
	// "metro" is on one document per side — genuinely distinctive.
	poa := []string{
		"metro arm unit fabric",
		"combi stool",
		"scatter package",
	}
	bc := []string{
		"metro arm unit fabric grade b",
		"combi stool fabric grade b",
		"scatter package fabric grade b",
	}
	v := buildVocab(poa, bc)

	if w := v.weightOf("fabric"); w != 0 {
		t.Errorf("weightOf(fabric) = %.3f, want 0 — it is on every BC document, so it cannot tell them apart", w)
	}
	if w := v.weightOf("metro"); w <= 0.5 {
		t.Errorf("weightOf(metro) = %.3f, want > 0.5 — it appears once per side and is the discriminating token", w)
	}
	// A token nobody in the corpus printed is not boilerplate; it just was
	// not seen. Defaulting it to 0 would silently erase enrichment text.
	if w := v.weightOf("unseen"); w != 1 {
		t.Errorf("weightOf(unseen) = %.3f, want 1", w)
	}
}

// TestWeightedOverlap_BoilerplateDoesNotManufactureSimilarity is the failure
// mode that drove the 90% flag rate: two unrelated products sharing nothing
// but comment-label boilerplate scored as though they resembled each other.
func TestWeightedOverlap_BoilerplateDoesNotManufactureSimilarity(t *testing.T) {
	poa := []string{
		"arm unit fabric grade b",
		"combi stool fabric grade b",
	}
	bc := []string{
		"arm unit fabric grade b feet options",
		"combi stool fabric grade b feet options",
	}
	v := buildVocab(poa, bc)

	armPOA := cleanTokenSet(poa[0])
	stoolBC := cleanTokenSet(bc[1])

	// The unweighted metric is what this replaces: the only tokens the two
	// sides share are the boilerplate ones, and it credits every one of them.
	raw := overlapSimilaritySets(armPOA, stoolBC)
	if raw == 0 {
		t.Fatal("fixture invalid: the sides share no tokens at all, so there is no boilerplate-inflated score to improve on")
	}

	got, ok := weightedOverlap(v, armPOA, stoolBC)
	if !ok {
		t.Fatal("weightedOverlap reported no evidence; want a real score (both sides carry distinctive tokens)")
	}
	if got != 0 {
		t.Errorf("weightedOverlap(arm unit, combi stool) = %.3f, want 0 — they share only boilerplate (unweighted scored %.3f)", got, raw)
	}
}

// TestWeightedOverlap_NoEvidenceReportsNotOK guards the divide-by-nothing
// case: when every token on a side is boilerplate there is no evidence to
// form a ratio from, and reporting 0 would flag a line that may well match.
func TestWeightedOverlap_NoEvidenceReportsNotOK(t *testing.T) {
	descs := []string{"feet options", "feet options"}
	v := buildVocab(descs, descs)

	set := cleanTokenSet("feet options")
	if _, ok := weightedOverlap(v, set, set); ok {
		t.Error("weightedOverlap reported ok on a pair whose every token weighs 0; want ok=false so scorePair falls back to the raw score")
	}
}

// TestCleanTokenSet_DiscardsNothing pins the v6.3 change in kind: cleaning
// used to remove stopwords, and now only normalises. Anything that still
// disappears here is a bug, because weighting cannot discount a token that
// never reaches it.
func TestCleanTokenSet_DiscardsNothing(t *testing.T) {
	got := cleanTokenSet("WM Fabric Grade B")
	for _, want := range []string{"wm", "fabric", "grade", "b"} {
		if !got[want] {
			t.Errorf("cleanTokenSet dropped %q; v6.3 weights boilerplate rather than discarding it (got %v)", want, got)
		}
	}
}

func TestCleanTokenSet_ExpandsDomainAliasesAndStems(t *testing.T) {
	got := cleanTokenSet("2STR Sofas")
	if !got["2"] || !got["seater"] {
		t.Errorf(`cleanTokenSet("2STR ...") = %v, want "2" and "seater" (domain alias expansion)`, got)
	}
	if !got["sofa"] {
		t.Errorf(`cleanTokenSet("... Sofas") = %v, want "sofa" (plural stemming)`, got)
	}
}

// TestPrepareMatch_UsesEnrichedCombinedDesc guards the ordering requirement in
// MatchPOA: weights must be derived after CaseysItems enrichment rewrites
// CombinedDesc. Deriving them first would leave enriched text unseen by the
// corpus, and unseen text defaults to weight 1 — scoring range boilerplate as
// though it were the most distinctive thing on the line.
func TestPrepareMatch_UsesEnrichedCombinedDesc(t *testing.T) {
	groups := []BCLineGroup{
		{Item: businesscentral.PurchaseOrderLine{ID: "bc-0"}, CombinedDesc: "arm unit metro"},
		{Item: businesscentral.PurchaseOrderLine{ID: "bc-1"}, CombinedDesc: "combi stool metro"},
	}
	products := []OrderDetail{{Product: "arm unit"}, {Product: "combi stool"}}

	_, v := prepareMatch(products, groups)
	if w := v.weightOf("metro"); w != 0 {
		t.Errorf("weightOf(metro) = %.3f, want 0 — Range_Code is on every enriched group, so it cannot tell them apart", w)
	}
}
