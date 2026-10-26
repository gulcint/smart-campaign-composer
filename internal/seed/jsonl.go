package seed

import (
	"bytes"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// writeJSONL renders the export format of seed.md §4: customers (id ascending) then events
// (id ascending), compact JSON with keys in the documented order, "\n" after every line.
func (d *Dataset) writeJSONL(w io.Writer) error {
	cs := make([]*customer, len(d.customers))
	var evs []*event
	for i := range d.customers {
		cs[i] = &d.customers[i]
		for j := range d.customers[i].events {
			evs = append(evs, &d.customers[i].events[j])
		}
	}
	slices.SortFunc(cs, func(a, b *customer) int { return strings.Compare(a.id, b.id) })
	slices.SortFunc(evs, func(a, b *event) int { return strings.Compare(a.id, b.id) })

	var b bytes.Buffer
	for _, c := range cs {
		b.WriteString(`{"kind":"customer","id":`)
		str(&b, c.id)
		b.WriteString(`,"first_name":`)
		str(&b, c.firstName)
		b.WriteString(`,"last_name":`)
		str(&b, c.lastName)
		b.WriteString(`,"email":`)
		optStr(&b, c.email)
		b.WriteString(`,"phone":`)
		optStr(&b, c.phone)
		b.WriteString(`,"city":`)
		str(&b, c.city)
		b.WriteString(`,"language":`)
		str(&b, c.language)
		b.WriteString(`,"loyalty_tier":`)
		str(&b, c.loyaltyTier)
		b.WriteString(`,"sms_opt_in":`)
		b.WriteString(strconv.FormatBool(c.smsOptIn))
		b.WriteString(`,"sms_opt_in_at":`)
		optTime(&b, c.smsOptInAt)
		b.WriteString(`,"email_opt_in":`)
		b.WriteString(strconv.FormatBool(c.emailOptIn))
		b.WriteString(`,"email_opt_in_at":`)
		optTime(&b, c.emailOptInAt)
		b.WriteString(`,"created_at":`)
		optTime(&b, &c.createdAt)
		b.WriteString(`,"last_seen_at":`)
		optTime(&b, c.lastSeenAt)
		b.WriteString("}\n")
	}
	for _, e := range evs {
		b.WriteString(`{"kind":"event","id":`)
		str(&b, e.id)
		b.WriteString(`,"customer_id":`)
		str(&b, e.customerID)
		b.WriteString(`,"type":`)
		str(&b, e.typ)
		b.WriteString(`,"occurred_at":`)
		optTime(&b, &e.occurredAt)
		b.WriteString(`,"amount":`)
		if e.typ == "purchase" {
			b.WriteString(amountString(e.amountCents))
		} else {
			b.WriteString("null")
		}
		b.WriteString(`,"product_category":`)
		optStr(&b, e.category)
		b.WriteString("}\n")
	}
	_, err := w.Write(b.Bytes())
	return err
}

// amountString renders cents as a JSON number with exactly two decimals ("149.90").
func amountString(cents int64) string {
	return strconv.FormatInt(cents/100, 10) + "." + leftPad2(cents%100)
}

func leftPad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

func str(b *bytes.Buffer, s string) {
	v, _ := marshalNoEscape(s) // a string always marshals
	b.Write(v)
}

func optStr(b *bytes.Buffer, s *string) {
	if s == nil {
		b.WriteString("null")
		return
	}
	str(b, *s)
}

func optTime(b *bytes.Buffer, t *time.Time) {
	if t == nil {
		b.WriteString("null")
		return
	}
	str(b, t.UTC().Format(time.RFC3339))
}
