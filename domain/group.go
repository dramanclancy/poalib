// Line groups: one product, laid out as different lines on each side.
//
// The assignment in match.go is one-to-one, which assumes the supplier and
// Business Central agree on how many lines a product takes. They often do
// not. Parker Knoll prints one line per chair where BC holds "Wing Chair x 2"
// (PF131114); Ashwood prints a 180cm divan base as its two 90cm halves where
// BC holds one base (PF131012); BC can carry the same item twice where the
// supplier prints it once with qty 2 (PF130543). Read one-to-one, every one of
// those orders reports a quantity mismatch, a price mismatch and an unmatched
// line — three wrong sentences about goods that agree — and never
// auto-approves.
//
// A group puts one ANCHOR line on one side together with two or more MEMBER
// lines on the other. It is only ever formed to fix what the one-to-one pass
// left broken, it never breaks up a pair that passed every check, and it
// never weakens a check: each member is still paired with the anchor and
// checked for description, orientation, seats and arithmetic on its own.
// Quantity and price, which only make sense for the whole, are checked once
// on the combined figures.
//
// Grouping follows the rule pairing does. Identity is decided by description
// and quantity; money is verified on the result, never used to form it —
// except where it is the independent confirmation of a quantity
// interpretation, exactly as packRatio already uses unit prices to confirm a
// pack size. Two quantity shapes are recognised:
//
//	sum    the many side is the product with its quantity split between
//	       lines: the quantities add up to the anchor's. 1 + 1 chairs = 2.
//	parts  the many side is the parts of the product: each carries the
//	       anchor's quantity, and their unit prices add up to its unit
//	       price. Divan halves at 245.44 + 245.44 = one base at 490.88.
//
// and two kinds of evidence that the lines belong together:
//
//	identity  every member passes the description check against the anchor
//	          on its own terms (code match, or similarity and margin).
//	money     the members' descriptions are weaker, but each resembles the
//	          anchor at least as much as anything else still in play, and
//	          the combined money agrees. The weak description is still
//	          flagged on the row, so this changes what the flag says, never
//	          whether the order auto-approves.
//
// A sum group without matching descriptions needs the money to agree; a
// parts group always does, by construction. Nothing else forms a group, so a
// duplicated acknowledgement line — qty 1 printed twice against BC's qty 1 —
// matches neither shape and stays unmatched, which is what it should say.

package domain

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
)

// GroupKind says which side carries the several lines.
type GroupKind string

const (
	// GroupAggregate — several acknowledgement lines are one BC product.
	GroupAggregate GroupKind = "aggregate"
	// GroupSplit — one acknowledgement line is several BC products.
	GroupSplit GroupKind = "split"
)

// QuantityRule says how quantities spread over several lines reconciled.
type QuantityRule string

const (
	// RuleSum — the quantities on the many side add up to the anchor's.
	RuleSum QuantityRule = "sum"
	// RuleParts — each line on the many side carries the anchor's quantity,
	// and their unit prices add up to the anchor's unit price.
	RuleParts QuantityRule = "parts"
)

// GroupEvidence says what established that a group's lines belong together.
type GroupEvidence string

const (
	// EvidenceIdentity — every member passed the description check against
	// the anchor on its own.
	EvidenceIdentity GroupEvidence = "identity"
	// EvidenceMoney — the descriptions were weaker, and the combined money
	// agreeing is what corroborated the structure. The rows still carry the
	// failed description check.
	EvidenceMoney GroupEvidence = "money"
)

// LineGroup records that several lines on one side of the comparison are,
// together, one line on the other: the same product, laid out differently by
// the supplier and by Business Central. Every ProductResult in the group
// points at the same LineGroup.
//
// Reconcile forms a group only where the one-to-one assignment left something
// broken, never from a pair that passed every check, and never from a line
// with no quantity. The lines must form one of two quantity shapes (Rule):
// their quantities add up to the anchor's (RuleSum), or each carries the
// anchor's quantity and their unit prices add up to its unit price
// (RuleParts). And the grouping must be evidenced (Evidence): every member
// identified against the anchor by description or code (EvidenceIdentity), or
// each member closer to the anchor than to anything else still unpaired with
// the combined money agreeing (EvidenceMoney).
//
// On every row of a group, Quantity and Price are checked on the combined
// figures. Description, orientation, seats and arithmetic are still checked
// per row, so a weak description or a wrong hand still blocks the write gate.
type LineGroup struct {
	Kind     GroupKind     `json:"kind"`
	Rule     QuantityRule  `json:"rule"`
	Evidence GroupEvidence `json:"evidence"`
	// POAIndexes and BCIndexes are the lines in the group, ascending. One of
	// them always holds exactly one index: the anchor.
	POAIndexes []int `json:"poaLineIndexes"`
	BCIndexes  []int `json:"bcLineIndexes"`
}

