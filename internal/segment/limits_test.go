package segment_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Cost bounds of Parse + Validate (segment-dsl.md §7.3 last paragraph, go-interfaces.md §segment
// Parse: "one linear pass; it does not walk past the limits"). The bounds are generous: a linear
// implementation needs a few milliseconds, the quadratic behavior they guard against needs seconds.
const (
	costDeadline  = 500 * time.Millisecond * raceSlowdown
	costAllocMax  = 50 << 20 // bytes allocated by one Parse + Validate
	deepNotLevels = 9000
	flatConds     = 60000
)

// deepNot returns {"rule": {"not": {"not": ... cond ...}}} with levels nested "not" groups.
func deepNot(levels int) string {
	var b strings.Builder
	b.WriteString(`{"rule":`)
	for range levels {
		b.WriteString(`{"not":`)
	}
	b.WriteString(cond("city", "eq", `"İzmir"`))
	b.WriteString(strings.Repeat("}", levels))
	b.WriteString("}")
	return b.String()
}

// flatAll returns {"rule": {"all": [n conditions]}}.
func flatAll(n int) string {
	return doc(all(repeat(cond("city", "eq", `"İzmir"`), n)...))
}

// withinDeadline runs f and fails the test if it does not return within d. The deadline is only a
// cost bound; correctness is asserted separately on f's result.
func withinDeadline(t *testing.T, d time.Duration, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		t.Fatalf("Parse + Validate did not finish within %v (the limits must stop the walk early)", d)
		return nil
	}
}

// REQ-AUD-01, REQ-AUD-02, REQ-AI-01
// A pathological document — thousands of nested "not" groups or tens of thousands of conditions —
// is refused with the contract errors (depth: the path of the first group over MaxDepth; count: path
// "rule"), as the only error, and quickly.
func TestParseValidate_LimitsStopTheWalkEarly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		sentinel error
		want     []pathCode
	}{
		{
			name:     fmt.Sprintf("%d nested not groups", deepNotLevels),
			raw:      deepNot(deepNotLevels),
			sentinel: segment.ErrMaxDepthExceeded,
			want:     []pathCode{{Path: "rule.not.not.not", Code: validation.CodeMaxDepthExceeded}},
		},
		{
			name:     "5 nested not groups (first level Parse cuts off)",
			raw:      deepNot(5),
			sentinel: segment.ErrMaxDepthExceeded,
			want:     []pathCode{{Path: "rule.not.not.not", Code: validation.CodeMaxDepthExceeded}},
		},
		{
			name:     fmt.Sprintf("%d conditions in one group", flatConds),
			raw:      flatAll(flatConds),
			sentinel: segment.ErrMaxConditionsExceeded,
			want:     []pathCode{{Path: "rule", Code: validation.CodeMaxConditionsExceeded}},
		},
		{
			name:     "12 conditions (first count Parse cuts off)",
			raw:      flatAll(segment.MaxConditions + 2),
			sentinel: segment.ErrMaxConditionsExceeded,
			want:     []pathCode{{Path: "rule", Code: validation.CodeMaxConditionsExceeded}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := withinDeadline(t, costDeadline, func() error { return parseAndValidate(tc.raw) })
			assertFieldError(t, err, tc.sentinel, tc.want[0].Path, tc.want[0].Code)
			if diff := cmp.Diff(tc.want, pathCodes(fieldErrors(t, err))); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-AUD-01, REQ-AUD-02
// Memory bound of the same inputs: one Parse + Validate allocates far less than 50 MB. Not parallel:
// runtime.MemStats.TotalAlloc is process-wide, so no other test may run meanwhile.
func TestParseValidate_LimitsBoundMemory(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: fmt.Sprintf("%d nested not groups", deepNotLevels), raw: deepNot(deepNotLevels)},
		{name: fmt.Sprintf("%d conditions in one group", flatConds), raw: flatAll(flatConds)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			r, err := segment.Parse(raw)
			if err == nil {
				err = segment.Validate(r)
			}
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("Parse + Validate accepted a document over the limits")
			}
			if got := after.TotalAlloc - before.TotalAlloc; got > costAllocMax {
				t.Fatalf("Parse + Validate allocated %d MB for a %d KB document, want < %d MB", got>>20, len(raw)>>10, costAllocMax>>20)
			}
		})
	}
}

const invalidKey = "_invalid_key_"

