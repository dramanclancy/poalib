package bc

import "testing"

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