// String names the group for a report cell: "POA 0 + 1 = BC 0 (aggregate,
// sum, by identity)".
func (g LineGroup) String() string {
	return fmt.Sprintf("POA %s = BC %s (%s, %s, by %s)",
		joinIndexes(g.POAIndexes), joinIndexes(g.BCIndexes), g.Kind, g.Rule, g.Evidence)
}

func joinIndexes(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, " + ")
}

// ---------------------------------------------------------------------------
// Finding groups
// ---------------------------------------------------------------------------

// groupLine is one line's figures, as the quantity rules read them.
type groupLine struct {
	index   int
	qty     float64
	unitNet float64
	net     float64
}

func poaGroupLine(i int, p POAProduct) groupLine {
	return groupLine{index: i, qty: float64(p.Qty), unitNet: p.UnitNet(), net: p.Net()}
}

func bcGroupLine(j int, b BCProduct) groupLine {
	return groupLine{index: j, qty: b.Qty, unitNet: b.UnitNet(), net: b.Net}
}

// groupMember is a candidate for the many side of a group.
type groupMember struct {
	groupLine
	sim float64
	// identified: the pairing with the anchor passes the description check
	// on its own terms.
	identified bool
	// closest: nothing still in play resembles this line more than the
	// anchor does.
	closest bool
	// rehomed: the line currently sits in a failing pair elsewhere and
	// would leave it to join the group.
	rehomed bool
}

