package segment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// istanbulCartAbandoner is a customer the example brief rule selects (seed.md §3.3 defaults).
func istanbulCartAbandoner() segment.Facts {
	return segment.Facts{
		City:          "İstanbul",
		Language:      "tr",
		LoyaltyTier:   "bronze",
		LastCartAddAt: ptrTime(refTime.Add(-days(7))),
		CartAdds30d:   1,
	}
}

// REQ-AUD-00, REQ-AUD-01
// The example brief rule (segment-dsl.md §7.1) evaluated in memory, including the 29/30/31-day
// edges of seed.md §3.3: the lower bound ref − 30×24h is inclusive (segment-dsl.md §4).
func TestMatch_ExampleBriefRule(t *testing.T) {
	t.Parallel()
	r := mustRule(t, doc(exampleRule))
	tests := []struct {
		name   string
		modify func(f *segment.Facts)
		want   bool
	}{
		{name: "Istanbul cart add 7 days ago without purchase matches", modify: func(*segment.Facts) {}, want: true},
		{name: "cart add exactly 29 days ago matches", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime.Add(-days(29))) }, want: true},
		{name: "cart add exactly 30 days ago matches (inclusive bound)", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime.Add(-days(30))) }, want: true},
		{name: "cart add 30 days and 1 second ago does not match", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime.Add(-days(30) - time.Second)) }, want: false},
		{name: "cart add exactly 31 days ago does not match", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime.Add(-days(31))) }, want: false},
		{name: "cart add at the reference time matches", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime) }, want: true},
		{name: "cart add after the reference time does not match", modify: func(f *segment.Facts) { f.LastCartAddAt = ptrTime(refTime.Add(time.Second)) }, want: false},
		{name: "never added to cart does not match", modify: func(f *segment.Facts) { f.LastCartAddAt = nil; f.CartAdds30d = 0 }, want: false},
		{name: "a purchase in the last 30 days excludes", modify: func(f *segment.Facts) {
			f.Orders30d = 1
			f.TotalOrders = 1
			f.LastPurchaseAt = ptrTime(refTime.Add(-days(5)))
		}, want: false},
		{name: "purchase before the cart add within 30 days still excludes", modify: func(f *segment.Facts) {
			f.LastCartAddAt = ptrTime(refTime.Add(-days(10)))
			f.LastPurchaseAt = ptrTime(refTime.Add(-days(20)))
			f.Orders30d = 1
			f.TotalOrders = 1
		}, want: false},
		{name: "an old purchase outside 30 days does not exclude", modify: func(f *segment.Facts) {
			f.LastPurchaseAt = ptrTime(refTime.Add(-days(60)))
			f.TotalOrders = 3
			f.TotalSpent = 450.50
		}, want: true},
		{name: "another city does not match", modify: func(f *segment.Facts) { f.City = "Ankara" }, want: false},
		{name: "city comparison is case-sensitive", modify: func(f *segment.Facts) { f.City = "istanbul" }, want: false},
		{name: "ASCII spelling of the city does not match", modify: func(f *segment.Facts) { f.City = "Istanbul" }, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := istanbulCartAbandoner()
			tc.modify(&f)
			got, err := segment.Match(r, f, refTime)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

// REQ-AUD-01
// Operator semantics of segment-dsl.md §3/§4 (same semantics as Compile + SQL).
func TestMatch_OperatorSemantics(t *testing.T) {
	t.Parallel()
	base := segment.Facts{
		City:           "İzmir",
		Language:       "en",
		LoyaltyTier:    "gold",
		LastCartAddAt:  ptrTime(refTime.Add(-days(3))),
		LastPurchaseAt: ptrTime(refTime.Add(-days(90))),
		Orders30d:      0,
		CartAdds30d:    2,
		TotalOrders:    4,
		TotalSpent:     1000,
	}
	noPurchase := base
	noPurchase.LastPurchaseAt = nil
	tests := []struct {
		name  string
		group string
		facts segment.Facts
		want  bool
	}{
		{name: "neq on another city matches", group: all(cond("city", "neq", `"Ankara"`)), facts: base, want: true},
		{name: "neq on the same city does not match", group: all(cond("city", "neq", `"İzmir"`)), facts: base, want: false},
		{name: "in matches a member", group: all(cond("city", "in", `["Ankara","İzmir"]`)), facts: base, want: true},
		{name: "not_in excludes a member", group: all(cond("city", "not_in", `["Ankara","İzmir"]`)), facts: base, want: false},
		{name: "enum eq", group: all(cond("language", "eq", `"en"`)), facts: base, want: true},
		{name: "enum in", group: all(cond("loyalty_tier", "in", `["gold","platinum"]`)), facts: base, want: true},
		{name: "between includes the lower bound", group: all(cond("total_spent", "between", `{"min":1000,"max":5000}`)), facts: base, want: true},
		{name: "between includes the upper bound", group: all(cond("total_orders", "between", `{"min":1,"max":4}`)), facts: base, want: true},
		{name: "between excludes above the upper bound", group: all(cond("total_orders", "between", `{"min":1,"max":3}`)), facts: base, want: false},
		{name: "gt is strict", group: all(cond("cart_adds_30d", "gt", "2")), facts: base, want: false},
		{name: "gte includes equality", group: all(cond("cart_adds_30d", "gte", "2")), facts: base, want: true},
		{name: "lt is strict", group: all(cond("total_spent", "lt", "1000")), facts: base, want: false},
		{name: "lte includes equality", group: all(cond("total_spent", "lte", "1000")), facts: base, want: true},
		{name: "older_than matches an older event", group: all(cond("last_purchase_at", "older_than", `{"days":60}`)), facts: base, want: true},
		{name: "older_than exactly N days does not match (strict)", group: all(cond("last_purchase_at", "older_than", `{"days":90}`)), facts: base, want: false},
		{name: "older_than does not match NULL", group: all(cond("last_purchase_at", "older_than", `{"days":1}`)), facts: noPurchase, want: false},
		{name: "within_last does not match NULL", group: all(cond("last_purchase_at", "within_last", `{"days":3650}`)), facts: noPurchase, want: false},
		{name: "is_null matches NULL", group: all(cond("last_purchase_at", "is_null", "")), facts: noPurchase, want: true},
		{name: "is_null does not match a value", group: all(cond("last_purchase_at", "is_null", "")), facts: base, want: false},
		{name: "is_not_null matches a value", group: all(cond("last_purchase_at", "is_not_null", "null")), facts: base, want: true},
		{name: "not inverts a condition", group: all(not(cond("last_purchase_at", "is_null", ""))), facts: base, want: true},
		{name: "any matches when one child matches", group: anyOf(cond("city", "eq", `"Ankara"`), cond("loyalty_tier", "eq", `"gold"`)), facts: base, want: true},
		{name: "any fails when no child matches", group: anyOf(cond("city", "eq", `"Ankara"`), cond("loyalty_tier", "eq", `"bronze"`)), facts: base, want: false},
		{name: "all fails when one child fails", group: all(cond("city", "eq", `"İzmir"`), cond("language", "eq", `"tr"`)), facts: base, want: false},
		{name: "SQL text as a value matches nobody", group: all(cond("city", "eq", jsonString("'; DROP TABLE customers; --"))), facts: base, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := segment.Match(mustRule(t, doc(tc.group)), tc.facts, refTime)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

// REQ-AUD-01
// Match refuses an invalid rule with Validate's error.
func TestMatch_InvalidRuleIsRejected(t *testing.T) {
	t.Parallel()
	r, err := segment.Parse([]byte(doc(all(cond("sms_opt_in", "eq", "true")))))
	if err != nil {
		t.Fatalf("Parse() error = %v (structure is valid)", err)
	}
	_, err = segment.Match(r, istanbulCartAbandoner(), refTime)
	if !errors.Is(err, segment.ErrUnknownField) {
		t.Fatalf("Match() error = %v, want %v", err, segment.ErrUnknownField)
	}
}
