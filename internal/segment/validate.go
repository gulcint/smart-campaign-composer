package segment

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Value limits (segment-dsl.md §1.1, §3).
const (
	maxStringRunes = 100
	maxListItems   = 20
	maxInteger     = 1000000
	maxNumber      = 100000000
	maxDays        = 3650
)

// Validate performs semantic checks and returns nil or a *validation.Error with every problem
// (segment-dsl.md §1.1, §3, §7.3), in depth-first order. The depth and condition-count limits are
// checked first; if either is exceeded the per-condition checks are skipped.
func Validate(r Rule) error {
	if !isGroup(r.Root) {
		return &validation.Error{Fields: []validation.FieldError{schemaViolation("rule", "rule root must be a group (all, any or not)")}}
	}
	var fields []validation.FieldError
	if n, ok := countConditions(r.Root); !ok {
		return &validation.Error{Fields: []validation.FieldError{schemaViolation("rule", "every node must be exactly one group or condition")}}
	} else if n > MaxConditions {
		limit, actual := MaxConditions, n
		fields = append(fields, validation.FieldError{
			Path: "rule", Code: validation.CodeMaxConditionsExceeded,
			Message: "segment rule has more than " + strconv.Itoa(MaxConditions) + " conditions",
			Limit:   &limit, Actual: &actual, Err: ErrMaxConditionsExceeded,
		})
	}
	fields = appendDepthErrors(fields, r.Root, "rule", 1)
	if len(fields) == 0 {
		fields = validateNode(fields, r.Root, "rule")
	}
	if len(fields) == 0 {
		return nil
	}
	return &validation.Error{Fields: fields}
}

func schemaViolation(path, msg string) validation.FieldError {
	return validation.FieldError{Path: path, Code: validation.CodeSchemaViolation, Message: msg, Err: validation.ErrSchemaViolation}
}

func isGroup(n Node) bool { return kind(n) == kindAll || kind(n) == kindAny || kind(n) == kindNot }

type nodeKind int

const (
	kindInvalid nodeKind = iota
	kindAll
	kindAny
	kindNot
	kindCond
)

// kind classifies a node; a node with zero or several members set is invalid.
func kind(n Node) nodeKind {
	k, set := kindInvalid, 0
	if n.All != nil {
		k, set = kindAll, set+1
	}
	if n.Any != nil {
		k, set = kindAny, set+1
	}
	if n.Not != nil {
		k, set = kindNot, set+1
	}
	if n.Cond != nil {
		k, set = kindCond, set+1
	}
	if set != 1 {
		return kindInvalid
	}
	return k
}

func children(n Node) []Node {
	switch kind(n) {
	case kindAll:
		return n.All
	case kindAny:
		return n.Any
	case kindNot:
		return []Node{*n.Not}
	}
	return nil
}

func childPath(n Node, path string, i int) string {
	switch kind(n) {
	case kindAll:
		return path + ".all[" + strconv.Itoa(i) + "]"
	case kindAny:
		return path + ".any[" + strconv.Itoa(i) + "]"
	}
	return path + ".not"
}

// countConditions counts leaves; ok is false if some node is not exactly one group or condition.
func countConditions(n Node) (int, bool) {
	switch kind(n) {
	case kindInvalid:
		return 0, false
	case kindCond:
		return 1, true
	}
	total := 0
	for _, c := range children(n) {
		k, ok := countConditions(c)
		if !ok {
			return 0, false
		}
		total += k
	}
	return total, true
}

// appendDepthErrors reports every group whose depth (groups on the path from the root, itself
// included) exceeds MaxDepth; it does not descend below the first exceeding group of a branch.
func appendDepthErrors(fields []validation.FieldError, n Node, path string, depth int) []validation.FieldError {
	if !isGroup(n) {
		return fields
	}
	if depth > MaxDepth {
		limit, actual := MaxDepth, depth
		return append(fields, validation.FieldError{
			Path: path, Code: validation.CodeMaxDepthExceeded,
			Message: "segment rule is nested deeper than " + strconv.Itoa(MaxDepth) + " groups",
			Limit:   &limit, Actual: &actual, Err: ErrMaxDepthExceeded,
		})
	}
	for i, c := range children(n) {
		fields = appendDepthErrors(fields, c, childPath(n, path, i), depth+1)
	}
	return fields
}

func validateNode(fields []validation.FieldError, n Node, path string) []validation.FieldError {
	switch kind(n) {
	case kindCond:
		if fe := checkCondition(*n.Cond, path); fe != nil {
			fields = append(fields, *fe)
		}
		return fields
	case kindAll, kindAny:
		if len(children(n)) == 0 {
			key := "all"
			if kind(n) == kindAny {
				key = "any"
			}
			return append(fields, validation.FieldError{
				Path: path + "." + key, Code: validation.CodeEmptyGroup, Message: key + " must not be empty", Err: ErrEmptyGroup,
			})
		}
	}
	for i, c := range children(n) {
		fields = validateNode(fields, c, childPath(n, path, i))
	}
	return fields
}

// checkCondition returns the first failing check in the order unknown_field -> unknown_operator ->
// operator_not_allowed -> type_mismatch -> invalid_value, or nil.
func checkCondition(c Condition, path string) *validation.FieldError {
	spec, ok := lookupField(c.Field)
	if !ok {
		return &validation.FieldError{Path: path + ".field", Code: validation.CodeUnknownField,
			Message: "field is not allowed in segment rules", Err: ErrUnknownField}
	}
	if !slices.Contains(knownOperators, c.Op) {
		return &validation.FieldError{Path: path + ".op", Code: validation.CodeUnknownOperator,
			Message: "operator is not supported", Err: ErrUnknownOperator}
	}
	if !slices.Contains(spec.Operators, c.Op) {
		return &validation.FieldError{Path: path + ".op", Code: validation.CodeOperatorNotAllowed,
			Message: "operator " + c.Op + " is not allowed for " + string(spec.Type) + " field " + spec.Name, Err: ErrOperatorNotAllowed}
	}
	if _, fe := decodeValue(spec, c.Op, c.Value); fe != nil {
		fe.Path = path + ".value"
		return fe
	}
	return nil
}

