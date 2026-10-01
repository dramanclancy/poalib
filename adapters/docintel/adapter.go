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

	o := domain.POAOrder{
		PF:                   pf,
		TotalExVAT:           s.moneyPtr(pf, "total amount exVAT", totalExVAT.Content),
		ProposedDeliveryDate: strings.TrimSpace(delDate.Content),
		// The label is sometimes captured along with the value ("Ack No:\n2332206").
		// Keeping only the last line gives the reference a human would quote.
		ReferenceNumber: lastLine(reference.Content),
		Products:        make([]domain.POAProduct, 0, len(orderDetails.ValueArray)),
	}

	for _, item := range orderDetails.ValueArray {
		vo := item.ValueObject
		if vo == nil {
			continue
		}
		o.Products = append(o.Products, domain.POAProduct{
			Code:            vo["Product"].Content,
			Description:     vo["Description"].Content,
			Description2:    vo["Description 2"].Content,
			Description3:    vo["Description 3"].Content,
			Qty:             int(s.money(pf, "qty", vo["Qty"].Content)),
			UnitPrice:       s.money(pf, "unit price", vo["Unit Product Price"].Content),
			DiscountPercent: s.money(pf, "discount %", vo["Discount%"].Content),
			DiscountAmount:  s.money(pf, "discount amount", vo["Discount Amount"].Content),
			LineTotal:       s.moneyPtr(pf, "line total", vo["Total Product Qty Price"].Content),
		})
	}
	return o
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
