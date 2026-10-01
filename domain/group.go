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
	s := newGroupSearch(pairs, settled, poa, bc, cfg)
	var groups []LineGroup

	// Several acknowledgement lines, one BC product.
	for j := range bc.Products {
		if g, ok := s.aggregateAt(j); ok {
			groups = append(groups, g)
		}
	}
	// One acknowledgement line, several BC products.
	for i := range poa.Products {
		if g, ok := s.splitAt(i); ok {
			groups = append(groups, g)
		}
	}
	return groups
}

// groupSearch is findGroups' bookkeeping: who each line is paired with after
// the one-to-one assignment, which of those pairs passed every check, and
// which lines a group has already claimed. Groups are found one anchor at a
// time and each claim updates this state, so the order anchors are visited in
// decides which later groups are still possible.
type groupSearch struct {
	pairs []pairing
	at    map[[2]int]pairing
	poa   POAOrder
	bc    BCOrder
	cfg   Config

	poaPartner map[int]int     // POA line -> the BC product it is paired with
	bcPartner  map[int]int     // BC product -> the POA line it is paired with
	clean      map[[2]int]bool // {POA, BC} pair -> passed every check
	poaGrouped map[int]bool    // POA lines already in a group
	bcGrouped  map[int]bool    // BC products already in a group
}

func newGroupSearch(pairs []pairing, settled []ProductResult, poa POAOrder, bc BCOrder, cfg Config) *groupSearch {
	s := &groupSearch{
		pairs: pairs, at: pairIndex(pairs), poa: poa, bc: bc, cfg: cfg,
		poaPartner: map[int]int{}, bcPartner: map[int]int{}, clean: map[[2]int]bool{},
		poaGrouped: map[int]bool{}, bcGrouped: map[int]bool{},
	}
	for _, r := range settled {
		s.poaPartner[r.POAIndex], s.bcPartner[r.BCIndex] = r.BCIndex, r.POAIndex
		s.clean[[2]int{r.POAIndex, r.BCIndex}] = r.OK()
	}
	return s
}

// cleanPOA and cleanBC report whether a line sits in a pair that passed every
// check. Such a pair is never broken up.
func (s *groupSearch) cleanPOA(i int) bool {
	j, ok := s.poaPartner[i]
	return ok && s.clean[[2]int{i, j}]
}

func (s *groupSearch) cleanBC(j int) bool {
	i, ok := s.bcPartner[j]
	return ok && s.clean[[2]int{i, j}]
}

// standing is where a line on the many side stands relative to a group being
// formed around an anchor, judged by the pair it sits in now.
type standing int

const (
	excluded       standing = iota // sits in a pair that should stand; cannot join
	unpaired                       // in no pair; free to join
	anchorsPartner                 // already paired with the anchor; every group must include it
	rehomed                        // leaves a failing pair it fits worse than the anchor
)

// standing classifies a candidate line. toAnchor is its pairing with the
// anchor; current is its pairing with its present partner, meaningful only
// when paired; partnerIsAnchor says whether that partner is the anchor itself.
func (s *groupSearch) standing(toAnchor, current pairing, paired, partnerIsAnchor bool) standing {
	switch {
	case paired && partnerIsAnchor:
		return anchorsPartner
	case !paired:
		return unpaired
	case !s.clean[[2]int{current.poaIndex, current.bcIndex}] && rehomes(toAnchor, current, s.cfg):
		return rehomed
	default:
		return excluded
	}
}

// identifies reports whether a pairing passes the description check with the
// given margin evidence.
func (s *groupSearch) identifies(p pairing, ev MatchEvidence) bool {
	return !descriptionCheck(p.poa, p.bc, ev, s.bc.Enrichment, s.cfg).Failed()
}

// ---------------------------------------------------------------------------
// Several acknowledgement lines, one BC product (aggregate)
// ---------------------------------------------------------------------------

// aggregateAt tries to form a group of POA lines around BC product j.
func (s *groupSearch) aggregateAt(j int) (LineGroup, bool) {
	anchor := s.bc.Products[j]
	if s.bcGrouped[j] || anchor.Qty <= 0 || s.cleanBC(j) {
		return LineGroup{}, false
	}
	cands, required := s.aggregateCandidates(j)
	if _, partnered := s.bcPartner[j]; partnered && required < 0 {
		return LineGroup{}, false // the anchor's partner cannot join, so no group can replace the pair
	}
	members, rule, evidence, ok := chooseGroup(bcGroupLine(j, anchor), cands, required, s.cfg)
	if !ok {
		return LineGroup{}, false
	}
	g := LineGroup{Kind: GroupAggregate, Rule: rule, Evidence: evidence, BCIndexes: []int{j}}
	for _, m := range members {
		g.POAIndexes = append(g.POAIndexes, m.index)
	}
	s.claimAggregate(j, members)
	return g, true
}

