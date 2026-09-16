package docintel

import "testing"

func TestParseMoney(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		// UK convention, as every supplier printed before Ego Italiano.
		{"1,377.00", 1377, true},
		{"£1,377.00", 1377, true},
		{"GBP 1,377.00", 1377, true},
		{"1,234,567.89", 1234567.89, true},
		{"1,377", 1377, true},
		{"608", 608, true},
		{"608.50", 608.5, true},

		// Continental convention. PF130853 (Ego Italiano 419790) printed these
		// and the old parser read them one hundred times too large.
		{"672,00", 672, true},
		{"15,00", 15, true},
		{"15,00%", 15, true},
		{"571,20", 571.2, true},
		{"EUR 571,20", 571.2, true},
		{"€1.377,00", 1377, true},
		{"1.234.567,89", 1234567.89, true},
		{"1.234.567", 1234567, true},
		{"0,84", 0.84, true},
		{"1,5", 1.5, true},

		{"", 0, false},
		{"  ", 0, false},
		{"n/a", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMoney(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("parseMoney(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
