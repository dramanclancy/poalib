package poa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Exact-code identity
// ---------------------------------------------------------------------------

var nonAlnumSquash = regexp.MustCompile(`[^A-Za-z0-9]+`)

// squash reduces a code (or a whole POA text) to comparable form: uppercase,
// alphanumerics only. "1341 CASH SKY21 RAL 9005" and "1341CASHSKY21RAL9005"
// both become "1341CASHSKY21RAL9005", so a code prints identically whether
// or not the POA spaced it out.
func squash(s string) string {
	return strings.ToUpper(nonAlnumSquash.ReplaceAllString(s, ""))
}

// commonPrefixLen is the length of the leading run a and b share.
func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// abbreviatesRange reports whether any token of poaText reads as an
// abbreviation of rangeSquashed.
//
// bcTokens is every token BC prints anywhere on this order. A POA token BC
// also prints as a word is prose, not an abbreviation — the two sides already
// agree on it, and whatever prefix it happens to share with a range name is
// coincidence. Without that guard a POA line reading "SOFAS" would abbreviate
// a Range_Code of "SOFTLINE" on the strength of "SOF", and invent identity
// evidence out of two unrelated words starting alike.
func abbreviatesRange(poaText, rangeSquashed string, bcTokens map[string]bool) bool {
	for _, raw := range strings.Fields(poaText) {
		t := squash(raw)
		if len(t) < minProductCodeLen || bcTokens[stem(strings.ToLower(t))] {
			continue
		}
		if commonPrefixLen(t, rangeSquashed) >= minRangePrefixLen {
			return true
		}
	}
	return false
}

// matchedRangeCode returns the one BC Range_Code on this order that a token of
// poaText abbreviates, or "" if none does — or if more than one does.
//
// Ashwood prints range-prefixed codes (HANCLR, HAN2HFR, HAN3SL) against a
// Range_Code of HANSSON, so the range is present on both sides but only BC
// spells it out. Token overlap cannot bridge "hanclr" and "hansson"; they are
// simply different tokens, and Phase 1b's decision to put Range_Code into
// BCLineGroup.CombinedDesc only pays off for suppliers who print the range in
// full (Whitemeadow's "Metro"). Returning the BC spelling lets the caller
// append it to the POA text, after which the range IS a shared token and the
// ordinary weighting decides what it is worth — including deciding it is
// worth nothing when every group on the order carries the same Range_Code,
// which is correct, since a range shared by all candidates cannot tell them
// apart.
//
// Two distinct ranges abbreviating to the same prefix returns "": inventing a
// shared token with both groups would manufacture exactly the confusion
// MarginThreshold exists to catch. Mirrors resolveCodeAmbiguity.
func matchedRangeCode(poaText string, groups []BCLineGroup, bcTokens map[string]bool) string {
	var found string
	for _, g := range groups {
		if g.Detail == nil || g.Detail.RangeCode == "" {
			continue
		}
		rc := squash(g.Detail.RangeCode)
		if len(rc) < minRangePrefixLen {
			continue
		}
		if !abbreviatesRange(poaText, rc, bcTokens) {
			continue
		}
		if found != "" && !strings.EqualFold(found, g.Detail.RangeCode) {
			return ""
		}
		// The original spelling, not the squashed one: BC's CombinedDesc
		// holds it unsquashed, so a multi-word range ("NATURAL COMFORT") has
		// to tokenize the same way on both sides to match at all.
		found = g.Detail.RangeCode
	}
	return found
}

// prepareMatch derives the POA texts and the token weights one reconciliation
// scores against. They are built together because they must agree: a range
// code appended to a POA line has to be in the corpus that weights it, or it
// would default to weight 1 and count as the most distinctive thing on the
// line.
func prepareMatch(products []OrderDetail, groups []BCLineGroup) (poaTexts []string, v Vocab) {
	bcTokens := map[string]bool{}
	bcDescs := make([]string, 0, len(groups))
	for _, g := range groups {
		bcDescs = append(bcDescs, g.CombinedDesc)
		for t := range cleanTokenSet(g.CombinedDesc) {
			bcTokens[t] = true
		}
	}

	poaTexts = make([]string, 0, len(products))
	for _, d := range products {
		text := combinedPOADesc(d)
		if rc := matchedRangeCode(text, groups, bcTokens); rc != "" {
			text += " " + rc
		}
		poaTexts = append(poaTexts, text)
	}
	return poaTexts, buildVocab(poaTexts, bcDescs)
}