// ---- values ----

// numRange is a decoded between value.
type numRange struct{ min, max float64 }

// decodeValue checks a condition value and returns it typed:
// string (eq/neq on string/enum), []string (in/not_in), float64 (numeric comparisons),
// numRange (between), int (within_last/older_than days) or nil (is_null/is_not_null).
func decodeValue(spec FieldSpec, op string, raw json.RawMessage) (any, *validation.FieldError) {
	switch spec.Type {
	case TypeString, TypeEnum:
		if op == opIn || op == opNotIn {
			return decodeStringList(spec, raw)
		}
		s, ok := asString(raw)
		if !ok {
			return nil, typeMismatch("value must be a string")
		}
		if fe := checkStringMember(spec, s); fe != nil {
			return nil, fe
		}
		return s, nil
	case TypeInteger, TypeNumber:
		if op == opBetween {
			return decodeRange(spec, raw)
		}
		f, ok := asNumber(raw)
		if !ok {
			return nil, typeMismatch("value must be a number")
		}
		if fe := checkNumber(spec, f); fe != nil {
			return nil, fe
		}
		return f, nil
	case TypeTimestamp:
		if op == opIsNull || op == opIsNotNull {
			if raw != nil && jsonKind(raw) != 'n' {
				return nil, invalidValue("is_null / is_not_null take no value")
			}
			return nil, nil
		}
		return decodeDays(raw)
	}
	return nil, typeMismatch("unsupported field type")
}

func decodeStringList(spec FieldSpec, raw json.RawMessage) (any, *validation.FieldError) {
	if jsonKind(raw) != '[' {
		return nil, typeMismatch("value must be an array of strings")
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, typeMismatch("value must be an array of strings")
	}
	list := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := asString(it)
		if !ok {
			return nil, typeMismatch("value must be an array of strings")
		}
		list = append(list, s)
	}
	if len(list) < 1 || len(list) > maxListItems {
		return nil, invalidValue("value must hold 1 to " + strconv.Itoa(maxListItems) + " items")
	}
	for _, s := range list {
		if fe := checkStringMember(spec, s); fe != nil {
			return nil, fe
		}
	}
	return list, nil
}

func checkStringMember(spec FieldSpec, s string) *validation.FieldError {
	if spec.Type == TypeEnum {
		if !slices.Contains(spec.Enum, s) {
			return invalidValue("value is not one of the allowed values")
		}
		return nil
	}
	if n := utf8.RuneCountInString(s); n < 1 || n > maxStringRunes {
		return invalidValue("value must be 1 to " + strconv.Itoa(maxStringRunes) + " characters")
	}
	return nil
}

func checkNumber(spec FieldSpec, f float64) *validation.FieldError {
	upper := float64(maxNumber)
	if spec.Type == TypeInteger {
		if f != math.Trunc(f) {
			return invalidValue("value must be a whole number")
		}
		upper = maxInteger
	}
	if f < 0 || f > upper {
		return invalidValue("value is out of range")
	}
	return nil
}

func decodeRange(spec FieldSpec, raw json.RawMessage) (any, *validation.FieldError) {
	obj, ok := asObject(raw)
	if !ok || len(obj) != 2 {
		return nil, typeMismatch(`value must be {"min": number, "max": number}`)
	}
	lo, okLo := asNumber(obj["min"])
	hi, okHi := asNumber(obj["max"])
	if !okLo || !okHi {
		return nil, typeMismatch(`value must be {"min": number, "max": number}`)
	}
	if fe := checkNumber(spec, lo); fe != nil {
		return nil, fe
	}
	if fe := checkNumber(spec, hi); fe != nil {
		return nil, fe
	}
	if lo > hi {
		return nil, invalidValue("min must not be greater than max")
	}
	return numRange{min: lo, max: hi}, nil
}

func decodeDays(raw json.RawMessage) (any, *validation.FieldError) {
	obj, ok := asObject(raw)
	if !ok || len(obj) != 1 {
		return nil, typeMismatch(`value must be {"days": integer}`)
	}
	d, ok := asNumber(obj["days"])
	if !ok {
		return nil, typeMismatch(`value must be {"days": integer}`)
	}
	if d != math.Trunc(d) || d < 1 || d > maxDays {
		return nil, invalidValue("days must be a whole number from 1 to " + strconv.Itoa(maxDays))
	}
	return int(d), nil
}

func asString(raw json.RawMessage) (string, bool) {
	if jsonKind(raw) != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func asNumber(raw json.RawMessage) (float64, bool) {
	k := jsonKind(raw)
	if k != '-' && (k < '0' || k > '9') {
		return 0, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if dec.Decode(&n) != nil {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if jsonKind(raw) != '{' {
		return nil, false
	}
	var obj map[string]json.RawMessage
	return obj, json.Unmarshal(raw, &obj) == nil
}

func typeMismatch(msg string) *validation.FieldError {
	return &validation.FieldError{Code: validation.CodeTypeMismatch, Message: msg, Err: ErrTypeMismatch}
}

func invalidValue(msg string) *validation.FieldError {
	return &validation.FieldError{Code: validation.CodeInvalidValue, Message: msg, Err: validation.ErrInvalidValue}
}
