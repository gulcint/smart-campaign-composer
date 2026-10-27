package segment_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// refTime is the fixed test reference time (go-interfaces.md §clock, segment-dsl.md §4).
var refTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// exampleRule is segment-dsl.md §7.1 "rule" (root group), also llm-tools.md SEG_OK's rule.
const exampleRule = `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}`

// doc wraps a root group into the {"rule": <group>} document accepted by segment.Parse.
func doc(group string) string { return `{"rule":` + group + `}` }

// cond renders one condition; value is raw JSON ("" => no value key).
func cond(field, op, value string) string {
	if value == "" {
		return fmt.Sprintf(`{"field":%q,"op":%q}`, field, op)
	}
	return fmt.Sprintf(`{"field":%q,"op":%q,"value":%s}`, field, op, value)
}

// all builds {"all":[...]} from raw nodes.
func all(nodes ...string) string { return `{"all":[` + strings.Join(nodes, ",") + `]}` }

// any builds {"any":[...]} from raw nodes.
func anyOf(nodes ...string) string { return `{"any":[` + strings.Join(nodes, ",") + `]}` }

// not builds {"not": node}.
func not(node string) string { return `{"not":` + node + `}` }

// repeat returns n copies of node.
func repeat(node string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = node
	}
	return out
}

// jsonString renders s as a JSON string literal.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// jsonStrings renders a JSON array of n distinct strings.
func jsonStrings(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = jsonString(fmt.Sprintf("Şehir%d", i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// parseAndValidate runs the full backend path: Parse (structure) then Validate (semantics).
// Which of the two reports a given problem is not part of the assertion unless a test says so.
func parseAndValidate(raw string) error {
	r, err := segment.Parse([]byte(raw))
	if err != nil {
		return err
	}
	return segment.Validate(r)
}

// mustRule parses and validates a document or fails the test.
func mustRule(t *testing.T, raw string) segment.Rule {
	t.Helper()
	r, err := segment.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", raw, err)
	}
	if err := segment.Validate(r); err != nil {
		t.Fatalf("Validate(%s) error = %v", raw, err)
	}
	return r
}

// fieldErrors extracts the *validation.Error field list or fails the test.
func fieldErrors(t *testing.T, err error) []validation.FieldError {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want a *validation.Error", err, err)
	}
	if len(ve.Fields) == 0 {
		t.Fatalf("*validation.Error has no field errors")
	}
	return ve.Fields
}

type pathCode struct {
	Path string
	Code string
}

func pathCodes(fes []validation.FieldError) []pathCode {
	out := make([]pathCode, len(fes))
	for i, fe := range fes {
		out[i] = pathCode{Path: fe.Path, Code: fe.Code}
	}
	return out
}

// assertFieldError checks the sentinel and that one field error has exactly path and code.
func assertFieldError(t *testing.T, err error, sentinel error, path, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v at %q (%s)", sentinel, path, code)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, sentinel)
	}
	fes := fieldErrors(t, err)
	for _, fe := range fes {
		if fe.Path == path && fe.Code == code {
			return
		}
	}
	t.Fatalf("field errors = %+v, want one with path %q and code %q", pathCodes(fes), path, code)
}

// numberEquals reports whether a compiled argument is the number want, whatever numeric Go type
// the implementation chose (the contract fixes the value, not the Go type).
func numberEquals(v any, want float64) bool {
	switch n := v.(type) {
	case int:
		return float64(n) == want
	case int32:
		return float64(n) == want
	case int64:
		return float64(n) == want
	case float64:
		return n == want
	case json.Number:
		f, err := n.Float64()
		return err == nil && f == want
	default:
		return false
	}
}

// stringsOf returns the elements of an "in" array argument ([]string or []any of strings).
func stringsOf(v any) ([]string, bool) {
	switch a := v.(type) {
	case []string:
		return a, true
	case []any:
		out := make([]string, 0, len(a))
		for _, e := range a {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
