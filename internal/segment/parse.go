package segment

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Parse decodes a document of the form {"rule": <group>} (the emit_segment_rule tool input and
// the POST /api/segments/count body). Structural checks only: unknown keys, group/condition
// mixing, non-string or missing field/op, a bare condition as root. Field and operator names,
// value types and limits are Validate's job. Errors: *validation.Error matching
// validation.ErrInvalidJSON or validation.ErrSchemaViolation.
func Parse(raw []byte) (Rule, error) {
	if !json.Valid(raw) {
		return Rule{}, &validation.Error{Fields: []validation.FieldError{{
			Path: "", Code: validation.CodeInvalidJSON, Message: "segment rule is not valid JSON", Err: validation.ErrInvalidJSON,
		}}}
	}
	p := &parser{}
	var doc map[string]json.RawMessage
	if jsonKind(raw) != '{' || json.Unmarshal(raw, &doc) != nil {
		p.violation("", "document must be a JSON object with a rule property")
		return Rule{}, p.err()
	}
	for _, k := range sortedKeys(doc) {
		if k != "rule" {
			p.violation(k, "unknown property")
		}
	}
	ruleRaw, ok := doc["rule"]
	if !ok {
		p.violation("rule", "rule is missing")
		return Rule{}, p.err()
	}
	root, isGroup := p.node(ruleRaw, "rule")
	if root != nil && !isGroup {
		p.violation("rule", "rule root must be a group (all, any or not)")
	}
	if err := p.err(); err != nil {
		return Rule{}, err
	}
	return Rule{Root: *root}, nil
}

type parser struct{ fields []validation.FieldError }

func (p *parser) violation(path, msg string) {
	p.fields = append(p.fields, validation.FieldError{
		Path: path, Code: validation.CodeSchemaViolation, Message: msg, Err: validation.ErrSchemaViolation,
	})
}

func (p *parser) err() error {
	if len(p.fields) == 0 {
		return nil
	}
	return &validation.Error{Fields: p.fields}
}

// node parses one group or condition object. It returns nil when the object is structurally
// broken (an error was recorded) and reports whether it is a group.
func (p *parser) node(raw json.RawMessage, path string) (*Node, bool) {
	var obj map[string]json.RawMessage
	if jsonKind(raw) != '{' || json.Unmarshal(raw, &obj) != nil {
		p.violation(path, "must be a group or a condition object")
		return nil, false
	}
	var groupKeys, condKeys, otherKeys []string
	for _, k := range sortedKeys(obj) {
		switch k {
		case "all", "any", "not":
			groupKeys = append(groupKeys, k)
		case "field", "op", "value":
			condKeys = append(condKeys, k)
		default:
			otherKeys = append(otherKeys, k)
		}
	}
	switch {
	case len(groupKeys) > 0 && len(condKeys) > 0, len(groupKeys) > 1:
		p.violation(path, "an object is either a group (exactly one of all, any, not) or a condition")
		return nil, false
	case len(otherKeys) > 0:
		for _, k := range otherKeys {
			p.violation(path+"."+k, "unknown property")
		}
		return nil, false
	case len(groupKeys) == 1:
		return p.group(obj, groupKeys[0], path), true
	case len(condKeys) > 0:
		return p.condition(obj, path), false
	default:
		p.violation(path, "must be a group or a condition object")
		return nil, false
	}
}

func (p *parser) group(obj map[string]json.RawMessage, key, path string) *Node {
	raw := obj[key]
	kpath := path + "." + key
	if key == "not" {
		child, _ := p.node(raw, kpath)
		if child == nil {
			return nil
		}
		return &Node{Not: child}
	}
	var items []json.RawMessage
	if jsonKind(raw) != '[' || json.Unmarshal(raw, &items) != nil {
		p.violation(kpath, key+" must be an array")
		return nil
	}
	children := make([]Node, 0, len(items))
	ok := true
	for i, item := range items {
		child, _ := p.node(item, kpath+"["+strconv.Itoa(i)+"]")
		if child == nil {
			ok = false
			continue
		}
		children = append(children, *child)
	}
	if !ok {
		return nil
	}
	if key == "all" {
		return &Node{All: children}
	}
	return &Node{Any: children}
}

func (p *parser) condition(obj map[string]json.RawMessage, path string) *Node {
	field, okField := p.stringProp(obj, "field", path)
	op, okOp := p.stringProp(obj, "op", path)
	if !okField || !okOp {
		return nil
	}
	c := &Condition{Field: field, Op: op}
	if v, ok := obj["value"]; ok {
		c.Value = append(json.RawMessage(nil), bytes.TrimSpace(v)...)
	}
	return &Node{Cond: c}
}

func (p *parser) stringProp(obj map[string]json.RawMessage, key, path string) (string, bool) {
	raw, ok := obj[key]
	if !ok {
		p.violation(path+"."+key, key+" is missing")
		return "", false
	}
	var s string
	if jsonKind(raw) != '"' || json.Unmarshal(raw, &s) != nil {
		p.violation(path+"."+key, key+" must be a string")
		return "", false
	}
	return s, true
}

// jsonKind returns the first significant byte of a JSON value: '{', '[', '"', 'n', 't', 'f' or a
// digit/'-' for numbers; 0 for empty input.
func jsonKind(raw []byte) byte {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return 0
	}
	return t[0]
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
