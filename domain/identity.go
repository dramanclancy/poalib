// Exact-code identity: the supplier printing a BC catalogue code verbatim,
// and the three ways that can turn out to prove nothing.
package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nonAlnumSquash = regexp.MustCompile(`[^A-Za-z0-9]+`)

// squash reduces a code (or a whole POA text) to comparable form: uppercase,
// alphanumerics only. "1341 CASH SKY21 RAL 9005" and "1341CASHSKY21RAL9005"
// both become the same string, so a code compares equal whether or not the
// POA spaced it out.
func squash(s string) string {
	return strings.ToUpper(nonAlnumSquash.ReplaceAllString(s, ""))
}

// productCodeIn returns the first token of s that looks like a supplier
// product code, or "" if none does.
//
// The test is structural — four or more characters, starting with a letter,
// containing at least one digit (NFX2S, HAN2HFR, STGTS1, CA24, C191-TAN-P) —
// because the alternative is a dictionary of real words, and a
// hand-maintained word list is exactly what this engine removed.
//
// Leading letter is what separates a code from a measurement: dimensions and
// quantities lead with the number (150CM, 2 SEATER), codes lead with the
// range or family.
func productCodeIn(s string, cfg Config) string {
	for _, raw := range strings.Fields(s) {
		if looksLikeProductCode(raw, cfg) {
			return squash(raw)
		}
	}
	return ""
}

// looksLikeProductCode is the structural test itself, split out so anything
// else deciding "is this token a code?" asks the same question rather than
// growing a second answer to it.
func looksLikeProductCode(token string, cfg Config) bool {
	t := squash(token)
	if len(t) < cfg.MinProductCodeLen || t[0] < 'A' || t[0] > 'Z' {
		return false
	}
	return strings.ContainsAny(t, "0123456789")
}

// codeHits reports whether code, squashed, is a real match inside poaSquashed.
func codeHits(poaSquashed, code string, cfg Config) bool {
	sc := squash(code)
	return len(sc) >= cfg.MinCodeLen && strings.Contains(poaSquashed, sc)
}

// codeMatch reports whether the POA text code-matches this BC product, and
// via which field.
//
// Vendor_Item_No is vendor-scoped: it is THIS vendor's catalogue code for the
// item, and only meaningful when the item belongs to the order's vendor —
// otherwise the stored code belongs to a different supplier and a textual
// coincidence would be a false positive dressed as authoritative. Model_No
// carries no such constraint.
func codeMatch(poaSquashed string, bc BCProduct, vendorNo string, cfg Config) (matched bool, source string) {
	if bc.Detail == nil {
		return false, ""
	}
	modelHit := codeHits(poaSquashed, bc.Detail.ModelNo, cfg)
	vendorScoped := vendorNo != "" && bc.Detail.VendorNo == vendorNo
	vendorHit := vendorScoped && codeHits(poaSquashed, bc.Detail.VendorItemNo, cfg)
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

// codeStatus explains what is known about BC's codes for this product,
// consulting the enrichment state before making any claim about master data.
//
// Saying "BC item IT0188674 has no Vendor_Item_No on file" when the lookup
// never ran is confidently wrong, and sends someone to inspect a field that
// may be perfectly populated. In the batch of 2026-09-04 not one of thirty
// lines produced a code match, and the report could not say whether that was
// a master-data gap or a lookup that never executed.
func codeStatus(bc BCProduct, state EnrichmentState) (known bool, note string) {
	switch {
	case !state.Ran():
		return false, "item enrichment was not run for this order, so BC's codes are unknown"
	case bc.Detail == nil && state == EnrichmentFailed:
		return false, "item enrichment failed for this order, so BC's codes are unknown"
	case bc.Detail == nil:
		return false, fmt.Sprintf("item enrichment returned no record for BC item %s", bc.ItemNo)
	case !bc.hasCodeOnFile():
		return true, fmt.Sprintf("BC item %s has no Vendor_Item_No or Model_No on file", bc.ItemNo)
	default:
		return true, ""
	}
}

// ---------------------------------------------------------------------------
// Ambiguity — a property of the candidate set, never of a string
// ---------------------------------------------------------------------------

// resolveCodeAmbiguity clears CodeMatch on any pairing whose code also matches
// another BC product for the same POA line. The tentative source is left in
// place and CodeAmbiguousWith names what it collided with.
func resolveCodeAmbiguity(all []pairing) {
	byPOA := map[int][]int{}
	for i, p := range all {
		if p.ev.CodeMatch {
			byPOA[p.poaIndex] = append(byPOA[p.poaIndex], i)
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
					others = append(others, all[j].bc.Label())
				}
			}
			all[i].ev.CodeMatch = false
			all[i].ev.CodeAmbiguousWith = strings.Join(others, ", ")
		}
	}
}

