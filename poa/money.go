package poa

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/dramanclancy/poalib/businesscentral"
)

// ---------------------------------------------------------------------------
// Money parsing
// ---------------------------------------------------------------------------

// ErrMoneyAbsent means the field was blank. Callers store nil.
var ErrMoneyAbsent = errors.New("money field absent")

var (
	moneyStripRe = regexp.MustCompile(`[£€$\p{Zs}\x{00A0}]|GBP|EUR|USD`)
	moneyNumRe   = regexp.MustCompile(`^-?[0-9][0-9.,]*$`)
)

// ParseMoney converts a document money string into a value.
//
// decimalComma selects the supplier's convention: false for 1,377.00
// (Ashwood, Baker), true for 1.377,00 (Egoitaliano, Vispring). Without it
// "1.377" is ambiguous between one thousand and one-point-something, and
// guessing wrong is a silent 1000x error rather than a visible failure.
//
// Returns ErrMoneyAbsent for blank input, and a descriptive error when the
// field was PRESENT but unparseable — that second case must reach the write
// gate, not just a log line.
func ParseMoney(raw string, decimalComma bool) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, ErrMoneyAbsent
	}

	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, strings.Trim(s, "()")
	}
	for _, suf := range []string{"CR", "DR"} {
		if strings.HasSuffix(strings.ToUpper(s), suf) {
			neg = neg != (suf == "CR")
			s = strings.TrimSpace(s[:len(s)-2])
		}
	}
	s = strings.ReplaceAll(s, "−", "-") // unicode minus
	s = strings.TrimSpace(moneyStripRe.ReplaceAllString(s, ""))
	if s == "" {
		return 0, fmt.Errorf("no digits in %q", raw)
	}
	if strings.HasPrefix(s, "-") {
		neg, s = !neg, s[1:]
	}
	if !moneyNumRe.MatchString(s) {
		return 0, fmt.Errorf("not a number: %q", raw)
	}

	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0:
		// Both present — the rightmost is the decimal separator.
		if comma > dot {
			s = strings.Replace(strings.ReplaceAll(s, ".", ""), ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case comma >= 0:
		if decimalComma {
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case dot >= 0:
		if decimalComma && len(s)-dot-1 == 3 {
			s = strings.ReplaceAll(s, ".", "") // 1.377 -> 1377
		}
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing %q: %w", raw, err)
	}
	if neg {
		v = -v
	}
	return v, nil
}

// MoneyPtr parses an OPTIONAL money field for the extractor.
// nil means "not on this document". present reports whether content existed,
// so a parse failure can be counted into ExtractionErrors rather than being
// silently indistinguishable from an absent field.
func MoneyPtr(raw string, decimalComma bool) (v *float64, present bool, err error) {
	f, err := ParseMoney(raw, decimalComma)
	switch {
	case errors.Is(err, ErrMoneyAbsent):
		return nil, false, nil
	case err != nil:
		return nil, true, err
	}
	return &f, true, nil
}

// extractMoney converts DocIntel content like "1,377.00", "£1,377.00",
// "GBP 1,377.00" to a float. The second return is false when the value is
// empty/absent OR present but unparseable — callers who care can distinguish
// via the raw content.
//
// This is deliberately simpler than ParseMoney: it's for the DocIntel
// extractor only, which never sees the decimal-comma / CR-DR / parenthesized
// conventions ParseMoney exists to handle.
func extractMoney(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "GBP")
	s = strings.TrimPrefix(s, "EUR")
	s = strings.TrimSuffix(s, "%")
	s = strings.Trim(s, "£€$ ")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// mustMoney parses a money field and logs loudly when content was present
// but unparseable — the failure mode that silently produced zeros before
// (e.g. ParseFloat choking on the thousands comma in "1,377.00").
func mustMoney(context, fieldName, content string) float64 {
	v, ok := extractMoney(content)
	if !ok && strings.TrimSpace(content) != "" {
		log.Printf("extract: %s: unparseable %s %q — treating as 0", context, fieldName, content)
	}
	return v
}

func mustMoneyPtr(pf, fieldName, content string) *float64 {
	if strings.TrimSpace(content) == "" {
		return nil // absent — expected for Ashwood's ex-VAT total
	}
	v, ok := extractMoney(content)
	if !ok {
		log.Printf("extract: %s: unparseable %s %q — treating as absent", pf, fieldName, content)
		return nil
	}
	return &v // fresh local each call, so no aliasing between documents
}

// ---------------------------------------------------------------------------
// Description helpers
// ---------------------------------------------------------------------------

// CommentDescs pulls the description text out of a group's comment lines.
func CommentDescs(lines []businesscentral.PurchaseOrderLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Description)
	}
	return out
}
