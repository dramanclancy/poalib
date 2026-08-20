package poa

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/dramanclancy/poalib/docintel"
)

type ProposedDeliveryDate struct {
	time.Time
}

var pageFieldRe = regexp.MustCompile(`^(.+) - Page (\d+)$`)

// maxInstance scans fields for the highest page-suffixed instance number
// that actually has content, telling us how many POs are batched in this
// file. Unused trailing slots (e.g. a model that supports up to 3 batched
// orders when only 2 are present) show up as fields with no Content and no
// ValueArray, and are ignored.
func maxInstance(fields map[string]docintel.Field) int {
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

// fieldForInstance returns the field for name at the given instance.
// Instance 1 uses the bare label; instance N>1 uses "<name> - Page N",
// since Document Intelligence custom-model labels must be unique and a
// field recurring for a later batched order can't reuse the same label.
func fieldForInstance(fields map[string]docintel.Field, name string, instance int) docintel.Field {
	if instance == 1 {
		return fields[name]
	}
	return fields[fmt.Sprintf("%s - Page %d", name, instance)]
}

// ExtractAllProducts extracts one Products per PO batched within the
// analyzed file (e.g. WhiteMeadow order acknowledgements sent several to a
// PDF). It also tolerates the case where Document Intelligence genuinely
// returns more than one entry in result.Documents, by running the same
// per-instance expansion against each document's fields.
func ExtractAllProducts(docs []docintel.Document) []Products {
	var all []Products
	for _, doc := range docs {
		n := maxInstance(doc.Fields)
		for i := 1; i <= n; i++ {
			poNumber := fieldForInstance(doc.Fields, "Purchase Order No.", i)
			if poNumber.Content == "" {
				continue // unused instance slot
			}
			all = append(all, extractProductsAt(doc.Fields, i, poNumber.Content))
		}
	}
	return all
}

// extractProductsAt builds one Products from the fields belonging to a
// single batched-order instance.
func extractProductsAt(fields map[string]docintel.Field, instance int, pf string) Products {
	delDate := fieldForInstance(fields, "Proposed Delivery Date", instance)
	reference := fieldForInstance(fields, "Reference Number", instance)
	totalExVAT := fieldForInstance(fields, "Total Order Amount - exVAT", instance)
	orderDetails := fieldForInstance(fields, "Order Details", instance)

	p := Products{
		PF:                   pf,
		TotalAmountexVAT:     mustMoneyPtr(pf, "total amount exVAT", totalExVAT.Content),
		ProposedDeliveryDate: delDate.Content,
		ReferenceNumber:      reference.Content,
		Products:             make([]OrderDetail, 0, len(orderDetails.ValueArray)),
	}

	for _, item := range orderDetails.ValueArray {
		vo := item.ValueObject
		if vo == nil {
			continue
		}
		p.Products = append(p.Products, OrderDetail{
			Product:              vo["Product"].Content,
			Description:          vo["Description"].Content,
			Description2:         vo["Description 2"].Content,
			Description3:         vo["Description 3"].Content,
			Qty:                  int(mustMoney(pf, "qty", vo["Qty"].Content)),
			UnitProductPrice:     mustMoney(pf, "unit price", vo["Unit Product Price"].Content),
			DiscountPercent:      mustMoney(pf, "discount %", vo["Discount%"].Content),
			DiscountFigure:       mustMoney(pf, "discount amount", vo["Discount Amount"].Content),
			TotalProductQtyPrice: mustMoneyPtr(pf, "line total", vo["Total Product Qty Price"].Content),
		})
	}
	return p
}
