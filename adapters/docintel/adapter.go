// Translation from a Document Intelligence analysis into the domain.
//
// This is the only file that knows a batched order appears as "<field> - Page
// N", or that a supplier writes money as "1,377.00". Everything downstream
// sees domain.POAOrder.
package docintel

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/dramanclancy/poalib/domain"
)

// Scanner turns a supplier PDF into POA orders.
type Scanner struct {
	Client *DocIntelClient
	Log    func(format string, args ...any)
}

// Scan analyses pdf with the supplier's custom model and extracts one
// POAOrder per purchase order the file acknowledges.
func (s *Scanner) Scan(ctx context.Context, pdf []byte, modelID string) ([]domain.POAOrder, error) {
	result, err := s.Client.AnalyzeDocumentFromBytes(ctx, modelID, pdf)
	if err != nil {
		return nil, err
	}
	if len(result.Documents) == 0 {
		return nil, fmt.Errorf("no document recognised in file")
	}
	return s.extractOrders(result.Documents), nil
}

func (s *Scanner) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
		return
	}
	log.Printf(format, args...)
}

var pageFieldRe = regexp.MustCompile(`^(.+) - Page (\d+)$`)

// extractOrders returns one POAOrder per PO batched within the analysed file
// (WhiteMeadow send several acknowledgements to a PDF). It also tolerates
// Document Intelligence genuinely returning more than one document, by running
// the same per-instance expansion against each.
func (s *Scanner) extractOrders(docs []Document) []domain.POAOrder {
	var all []domain.POAOrder
	for _, doc := range docs {
		n := maxInstance(doc.Fields)
		for i := 1; i <= n; i++ {
			poNumber := fieldForInstance(doc.Fields, "Purchase Order No.", i)
			if poNumber.Content == "" {
				continue // unused instance slot
			}
			all = append(all, s.extractOrderAt(doc.Fields, i, poNumber.Content))
		}
	}
	return all
}

