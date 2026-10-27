package segment_test

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// REQ-AUD-02, REQ-AUD-01
// Compile turns the example rule into a parameterized predicate: $1 is the reference time and
// every rule value is its own parameter, never part of the SQL text (segment-dsl.md §5).
func TestCompile_ExampleRuleIsParameterized(t *testing.T) {
	t.Parallel()
	c, err := segment.Compile(mustRule(t, doc(exampleRule)), refTime)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(c.Args) != 4 {
		t.Fatalf("len(Args) = %d (%v), want 4: ref, \"İstanbul\", 30 days, 0", len(c.Args), c.Args)
	}
	assertRefArg(t, c, refTime)
	if s, ok := c.Args[1].(string); !ok || s != "İstanbul" {
		t.Errorf("Args[1] = %#v, want \"İstanbul\"", c.Args[1])
	}
	if !numberEquals(c.Args[2], 30) {
		t.Errorf("Args[2] = %#v, want the number 30 (within_last days)", c.Args[2])
	}
	if !numberEquals(c.Args[3], 0) {
		t.Errorf("Args[3] = %#v, want the number 0 (orders_30d)", c.Args[3])
	}
	assertPlaceholders(t, c)
	if strings.Contains(c.Where, "İstanbul") || strings.Contains(c.Where, "stanbul") {
		t.Fatalf("Where contains a rule value: %q", c.Where)
	}
	if strings.Contains(strings.ToLower(c.Where), "now()") {
		t.Fatalf("Where uses now(); the reference time must be injected as $1: %q", c.Where)
	}
}

// REQ-AUD-02
// SQL fragments in rule values stay parameters: they appear unchanged in Args and nowhere in Where.
func TestCompile_InjectionPayloadsStayParameters(t *testing.T) {
	t.Parallel()
	payloads := []string{
		"'; DROP TABLE customers; --",
		"İstanbul' OR '1'='1",
		`x"); DELETE FROM events; --`,
		"$2) OR (1=1",
		`\'; SELECT pg_sleep(10); --`,
		"İstanbul'); UPDATE customers SET sms_opt_in = true; --",
	}
	forbidden := []string{"DROP", "DELETE", "UPDATE", "pg_sleep", "1=1", "'1'"}
	for i, p := range payloads {
		ops := []struct {
			name  string
			group string
			inArr bool
		}{
			{name: "eq", group: all(cond("city", "eq", jsonString(p)))},
			{name: "neq", group: all(cond("city", "neq", jsonString(p)))},
			{name: "in", group: all(cond("city", "in", "["+jsonString("İzmir")+","+jsonString(p)+"]")), inArr: true},
			{name: "not_in under not", group: all(not(cond("city", "not_in", "["+jsonString(p)+"]"))), inArr: true},
		}
		for _, op := range ops {
			t.Run(fmt.Sprintf("payload %d with %s", i, op.name), func(t *testing.T) {
				t.Parallel()
				c, err := segment.Compile(mustRule(t, doc(op.group)), refTime)
				if err != nil {
					t.Fatalf("Compile() error = %v, want nil (the value is a valid string)", err)
				}
				if strings.Contains(c.Where, p) {
					t.Fatalf("Where contains the payload verbatim: %q", c.Where)
				}
				for _, f := range forbidden {
					if strings.Contains(c.Where, f) {
						t.Fatalf("Where contains %q from a rule value: %q", f, c.Where)
					}
				}
				if !argsContain(c.Args[1:], p, op.inArr) {
					t.Fatalf("Args = %#v, want the payload %q unchanged as a parameter", c.Args, p)
				}
				assertRefArg(t, c, refTime)
				assertPlaceholders(t, c)
			})
		}
	}
}

