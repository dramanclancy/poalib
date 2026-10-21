// Unmatched acknowledgement lines: what a line with no BC counterpart appears
// to be, and whether it should stop the order.
//
// An unmatched line used to mean one thing — "the supplier acknowledged
// something the order does not contain" — and every one blocked. Across the
// captured corpus most of them were nothing of the kind: SEAT HEIGHT = 65cm,
// a "-" or blank row, an inclusive scatter pack, all at 0.00, on orders whose
// products agreed to the penny. The rest were either a product BC does not
// hold (Mateo 1 Arm Small Sofa, 608.00) or a charge the supplier adds
// (PF131539: 5% delivery surcharge, 5% operational surcharge, delivery 1/5
// items — 126.00 on 245.00 of goods).
//
// Money decides whether a line blocks; the classification only decides what
// the report says about it. A line carrying no money cannot change what is
// paid, so it blocks only when it might be an ordered item the supplier
// described too differently to pair. A line carrying money always blocks: a
// charge the order does not hold is still money a person must agree to. Its
// evidence is recorded so that person sees "supplier charge, 5% stated, 5% of
// 245.00 would be 12.25" rather than an unexplained extra line.
//
// Every signal is structural, like the rest of the engine: no word list and
// nothing keyed by supplier.

package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// UnmatchedKind is what an acknowledgement line with no BC counterpart appears
// to be. Values are printed in the workbook.
type UnmatchedKind string

const (
	// UnmatchedNote — the line carries no money: a specification, option,
	// customer note or blank row.
	UnmatchedNote UnmatchedKind = "note"
	// UnmatchedCharge — the line carries money and the evidence says it is a
	// charge (surcharge, duty, delivery) rather than a product.
	UnmatchedCharge UnmatchedKind = "charge"
	// UnmatchedProduct — the line carries money and may be a product the
	// order does not hold.
	UnmatchedProduct UnmatchedKind = "product"
)

// UnmatchedLine is one acknowledgement line that could not be paired, with
// what it appears to be and why. The line itself is POA.Products[POAIndex].
type UnmatchedLine struct {
	POAIndex int           `json:"poaLineIndex"`
	Kind     UnmatchedKind `json:"kind"`
	// Blocks says whether this line stops automatic acceptance.
	Blocks bool `json:"blocks"`
	// Evidence is each observation the classification rests on, in plain
	// English — the audit trail for the Kind.
	Evidence []string `json:"evidence"`
	Message  string   `json:"message"`
}

