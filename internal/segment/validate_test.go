package segment_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// REQ-AUD-01, REQ-AUD-04
// The allowed fields are the closed list of segment-dsl.md §2, in order, with the operators of §3.
// Contact, consent and identity columns are deliberately absent: consent is the system's job.
func TestFields_ClosedList(t *testing.T) {
	t.Parallel()
	str := []string{"eq", "in", "neq", "not_in"}
	num := []string{"between", "eq", "gt", "gte", "lt", "lte", "neq"}
	ts := []string{"is_not_null", "is_null", "older_than", "within_last"}
	type spec struct {
		Name      string
		Type      segment.FieldType
		Operators []string
		Enum      []string
	}
	want := []spec{
		{Name: "city", Type: segment.TypeString, Operators: str},
		{Name: "language", Type: segment.TypeEnum, Operators: str, Enum: []string{"en", "tr"}},
		{Name: "loyalty_tier", Type: segment.TypeEnum, Operators: str, Enum: []string{"bronze", "gold", "platinum", "silver"}},
		{Name: "last_cart_add_at", Type: segment.TypeTimestamp, Operators: ts},
		{Name: "last_purchase_at", Type: segment.TypeTimestamp, Operators: ts},
		{Name: "orders_30d", Type: segment.TypeInteger, Operators: num},
		{Name: "cart_adds_30d", Type: segment.TypeInteger, Operators: num},
		{Name: "total_orders", Type: segment.TypeInteger, Operators: num},
		{Name: "total_spent", Type: segment.TypeNumber, Operators: num},
	}
	var got []spec
	for _, f := range segment.Fields() {
		ops := slices.Clone(f.Operators)
		slices.Sort(ops)
		var enum []string
		if len(f.Enum) > 0 {
			enum = slices.Clone(f.Enum)
			slices.Sort(enum)
		}
		got = append(got, spec{Name: f.Name, Type: f.Type, Operators: ops, Enum: enum})
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Fields() mismatch (-want +got):\n%s", diff)
	}
	for _, f := range segment.Fields() {
		for _, forbidden := range []string{"email", "phone", "sms_opt_in", "email_opt_in", "first_name", "last_name", "id"} {
			if f.Name == forbidden {
				t.Fatalf("Fields() exposes %q; consent and contact data must not be rule fields", forbidden)
			}
		}
	}
	if segment.MaxDepth != 3 || segment.MaxConditions != 10 {
		t.Fatalf("MaxDepth, MaxConditions = %d, %d; want 3, 10", segment.MaxDepth, segment.MaxConditions)
	}
}