// productCodeIn returns the first token of s that looks like a supplier
// product code, or "" if none does.
//
// The test is structural — four or more characters, starting with a letter,
// containing at least one digit (NFX2S, HAN2HFR, STGTS1, CA24, C191-TAN-P) —
// because the alternative is a dictionary of real words, and a
// hand-maintained word list is exactly what v6.3 removed from vocab.go.
//
// Leading letter is what separates a code from a measurement: dimensions and
// quantities lead with the number (150CM, 2 SEATER), codes lead with the
// range or family. It is deliberately conservative and misses codes of pure
// letters — Ashwood's HANCLR — which are instead reached by range-prefix
// narrowing, a signal with real BC data behind it rather than a guess about
// English.
func productCodeIn(s string) string {
	for _, raw := range strings.Fields(s) {
		t := squash(raw)
		if len(t) < minProductCodeLen || t[0] < 'A' || t[0] > 'Z' {
			continue
		}
		if strings.ContainsAny(t, "0123456789") {
			return t
		}
	}
	return ""
}

// hasCodeOnFile reports whether BC holds any code for this group's item that
// a POA product code could have been matched against. Both fields empty means
// the absence of a match is a gap in master data, not a disagreement.
func (g BCLineGroup) hasCodeOnFile() bool {
	return g.Detail != nil && (g.Detail.VendorItemNo != "" || g.Detail.ModelNo != "")
}

// codeHits reports whether code, squashed, is a real (non-trivial) match
// inside poaSquashed. Below minCodeLen a code is short enough to turn up in
// unrelated text by chance, so it is never eligible.
func codeHits(poaSquashed, code string) bool {
	sc := squash(code)
	return len(sc) >= minCodeLen && strings.Contains(poaSquashed, sc)
}

// codeMatch reports whether the POA text code-matches g's item, and via
// which field(s).
//
// Vendor_Item_No is vendor-scoped: it is THIS vendor's catalogue code for the
// item, and only meaningful when the item actually belongs to the PO's
// vendor (g.Detail.VendorNo == mc.VendorNo) — otherwise the stored code
// belongs to a different supplier and a textual coincidence would be a false
// positive dressed up as authoritative. Model_No carries no such constraint.
func codeMatch(poaSquashed string, g BCLineGroup, mc MatchContext) (matched bool, source string) {
	if g.Detail == nil {
		return false, ""
	}
	modelHit := codeHits(poaSquashed, g.Detail.ModelNo)
	vendorScoped := mc.VendorNo != "" && g.Detail.VendorNo == mc.VendorNo
	vendorHit := vendorScoped && codeHits(poaSquashed, g.Detail.VendorItemNo)
	switch {
	case modelHit && vendorHit:
		return true, "both"
	case modelHit:
		return true, "Model_No"
	case vendorHit:
		return true, "Vendor_Item_No"
	default:
		return false, ""
	}
}

// resolveCodeAmbiguity clears CodeMatch on any pairing whose code also
// matches another BC group for the same POA line — "ambiguity kills it" per
// phase1b-item-enrichment.md section 2. The tentative CodeMatchSource is
// left in place and CodeAmbiguousWith is filled in so Discrepancies() can
// name what it collided with. Must run before the code-match bonus is
// applied to Score and before fillDescMargins/the sort, since both depend on
// the final CodeMatch value through IdentityConfident/FullyVerified.
// variantSuffixPattern finds a product code followed by a parenthesised
// variant suffix — "STGTS1 (A)", "NFX2S (C)". The base must contain a letter,
// which keeps a printed quantity or price ("5000 (QJ)") from reading as one.
var variantSuffixPattern = regexp.MustCompile(`([A-Z0-9]*[A-Z][A-Z0-9]*)\s*\(([A-Z0-9]{1,3})\)`)

