// POA orientation handling.
//
// Orientation (LHF / RHF) is a *hard constraint*, not a fuzzy signal. It must
// never contribute to the similarity score used to pair a POA line with a BC
// line group — pairing on identity and validating on orientation are separate
// jobs. Orientation is resolved independently on each side, compared after
// pairing, and gates the BC write.
package poa

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Orientation
// ---------------------------------------------------------------------------

type Orientation int

const (
	// OrientUnknown — no orientation token present. Correct for surcharge,
	// carriage and other non-handed lines.
	OrientUnknown Orientation = iota
	OrientLeft
	OrientRight
	// OrientEither — the text explicitly covers both hands ("RHF/LHF",
	// "LEFT/RIGHT OPTION"). This is a universal SKU description or an option
	// *header*, never a specification. Must be skipped, not treated as a match.
	OrientEither
	// OrientAmbiguous — two conflicting definite readings in the same text or
	// line group. Always a human review.
	OrientAmbiguous
)

func (o Orientation) String() string {
	switch o {
	case OrientLeft:
		return "LHF"
	case OrientRight:
		return "RHF"
	case OrientEither:
		return "LHF/RHF"
	case OrientAmbiguous:
		return "AMBIGUOUS"
	default:
		return "UNKNOWN"
	}
}

func (o Orientation) definite() bool { return o == OrientLeft || o == OrientRight }

// ---------------------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------------------

// Longest alternatives first: Go's regexp is leftmost-first, so "LHF" must be
// tried before "LH", or "LHF" would match as "LH" and fail the trailing \b.
const (
	leftTok  = `(?:L\.?H\.?F\.?|L/H|LH|LEFT[-\s]?HAND(?:ED)?(?:[-\s]+FACING)?|LEFT)`
	rightTok = `(?:R\.?H\.?F\.?|R/H|RH|RIGHT[-\s]?HAND(?:ED)?(?:[-\s]+FACING)?|RIGHT)`
	sep      = `\s*(?:/|\\|\||&|,|-|\bOR\b|\bAND\b)\s*`
)

var (
	// eitherRe must be tested FIRST. "LEFT/RIGHT OPTION" and "RHF/LHF" both
	// contain a left token and a right token; without this check they classify
	// as Ambiguous and every Ashwood line goes to manual review.
	eitherRe = regexp.MustCompile(`(?i)\b` + leftTok + sep + rightTok + `\b|(?i)\b` + rightTok + sep + leftTok + `\b`)
	leftRe   = regexp.MustCompile(`(?i)\b` + leftTok + `\b`)
	rightRe  = regexp.MustCompile(`(?i)\b` + rightTok + `\b`)
)

// Classify reads the orientation stated by a single piece of text.
func Classify(s string) Orientation {
	if strings.TrimSpace(s) == "" {
		return OrientUnknown
	}
	// Strip the explicit both-hands phrases before looking for a definite
	// reading, so "RHF/LHF — Right Hand Facing" resolves to Right rather than
	// collapsing to Either.
	stripped := eitherRe.ReplaceAllString(s, " ")
	l, r := leftRe.MatchString(stripped), rightRe.MatchString(stripped)
	switch {
	case l && r:
		return OrientAmbiguous
	case l:
		return OrientLeft
	case r:
		return OrientRight
	}
	if eitherRe.MatchString(s) {
		return OrientEither
	}
	return OrientUnknown
}

// ---------------------------------------------------------------------------
// BC side
// ---------------------------------------------------------------------------

// ResolveBC walks an Item description followed by its Comment descriptions in
// sequence order and returns the orientation the purchase order actually
// specifies, plus the text it came from (for the review sheet).
//
// BC states orientation with a label/value comment pair:
//
//	"LEFT/RIGHT OPTION"    <- header, both hands, carries no specification
//	"Right Hand Facing"    <- the actual value
//
// so the walk skips OrientEither readings and takes definite ones only.
func ResolveBC(itemDesc string, commentDescs []string) (Orientation, string) {
	found, src := OrientUnknown, ""
	for _, text := range append([]string{itemDesc}, commentDescs...) {
		o := Classify(text)
		if o == OrientAmbiguous {
			return OrientAmbiguous, strings.TrimSpace(text)
		}
		if !o.definite() {
			continue
		}
		if found == OrientUnknown {
			found, src = o, strings.TrimSpace(text)
			continue
		}
		if found != o {
			return OrientAmbiguous, src + " | " + strings.TrimSpace(text)
		}
	}
	return found, src
}