// REQ-AUD-01
// Valid rules (segment-dsl.md §7.1, §7.2 and the boundaries of §1.1/§3) pass Parse and Validate.
func TestValidate_AcceptsValidRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "example brief rule", raw: doc(exampleRule)},
		{name: "section 7.2 any with in and between", raw: `{"rule":{"any":[{"field":"loyalty_tier","op":"in","value":["gold","platinum"]},{"field":"total_spent","op":"between","value":{"min":1000,"max":5000}}]}}`},
		{name: "section 7.2 neq with not is_null", raw: `{"rule":{"all":[{"field":"city","op":"neq","value":"Ankara"},{"not":{"field":"last_purchase_at","op":"is_null"}}]}}`},
		{name: "SQL text as a city value is only a value", raw: doc(all(cond("city", "eq", jsonString("'; DROP TABLE customers; --"))))},
		{name: "city not in the canonical list is valid", raw: doc(all(cond("city", "eq", `"Berlin"`)))},
		{name: "city of 100 chars is accepted", raw: doc(all(cond("city", "eq", jsonString(strings.Repeat("ş", 100)))))},
		{name: "city of 1 char is accepted", raw: doc(all(cond("city", "eq", `"A"`)))},
		{name: "in with 1 string is accepted", raw: doc(all(cond("city", "in", `["İzmir"]`)))},
		{name: "in with 20 strings is accepted", raw: doc(all(cond("city", "in", jsonStrings(20))))},
		{name: "not_in on an enum is accepted", raw: doc(all(cond("loyalty_tier", "not_in", `["bronze"]`)))},
		{name: "language neq en is accepted", raw: doc(all(cond("language", "neq", `"en"`)))},
		{name: "within_last 1 day is accepted", raw: doc(all(cond("last_cart_add_at", "within_last", `{"days":1}`)))},
		{name: "within_last 3650 days is accepted", raw: doc(all(cond("last_cart_add_at", "within_last", `{"days":3650}`)))},
		{name: "older_than on last_purchase_at is accepted", raw: doc(all(cond("last_purchase_at", "older_than", `{"days":90}`)))},
		{name: "is_null without value is accepted", raw: doc(all(cond("last_purchase_at", "is_null", "")))},
		{name: "is_null with null value is accepted", raw: doc(all(cond("last_purchase_at", "is_null", "null")))},
		{name: "is_not_null with null value is accepted", raw: doc(all(cond("last_cart_add_at", "is_not_null", "null")))},
		{name: "integer 0 is accepted", raw: doc(all(cond("orders_30d", "eq", "0")))},
		{name: "integer 1000000 is accepted", raw: doc(all(cond("total_orders", "lte", "1000000")))},
		{name: "integer between with equal bounds is accepted", raw: doc(all(cond("cart_adds_30d", "between", `{"min":2,"max":2}`)))},
		{name: "number with decimals is accepted", raw: doc(all(cond("total_spent", "gte", "149.90")))},
		{name: "number 100000000 is accepted", raw: doc(all(cond("total_spent", "lt", "100000000")))},
		{name: "depth 3 is accepted", raw: doc(all(anyOf(not(cond("city", "eq", `"İzmir"`)))))},
		{name: "10 conditions are accepted", raw: doc(all(repeat(cond("orders_30d", "eq", "0"), 10)...))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := parseAndValidate(tc.raw); err != nil {
				t.Fatalf("Parse+Validate(%s) error = %v, want nil", tc.raw, err)
			}
		})
	}
}