// variantSuffixPattern finds a product code followed by a parenthesised
// variant suffix — "STGTS1 (A)", "NFX2S (C)". The base must contain a letter,
// which keeps a printed quantity or price ("5000 (QJ)") from reading as one.
var variantSuffixPattern = regexp.MustCompile(`([A-Z0-9]*[A-Z][A-Z0-9]*)\s*\(([A-Z0-9]{1,3})\)`)

// splitVariantSuffix returns the product code and parenthesised variant
// suffix in s, or two empty strings if it carries none.
func splitVariantSuffix(s string, cfg Config) (base, suffix string) {
	m := variantSuffixPattern.FindStringSubmatch(strings.ToUpper(s))
	if m == nil || len(m[1]) < cfg.MinProductCodeLen {
		return "", ""
	}
	return m[1], m[2]
}

// bcDistinguishesVariant reports whether either BC code spells out the variant
// rather than just the family — a Vendor_Item_No of STGTS1C, not STGTS1.
func bcDistinguishesVariant(bc BCProduct, base, suffix string) bool {
	if bc.Detail == nil {
		return false
	}
	want := base + suffix
	for _, code := range []string{bc.Detail.ModelNo, bc.Detail.VendorItemNo} {
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
// discriminator: STGTS1 (A) and STGTS1 (C) resolve to different items. codeHits
// works by containment, so both contain a BC code of "STGTS1" and both claim an
// exact match to it. A code match settles identity outright, so without this
// one variant is silently auto-approved against the other's item.
//
// resolveCodeAmbiguity does not cover this: it catches one POA line matching
// several BC products. This is the mirror case, several POA lines matching one
// product, and it needs the POA text rather than the pairing to see it.
//
// A lone STGTS1 line is left alone — there is no second variant to confuse it
// with. A BC code that does spell the variant out is likewise untouched.
func resolveVariantAmbiguity(all []pairing, cfg Config) {
	variants := map[string]map[string]bool{}
	seen := map[int]bool{}
	for _, p := range all {
		if seen[p.poaIndex] {
			continue
		}
		seen[p.poaIndex] = true
		if base, suffix := splitVariantSuffix(p.poa.Text(), cfg); base != "" {
			if variants[base] == nil {
				variants[base] = map[string]bool{}
			}
			variants[base][suffix] = true
		}
	}

	for i := range all {
		if !all[i].ev.CodeMatch {
			continue
		}
		base, suffix := splitVariantSuffix(all[i].poa.Text(), cfg)
		if base == "" || len(variants[base]) < 2 || bcDistinguishesVariant(all[i].bc, base, suffix) {
			continue
		}
		competing := make([]string, 0, len(variants[base]))
		for s := range variants[base] {
			competing = append(competing, fmt.Sprintf("%s (%s)", base, s))
		}
		sort.Strings(competing)
		all[i].ev.CodeMatch = false
		all[i].ev.VariantAmbiguity = strings.Join(competing, " and ")
	}
}
