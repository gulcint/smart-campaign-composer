// Package segment is the audience rule DSL (docs/contracts/segment-dsl.md): structural Parse,
// semantic Validate against the closed field/operator/type sets, translation to a parameterized
// SQL predicate (Compile) and the equivalent in-memory evaluation (Match).
//
// LLM output never reaches SQL as text: Compile emits only constant SQL fragments chosen from a
// fixed table, and every rule value becomes a $n parameter.
package segment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	MaxDepth      = 3
	MaxConditions = 10
)

// FieldType is the DSL type of a field; it decides the allowed operators and value shapes.
type FieldType string

const (
	TypeString    FieldType = "string"
	TypeEnum      FieldType = "enum"
	TypeInteger   FieldType = "integer"
	TypeNumber    FieldType = "number"
	TypeTimestamp FieldType = "timestamp"
)

// FieldSpec describes one allowed field.
type FieldSpec struct {
	Name      string
	Type      FieldType
	Operators []string // allowed operators, segment-dsl.md §3
	Enum      []string // only for TypeEnum
}

// Operators (segment-dsl.md §3).
const (
	opEq         = "eq"
	opNeq        = "neq"
	opIn         = "in"
	opNotIn      = "not_in"
	opGt         = "gt"
	opGte        = "gte"
	opLt         = "lt"
	opLte        = "lte"
	opBetween    = "between"
	opWithinLast = "within_last"
	opOlderThan  = "older_than"
	opIsNull     = "is_null"
	opIsNotNull  = "is_not_null"
)

var knownOperators = []string{
	opEq, opNeq, opIn, opNotIn, opGt, opGte, opLt, opLte, opBetween,
	opWithinLast, opOlderThan, opIsNull, opIsNotNull,
}

var (
	stringOps    = []string{opEq, opNeq, opIn, opNotIn}
	numericOps   = []string{opEq, opNeq, opGt, opGte, opLt, opLte, opBetween}
	timestampOps = []string{opWithinLast, opOlderThan, opIsNull, opIsNotNull}
)

// fieldSpecs is the closed field list of segment-dsl.md §2, in that order.
var fieldSpecs = []FieldSpec{
	{Name: "city", Type: TypeString, Operators: stringOps},
	{Name: "language", Type: TypeEnum, Operators: stringOps, Enum: []string{"tr", "en"}},
	{Name: "loyalty_tier", Type: TypeEnum, Operators: stringOps, Enum: []string{"bronze", "silver", "gold", "platinum"}},
	{Name: "last_cart_add_at", Type: TypeTimestamp, Operators: timestampOps},
	{Name: "last_purchase_at", Type: TypeTimestamp, Operators: timestampOps},
	{Name: "orders_30d", Type: TypeInteger, Operators: numericOps},
	{Name: "cart_adds_30d", Type: TypeInteger, Operators: numericOps},
	{Name: "total_orders", Type: TypeInteger, Operators: numericOps},
	{Name: "total_spent", Type: TypeNumber, Operators: numericOps},
}

// Fields returns the 9 fields of segment-dsl.md §2, in that order (a copy).
func Fields() []FieldSpec {
	out := make([]FieldSpec, len(fieldSpecs))
	for i, f := range fieldSpecs {
		f.Operators = append([]string(nil), f.Operators...)
		if f.Enum != nil {
			f.Enum = append([]string(nil), f.Enum...)
		}
		out[i] = f
	}
	return out
}