// ---------------------------------------------------------------------------
// Comparison
// ---------------------------------------------------------------------------

type OrientationCheck struct {
	POA    Orientation
	BC     Orientation
	POASrc string
	BCSrc  string
	OK     bool
	Reason string
}

// Check compares the two resolved orientations. It fails closed: anything
// short of two definite, equal readings blocks the write, except a line where
// neither side is handed at all (surcharges, carriage, discounts).
func Check(poa, bc Orientation, poaSrc, bcSrc string) OrientationCheck {
	c := OrientationCheck{POA: poa, BC: bc, POASrc: poaSrc, BCSrc: bcSrc}
	switch {
	case poa == OrientUnknown && bc == OrientUnknown:
		c.OK, c.Reason = true, "not a handed line"
	case poa.definite() && bc.definite() && poa == bc:
		c.OK, c.Reason = true, fmt.Sprintf("both %s", poa)
	case poa.definite() && bc.definite():
		c.Reason = fmt.Sprintf("PO specifies %s, supplier confirmed %s", bc, poa)
	case bc.definite() && !poa.definite():
		c.Reason = fmt.Sprintf("PO specifies %s, acknowledgement does not state an orientation", bc)
	case poa.definite() && !bc.definite():
		c.Reason = fmt.Sprintf("supplier confirmed %s, PO does not specify an orientation (%s)", poa, bc)
	default:
		c.Reason = fmt.Sprintf("orientation unresolved (POA %s, BC %s)", poa, bc)
	}
	return c
}

// ---------------------------------------------------------------------------
// Write gate
// ---------------------------------------------------------------------------

// GateInput is everything the write decision depends on.
type GateInput struct {
	POALineCount     int // lines extracted from the acknowledgement
	BCGroupCount     int // Item groups + Account lines on the purchase order
	MatchedCount     int
	VerifiedCount    int // matched lines passing price, qty AND orientation
	TotalExVATOK     bool
	ExtractionErrors int
}

// EvaluateWriteGate returns whether the acknowledgement may be written back to
// Business Central, and every reason it may not.
//
// The MatchedCount > 0 term is the one that matters: the previous
// implementation derived writeOK by looping over matched lines and clearing a
// flag on any failure, so a run with ZERO matches never entered the loop and
// the flag survived at its initial true. Vacuous truth. Count-based
// preconditions cannot fail that way.
func EvaluateWriteGate(in GateInput) (bool, []string) {
	var reasons []string
	if in.ExtractionErrors > 0 {
		reasons = append(reasons, fmt.Sprintf("%d extraction error(s)", in.ExtractionErrors))
	}
	if in.POALineCount == 0 {
		reasons = append(reasons, "no lines extracted from the acknowledgement")
	}
	if in.MatchedCount == 0 && in.POALineCount > 0 {
		reasons = append(reasons, "no acknowledgement line could be paired with a purchase order line")
	}
	if in.MatchedCount != in.POALineCount {
		reasons = append(reasons, fmt.Sprintf("%d of %d acknowledgement line(s) unmatched",
			in.POALineCount-in.MatchedCount, in.POALineCount))
	}
	if in.MatchedCount != in.BCGroupCount {
		reasons = append(reasons, fmt.Sprintf("%d of %d purchase order line(s) unacknowledged",
			in.BCGroupCount-in.MatchedCount, in.BCGroupCount))
	}
	if in.VerifiedCount != in.MatchedCount {
		reasons = append(reasons, fmt.Sprintf("%d matched line(s) failed verification",
			in.MatchedCount-in.VerifiedCount))
	}
	if !in.TotalExVATOK {
		reasons = append(reasons, "document total does not reconcile")
	}
	return len(reasons) == 0, reasons
}

// StripOrientation removes LHF/RHF tokens from a description.
//
// Orientation is verified on its own terms by Check(), so leaving the tokens
// in the text only adds noise to the similarity score: "RHF/LHF" will never
// look like "RIGHT" to any string metric, whether or not the line is correct.
func StripOrientation(s string) string {
	s = eitherRe.ReplaceAllString(s, " ")
	s = leftRe.ReplaceAllString(s, " ")
	s = rightRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}