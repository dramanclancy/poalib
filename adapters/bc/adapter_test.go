package bc

import (
	"testing"

	"github.com/dramanclancy/poalib/domain"
)

// TestGroupLines pins how purchase order lines become products: sorted by
// sequence, a product per value-bearing line (Account included — surcharges
// went unmatched when only Item counted), comment lines attached to the line
// before them, and a comment with no owner dropped.
func TestGroupLines(t *testing.T) {
	lines := []PurchaseOrderLine{
		{Sequence: 40, LineType: "Account", LineObjectNumber: "718000", Description: "Vendor Surcharges", Quantity: 1, NetAmount: 25},
		{Sequence: 30, LineType: "Comment", Description: "Right Hand Facing"},
		{Sequence: 5, LineType: "Comment", Description: "Delivery to store 12"},
		{Sequence: 10, LineType: "Item", LineObjectNumber: "IT0188674", Description: "Hansson 2.5 Seater End", Quantity: 1, NetAmount: 541},
		{Sequence: 20, LineType: "Comment", Description: "LEFT/RIGHT OPTION"},
	}

	got := groupLines(lines)

	if len(got) != 2 {
		t.Fatalf("built %d products, want 2 (the item and the surcharge)", len(got))
	}
	item, surcharge := got[0], got[1]
	if item.ItemNo != "IT0188674" || item.Net != 541 {
		t.Errorf("first product = %s / %g, want IT0188674 / 541", item.ItemNo, item.Net)
	}
	if len(item.Comments) != 2 || item.Comments[0] != "LEFT/RIGHT OPTION" || item.Comments[1] != "Right Hand Facing" {
		t.Errorf("item comments = %q, want the two that follow it, in sequence order", item.Comments)
	}
	if surcharge.LineType != "Account" || surcharge.Net != 25 || len(surcharge.Comments) != 0 {
		t.Errorf("surcharge = %+v, want an Account product with no comments", surcharge)
	}
}

// TestMergedDescription pins the text the comparison actually reads.
func TestMergedDescription(t *testing.T) {
	tests := []struct {
		name string
		p    domain.BCProduct
		want string
	}{
		{
			name: "display names lead, then item detail, then comments",
			p: domain.BCProduct{
				DisplayName: "Hansson 2.5 Seater End", DisplayName2: "Grey",
				LineDescription: "HANSSON END (ignored: a display name exists)",
				Detail:          &domain.ItemDetail{Description3: "Fabric Grade A", RangeCode: "HANSSON"},
				Comments:        []string{"Right Hand Facing"},
			},
			want: "Hansson 2.5 Seater End Grey Fabric Grade A HANSSON Right Hand Facing",
		},
		{
			name: "no display name falls back to the line's own text",
			p:    domain.BCProduct{LineDescription: "Vendor Surcharges"},
			want: "Vendor Surcharges",
		},
		{
			// A purchase order often repeats the item name as its first
			// comment; counting it twice would double its weight.
			name: "a comment restating the name is dropped, whatever its case or punctuation",
			p: domain.BCProduct{
				DisplayName: "Metro Footstool",
				Comments:    []string{"METRO  FOOTSTOOL.", "WM"},
			},
			// Joined with spaces: "" once glued words into "FootstoolWM".
			want: "Metro Footstool WM",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergedDescription(tc.p); got != tc.want {
				t.Errorf("mergedDescription = %q\n                 want %q", got, tc.want)
			}
		})
	}
}

// TestToItemDetail_ZeroSeatsMeansNotRecorded: CaseysItems sends 0 both for
// "no seats" and "never filled in", so 0 must become nil, never a seat count
// every sofa would then disagree with.
func TestToItemDetail_ZeroSeatsMeansNotRecorded(t *testing.T) {
	if d := toItemDetail(CaseysItem{No: "IT1", NoOfSeats: 0}); d.Seats != nil {
		t.Errorf("Seats = %v for No_of_Seats 0, want nil", *d.Seats)
	}
	if d := toItemDetail(CaseysItem{No: "IT1", NoOfSeats: 3, ModelNo: "  NOV4S "}); d.Seats == nil || *d.Seats != 3 || d.ModelNo != "NOV4S" {
		t.Errorf("detail = %+v, want 3 seats and a trimmed Model_No", d)
	}
}

// TestBaseOrderNumber covers the supplier annotation an acknowledgement can
// carry on the purchase order number. The fallback only ever runs after
// Business Central has said the printed number does not exist, so this needs
// only to identify what to try next — and to decline when there is nothing
// sensible to try.
func TestBaseOrderNumber(t *testing.T) {
	tests := []struct{ in, want string }{
		{"PF126282/EXC", "PF126282"},
		{"PF126282/EXC/2", "PF126282"}, // first separator wins
		{"PF130368", ""},               // nothing to strip
		{"PF126282/", ""},              // a trailing slash leaves no annotation
		{"/EXC", ""},                   // no order number before the separator
		{"", ""},
	}
	for _, tc := range tests {
		if got := baseOrderNumber(tc.in); got != tc.want {
			t.Errorf("baseOrderNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