// findGroups looks for products the one-to-one assignment could not express.
//
// settled is that assignment with its checks run. A line is a candidate member
// only if it is unmatched, is the anchor's own partner, or sits in a pair that
// failed a check and resembles the anchor clearly more than its partner (see
// rehomes). An anchor whose pair passed every check is never touched: there
// is nothing to fix, and a group could only make it worse.
//
// Aggregate groups are looked for first, then splits. A line belongs to at
// most one group, so many-to-many is never formed — that is a reshuffle of
// the whole order, not a different layout of one product, and guessing at it
// would cost more trust than it gains.
func findGroups(pairs []pairing, settled []ProductResult, poa POAOrder, bc BCOrder, cfg Config) []LineGroup {
	at := pairIndex(pairs)
	poaPartner, bcPartner := map[int]int{}, map[int]int{}
	clean := map[[2]int]bool{}
	for _, r := range settled {
		poaPartner[r.POAIndex], bcPartner[r.BCIndex] = r.BCIndex, r.POAIndex
		clean[[2]int{r.POAIndex, r.BCIndex}] = r.OK()
	}
	poaGrouped, bcGrouped := map[int]bool{}, map[int]bool{}
	cleanPOA := func(i int) bool { j, ok := poaPartner[i]; return ok && clean[[2]int{i, j}] }
	cleanBC := func(j int) bool { i, ok := bcPartner[j]; return ok && clean[[2]int{i, j}] }

	var groups []LineGroup

	// Several acknowledgement lines, one BC product.
	for j, anchorBC := range bc.Products {
		if bcGrouped[j] || anchorBC.Qty <= 0 || cleanBC(j) {
			continue
		}
		var cands []groupMember
		required := -1
		for i, p := range poa.Products {
			if poaGrouped[i] || p.Qty <= 0 {
				continue
			}
			pair := at[[2]int{i, j}]
			m := groupMember{groupLine: poaGroupLine(i, p), sim: similarityValue(pair.ev.Similarity)}
			switch k, paired := poaPartner[i]; {
			case paired && k == j:
				required = len(cands)
			case !paired:
			case !clean[[2]int{i, k}] && rehomes(pair, at[[2]int{i, k}], cfg):
				m.rehomed = true
			default:
				continue
			}
			// Margin from the member's own side: the BC products it could
			// still have been, other than this anchor.
			inPlay := func(k int) bool { return k != j && !bcGrouped[k] && !cleanBC(k) }
			ev := marginEvidence(pairs, pair, inPlay)
			m.identified = !descriptionCheck(pair.poa, pair.bc, ev, bc.Enrichment, cfg).Failed()
			m.closest = ev.MarginNA || ev.Margin >= 0
			cands = append(cands, m)
		}
		if _, partnered := bcPartner[j]; partnered && required < 0 {
			continue // the anchor's partner cannot join, so no group can replace the pair
		}
		members, rule, evidence, ok := chooseGroup(bcGroupLine(j, anchorBC), cands, required, cfg)
		if !ok {
			continue
		}
		g := LineGroup{Kind: GroupAggregate, Rule: rule, Evidence: evidence, BCIndexes: []int{j}}
		for _, m := range members {
			g.POAIndexes = append(g.POAIndexes, m.index)
			poaGrouped[m.index] = true
			if m.rehomed {
				delete(bcPartner, poaPartner[m.index])
			}
			delete(poaPartner, m.index)
		}
		bcGrouped[j] = true
		delete(bcPartner, j)
		groups = append(groups, g)
	}

	// One acknowledgement line, several BC products.
	for i, anchorPOA := range poa.Products {
		if poaGrouped[i] || anchorPOA.Qty <= 0 || cleanPOA(i) {
			continue
		}
		var cands []groupMember
		required := -1
		for j, b := range bc.Products {
			if bcGrouped[j] || b.Qty <= 0 {
				continue
			}
			pair := at[[2]int{i, j}]
			m := groupMember{groupLine: bcGroupLine(j, b), sim: similarityValue(pair.ev.Similarity)}
			switch k, paired := bcPartner[j]; {
			case paired && k == i:
				required = len(cands)
			case !paired:
			case !clean[[2]int{k, j}] && rehomes(pair, at[[2]int{k, j}], cfg):
				m.rehomed = true
			default:
				continue
			}
			cands = append(cands, m)
		}
		if _, partnered := poaPartner[i]; partnered && required < 0 {
			continue
		}
		isCand := map[int]bool{}
		for _, m := range cands {
			isCand[m.index] = true
		}
		for n := range cands {
			pair := at[[2]int{i, cands[n].index}]
			// The description check reads margin from the POA line's side,
			// and the anchor's own alternatives are the BC products outside
			// the candidate set — the others are the same product, not
			// rivals to it.
			inPlay := func(k int) bool { return !isCand[k] && !bcGrouped[k] && !cleanBC(k) }
			ev := marginEvidence(pairs, pair, inPlay)
			cands[n].identified = !descriptionCheck(pair.poa, pair.bc, ev, bc.Enrichment, cfg).Failed()
			// "Closest" from the BC product's side: no other acknowledgement
			// line still in play resembles it more.
			cands[n].closest = true
			for m := range poa.Products {
				if m == i || poaGrouped[m] || cleanPOA(m) {
					continue
				}
				if similarityValue(at[[2]int{m, cands[n].index}].ev.Similarity) > cands[n].sim {
					cands[n].closest = false
					break
				}
			}
		}
		members, rule, evidence, ok := chooseGroup(poaGroupLine(i, anchorPOA), cands, required, cfg)
		if !ok {
			continue
		}
		g := LineGroup{Kind: GroupSplit, Rule: rule, Evidence: evidence, POAIndexes: []int{i}}
		for _, m := range members {
			g.BCIndexes = append(g.BCIndexes, m.index)
			bcGrouped[m.index] = true
			if m.rehomed {
				delete(poaPartner, bcPartner[m.index])
			}
			delete(bcPartner, m.index)
		}
		poaGrouped[i] = true
		delete(poaPartner, i)
		groups = append(groups, g)
	}
	return groups
}

// rehomes reports whether a line already paired elsewhere may leave that pair
// to join a group: it must resemble the anchor clearly more — by at least the
// margin that makes a pairing unambiguous — or code-match the anchor where it
// did not code-match its partner. Never the other way round: a line its code
// identified stays where its code put it.
func rehomes(toAnchor, current pairing, cfg Config) bool {
	switch {
	case current.ev.CodeMatch:
		return false
	case toAnchor.ev.CodeMatch:
		return true
	default:
		return similarityValue(toAnchor.ev.Similarity)-similarityValue(current.ev.Similarity) >= cfg.MarginThreshold
	}
}

