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

// REQ-AUD-02
// SQL text inside a value is only a value: it matches nobody.
func TestMatch_OperatorSemantics(t *testing.T) {
	t.Parallel()
	base := segment.Facts{
		City:           "İzmir",
		Language:       "en",
		LoyaltyTier:    "gold",
		LastCartAddAt:  ptrTime(refTime.Add(-days(3))),
		LastPurchaseAt: ptrTime(refTime.Add(-days(90))),
		CartAdds30d:    2,
		TotalOrders:    4,
		TotalSpent:     1000,
	}
	tests := []struct {
		name  string
		group string
		facts segment.Facts
		want  bool
	}{
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