// percentPattern finds a percentage the supplier states in a line's text:
// "5% Surcharge delivery to Ireland", "4.5 % fuel surcharge".
var percentPattern = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*%`)

// classifyUnmatched describes every unmatched acknowledgement line.
//
// goods is the supplier's net value of the lines that did pair with BC
// products, which is the base a stated percentage is checked against.
// unacknowledged is the BC lines nobody paired with; an unacknowledged item
// among them is what keeps a zero-value line blocking.
func classifyUnmatched(poa POAOrder, unmatched []int, pairs []pairing, final []ProductResult, unacknowledged []BCProduct, cfg Config) []UnmatchedLine {
	var openItems []string
	for _, b := range unacknowledged {
		if !isChargeLine(b) {
			openItems = append(openItems, b.Label())
		}
	}
	goods := pairedGoods(final)

	out := make([]UnmatchedLine, 0, len(unmatched))
	for _, i := range unmatched {
		p := poa.Products[i]
		var l UnmatchedLine
		if carriesNoMoney(p) {
			l = classifyNote(i, openItems)
		} else {
			l = classifyMoneyLine(i, p, pairs, goods, cfg)
		}
		out = append(out, l)
	}
	return out
}

// carriesNoMoney reports whether a line is worth nothing on both of the
// supplier's figures: the net it computes to and the total it printed.
func carriesNoMoney(p POAProduct) bool {
	printed, _ := Deref(p.LineTotal)
	return math.Abs(p.Net()) < 0.005 && math.Abs(printed) < 0.005
}

// classifyNote describes a zero-value line. It blocks only while an ordered
// item is unacknowledged: then the line may be that item, described too
// differently to pair and printed without a price, and a person should look.
func classifyNote(i int, openItems []string) UnmatchedLine {
	l := UnmatchedLine{POAIndex: i, Kind: UnmatchedNote, Evidence: []string{"carries no money"}}
	if len(openItems) > 0 {
		l.Blocks = true
		l.Evidence = append(l.Evidence, "BC item(s) "+strings.Join(openItems, ", ")+" not acknowledged")
		l.Message = "a line carrying no money, while the order has an unacknowledged item it may be — check it is not that item"
		return l
	}
	l.Message = "a note carrying no money (specification, option or blank line); every ordered item is acknowledged, so it does not block"
	return l
}

// classifyMoneyLine describes a line that carries money. It always blocks;
// the evidence decides only whether it is reported as a charge or a product.
//
// A charge resembles no BC product — below DescThreshold against every one —
// and either prints no product code or states a percentage. The percentage is
// needed because the structural code test reads Port2 as a code.
func classifyMoneyLine(i int, p POAProduct, pairs []pairing, goods float64, cfg Config) UnmatchedLine {
	l := UnmatchedLine{POAIndex: i, Blocks: true}
	net := p.Net()

	// A line that computes to nothing but prints a total is a misread
	// product, not a charge: PF131507's Rowen Footstool came through at qty
	// 0 with 246.00 printed.
	if math.Abs(net) < 0.005 {
		printed, _ := Deref(p.LineTotal)
		l.Kind = UnmatchedProduct
		l.Evidence = []string{fmt.Sprintf("computes to 0.00 from its quantity and price, but prints %.2f", printed)}
		l.Message = fmt.Sprintf("an acknowledged line printing %.2f with no counterpart on the order; its own figures disagree", printed)
		return l
	}

	code := productCodeIn(p.Text(), cfg)
	if code == "" {
		l.Evidence = append(l.Evidence, "prints no product code")
	} else {
		l.Evidence = append(l.Evidence, fmt.Sprintf("prints product code %q", code))
	}

	resemblesNone, best, bestLabel := resemblesNoBCProduct(i, pairs, cfg)
	switch {
	case resemblesNone:
		l.Evidence = append(l.Evidence, fmt.Sprintf("resembles no BC product (best similarity %.2f)", best))
	case bestLabel != "":
		l.Evidence = append(l.Evidence, fmt.Sprintf("resembles BC %s (similarity %.2f)", bestLabel, best))
	default:
		l.Evidence = append(l.Evidence, "description similarity was not calculated")
	}

	pct, stated := statedPercent(p.Text())
	if stated && goods > 0 {
		expected := goods * pct / 100
		if math.Abs(net-expected) < cfg.MoneyTolerance {
			l.Evidence = append(l.Evidence, fmt.Sprintf("states %g%%, and %g%% of the paired goods (%.2f) is %.2f — agrees", pct, pct, goods, expected))
		} else {
			l.Evidence = append(l.Evidence, fmt.Sprintf("states %g%%, but %g%% of the paired goods (%.2f) is %.2f, not %.2f", pct, pct, goods, expected, net))
		}
	} else if stated {
		l.Evidence = append(l.Evidence, fmt.Sprintf("states %g%%, but no goods paired to check it against", pct))
	}

	if resemblesNone && (code == "" || stated) {
		l.Kind = UnmatchedCharge
		l.Message = fmt.Sprintf("a supplier charge of %+.2f that the order does not hold — agree it before accepting", net)
		return l
	}
	l.Kind = UnmatchedProduct
	l.Message = fmt.Sprintf("an acknowledged line worth %.2f with no counterpart on the order", net)
	return l
}

// resemblesNoBCProduct reports whether POA line i scores below DescThreshold
// against every BC product, and its best score. A missing embedding is no
// evidence, so it never counts as resembling nothing.
func resemblesNoBCProduct(i int, pairs []pairing, cfg Config) (none bool, best float64, bestLabel string) {
	none, seen := true, false
	for _, p := range pairs {
		if p.poaIndex != i {
			continue
		}
		if p.ev.Similarity == nil {
			return false, 0, ""
		}
		seen = true
		if s := *p.ev.Similarity; s > best || bestLabel == "" {
			best, bestLabel = s, p.bc.Label()
		}
		if *p.ev.Similarity >= cfg.DescThreshold {
			none = false
		}
	}
	return none && seen, best, bestLabel
}

// statedPercent returns the first percentage printed in s.
func statedPercent(s string) (float64, bool) {
	m := percentPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// pairedGoods is the supplier's net value of the product lines that paired,
// each counted once. A POA charge that paired with a BC charge line is not
// goods.
func pairedGoods(final []ProductResult) float64 {
	var goods float64
	counted := map[int]bool{}
	for _, m := range final {
		if counted[m.POAIndex] || isChargeLine(m.BC) {
			continue
		}
		counted[m.POAIndex] = true
		goods += m.POA.Net()
	}
	return goods
}

// addChargeShare extends a failed totals message with the part of the
// difference that is supplier charges, so "differs by +126.00" reads as
// charges to agree rather than a price dispute on the goods.
func (t *TotalsCheckResult) addChargeShare(poa POAOrder, lines []UnmatchedLine) {
	if t.Failed() {
		t.Message += chargeShare(poa, lines)
	}
}

// chargeShare explains how much of a total difference is supplier charges the
// order does not hold, naming them.
func chargeShare(poa POAOrder, lines []UnmatchedLine) string {
	var sum float64
	var labels []string
	for _, l := range lines {
		if l.Kind != UnmatchedCharge {
			continue
		}
		p := poa.Products[l.POAIndex]
		sum += p.Net()
		name := strings.TrimSpace(p.Code)
		if name == "" {
			name = strings.TrimSpace(p.Description)
		}
		labels = append(labels, fmt.Sprintf("%s %.2f", name, p.Net()))
	}
	if len(labels) == 0 {
		return ""
	}
	return fmt.Sprintf("; %+.2f is %d supplier charge line(s) the order does not hold (%s)",
		sum, len(labels), strings.Join(labels, ", "))
}
