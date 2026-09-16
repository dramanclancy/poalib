// Column specifications for the workbook.
//
// Each column carries its own documentation, and the Definitions sheet is
// generated from these same specs — so a column can never exist without an
// explanation, and an explanation can never describe a column that was
// renamed or removed.
package excel

import (
	"fmt"

	"github.com/dramanclancy/poalib/domain"
)

// column is one column of the Matches sheet: how to render it, and how to
// explain it to somebody who did not write the engine.
type column struct {
	Header string
	// Meaning: what the column is.
	Meaning string
	// Source: which data the value is read from.
	Source string
	// Calc: how the value is produced.
	Calc string
	// Decision: what it changes about matching or review.
	Decision string
	// Value renders the cell.
	Value func(m domain.ProductResult) any
}

func status(c domain.CheckResult) any { return string(c.Status) }

func optNum(p *float64) any {
	if p == nil {
		return nil // blank cell: not calculated is not zero
	}
	return *p
}

// matchColumns is the Matches sheet, in reading order: what the supplier said,
// what BC says, why the two were paired, and what each check concluded.
func matchColumns(cfg domain.Config) []column {
	return []column{
		{
			Header:   "POA Line",
			Meaning:  "Position of this line in the supplier's acknowledgement, counting from 0.",
			Source:   "The order the lines were extracted from the PDF.",
			Calc:     "Assigned during extraction; stable for a given document.",
			Decision: "Identifies the line for review feedback. It has no effect on matching.",
			Value:    func(m domain.ProductResult) any { return m.POAIndex },
		},
		{
			Header:   "POA Product Code",
			Meaning:  "The supplier's own product reference as printed on the acknowledgement.",
			Source:   "The \"Product\" field of the acknowledgement line.",
			Calc:     "Taken verbatim. Some suppliers print a catalogue code here (NFX2S (A)); others print prose.",
			Decision: "Used for exact-code matching against BC's Model_No / Vendor_Item_No, and included in the text that is compared for similarity.",
			Value:    func(m domain.ProductResult) any { return m.POA.Code },
		},
		{
			Header:   "POA Description (compared)",
			Meaning:  "The exact supplier text the engine compared — code and all description fields joined.",
			Source:   "The acknowledgement line's Product, Description, Description 2 and Description 3 fields.",
			Calc:     "Joined with spaces. Handedness words (LHF/RHF and equivalents) are removed before similarity is measured, because orientation is checked separately.",
			Decision: "This is one half of the description similarity. If it looks wrong, the extraction model is the thing to fix, not the engine.",
			Value:    func(m domain.ProductResult) any { return m.Description.POADescription },
		},
		{
			Header:   "POA Qty",
			Meaning:  "Quantity the supplier says it will deliver.",
			Source:   "The acknowledgement line's Qty field.",
			Calc:     "Taken verbatim.",
			Decision: "Compared against BC Qty by the quantity check; a small part of the pairing score.",
			Value:    func(m domain.ProductResult) any { return m.POA.Qty },
		},
		{
			Header:   "POA Unit Price",
			Meaning:  "Price per unit the supplier states, before discount.",
			Source:   "The acknowledgement line's unit price field.",
			Calc:     "Taken verbatim.",
			Decision: "Feeds POA Net, and confirms pack ratios when the two sides count differently.",
			Value:    func(m domain.ProductResult) any { return m.POA.UnitPrice },
		},
		{
			Header:   "POA Discount %",
			Meaning:  "Percentage discount the supplier states.",
			Source:   "The acknowledgement line's discount percentage field.",
			Calc:     "Taken verbatim.",
			Decision: "Used for POA Net only when no cash discount is printed — the two are one discount expressed two ways and are never added together.",
			Value:    func(m domain.ProductResult) any { return m.POA.DiscountPercent },
		},
		{
			Header:   "POA Discount Amount",
			Meaning:  "Cash discount the supplier states.",
			Source:   "The acknowledgement line's discount amount field.",
			Calc:     "Taken verbatim.",
			Decision: "Takes precedence over the percentage when both are present: it is what posts, and it survives rounding a recomputed percentage would not.",
			Value:    func(m domain.ProductResult) any { return m.POA.DiscountAmount },
		},
		{
			Header:   "POA Line Total (printed)",
			Meaning:  "The line total the supplier printed. Blank means the supplier prints no such column.",
			Source:   "The acknowledgement line's total field.",
			Calc:     "Taken verbatim. Blank is recorded as absent, never as zero.",
			Decision: "The arithmetic check compares it against POA Net. When blank that check reports \"no data\" and passes — a check that cannot be performed does not block.",
			Value:    func(m domain.ProductResult) any { return optNum(m.POA.LineTotal) },
		},
		{
			Header:   "POA Net",
			Meaning:  "What the line is worth according to the supplier, after discount.",
			Source:   "POA Qty, POA Unit Price and whichever discount field is populated.",
			Calc:     "Qty x Unit Price, minus the cash discount if there is one, otherwise less the discount percentage.",
			Decision: "The figure the price check compares against BC Net.",
			Value:    func(m domain.ProductResult) any { return m.Price.POAPrice },
		},

		{
			Header:   "BC Item No",
			Meaning:  "The Business Central item number this line was paired with.",
			Source:   "The purchase order line (lineObjectNumber).",
			Calc:     "Taken verbatim. Blank on account, G/L, fixed asset and resource lines.",
			Decision: "Identifies the BC line for review feedback and for any later master-data fix.",
			Value:    func(m domain.ProductResult) any { return m.BC.ItemNo },
		},
		{
			Header:   "BC Description (compared)",
			Meaning:  "The exact BC text the engine compared. This is the merged description, not the raw purchase order line.",
			Source:   "The item's display names, then its enrichment record (extra description and range code), then every comment line attached to the purchase order line.",
			Calc:     "Joined with spaces, skipping any comment that merely repeats the item name. Handedness words are removed before similarity is measured.",
			Decision: "This is the other half of the description similarity. The raw purchase order line text appears on the BC Lines sheet and is deliberately NOT what was compared.",
			Value:    func(m domain.ProductResult) any { return m.Description.BCDescription },
		},
		{
			Header:   "BC Qty",
			Meaning:  "Quantity the purchase order asks for.",
			Source:   "The purchase order line.",
			Calc:     "Taken verbatim.",
			Decision: "Compared against POA Qty by the quantity check.",
			Value:    func(m domain.ProductResult) any { return m.BC.Qty },
		},
		{
			Header:   "BC Unit Cost",
			Meaning:  "Cost per unit on the purchase order, before discount.",
			Source:   "The purchase order line (directUnitCost).",
			Calc:     "Taken verbatim.",
			Decision: "Confirms pack ratios when the supplier counts packs and BC counts singles.",
			Value:    func(m domain.ProductResult) any { return m.BC.UnitCost },
		},
		{
			Header:   "BC Net",
			Meaning:  "What the line is worth according to Business Central.",
			Source:   "The purchase order line (netAmount).",
			Calc:     "Taken verbatim — BC's own calculation, not recomputed here.",
			Decision: "The figure the price check compares POA Net against.",
			Value:    func(m domain.ProductResult) any { return m.Price.BCPrice },
		},
		{
			Header:   "Price Difference",
			Meaning:  "How much the supplier's figure differs from BC's, signed.",
			Source:   "POA Net and BC Net.",
			Calc:     "POA Net minus BC Net. Positive means the supplier is charging more than the order says.",
			Decision: fmt.Sprintf("A difference of %.2f or more fails the price check and always needs a human — a price difference can never be approved into a rule.", cfg.MoneyTolerance),
			Value:    func(m domain.ProductResult) any { return m.Price.Difference },
		},

		{
			Header:  "Pairing Score",
			Meaning: "How strongly this POA line and this BC line were judged to be the same product.",
			Source:  "Description similarity and quantity agreement only. Price and handedness are deliberately excluded.",
			Calc: fmt.Sprintf("%.2f x description similarity, plus %.2f when the quantities agree, plus %.2f when a supplier code matched exactly.",
				cfg.DescWeight, cfg.QtyBonus, cfg.CodeMatchBonus),
			Decision: "The pairing chosen for the whole order is the combination of lines with the highest total score, each line used once. The score never refuses a pairing — it only ranks them.",
			Value:    func(m domain.ProductResult) any { return m.Score },
		},
		{
			Header:  "DescScore",
			Meaning: "How similar the two descriptions are in meaning, from -1 to 1 (in practice roughly 0.2 to 0.9).",
			Source:  "The POA Description (compared) and BC Description (compared) columns on this row.",
			Calc:    "Each description is sent to an embedding model, which turns it into a list of numbers representing its meaning; DescScore is the cosine similarity between the two lists. It is not word overlap — \"2.5 Seater End\" and \"2.5 STR END\" can score highly without sharing words. Blank means it was not calculated.",
			Decision: fmt.Sprintf("Below %.2f the engine reports that it cannot identify the line from the descriptions, and the line is flagged. High is not proof of a correct pairing: lines from one order share fabric and leg wording, so most score high. The margin column is the sharper test.",
				cfg.DescThreshold),
			Value: func(m domain.ProductResult) any { return optNum(m.Description.SimilarityScore) },
		},
		{
			Header:  "DescMargin",
			Meaning: "How far ahead of the runner-up this pairing scored.",
			Source:  "This row's DescScore, and the best DescScore the same POA line reached against any BC line still available to it.",
			Calc:    "DescScore minus the runner-up's DescScore. Blank means there was no alternative to compare against. Lines already paired to another POA line are excluded — they were never really available.",
			Decision: fmt.Sprintf("Below %.2f the pairing is reported as ambiguous and flagged, because resembling two lines almost equally is not identification. A negative value means the runner-up actually scored higher and was taken by a different line.",
				cfg.MarginThreshold),
			Value: func(m domain.ProductResult) any { return optNum(m.Description.Margin) },
		},
		{
			Header:   "Runner-Up",
			Meaning:  "The BC line this POA line most resembled, apart from the one it was paired with.",
			Source:   "The candidate scoring for this POA line.",
			Calc:     "The available BC line with the highest DescScore other than the chosen one.",
			Decision: "Context for an ambiguous pairing: it names what a reviewer should check the line against.",
			Value:    func(m domain.ProductResult) any { return m.Description.RunnerUp },
		},
		{
			Header:   "Code Match",
			Meaning:  "Whether the supplier printed a Business Central catalogue code for this item verbatim.",
			Source:   "The POA line's text, against the item's Model_No and Vendor_Item_No from item enrichment.",
			Calc:     fmt.Sprintf("Both sides are reduced to letters and digits and compared by containment. Codes shorter than %d characters are ignored as too likely to appear by chance. Vendor_Item_No only counts when the item belongs to this order's vendor.", cfg.MinCodeLen),
			Decision: "A code match settles identity outright, even when DescScore is low — a supplier's prose can describe a different attribute of the same item. It never suppresses a price, quantity or handedness problem.",
			Value:    func(m domain.ProductResult) any { return m.Description.CodeMatch },
		},
		{
			Header:   "Code Match Source",
			Meaning:  "Which BC field matched: Model_No, Vendor_Item_No, or both.",
			Source:   "Item enrichment.",
			Calc:     "Recorded when a code match is found. It stays filled in even if the match was later withdrawn as ambiguous, so the reason is auditable.",
			Decision: "Audit only. Vendor_Item_No matches are the ones a feedback loop would eventually create more of.",
			Value:    func(m domain.ProductResult) any { return m.Description.CodeMatchSource },
		},
		{
			Header:   "Code Data State",
			Meaning:  "What is actually known about Business Central's codes for this item.",
			Source:   "Whether item enrichment ran for this order, and what it returned for this item.",
			Calc:     "\"code_on_file\" — BC holds at least one code. \"no_code_on_file\" — enrichment ran and the item has neither. \"unknown\" — enrichment did not run or failed, so nothing can be said either way.",
			Decision: "Prevents the report blaming missing master data for a lookup that never happened. Only \"no_code_on_file\" is a genuine master-data gap worth acting on.",
			Value:    func(m domain.ProductResult) any { return m.Description.CodeDataState },
		},
		{
			Header:   "POA Code Candidate",
			Meaning:  "The token in the supplier's text that looks like a product code, whether or not it matched anything.",
			Source:   "The POA Description (compared) column.",
			Calc:     fmt.Sprintf("The first word of %d or more characters that starts with a letter and contains a digit. Measurements are excluded because they lead with their number (150CM).", cfg.MinProductCodeLen),
			Decision: "The lookup key a confirmed match would be recorded under, and what the missing-code message quotes.",
			Value:    func(m domain.ProductResult) any { return m.Description.POACode },
		},
		{
			Header:   "Pack Ratio",
			Meaning:  "How many BC units the supplier counts as one.",
			Source:   "POA Qty, BC Qty and both unit prices.",
			Calc:     "1 when both sides count the same unit. Above 1 when BC's quantity is a whole multiple of the supplier's AND the unit prices confirm the same multiple. 0 when the quantities do not reconcile at all.",
			Decision: "Lets a genuine packs-versus-singles difference pass the quantity check, while a real quantity change still fails it — the price has to agree with the count.",
			Value:    func(m domain.ProductResult) any { return m.Quantity.PackRatio },
		},

		{
			Header:   "Price Check",
			Meaning:  "Whether the money agrees.",
			Source:   "POA Net against BC Net.",
			Calc:     fmt.Sprintf("match when the difference is under %.2f, otherwise mismatch. There is no \"no data\" case — both sides always state a figure.", cfg.MoneyTolerance),
			Decision: "A mismatch blocks automatic acceptance and can never be approved into a rule. Money is a control.",
			Value:    func(m domain.ProductResult) any { return status(m.Price.CheckResult) },
		},
		{
			Header:   "Quantity Check",
			Meaning:  "Whether the two sides mean the same amount of goods.",
			Source:   "POA Qty, BC Qty, both unit prices.",
			Calc:     "match when the quantities are equal, or when they differ by a pack ratio the unit prices confirm. Otherwise mismatch.",
			Decision: "A mismatch blocks automatic acceptance and can never be approved into a rule. Quantity is a control.",
			Value:    func(m domain.ProductResult) any { return status(m.Quantity.CheckResult) },
		},
		{
			Header:   "Arithmetic Check",
			Meaning:  "Whether the supplier's own figures add up. Nothing to do with Business Central.",
			Source:   "POA Net against POA Line Total (printed).",
			Calc:     fmt.Sprintf("match when they agree within %.2f, mismatch when they do not, unknown when the supplier prints no line total.", cfg.MoneyTolerance),
			Decision: "A mismatch usually means the document was misread during extraction rather than that the supplier is wrong — it points at the extraction model. unknown passes.",
			Value:    func(m domain.ProductResult) any { return status(m.Arithmetic.CheckResult) },
		},
		{
			Header:   "Orientation Check",
			Meaning:  "Whether handedness agrees — which way a corner unit or chaise faces.",
			Source:   "Handedness words in the POA text, and in the BC line description and its comment lines.",
			Calc:     "Each side is read independently. A BC line saying \"RHF/LHF\" is a heading offering both, not a specification, so the walk keeps looking and takes the definite reading from a comment line. match needs two definite, equal readings; unknown means neither side is handed (surcharges, carriage); anything else is a mismatch.",
			Decision: "A mismatch blocks automatic acceptance. It is learnable only as a supplier-wide convention, never as an exemption for one line.",
			Value:    func(m domain.ProductResult) any { return status(m.Orientation.CheckResult) },
		},
		{
			Header:   "POA Orientation",
			Meaning:  "The handedness read from the supplier's text.",
			Source:   "The POA Description (compared) column.",
			Calc:     "LHF, RHF, LHF/RHF (both offered), AMBIGUOUS (two conflicting readings) or UNKNOWN (none stated).",
			Decision: "Half of the orientation check; shown so a reviewer can see what was read rather than only the verdict.",
			Value:    func(m domain.ProductResult) any { return m.Orientation.POAOrientation },
		},
		{
			Header:   "BC Orientation",
			Meaning:  "The handedness the purchase order specifies.",
			Source:   "The BC line description and its comment lines, in sequence order.",
			Calc:     "As above. Headings offering both hands are skipped in favour of a definite value further down.",
			Decision: "The other half of the orientation check.",
			Value:    func(m domain.ProductResult) any { return m.Orientation.BCOrientation },
		},
		{
			Header:   "Seat Check",
			Meaning:  "Whether the seat count the supplier states matches the one Business Central records.",
			Source:   "A seat count read from the POA text (\"3 seater\", \"2.5 STR\"), against BC's own seat count — No_of_Seats from item enrichment where the item record holds one, otherwise the count in BC's line description (\"4 Seater Sofa\").",
			Calc:     fmt.Sprintf("match when they agree, mismatch when they differ, unknown when either side has nothing to compare. A BC seat count below %g is not treated as a real seat count.", cfg.MinSeats),
			Decision: "A structural check independent of price: a supplier can print the wrong seat count at a price that matches perfectly. A mismatch blocks automatic acceptance. See Seat Data State for where BC's figure came from, or why a check did not run.",
			Value:    func(m domain.ProductResult) any { return status(m.Seats.CheckResult) },
		},
		{
			Header:   "Seat Data State",
			Meaning:  "Why the seat check reached its verdict — in particular, which kind of missing data stopped it.",
			Source:   "The enrichment state of the order, the item's record, and which source supplied BC's figure.",
			Calc:     "comparable; comparable_from_description; enrichment_not_run; enrichment_failed; no_item_record; not_recorded_on_item; implausible_on_item; not_stated_on_poa.",
			Decision: "Distinguishes a master-data gap (BC has no seat count for a real item) from a configuration problem (enrichment did not run at all). Only the first is worth someone's time in Business Central. comparable_from_description means the check did run, but on the seat count in BC's line description because the item record had none — the verdict is sound and the master-data gap is still real.",
			Value:    func(m domain.ProductResult) any { return m.Seats.DataState },
		},
		{
			Header:   "POA Seats",
			Meaning:  "Seat count stated by the supplier. Blank when none was stated.",
			Source:   "The POA Description (compared) column.",
			Calc:     "Read from patterns like \"3 seater\", \"2.5 STR\".",
			Decision: "Compared against BC Seats.",
			Value:    func(m domain.ProductResult) any { return optNum(m.Seats.POASeats) },
		},
		{
			Header:   "BC Seats",
			Meaning:  "Business Central's seat count for this line. Blank when neither the item record nor the line description states one.",
			Source:   "No_of_Seats from item enrichment where the item record holds one; otherwise read from BC's line description. Seat Data State says which.",
			Calc:     "Taken verbatim. A record with no value is left blank rather than shown as 0, because zero and absent mean different things.",
			Decision: "Compared against POA Seats. Blank always means \"cannot compare\", never \"zero seats\".",
			Value:    func(m domain.ProductResult) any { return optNum(m.Seats.BCSeats) },
		},
		{
			Header:  "Description Check",
			Meaning: "The identity verdict: are these two lines the same product?",
			Source:  "DescScore, DescMargin and Code Match on this row.",
			Calc: fmt.Sprintf("match when a code matched, or when DescScore is at least %.2f and DescMargin is at least %.2f. warning when identity holds but a code match was withdrawn as ambiguous. mismatch otherwise.",
				cfg.DescThreshold, cfg.MarginThreshold),
			Decision: "A mismatch blocks automatic acceptance. This is the check a reviewer's confirmation would eventually teach — unlike price and quantity, an identity judgement can become a rule.",
			Value:    func(m domain.ProductResult) any { return status(m.Description.CheckResult) },
		},
		{
			Header:   "Identity Note",
			Meaning:  "The description check's own explanation, in words.",
			Source:   "The description check.",
			Calc:     "Generated from whichever condition decided the verdict.",
			Decision: "This is the sentence a reviewer reads; the Review sheet repeats it as a discrepancy.",
			Value:    func(m domain.ProductResult) any { return m.Description.Message },
		},
		{
			Header:   "Accepted",
			Meaning:  "Whether this line passed every check without needing a human.",
			Source:   "All six checks on this row.",
			Calc:     "TRUE when no check returned mismatch. warning and unknown do not block — a check that cannot be performed passes.",
			Decision: "Every line must be TRUE, and every acknowledged line must have been paired, for the order to be accepted automatically.",
			Value:    func(m domain.ProductResult) any { return m.OK() },
		},
	}
}
