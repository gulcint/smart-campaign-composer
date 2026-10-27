package segment_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// hoursForm matches the whitespace-free, lower-cased §5 rule 4 form: placeholder, optional type
// cast, multiplier 24 (the cast is an implementation detail, not part of the contract).
var hoursForm = regexp.MustCompile(`make_interval\(hours=>\$\d+(::[a-z]+)?\*24\)`)

// compact removes all whitespace (segment-dsl.md §5: whitespace in Where is not part of the contract).
func compact(s string) string { return strings.Join(strings.Fields(s), "") }

// REQ-AUD-01, REQ-AUD-02
// Relative windows are whole 24-hour days (segment-dsl.md §4, §5 rule 4): the integer day count is
// passed unchanged as its own parameter (not pre-multiplied, not a time value), and within_last uses
// the hours-based interval form "make_interval(hours => $n * 24)" — never a calendar-day interval,
// which would shift by an hour across a DST change in the session time zone.
func TestCompile_DayWindowsAreWholeTwentyFourHours(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		field, op string
		days      int
		hoursForm bool // §5 rule 4 spells the SQL form out for within_last only
	}{
		{name: "within_last 30 on last_cart_add_at", field: "last_cart_add_at", op: "within_last", days: 30, hoursForm: true},
		{name: "within_last 1 on last_purchase_at", field: "last_purchase_at", op: "within_last", days: 1, hoursForm: true},
		{name: "within_last 3650 on last_cart_add_at", field: "last_cart_add_at", op: "within_last", days: 3650, hoursForm: true},
		{name: "older_than 90 on last_purchase_at", field: "last_purchase_at", op: "older_than", days: 90},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value := `{"days":` + strconv.Itoa(tc.days) + `}`
			c, err := segment.Compile(mustRule(t, doc(all(cond(tc.field, tc.op, value)))), refTime)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if len(c.Args) != 2 {
				t.Fatalf("len(Args) = %d (%#v), want 2: ref and the day count", len(c.Args), c.Args)
			}
			assertRefArg(t, c, refTime)
			if !numberEquals(c.Args[1], float64(tc.days)) {
				t.Fatalf("Args[1] = %#v (%T), want the integer day count %v unchanged", c.Args[1], c.Args[1], tc.days)
			}
			w := compact(strings.ToLower(c.Where))
			if strings.Contains(w, "make_interval(days") || strings.Contains(w, "days=>") || strings.Contains(w, "day'") || strings.Contains(w, "days'") {
				t.Fatalf("Where uses a calendar-day interval: %q", c.Where)
			}
			if tc.hoursForm && !hoursForm.MatchString(w) {
				t.Fatalf("Where = %q, want the hours-based form make_interval(hours => $2 * 24) of segment-dsl.md §5 rule 4", c.Where)
			}
		})
	}
}