// splitVariantSuffix returns the product code and parenthesised variant
// suffix in s, or two empty strings if it carries none.
func splitVariantSuffix(s string) (base, suffix string) {
	m := variantSuffixPattern.FindStringSubmatch(strings.ToUpper(s))
	if m == nil || len(m[1]) < minProductCodeLen {
		return "", ""
	}
	return m[1], m[2]
}

// bcDistinguishesVariant reports whether either BC code for this group spells
// out the variant rather than just the family — a Vendor_Item_No of STGTS1C
// rather than STGTS1.
func bcDistinguishesVariant(g BCLineGroup, base, suffix string) bool {
	if g.Detail == nil {
		return false
	}
	want := base + suffix
	for _, code := range []string{g.Detail.ModelNo, g.Detail.VendorItemNo} {
		if code != "" && strings.Contains(squash(code), want) {
			return true
		}
	}
	return false
}

// resolveVariantAmbiguity clears CodeMatch where the code names a product
// family but not which variant of it the line is.
//
// Ashwood prints a parenthesised grade suffix, and the suffix is the
// discriminator: STGTS1 (A) and STGTS1 (C) resolve to different items
// (IT0233832, IT0233835). codeHits works by containment over the squashed POA
// text, so "STGTS1A" and "STGTS1C" both contain a BC code of "STGTS1" and both
// claim an exact match to it. CodeMatch settles identity outright in
// IdentityConfident — it bypasses description scoring entirely — so without
// this one variant is silently auto-approved against the other's item.
//
// resolveCodeAmbiguity does not cover this: it catches one POA line matching
// several BC groups. This is the mirror case, several POA lines matching one
// group, and it needs the POA text rather than the pairing to see it.
//
// Ambiguity is a property of the candidate set, not of the string. A lone
// STGTS1 line matched to a BC code of STGTS1 is unambiguous and is left alone
// — there is no second variant for it to be confused with. Two lines sharing a
// base and differing by suffix, against a code carrying no suffix, are not:
// neither can claim it. A BC code that does spell the variant out
// (bcDistinguishesVariant) is likewise untouched, because then the code really
// does identify the line.
func resolveVariantAmbiguity(all []LineMatch) {
	variants := map[string]map[string]bool{}
	seen := map[int]bool{}
	for _, m := range all {
		if seen[m.poaIndex] {
			continue
		}
		seen[m.poaIndex] = true
		if base, suffix := splitVariantSuffix(combinedPOADesc(m.POA)); base != "" {
			if variants[base] == nil {
				variants[base] = map[string]bool{}
			}
			variants[base][suffix] = true
		}
	}

	for i := range all {
		if !all[i].CodeMatch {
			continue
		}
		base, suffix := splitVariantSuffix(combinedPOADesc(all[i].POA))
		if base == "" || len(variants[base]) < 2 || bcDistinguishesVariant(all[i].BC, base, suffix) {
			continue
		}
		competing := make([]string, 0, len(variants[base]))
		for s := range variants[base] {
			competing = append(competing, fmt.Sprintf("%s (%s)", base, s))
		}
		sort.Strings(competing)
		all[i].CodeMatch = false
		all[i].CodeVariantAmbiguous = strings.Join(competing, " and ")
	}
}

func resolveCodeAmbiguity(all []LineMatch) {
	byPOA := map[int][]int{}
	for i, m := range all {
		if m.CodeMatch {
			byPOA[m.poaIndex] = append(byPOA[m.poaIndex], i)
		}
	}
	for _, idxs := range byPOA {
		if len(idxs) < 2 {
			continue
		}
		for _, i := range idxs {
			var others []string
			for _, j := range idxs {
				if j != i {
					others = append(others, bcGroupLabel(all[j].BC))
				}
			}
			all[i].CodeMatch = false
			all[i].CodeAmbiguousWith = strings.Join(others, ", ")
		}
	}
}
