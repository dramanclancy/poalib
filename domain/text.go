// Text handling: orientation classification and normalisation.
//
// Orientation (LHF / RHF) is resolved here and compared in checks.go
// (orientationCheck). It is a *hard constraint*, not a fuzzy signal. It must
// never contribute to the similarity score used to pair a POA product with a
// BC product — pairing on identity and validating on orientation are separate
// jobs. Orientation is resolved independently on each side, compared after
// pairing, and gates the BC write.

package domain

import (
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

// StripOrientation removes LHF/RHF tokens from a description.
//
// Orientation is verified on its own terms by orientationCheck, so leaving the tokens
// in the text only adds noise to the similarity score: "RHF/LHF" will never
// look like "RIGHT" to any string metric, whether or not the line is correct.
func StripOrientation(s string) string {
	s = eitherRe.ReplaceAllString(s, " ")
	s = leftRe.ReplaceAllString(s, " ")
	s = rightRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

var nonAlnum = regexp.MustCompile(`[^a-z0-9 ]+`)

// Normalize lowercases, strips punctuation and known mojibake, and collapses
// whitespace, so the same text compares equal regardless of formatting.
// Exported because an adapter merging BC text has to ask the same question
// about duplicates that the comparison does.
func Normalize(s string) string {
	s = strings.ReplaceAll(s, "Â", " ") // mojibake 'Â'
	s = strings.ReplaceAll(s, " ", " ") // NBSP
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// EmbeddingText prepares one description for the embedder.
//
// Orientation tokens are removed: handedness is verified on its own terms by
// orientationCheck, and "RHF/LHF" carries no description identity of its own.
//
// Product codes are deliberately LEFT IN, though the same argument appears to
// apply since codeMatch compares them exactly. Removing them was measured on
// a three-candidate probe: it raised true-pair similarity by ~0.07 but
// flipped the winning candidate to the wrong BC line, because a shorter
// string gives every remaining word — including an incidental fabric name — a
// larger share of the vector. The probe used invented text and settles
// nothing; it is on the list to measure over the fixture corpus, where it
// costs one test run instead of a batch against live services.
func EmbeddingText(s string) string {
	return StripOrientation(s)
}