// REQ-AUD-02, REQ-UI-01, REQ-AI-01
// segment-dsl.md §7.3 path rule: an unknown key that is an identifier ([A-Za-z_][A-Za-z0-9_]*) of at
// most 64 characters is echoed in the path; any other unknown key name (spaces, quotes, non-ASCII,
// leading digit, more than 64 characters) is replaced by the fixed segment "_invalid_key_", so
// attacker-chosen text never reaches error paths (and the UI that renders them).
func TestParse_UnknownKeyPathPlaceholder(t *testing.T) {
	t.Parallel()
	valid := exampleRule
	key64 := strings.Repeat("a", 64)
	key65 := strings.Repeat("a", 65)
	tests := []struct {
		name string
		raw  string
		path string
	}{
		{name: "document-level identifier is echoed", raw: `{"rule":` + valid + `,"sql":"SELECT 1"}`, path: "sql"},
		{name: "document-level identifier of 64 chars is echoed", raw: `{"rule":` + valid + `,` + jsonString(key64) + `:1}`, path: key64},
		{name: "document-level key of 65 chars is replaced", raw: `{"rule":` + valid + `,` + jsonString(key65) + `:1}`, path: invalidKey},
		{name: "document-level key with a space is replaced", raw: `{"rule":` + valid + `,"drop table":1}`, path: invalidKey},
		{name: "document-level key with a quote is replaced", raw: `{"rule":` + valid + `,"a\"b":1}`, path: invalidKey},
		{name: "document-level key with Turkish letters is replaced", raw: `{"rule":` + valid + `,"şehir":1}`, path: invalidKey},
		{name: "document-level key starting with a digit is replaced", raw: `{"rule":` + valid + `,"1abc":1}`, path: invalidKey},
		{name: "document-level key with HTML is replaced", raw: `{"rule":` + valid + `,"<script>alert(1)</script>":1}`, path: invalidKey},
		{name: "document-level underscore identifier is echoed", raw: `{"rule":` + valid + `,"_x9":1}`, path: "_x9"},
		{name: "rule-level identifier is echoed", raw: `{"rule":{"raw_sql":"DROP TABLE customers"}}`, path: "rule.raw_sql"},
		{name: "rule-level key with a space is replaced", raw: `{"rule":{"where clause":"1=1"}}`, path: "rule." + invalidKey},
		{name: "rule-level key of 65 chars is replaced", raw: `{"rule":{` + jsonString(key65) + `:"1=1"}}`, path: "rule." + invalidKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := parseAndValidate(tc.raw)
			assertFieldError(t, err, validation.ErrSchemaViolation, tc.path, validation.CodeSchemaViolation)
			for _, fe := range fieldErrors(t, err) {
				if strings.ContainsAny(fe.Path, " \"<>ş") || strings.Contains(fe.Path, key65) {
					t.Fatalf("field path %q carries raw key text", fe.Path)
				}
			}
		})
	}
}

// REQ-AUD-01, REQ-AUD-02, REQ-AI-01
// A document returns at most 20 field errors (segment-dsl.md §7.3), the first ones in depth-first
// order when Validate collects them.
func TestParseValidate_AtMostTwentyFieldErrors(t *testing.T) {
	t.Parallel()

	t.Run("30 empty groups report the first 20 in depth-first order", func(t *testing.T) {
		t.Parallel()
		raw := doc(all(repeat(`{"all":[]}`, 30)...))
		err := parseAndValidate(raw)
		if !errors.Is(err, segment.ErrEmptyGroup) {
			t.Fatalf("error = %v, want %v", err, segment.ErrEmptyGroup)
		}
		want := make([]pathCode, 20)
		for i := range want {
			want[i] = pathCode{Path: fmt.Sprintf("rule.all[%d].all", i), Code: validation.CodeEmptyGroup}
		}
		if diff := cmp.Diff(want, pathCodes(fieldErrors(t, err))); diff != "" {
			t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("20 empty groups are all reported", func(t *testing.T) {
		t.Parallel()
		err := parseAndValidate(doc(all(repeat(`{"all":[]}`, 20)...)))
		if got := len(fieldErrors(t, err)); got != 20 {
			t.Fatalf("%d field errors, want 20", got)
		}
	})

	t.Run("30 unknown document keys report at most 20", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString(`{"rule":` + exampleRule)
		for i := range 30 {
			fmt.Fprintf(&b, `,"k%02d":1`, i)
		}
		b.WriteString("}")
		err := parseAndValidate(b.String())
		fes := fieldErrors(t, err)
		if len(fes) > 20 {
			t.Fatalf("%d field errors, want at most 20", len(fes))
		}
		for _, fe := range fes {
			if fe.Code != validation.CodeSchemaViolation {
				t.Fatalf("field error %+v, want only schema_violation", fe)
			}
		}
		seen := map[string]bool{}
		for _, fe := range fes {
			if seen[fe.Path] {
				t.Fatalf("field path %q reported twice", fe.Path)
			}
			seen[fe.Path] = true
		}
	})
}