// aggregateCandidates lists the POA lines that could join a group around BC
// product j, and the position of j's current partner among them (-1 if none).
//
// Evidence is judged from each member's own side, because each is a separate
// acknowledgement line: its margin is measured against the BC products it
// could still have been other than the anchor, and it is "closest" when none
// of those resembles it more than the anchor does.
func (s *groupSearch) aggregateCandidates(j int) ([]groupMember, int) {
	var cands []groupMember
	required := -1
	inPlay := func(k int) bool { return k != j && !s.bcGrouped[k] && !s.cleanBC(k) }
	for i, p := range s.poa.Products {
		if s.poaGrouped[i] || p.Qty <= 0 {
			continue
		}
		toAnchor := s.at[[2]int{i, j}]
		k, paired := s.poaPartner[i]
		m := groupMember{groupLine: poaGroupLine(i, p), sim: orZero(toAnchor.ev.Similarity)}
		switch s.standing(toAnchor, s.at[[2]int{i, k}], paired, k == j) {
		case anchorsPartner:
			required = len(cands)
		case rehomed:
			m.rehomed = true
		case excluded:
			continue
		}
		ev := marginEvidence(s.pairs, toAnchor, inPlay)
		m.identified = s.identifies(toAnchor, ev)
		m.closest = ev.MarginNA || ev.Margin >= 0
		cands = append(cands, m)
	}
	return cands, required
}

// claimAggregate marks the anchor and its members as grouped. A member pulled
// out of another pair leaves that pair's BC product unpartnered, free for a
// later group.
func (s *groupSearch) claimAggregate(j int, members []groupMember) {
	for _, m := range members {
		s.poaGrouped[m.index] = true
		if m.rehomed {
			delete(s.bcPartner, s.poaPartner[m.index])
		}
		delete(s.poaPartner, m.index)
	}
	s.bcGrouped[j] = true
	delete(s.bcPartner, j)
}

// ---------------------------------------------------------------------------
// One acknowledgement line, several BC products (split)
// ---------------------------------------------------------------------------

// splitAt tries to form a group of BC products around POA line i.
func (s *groupSearch) splitAt(i int) (LineGroup, bool) {
	anchor := s.poa.Products[i]
	if s.poaGrouped[i] || anchor.Qty <= 0 || s.cleanPOA(i) {
		return LineGroup{}, false
	}
	cands, required := s.splitCandidates(i)
	if _, partnered := s.poaPartner[i]; partnered && required < 0 {
		return LineGroup{}, false
	}
	members, rule, evidence, ok := chooseGroup(poaGroupLine(i, anchor), cands, required, s.cfg)
	if !ok {
		return LineGroup{}, false
	}
	g := LineGroup{Kind: GroupSplit, Rule: rule, Evidence: evidence, POAIndexes: []int{i}}
	for _, m := range members {
		g.BCIndexes = append(g.BCIndexes, m.index)
	}
	s.claimSplit(i, members)
	return g, true
}

// splitCandidates lists the BC products that could join a group around POA
// line i, and the position of i's current partner among them (-1 if none).
//
// Evidence works differently from an aggregate, and on purpose. Every member
// is paired with the same POA line, so the description check's margin is
// read from that line's side, and its rivals are the BC products OUTSIDE the
// candidate set — the other candidates are parts of the same product, not
// alternatives to it. That needs the whole candidate set, so evidence is
// judged after it is collected. "Closest" is read from each BC product's
// side: no other acknowledgement line still in play resembles it more.
func (s *groupSearch) splitCandidates(i int) ([]groupMember, int) {
	var cands []groupMember
	required := -1
	for j, b := range s.bc.Products {
		if s.bcGrouped[j] || b.Qty <= 0 {
			continue
		}
		toAnchor := s.at[[2]int{i, j}]
		k, paired := s.bcPartner[j]
		m := groupMember{groupLine: bcGroupLine(j, b), sim: orZero(toAnchor.ev.Similarity)}
		switch s.standing(toAnchor, s.at[[2]int{k, j}], paired, k == i) {
		case anchorsPartner:
			required = len(cands)
		case rehomed:
			m.rehomed = true
		case excluded:
			continue
		}
		cands = append(cands, m)
	}

	isCand := map[int]bool{}
	for _, m := range cands {
		isCand[m.index] = true
	}
	inPlay := func(k int) bool { return !isCand[k] && !s.bcGrouped[k] && !s.cleanBC(k) }
	for n := range cands {
		toAnchor := s.at[[2]int{i, cands[n].index}]
		cands[n].identified = s.identifies(toAnchor, marginEvidence(s.pairs, toAnchor, inPlay))
		cands[n].closest = s.noOtherPOALineCloser(i, cands[n])
	}
	return cands, required
}

// noOtherPOALineCloser reports whether no acknowledgement line still in play,
// other than the anchor, resembles BC product m more than the anchor does.
func (s *groupSearch) noOtherPOALineCloser(anchor int, m groupMember) bool {
	for i := range s.poa.Products {
		if i == anchor || s.poaGrouped[i] || s.cleanPOA(i) {
			continue
		}
		if orZero(s.at[[2]int{i, m.index}].ev.Similarity) > m.sim {
			return false
		}
	}
	return true
}

// claimSplit marks the anchor and its members as grouped. A member pulled out
// of another pair leaves that pair's POA line unpartnered, free for a later
// group.
func (s *groupSearch) claimSplit(i int, members []groupMember) {
	for _, m := range members {
		s.bcGrouped[m.index] = true
		if m.rehomed {
			delete(s.poaPartner, s.bcPartner[m.index])
		}
		delete(s.bcPartner, m.index)
	}
	s.poaGrouped[i] = true
	delete(s.poaPartner, i)
}

// ---------------------------------------------------------------------------
// Rules shared by both directions
// ---------------------------------------------------------------------------

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
		return orZero(toAnchor.ev.Similarity)-orZero(current.ev.Similarity) >= cfg.MarginThreshold
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
	r.CheckType = CheckQuantity
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
	r.CheckType = CheckPrice
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
