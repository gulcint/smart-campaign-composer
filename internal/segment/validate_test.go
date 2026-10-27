package segment_test

import (
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
		{name: "row 11 enum member outside the list", raw: doc(all(cond("language", "eq", `"de"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "row 12 depth 4", raw: doc(all(anyOf(not(all(city))))), sentinel: segment.ErrMaxDepthExceeded, path: "rule.all[0].any[0].not", code: "max_depth_exceeded"},
		{name: "row 13 eleven conditions", raw: doc(all(repeat(city, 11)...)), sentinel: segment.ErrMaxConditionsExceeded, path: "rule", code: "max_conditions_exceeded"},
		{name: "row 15 bare condition as root", raw: doc(cond("city", "eq", `"İstanbul"`)), sentinel: validation.ErrSchemaViolation, path: "rule", code: "schema_violation"},
		{name: "row 16 condition mixed with group key", raw: `{"rule":{"all":[{"field":"city","op":"eq","value":"İzmir","all":[]}]}}`, sentinel: validation.ErrSchemaViolation, path: "rule.all[0]", code: "schema_violation"},
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

		// enum values outside the allowed list (type of the field) and fractional integers
		{name: "loyalty tier outside the enum", raw: doc(all(cond("loyalty_tier", "eq", `"diamond"`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "enum in with a non-member", raw: doc(all(cond("loyalty_tier", "in", `["gold","vip"]`))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},
		{name: "fractional number on an integer field", raw: doc(all(cond("orders_30d", "eq", "1.5"))), sentinel: validation.ErrInvalidValue, path: "rule.all[0].value", code: "invalid_value"},

		// nested positions
		{name: "error path inside a nested any", raw: doc(all(city, anyOf(city, cond("email", "eq", `"x@example.com"`)))), sentinel: segment.ErrUnknownField, path: "rule.all[1].any[1].field", code: "unknown_field"},
		{name: "error path under not", raw: doc(all(not(cond("city", "gt", `"A"`)))), sentinel: segment.ErrOperatorNotAllowed, path: "rule.all[0].not.op", code: "operator_not_allowed"},
		{name: "eleven conditions spread over groups", raw: doc(all(append(repeat(city, 5), anyOf(repeat(city, 6)...))...)), sentinel: segment.ErrMaxConditionsExceeded, path: "rule", code: "max_conditions_exceeded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFieldError(t, parseAndValidate(tc.raw), tc.sentinel, tc.path, tc.code)
		})
	}
}

// REQ-AUD-02, REQ-AI-01
// Structurally broken documents are rejected: unknown keys (e.g. a SQL-carrying key), missing keys,
// wrong types and mixed group/condition objects are schema_violation; invalid JSON is invalid_json.
func TestParse_StructureOnly(t *testing.T) {
	t.Parallel()
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
