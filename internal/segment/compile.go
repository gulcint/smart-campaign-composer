package segment

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// sqlColumn is the fixed field -> SQL expression table (segment-dsl.md §5 rule 2). Field names
// from a rule are only ever used as keys into this map, never copied into SQL.
var sqlColumn = map[string]string{
	"city":             "c.city",
	"language":         "c.language",
	"loyalty_tier":     "c.loyalty_tier",
	"last_cart_add_at": "s.last_cart_add_at",
	"last_purchase_at": "s.last_purchase_at",
	"orders_30d":       "s.orders_30d",
	"cart_adds_30d":    "s.cart_adds_30d",
	"total_orders":     "s.total_orders",
	"total_spent":      "s.total_spent",
}

var sqlComparison = map[string]string{
	opEq: "=", opNeq: "<>", opGt: ">", opGte: ">=", opLt: "<", opLte: "<=",
}

// Compile validates r (returning Validate's error if invalid) and translates it to a
// parameterized predicate. No rule value ever appears in Where.
//
// Timestamp comparisons are guarded with IS NOT NULL so that a NULL timestamp makes the
// condition false (not unknown); `not` therefore behaves exactly like Match's two-valued logic.
// Relative days are exact 24-hour periods (make_interval(hours => days*24)), the same as Match's
// days×24h, so the result does not depend on the session TimeZone (calendar-day interval
// arithmetic would shift across DST changes).
func Compile(r Rule, ref time.Time) (Compiled, error) {
	if err := Validate(r); err != nil {
		return Compiled{}, err
	}
	c := &compiler{args: []any{ref.UTC()}}
	where := c.node(r.Root)
	return Compiled{Where: where, Args: c.args}, nil
}

type compiler struct{ args []any }

func (c *compiler) param(v any) string {
	c.args = append(c.args, v)
	return "$" + strconv.Itoa(len(c.args))
}

func (c *compiler) node(n Node) string {
	switch kind(n) {
	case kindAll:
		return c.join(n.All, " AND ")
	case kindAny:
		return c.join(n.Any, " OR ")
	case kindNot:
		return "NOT (" + c.node(*n.Not) + ")"
	}
	return c.condition(*n.Cond)
}

func (c *compiler) join(nodes []Node, sep string) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = c.node(n)
	}
	return "(" + strings.Join(parts, sep) + ")"
}

func (c *compiler) condition(cond Condition) string {
	spec, _ := lookupField(cond.Field)
	col := sqlColumn[spec.Name]
	v, _ := decodeValue(spec, cond.Op, cond.Value) // validated by Compile
	switch cond.Op {
	case opIn:
		return "(" + col + " = ANY(" + c.param(v) + "::text[]))"
	case opNotIn:
		return "(NOT (" + col + " = ANY(" + c.param(v) + "::text[])))"
	case opBetween:
		rg := v.(numRange)
		return "(" + col + " BETWEEN " + c.param(numArg(spec, rg.min)) + " AND " + c.param(numArg(spec, rg.max)) + ")"
	case opWithinLast:
		days := c.param(v)
		return "(" + col + " IS NOT NULL AND " + col + " >= $1 - make_interval(hours => " + days + "::integer * 24) AND " + col + " <= $1)"
	case opOlderThan:
		return "(" + col + " IS NOT NULL AND " + col + " < $1 - make_interval(hours => " + c.param(v) + "::integer * 24))"
	case opIsNull:
		return "(" + col + " IS NULL)"
	case opIsNotNull:
		return "(" + col + " IS NOT NULL)"
	}
	// eq, neq, gt, gte, lt, lte
	arg := v
	if f, ok := v.(float64); ok {
		arg = numArg(spec, f)
	}
	return "(" + col + " " + sqlComparison[cond.Op] + " " + c.param(arg) + ")"
}

// numArg returns an int for integer fields and a float64 for number fields.
func numArg(spec FieldSpec, f float64) any {
	if spec.Type == TypeInteger {
		return int(f)
	}
	return f
}

// Match evaluates r in memory with the same semantics as Compile + SQL (used by cmd/seed to
// compute manifest counts). r must be valid; otherwise Validate's error is returned.
func Match(r Rule, f Facts, ref time.Time) (bool, error) {
	if err := Validate(r); err != nil {
		return false, err
	}
	return matchNode(r.Root, f, ref.UTC()), nil
}

func matchNode(n Node, f Facts, ref time.Time) bool {
	switch kind(n) {
	case kindAll:
		for _, c := range n.All {
			if !matchNode(c, f, ref) {
				return false
			}
		}
		return true
	case kindAny:
		for _, c := range n.Any {
			if matchNode(c, f, ref) {
				return true
			}
		}
		return false
	case kindNot:
		return !matchNode(*n.Not, f, ref)
	}
	return matchCondition(*n.Cond, f, ref)
}

func matchCondition(cond Condition, f Facts, ref time.Time) bool {
	spec, _ := lookupField(cond.Field)
	v, _ := decodeValue(spec, cond.Op, cond.Value) // validated by Match
	switch spec.Type {
	case TypeString, TypeEnum:
		got := map[string]string{"city": f.City, "language": f.Language, "loyalty_tier": f.LoyaltyTier}[spec.Name]
		switch cond.Op {
		case opEq:
			return got == v.(string)
		case opNeq:
			return got != v.(string)
		case opIn:
			return slices.Contains(v.([]string), got)
		default: // not_in
			return !slices.Contains(v.([]string), got)
		}
	case TypeInteger, TypeNumber:
		got := map[string]float64{
			"orders_30d": float64(f.Orders30d), "cart_adds_30d": float64(f.CartAdds30d),
			"total_orders": float64(f.TotalOrders), "total_spent": f.TotalSpent,
		}[spec.Name]
		if cond.Op == opBetween {
			rg := v.(numRange)
			return got >= rg.min && got <= rg.max
		}
		want := v.(float64)
		switch cond.Op {
		case opEq:
			return got == want
		case opNeq:
			return got != want
		case opGt:
			return got > want
		case opGte:
			return got >= want
		case opLt:
			return got < want
		default: // lte
			return got <= want
		}
	}
	// timestamp
	t := f.LastCartAddAt
	if spec.Name == "last_purchase_at" {
		t = f.LastPurchaseAt
	}
	switch cond.Op {
	case opIsNull:
		return t == nil
	case opIsNotNull:
		return t != nil
	}
	if t == nil {
		return false
	}
	bound := ref.Add(-time.Duration(v.(int)) * 24 * time.Hour)
	if cond.Op == opWithinLast {
		return !t.Before(bound) && !t.After(ref)
	}
	return t.Before(bound) // older_than
}