// REQ-AUD-01, REQ-AUD-02, REQ-AI-01
// Every rejected input of segment-dsl.md §7.3 (and the §3 value rules) is refused with the
// contracted path, code and sentinel.
func TestValidate_RejectsInvalidRules(t *testing.T) {
	t.Parallel()
	city := cond("city", "eq", `"İzmir"`)
	tests := []struct {
		name     string
		raw      string
		sentinel error
		path     string
		code     string
	}{
		// segment-dsl.md §7.3, rows 2..18
		{name: "row 2 sql key next to rule", raw: `{"rule":` + exampleRule + `,"sql":"SELECT * FROM customers"}`, sentinel: validation.ErrSchemaViolation, path: "sql", code: "schema_violation"},
		{name: "row 3 where key inside rule", raw: `{"rule":{"where":"1=1"}}`, sentinel: validation.ErrSchemaViolation, path: "rule.where", code: "schema_violation"},
		{name: "row 4 email field", raw: doc(all(cond("email", "eq", `"a@example.com"`))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},
		{name: "row 5 consent field", raw: doc(all(cond("sms_opt_in", "eq", "true"))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},
		{name: "row 6 like operator", raw: doc(all(cond("city", "like", `"%stan%"`))), sentinel: segment.ErrUnknownOperator, path: "rule.all[0].op", code: "unknown_operator"},
		{name: "row 7 gt on a string field", raw: doc(all(cond("city", "gt", `"A"`))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].op", code: "operator_not_allowed"},
		{name: "row 8 string for an integer", raw: doc(all(cond("orders_30d", "eq", `"0"`))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "row 9 SQL in an integer value", raw: doc(all(cond("orders_30d", "eq", `"0 OR 1=1"`))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "row 10 within_last 0 days", raw: doc(all(cond("last_cart_add_at", "within_last", `{"days":0}`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "row 11 enum member outside the list", raw: doc(all(cond("language", "eq", `"de"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "row 12 depth 4", raw: doc(all(anyOf(not(all(city))))), sentinel: segment.ErrMaxDepthExceeded, path: "rule.all[0].any[0].not", code: "max_depth_exceeded"},
		{name: "row 13 eleven conditions", raw: doc(all(repeat(city, 11)...)), sentinel: segment.ErrMaxConditionsExceeded, path: "rule", code: "max_conditions_exceeded"},
		{name: "row 14 empty all", raw: `{"rule":{"all":[]}}`, sentinel: segment.ErrEmptyGroup, path: "rule.all", code: "empty_group"},
		{name: "row 15 bare condition as root", raw: doc(cond("city", "eq", `"İstanbul"`)), sentinel: validation.ErrSchemaViolation, path: "rule", code: "schema_violation"},
		{name: "row 16 condition mixed with group key", raw: `{"rule":{"all":[{"field":"city","op":"eq","value":"İzmir","all":[]}]}}`, sentinel: validation.ErrSchemaViolation, path: "rule.all[0]", code: "schema_violation"},
		{name: "row 17 between min greater than max", raw: doc(all(cond("total_spent", "between", `{"min":10,"max":5}`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "row 18 invalid JSON text", raw: `{"rule": {"all": [ ,]}}`, sentinel: validation.ErrInvalidJSON, path: "", code: "invalid_json"},

		// llm-tools.md SEG_INJECTION
		{name: "raw_sql key inside rule", raw: `{"rule":{"raw_sql":"DROP TABLE customers"}}`, sentinel: validation.ErrSchemaViolation, path: "rule.raw_sql", code: "schema_violation"},

		// segment-dsl.md §3 unknown_field examples
		{name: "phone field", raw: doc(all(cond("phone", "eq", `"+905000000001"`))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},
		{name: "first_name field", raw: doc(all(cond("first_name", "eq", `"Ayşe"`))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},
		{name: "email_opt_in field", raw: doc(all(cond("email_opt_in", "eq", "true"))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},
		{name: "field names are case-sensitive", raw: doc(all(cond("City", "eq", `"İstanbul"`))), sentinel: segment.ErrUnknownField, path: "rule.all[0].field", code: "unknown_field"},

		// unknown_operator / operator_not_allowed
		{name: "regex operator", raw: doc(all(cond("city", "regex", `".*"`))), sentinel: segment.ErrUnknownOperator, path: "rule.all[0].op", code: "unknown_operator"},
		{name: "raw operator", raw: doc(all(cond("orders_30d", "raw", `"1=1"`))), sentinel: segment.ErrUnknownOperator, path: "rule.all[0].op", code: "unknown_operator"},
		{name: "within_last on an integer field", raw: doc(all(cond("orders_30d", "within_last", `{"days":30}`))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].op", code: "operator_not_allowed"},
		{name: "eq on a timestamp field", raw: doc(all(cond("last_cart_add_at", "eq", `"2026-01-01T00:00:00Z"`))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].op", code: "operator_not_allowed"},
		{name: "between on an enum field", raw: doc(all(cond("loyalty_tier", "between", `{"min":1,"max":2}`))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].op", code: "operator_not_allowed"},
		{name: "is_null on a string field", raw: doc(all(cond("city", "is_null", ""))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].op", code: "operator_not_allowed"},

		// type_mismatch
		{name: "number for a string field", raw: doc(all(cond("city", "eq", "5"))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "bare number for within_last", raw: doc(all(cond("last_cart_add_at", "within_last", "30"))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "string for in", raw: doc(all(cond("city", "in", `"İstanbul"`))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "boolean for an integer field", raw: doc(all(cond("total_orders", "gt", "true"))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},
		{name: "array for eq", raw: doc(all(cond("city", "eq", `["İstanbul"]`))), sentinel: segment.ErrTypeMismatch, path: "rule.all[0].value", code: "type_mismatch"},

		// invalid_value
		{name: "empty city string", raw: doc(all(cond("city", "eq", `""`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "city of 101 chars", raw: doc(all(cond("city", "eq", jsonString(strings.Repeat("ş", 101))))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "in with 0 strings", raw: doc(all(cond("city", "in", `[]`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "in with 21 strings", raw: doc(all(cond("city", "in", jsonStrings(21)))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "loyalty tier outside the enum", raw: doc(all(cond("loyalty_tier", "eq", `"diamond"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "enum members are case-sensitive", raw: doc(all(cond("language", "eq", `"TR"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "enum in with a non-member", raw: doc(all(cond("loyalty_tier", "in", `["gold","vip"]`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "within_last 3651 days", raw: doc(all(cond("last_cart_add_at", "within_last", `{"days":3651}`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "older_than negative days", raw: doc(all(cond("last_purchase_at", "older_than", `{"days":-1}`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "is_null with a value", raw: doc(all(cond("last_purchase_at", "is_null", `"x"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "fractional number on an integer field", raw: doc(all(cond("orders_30d", "eq", "1.5"))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "negative integer", raw: doc(all(cond("orders_30d", "gte", "-1"))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "integer 1000001", raw: doc(all(cond("total_orders", "eq", "1000001"))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "number 100000001", raw: doc(all(cond("total_spent", "gt", "100000001"))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "fractional integer between bound", raw: doc(all(cond("orders_30d", "between", `{"min":0.5,"max":3}`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},

		// nested paths
		{name: "error path inside a nested any", raw: doc(all(city, anyOf(city, cond("email", "eq", `"x@example.com"`)))), sentinel: segment.ErrUnknownField, path: "rule.all[1].any[1].field", code: "unknown_field"},
		{name: "error path under not", raw: doc(all(not(cond("city", "gt", `"A"`)))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].not.op", code: "operator_not_allowed"},
		{name: "empty any nested", raw: doc(all(city, `{"any":[]}`)), sentinel: segment.ErrEmptyGroup, path: "rule.all[1].any", code: "empty_group"},
		{name: "eleven conditions spread over groups", raw: doc(all(append(repeat(city, 5), anyOf(repeat(city, 6)...))...)), sentinel: segment.ErrMaxConditionsExceeded, path: "rule", code: "max_conditions_exceeded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFieldError(t, parseAndValidate(tc.raw), tc.sentinel, tc.path, tc.code)
		})
	}
}

// REQ-AUD-01
// One condition reports exactly one error, the first failing check in the order
// unknown_field -> unknown_operator -> operator_not_allowed -> type_mismatch -> invalid_value.
func TestValidate_ErrorPrecedencePerCondition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cond     string
		want     pathCode
		notMatch []error
	}{
		{name: "unknown field wins over unknown operator and bad value", cond: cond("email", "like", "5"), want: pathCode{"rule.all[0].field", "unknown_field"}, notMatch: []error{segment.ErrUnknownOperator, segment.ErrTypeMismatch}},
		{name: "unknown operator wins over a type mismatch", cond: cond("city", "like", "5"), want: pathCode{"rule.all[0].op", "unknown_operator"}, notMatch: []error{segment.ErrTypeMismatch}},
		{name: "operator not allowed wins over a type mismatch", cond: cond("city", "gt", "5"), want: pathCode{"rule.all[0].op", "operator_not_allowed"}, notMatch: []error{segment.ErrTypeMismatch}},
		{name: "type mismatch wins over an invalid value", cond: cond("language", "eq", "7"), want: pathCode{"rule.all[0].value", "type_mismatch"}, notMatch: []error{validation.ErrInvalidValue}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := parseAndValidate(doc(all(tc.cond)))
			got := pathCodes(fieldErrors(t, err))
			if diff := cmp.Diff([]pathCode{tc.want}, got); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
			for _, s := range tc.notMatch {
				if errors.Is(err, s) {
					t.Fatalf("error also matches %v; only the first failing check may be reported", s)
				}
			}
		})
	}
}

// REQ-AUD-01
// Validate collects every problem of a rule in depth-first order.
func TestValidate_CollectsAllErrorsInOrder(t *testing.T) {
	t.Parallel()
	raw := doc(all(
		cond("email", "eq", `"x@example.com"`),
		cond("city", "eq", `"İstanbul"`),
		anyOf(cond("city", "like", `"%a%"`), cond("orders_30d", "eq", `"1"`)),
	))
	err := parseAndValidate(raw)
	want := []pathCode{
		{"rule.all[0].field", "unknown_field"},
		{"rule.all[2].any[0].op", "unknown_operator"},
		{"rule.all[2].any[1].value", "type_mismatch"},
	}
	if diff := cmp.Diff(want, pathCodes(fieldErrors(t, err))); diff != "" {
		t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
	}
	for _, s := range []error{segment.ErrUnknownField, segment.ErrUnknownOperator, segment.ErrTypeMismatch} {
		if !errors.Is(err, s) {
			t.Fatalf("error does not match %v", s)
		}
	}
}

// REQ-AUD-01
// Size limits are checked first; when one is exceeded the field/operator checks are skipped.
func TestValidate_SizeLimitsSkipOtherChecks(t *testing.T) {
	t.Parallel()
	bad := cond("email", "like", `"x"`)
	tests := []struct {
		name     string
		raw      string
		sentinel error
	}{
		{name: "depth 4 with an unknown field inside", raw: doc(all(anyOf(not(all(bad))))), sentinel: segment.ErrMaxDepthExceeded},
		{name: "eleven conditions with an unknown field", raw: doc(all(append([]string{bad}, repeat(cond("city", "eq", `"İzmir"`), 10)...)...)), sentinel: segment.ErrMaxConditionsExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := parseAndValidate(tc.raw)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("error = %v, want %v", err, tc.sentinel)
			}
			for _, s := range []error{segment.ErrUnknownField, segment.ErrUnknownOperator} {
				if errors.Is(err, s) {
					t.Fatalf("error also matches %v; field checks must be skipped once a size limit fails", s)
				}
			}
		})
	}
}

// REQ-AUD-01, REQ-AUD-02
// Parse checks structure only (segment-dsl.md §3 note): unknown names and size limits are left to
// Validate, while unknown keys, mixed group/condition objects and non-string names are
// schema_violation and invalid JSON is invalid_json.
func TestParse_StructureOnly(t *testing.T) {
	t.Parallel()
	t.Run("semantic problems pass Parse", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{
			doc(all(cond("email", "eq", `"a@example.com"`))),
			doc(all(cond("city", "like", `"%a%"`))),
			doc(all(anyOf(not(all(cond("city", "eq", `"İzmir"`)))))),
			doc(all(repeat(cond("city", "eq", `"İzmir"`), 11)...)),
		} {
			if _, err := segment.Parse([]byte(raw)); err != nil {
				t.Errorf("Parse(%s) error = %v, want nil (Validate reports it)", raw, err)
			}
		}
	})

	tests := []struct {
		name     string
		raw      string
		sentinel error
		code     string
	}{
		{name: "empty input", raw: ``, sentinel: validation.ErrInvalidJSON, code: "invalid_json"},
		{name: "trailing comma", raw: `{"rule": {"all": [{"field": "city", "op": "eq", "value": "İstanbul"},]}}`, sentinel: validation.ErrInvalidJSON, code: "invalid_json"},
		{name: "root is an array", raw: `[]`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "rule key missing", raw: `{}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "rule is a string", raw: `{"rule":"city = 'İstanbul'"}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "group with two group keys", raw: `{"rule":{"all":[` + cond("city", "eq", `"A"`) + `],"any":[` + cond("city", "eq", `"B"`) + `]}}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "field is not a string", raw: doc(all(`{"field":5,"op":"eq","value":"x"}`)), sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "op is not a string", raw: doc(all(`{"field":"city","op":["eq"],"value":"x"}`)), sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "op missing", raw: doc(all(`{"field":"city","value":"x"}`)), sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "field missing", raw: doc(all(`{"op":"eq","value":"x"}`)), sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "unknown key on a condition", raw: doc(all(`{"field":"city","op":"eq","value":"x","sql":"1=1"}`)), sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "unknown key at document level", raw: `{"rule":` + exampleRule + `,"where":"1=1"}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := segment.Parse([]byte(tc.raw))
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("Parse(%s) error = %v, want %v", tc.raw, err, tc.sentinel)
			}
			found := false
			for _, fe := range fieldErrors(t, err) {
				if fe.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("Parse(%s) field errors = %+v, want code %q", tc.raw, pathCodes(fieldErrors(t, err)), tc.code)
			}
		})
	}
}

// REQ-AUD-01, REQ-AI-01
// Rule.MarshalJSON emits the root group (the value of "rule"), so a parsed rule round-trips.
func TestRule_MarshalJSONEmitsRootGroup(t *testing.T) {
	t.Parallel()
	r := mustRule(t, doc(exampleRule))
	got, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	assertSameJSON(t, got, exampleRule)

	again := mustRule(t, doc(string(got)))
	got2, err := again.MarshalJSON()
	if err != nil {
		t.Fatalf("second MarshalJSON() error = %v", err)
	}
	assertSameJSON(t, got2, exampleRule)
}

func assertSameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var gotV, wantV any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatalf("output is not JSON (%v): %s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}
	if diff := cmp.Diff(wantV, gotV); diff != "" {
		t.Fatalf("JSON mismatch (-want +got):\n%s", diff)
	}
}