// REQ-AUD-02
// Values become $2, $3, ... from left to right in depth-first order; is_null adds no parameter.
func TestCompile_ParameterOrder(t *testing.T) {
	t.Parallel()
	raw := doc(all(
		cond("city", "eq", `"Ankara"`),
		anyOf(
			cond("orders_30d", "gt", "1"),
			cond("last_purchase_at", "is_null", ""),
		),
		cond("loyalty_tier", "in", `["gold","platinum"]`),
		cond("last_cart_add_at", "older_than", `{"days":10}`),
	))
	c, err := segment.Compile(mustRule(t, raw), refTime)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(c.Args) != 5 {
		t.Fatalf("len(Args) = %d (%#v), want 5: ref + 4 values (is_null has none)", len(c.Args), c.Args)
	}
	assertRefArg(t, c, refTime)
	if s, ok := c.Args[1].(string); !ok || s != "Ankara" {
		t.Errorf("Args[1] = %#v, want \"Ankara\"", c.Args[1])
	}
	if !numberEquals(c.Args[2], 1) {
		t.Errorf("Args[2] = %#v, want 1", c.Args[2])
	}
	if got, ok := stringsOf(c.Args[3]); !ok || !slices.Equal(got, []string{"gold", "platinum"}) {
		t.Errorf("Args[3] = %#v, want one array parameter [gold platinum]", c.Args[3])
	}
	if !numberEquals(c.Args[4], 10) {
		t.Errorf("Args[4] = %#v, want 10", c.Args[4])
	}
	assertPlaceholders(t, c)
}

// REQ-AUD-02
// Args[0] is the injected reference time in UTC, whatever zone the caller used.
func TestCompile_ReferenceTimeIsUTC(t *testing.T) {
	t.Parallel()
	istanbul := time.FixedZone("TRT", 3*60*60)
	ref := refTime.In(istanbul)
	c, err := segment.Compile(mustRule(t, doc(exampleRule)), ref)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	assertRefArg(t, c, refTime)
}

// REQ-AUD-01
// Compile validates first and returns Validate's error; nothing is compiled for an invalid rule.
func TestCompile_InvalidRuleIsRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		sentinel error
	}{
		{name: "unknown field", raw: doc(all(cond("email", "eq", `"a@example.com"`))), sentinel: segment.ErrUnknownField},
		{name: "unknown operator", raw: doc(all(cond("city", "like", `"%a%"`))), sentinel: segment.ErrUnknownOperator},
		{name: "depth 4", raw: doc(all(anyOf(not(all(cond("city", "eq", `"İzmir"`)))))), sentinel: segment.ErrMaxDepthExceeded},
		{name: "eleven conditions", raw: doc(all(repeat(cond("city", "eq", `"İzmir"`), 11)...)), sentinel: segment.ErrMaxConditionsExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := segment.Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse() error = %v (structure is valid)", err)
			}
			c, err := segment.Compile(r, refTime)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("Compile() error = %v, want %v", err, tc.sentinel)
			}
			if c.Where != "" {
				t.Fatalf("Compile() returned Where %q for an invalid rule", c.Where)
			}
		})
	}
}

func assertRefArg(t *testing.T, c segment.Compiled, want time.Time) {
	t.Helper()
	if len(c.Args) == 0 {
		t.Fatalf("Args is empty; Args[0] must be the reference time")
	}
	got, ok := c.Args[0].(time.Time)
	if !ok {
		t.Fatalf("Args[0] = %#v (%T), want time.Time", c.Args[0], c.Args[0])
	}
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("Args[0] = %v (%v), want %v in UTC", got, got.Location(), want)
	}
}

// placeholderRE matches one positional parameter ($1, $2, ..., $10, ...) as a whole token.
var placeholderRE = regexp.MustCompile(`\$([0-9]+)`)

// assertPlaceholders checks the placeholder layout of segment-dsl.md §5:
//   - $1 is the reference time; the fixed skeleton uses it in FROM (customer_stats_at($1)),
//     so Where may use it (within_last/older_than) or not (a rule on city alone);
//   - every rule value is $2..$len(Args), and each of them is used in Where;
//   - no placeholder outside 1..len(Args) appears ($0 or beyond the last argument).
func assertPlaceholders(t *testing.T, c segment.Compiled) {
	t.Helper()
	if c.Where == "" {
		t.Fatalf("Where is empty")
	}
	used := map[int]bool{}
	for _, m := range placeholderRE.FindAllStringSubmatch(c.Where, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > len(c.Args) {
			t.Fatalf("Where %q uses %s but the valid placeholders are $1..$%d", c.Where, m[0], len(c.Args))
		}
		used[n] = true
	}
	for i := 2; i <= len(c.Args); i++ {
		if !used[i] {
			t.Fatalf("Where %q does not use $%d (len(Args) = %d; rule values are $2..$%d)", c.Where, i, len(c.Args), len(c.Args))
		}
	}
}

func argsContain(args []any, want string, inArray bool) bool {
	for _, a := range args {
		if inArray {
			if ss, ok := stringsOf(a); ok && slices.Contains(ss, want) {
				return true
			}
			continue
		}
		if s, ok := a.(string); ok && s == want {
			return true
		}
	}
	return false
}