func lookupField(name string) (FieldSpec, bool) {
	for _, f := range fieldSpecs {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// Node is exactly one of: All, Any, Not (groups) or Cond (leaf).
type Node struct {
	All  []Node
	Any  []Node
	Not  *Node
	Cond *Condition
}

// Condition is one leaf of the rule tree.
type Condition struct {
	Field string
	Op    string
	Value json.RawMessage // nil when absent
}

// Rule is a parsed segment rule; Root is the root group.
type Rule struct{ Root Node }

// MarshalJSON emits the root group (e.g. {"all":[...]}), i.e. the value of the "rule" property.
// Keys are emitted in a fixed order (field, op, value) and values compacted, so equal rules
// marshal to equal bytes.
func (r Rule) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	if err := writeNode(&b, r.Root); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeNode(b *bytes.Buffer, n Node) error {
	switch {
	case n.All != nil:
		return writeGroup(b, "all", n.All)
	case n.Any != nil:
		return writeGroup(b, "any", n.Any)
	case n.Not != nil:
		b.WriteString(`{"not":`)
		if err := writeNode(b, *n.Not); err != nil {
			return err
		}
		b.WriteByte('}')
		return nil
	case n.Cond != nil:
		b.WriteString(`{"field":`)
		writeString(b, n.Cond.Field)
		b.WriteString(`,"op":`)
		writeString(b, n.Cond.Op)
		if n.Cond.Value != nil {
			b.WriteString(`,"value":`)
			if err := json.Compact(b, n.Cond.Value); err != nil {
				return errors.New("segment: condition value is not valid JSON")
			}
		}
		b.WriteByte('}')
		return nil
	}
	return errors.New("segment: empty node")
}

func writeGroup(b *bytes.Buffer, key string, children []Node) error {
	b.WriteString(`{"` + key + `":[`)
	for i, c := range children {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeNode(b, c); err != nil {
			return err
		}
	}
	b.WriteString("]}")
	return nil
}

// writeString writes s as a JSON string without HTML escaping.
func writeString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)       // a string always encodes
	b.Truncate(b.Len() - 1) // drop Encode's trailing newline
}

// Field errors of Validate match these sentinels ("invalid_value" matches validation.ErrInvalidValue).
var (
	ErrUnknownField          = errors.New("segment: unknown field")           // "unknown_field"
	ErrUnknownOperator       = errors.New("segment: unknown operator")        // "unknown_operator"
	ErrOperatorNotAllowed    = errors.New("segment: operator not allowed")    // "operator_not_allowed"
	ErrTypeMismatch          = errors.New("segment: type mismatch")           // "type_mismatch"
	ErrEmptyGroup            = errors.New("segment: empty group")             // "empty_group"
	ErrMaxDepthExceeded      = errors.New("segment: max depth exceeded")      // "max_depth_exceeded"
	ErrMaxConditionsExceeded = errors.New("segment: max conditions exceeded") // "max_conditions_exceeded"
)

// Compiled is a parameterized SQL predicate.
type Compiled struct {
	Where string // SQL boolean expression over aliases c (customers) and s (customer_stats_at($1))
	Args  []any  // Args[0] == ref (time.Time, UTC); rule values follow as $2, $3, ...
}

// Facts are one customer's DSL-visible attributes at a reference time (customer_stats_at semantics).
type Facts struct {
	City, Language, LoyaltyTier   string
	LastCartAddAt, LastPurchaseAt *time.Time
	Orders30d, CartAdds30d        int
	TotalOrders                   int
	TotalSpent                    float64
}

// Audience is the matched set size and its per-channel, consent-filtered subsets (REQ-AUD-03).
type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
	// Languages is the per-channel split by customers.language; tr + en == the channel count. nil only
	// for a campaign whose segment was generated before P8 (campaigns.audience_languages IS NULL; D63).
	Languages *AudienceLanguages `json:"languages,omitempty"`
}

// AudienceLanguages is the per-channel language split of an Audience (P8, D63).
type AudienceLanguages struct {
	SMS   LanguageCounts `json:"sms"`
	Email LanguageCounts `json:"email"`
}

// LanguageCounts are the customers of one channel by customers.language.
type LanguageCounts struct {
	TR int `json:"tr"`
	EN int `json:"en"`
}

// WantsEnglish reports whether an English version is worth generating: Languages != nil and
// SMS.EN + Email.EN > 0 (REQ-LNG-02).
func (a Audience) WantsEnglish() bool {
	return a.Languages != nil && a.Languages.SMS.EN+a.Languages.Email.EN > 0
}

// Counter counts the audience of a valid rule at ref (channel consent applied, segment-dsl.md §6).
// It also fills Audience.Languages (same population, split by customers.language; P8).
// Implemented by postgres.NewAudience.
type Counter interface {
	Count(ctx context.Context, r Rule, ref time.Time) (Audience, error)
}