// maxInstance scans fields for the highest page-suffixed instance number that
// actually has content, telling us how many POs are batched in this file.
// Unused trailing slots show up as fields with no Content and no ValueArray.
func maxInstance(fields map[string]Field) int {
	max := 1
	for key, f := range fields {
		m := pageFieldRe.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		if f.Content == "" && len(f.ValueArray) == 0 {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// fieldForInstance returns the field for name at the given instance. Instance
// 1 uses the bare label; instance N>1 uses "<name> - Page N", since Document
// Intelligence labels must be unique and a field recurring for a later batched
// order cannot reuse the same label.
func fieldForInstance(fields map[string]Field, name string, instance int) Field {
	if instance == 1 {
		return fields[name]
	}
	return fields[fmt.Sprintf("%s - Page %d", name, instance)]
}

func (s *Scanner) extractOrderAt(fields map[string]Field, instance int, pf string) domain.POAOrder {
	delDate := fieldForInstance(fields, "Proposed Delivery Date", instance)
	reference := fieldForInstance(fields, "Reference Number", instance)
	totalExVAT := fieldForInstance(fields, "Total Order Amount - exVAT", instance)
	orderDetails := fieldForInstance(fields, "Order Details", instance)
	orderDiscount := fieldForInstance(fields, "Order Discount", instance)

	o := domain.POAOrder{
		PF:         pf,
		TotalExVAT: s.moneyPtr(pf, "total amount exVAT", totalExVAT.Content),
		// Taken off the ex-VAT total; "5%" is a rate, anything else an amount.
		OrderDiscount:          s.moneyPtr(pf, "order discount", orderDiscount.Content),
		OrderDiscountIsPercent: strings.Contains(orderDiscount.Content, "%"),
		ProposedDeliveryDate:   strings.TrimSpace(delDate.Content),
		// The label is sometimes captured along with the value ("Ack No:\n2332206").
		// Keeping only the last line gives the reference a human would quote.
		ReferenceNumber: lastLine(reference.Content),
		Products:        make([]domain.POAProduct, 0, len(orderDetails.ValueArray)),
	}

	for n, item := range orderDetails.ValueArray {
		vo := item.ValueObject
		if vo == nil {
			continue
		}
		p := domain.POAProduct{
			Code:            vo["Product"].Content,
			Description:     vo["Description"].Content,
			Description2:    vo["Description 2"].Content,
			Description3:    vo["Description 3"].Content,
			Qty:             int(s.money(pf, "qty", vo["Qty"].Content)),
			UnitPrice:       s.money(pf, "unit price", vo["Unit Product Price"].Content),
			DiscountPercent: s.money(pf, "discount %", vo["Discount%"].Content),
			DiscountAmount:  s.money(pf, "discount amount", vo["Discount Amount"].Content),
			// Compounds on the first discount; see POAProduct.Net.
			FurtherDiscountPercent: s.money(pf, "further discount %", vo["Further Discount %"].Content),
			FurtherDiscountAmount:  s.money(pf, "further discount amount", vo["Further Discount Amount"].Content),
			LineTotal:              s.moneyPtr(pf, "line total", vo["Total Product Qty Price"].Content),
		}
		if blankLine(p) {
			s.logf("extract: %s: dropped line %d, which carries no text, quantity or money", pf, n+1)
			continue
		}
		p.Code, p.Description = splitSpilledCode(p.Code, p.Description)
		o.Products = append(o.Products, p)
	}
	return o
}

// blankLine reports a row the model returned with nothing in it: no letter or
// digit in any text column, and no quantity or money. Kaymed and Parker Knoll
// layouts produce these ("-", or empty) between real lines; kept, each became
// an unmatched acknowledgement line, and any unmatched line blocks acceptance
// (PF131048 carried three, PF131425 eight).
func blankLine(p domain.POAProduct) bool {
	text := p.Code + p.Description + p.Description2 + p.Description3
	if strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
		return false
	}
	total, _ := domain.Deref(p.LineTotal)
	return p.Qty == 0 && p.UnitPrice == 0 && total == 0
}

// splitSpilledCode repairs a row whose whole text landed in the Product
// column with Description empty — "CA24 Natural Indulge", "S21115.031000000.00EC00CB York".
// Harrison Spinks, Kaymed and Parker Knoll layouts do this on most lines.
//
// A leading token shaped like a product code stays the code and the rest
// becomes the description; with no such token the whole text is description.
// The remainder must hold a real word, so a code with a suffix ("NFX2S (A)")
// is left whole. Text() reads the same words either way, so the embedding and
// code matching are unaffected; what changes is that the report's code and
// description columns say what they claim to.
func splitSpilledCode(code, desc string) (string, string) {
	code = strings.TrimSpace(code)
	if strings.TrimSpace(desc) != "" {
		return code, desc
	}
	cut := strings.IndexFunc(code, unicode.IsSpace)
	if cut < 0 {
		return code, desc
	}
	first, rest := code[:cut], strings.TrimSpace(code[cut:])
	if !hasWord(rest) {
		return code, desc
	}
	if domain.LooksLikeProductCode(first, domain.DefaultConfig()) {
		return first, rest
	}
	return "", code
}

// hasWord reports whether s holds a run of three or more letters.
func hasWord(s string) bool {
	run := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			if run++; run >= 3 {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

// lastLine returns the final non-empty line of s, trimmed. Document
// Intelligence sometimes captures a field's printed label above its value.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Money
// ---------------------------------------------------------------------------

// parseMoney converts extracted content like "1,377.00", "£1,377.00",
// "GBP 1,377.00" or the continental "1.377,00" / "672,00" to a value. The
// second return is false when the field is absent or present but unparseable.
func parseMoney(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "GBP")
	s = strings.TrimPrefix(s, "EUR")
	s = strings.TrimSuffix(s, "%")
	s = strings.Trim(s, "£€$ ")
	s = normaliseSeparators(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// normaliseSeparators rewrites s so the decimal mark is "." and there are no
// thousands separators. Stripping every comma used to read Ego Italiano's
// "672,00" as 67200 and "15,00" as a 1500% discount — valid numbers, so
// nothing logged and the line failed on price instead of on extraction.
//
// The last separator printed is the decimal mark when both appear. A lone
// comma is decimal unless exactly three digits follow it: "1,377" stays one
// thousand three hundred and seventy-seven, as it always parsed.
func normaliseSeparators(s string) string {
	comma, dot := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case comma >= 0 && dot >= 0 && comma > dot: // "1.377,00"
		return strings.Replace(strings.ReplaceAll(s, ".", ""), ",", ".", 1)
	case comma >= 0 && dot >= 0: // "1,377.00"
		return strings.ReplaceAll(s, ",", "")
	case comma >= 0 && strings.Count(s, ",") == 1 && len(s)-comma-1 != 3: // "672,00"
		return strings.Replace(s, ",", ".", 1)
	case comma >= 0: // "1,377", "1,234,567"
		return strings.ReplaceAll(s, ",", "")
	case strings.Count(s, ".") > 1: // "1.234.567"
		return strings.ReplaceAll(s, ".", "")
	}
	return s
}

// money parses a required field, logging loudly when content was present but
// unparseable — the failure mode that silently produced zeros before.
func (s *Scanner) money(pf, field, content string) float64 {
	v, ok := parseMoney(content)
	if !ok && strings.TrimSpace(content) != "" {
		s.logf("extract: %s: unparseable %s %q — treating as 0", pf, field, content)
	}
	return v
}

// moneyPtr parses an optional field. A blank field returns nil, not zero:
// several suppliers print no line-total column at all, and treating that as
// 0.00 failed every one of their lines.
func (s *Scanner) moneyPtr(pf, field, content string) *float64 {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	v, ok := parseMoney(content)
	if !ok {
		s.logf("extract: %s: unparseable %s %q — treating as absent", pf, field, content)
		return nil
	}
	return domain.Ptr(v)
}