// chooseGroup picks the subset of candidates that best forms a group with the
// anchor, or reports that none does. required, when not -1, is the position of
// the anchor's current partner, which every subset must contain: a group
// replaces the anchor's pair, it does not sit beside it.
//
// Preference: identity evidence over money, then the most description
// similarity in total, then the fewest lines pulled out of other pairs. Ties
// go to the lowest line positions, so the choice is deterministic.
func chooseGroup(anchor groupLine, cands []groupMember, required int, cfg Config) ([]groupMember, QuantityRule, GroupEvidence, bool) {
	cands, required = capCandidates(cands, required, cfg.ExhaustiveLimit)

	var (
		best         []groupMember
		bestRule     QuantityRule
		bestEvidence GroupEvidence
		bestSim      float64
		bestRehomed  int
	)
	for mask := 1; mask < 1<<len(cands); mask++ {
		if bits.OnesCount(uint(mask)) < 2 || (required >= 0 && mask&(1<<required) == 0) {
			continue
		}
		var members []groupMember
		var sim float64
		var rehomed int
		for n, c := range cands {
			if mask&(1<<n) != 0 {
				members = append(members, c)
				sim += c.sim
				if c.rehomed {
					rehomed++
				}
			}
		}
		rule, ok := groupRule(anchor, members, cfg)
		if !ok {
			continue
		}
		evidence, ok := groupEvidence(anchor, members, cfg)
		if !ok {
			continue
		}
		better := best == nil ||
			(evidence == EvidenceIdentity && bestEvidence != EvidenceIdentity) ||
			(evidence == bestEvidence && (sim > bestSim+1e-9 ||
				(math.Abs(sim-bestSim) <= 1e-9 && rehomed < bestRehomed)))
		if better {
			best, bestRule, bestEvidence, bestSim, bestRehomed = members, rule, evidence, sim, rehomed
		}
	}
	return best, bestRule, bestEvidence, best != nil
}

// capCandidates keeps the search exhaustive but bounded: beyond limit, the
// partner and then the most similar candidates are kept. Order is restored to
// line order afterwards so the result does not depend on similarity ties.
func capCandidates(cands []groupMember, required, limit int) ([]groupMember, int) {
	if len(cands) <= limit {
		return cands, required
	}
	reqIndex := -1
	if required >= 0 {
		reqIndex = cands[required].index
	}
	kept := append([]groupMember(nil), cands...)
	sort.SliceStable(kept, func(a, b int) bool {
		if (kept[a].index == reqIndex) != (kept[b].index == reqIndex) {
			return kept[a].index == reqIndex
		}
		return kept[a].sim > kept[b].sim
	})
	kept = kept[:limit]
	sort.Slice(kept, func(a, b int) bool { return kept[a].index < kept[b].index })
	required = -1
	for n, c := range kept {
		if c.index == reqIndex {
			required = n
		}
	}
	return kept, required
}

// groupRule reports which quantity shape, if either, the members form with
// the anchor. Lines with no quantity were excluded before this: a line that
// acknowledges nothing is not part of anything.
func groupRule(anchor groupLine, members []groupMember, cfg Config) (QuantityRule, bool) {
	var qty, unitNet float64
	eachIsWhole := true
	for _, m := range members {
		qty += m.qty
		unitNet += m.unitNet
		// A part priced at nothing cannot corroborate anything, so a
		// zero-value line never makes up the parts rule.
		if math.Abs(m.qty-anchor.qty) > 0.001 || m.unitNet <= 0 {
			eachIsWhole = false
		}
	}
	switch {
	case math.Abs(qty-anchor.qty) < 0.001:
		return RuleSum, true
	case eachIsWhole && math.Abs(unitNet-anchor.unitNet) < cfg.MoneyTolerance:
		return RuleParts, true
	default:
		return "", false
	}
}

// groupEvidence reports what, if anything, establishes that the members
// belong with the anchor.
func groupEvidence(anchor groupLine, members []groupMember, cfg Config) (GroupEvidence, bool) {
	identified, closest := true, true
	var net float64
	for _, m := range members {
		identified = identified && m.identified
		closest = closest && m.closest
		net += m.net
	}
	switch {
	case identified:
		return EvidenceIdentity, true
	case closest && math.Abs(net-anchor.net) < cfg.MoneyTolerance:
		return EvidenceMoney, true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------------------
// Checks on the whole group
// ---------------------------------------------------------------------------

// groupQuantityCheck states the quantity verdict for a whole group. A group
// only exists because its quantities reconciled, so this always matches; it
// exists to say how, since "quantity agrees (1)" on a row whose BC side
// reads 2 would be a sentence nobody could check.
func groupQuantityCheck(g LineGroup, poas []POAProduct, bcs []BCProduct) QuantityCheckResult {
	r := QuantityCheckResult{Rule: g.Rule, PackRatio: 1}
	r.CheckType = "quantity"
	r.Status = StatusMatch

	poaQtys := make([]string, len(poas))
	for n, p := range poas {
		r.POAQty += float64(p.Qty)
		poaQtys[n] = strconv.Itoa(p.Qty)
	}
	bcQtys := make([]string, len(bcs))
	for n, b := range bcs {
		r.BCQty += b.Qty
		bcQtys[n] = strconv.FormatFloat(b.Qty, 'g', -1, 64)
	}

	switch {
	case g.Kind == GroupAggregate && g.Rule == RuleSum:
		r.Message = fmt.Sprintf("quantity agrees across %d acknowledgement lines (%s = %g)",
			len(poas), strings.Join(poaQtys, " + "), r.BCQty)
	case g.Kind == GroupSplit && g.Rule == RuleSum:
		r.Message = fmt.Sprintf("quantity agrees across %d order lines (%s = %g)",
			len(bcs), strings.Join(bcQtys, " + "), r.POAQty)
	case g.Kind == GroupAggregate:
		units := make([]string, len(poas))
		for n, p := range poas {
			units[n] = fmt.Sprintf("%.2f", p.UnitNet())
		}
		r.Message = fmt.Sprintf("quantity agrees: BC's %g is acknowledged as %d parts, whose unit prices add up to BC's (%s = %.2f)",
			bcs[0].Qty, len(poas), strings.Join(units, " + "), bcs[0].UnitNet())
	default:
		units := make([]string, len(bcs))
		for n, b := range bcs {
			units[n] = fmt.Sprintf("%.2f", b.UnitNet())
		}
		r.Message = fmt.Sprintf("quantity agrees: the acknowledged %d is ordered as %d parts, whose unit costs add up to the supplier's (%s = %.2f)",
			poas[0].Qty, len(bcs), strings.Join(units, " + "), poas[0].UnitNet())
	}
	return r
}

// groupPriceCheck compares the combined net value of the two sides of a group.
// Money stays a control: the group is checked as strictly as a single line,
// once, on the figures that actually describe the same goods.
func groupPriceCheck(poas []POAProduct, bcs []BCProduct, cfg Config) PriceCheckResult {
	var poaNet, bcNet float64
	for _, p := range poas {
		poaNet += p.Net()
	}
	for _, b := range bcs {
		bcNet += b.Net
	}
	diff := poaNet - bcNet
	r := PriceCheckResult{POAPrice: poaNet, BCPrice: bcNet, Difference: diff, Tolerance: cfg.MoneyTolerance}
	r.CheckType = "price"
	lines := len(poas) + len(bcs) - 1
	if math.Abs(diff) < cfg.MoneyTolerance {
		r.Status = StatusMatch
		r.Message = fmt.Sprintf("combined net value agrees across %d lines (%.2f)", lines, poaNet)
		return r
	}
	r.Status = StatusMismatch
	r.Message = fmt.Sprintf("combined net value differs from BC by %+.2f across %d lines (POA %.2f, BC %.2f)",
		diff, lines, poaNet, bcNet)
	return r
}

// applyGroup replaces a row's quantity and price verdicts with the group's.
// Unit prices stay the row's own, so a reader can still see each line's.
func (p *ProductResult) applyGroup(g *LineGroup, poa POAOrder, bc BCOrder, cfg Config) {
	poas := make([]POAProduct, len(g.POAIndexes))
	for n, i := range g.POAIndexes {
		poas[n] = poa.Products[i]
	}
	bcs := make([]BCProduct, len(g.BCIndexes))
	for n, j := range g.BCIndexes {
		bcs[n] = bc.Products[j]
	}
	p.Group = g
	p.Quantity = groupQuantityCheck(*g, poas, bcs)
	p.Price = groupPriceCheck(poas, bcs, cfg)
	p.Price.POAUnitPrice, p.Price.BCUnitPrice = p.POA.UnitPrice, p.BC.UnitCost
}
