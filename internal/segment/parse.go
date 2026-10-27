package segment

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Parse limits. Parse walks the document once, token by token (linear in the input size), and
// stops descending where Validate would reject anyway, so an adversarial document (deep nesting,
// huge arrays, many unknown keys) cannot make it expensive.
const (
	// maxParseDepth: a group below this depth is not parsed; its depth-(MaxDepth+1) ancestor is
	// reported as max_depth_exceeded at the same path Validate uses.
	maxParseDepth = MaxDepth + 1
	// maxParseConditions: up to this many conditions are parsed (so Validate can report
	// max_conditions_exceeded); beyond it Parse retains no more nodes, only counts the rest in the
	// same linear walk, and reports max_conditions_exceeded (with the exact count) itself.
	maxParseConditions = MaxConditions + 1
	// maxFieldErrors bounds the error list of one document.
	maxFieldErrors = 20
	// maxKeyRunes / invalidKey: an unknown property name is echoed into a field-error path only
	// if it is a short identifier; anything else (it may be model output) becomes invalidKey.
	maxKeyRunes = 64
	invalidKey  = "_invalid_key_"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse decodes a document of the form {"rule": <group>} (the emit_segment_rule tool input and
// the POST /api/segments/count body). Structural checks only: unknown keys, group/condition
// mixing, non-string or missing field/op, a bare condition as root. Field and operator names,
// value types and limits are Validate's job, except that Parse refuses to walk past the limits
// (a group nested deeper than MaxDepth+1 => max_depth_exceeded; more than MaxConditions+1
// conditions => max_conditions_exceeded). Errors: *validation.Error matching
// validation.ErrInvalidJSON or validation.ErrSchemaViolation (or the two limit sentinels).
func Parse(raw []byte) (Rule, error) {
	if !json.Valid(raw) {
		return Rule{}, &validation.Error{Fields: []validation.FieldError{{
			Path: "", Code: validation.CodeInvalidJSON, Message: "segment rule is not valid JSON", Err: validation.ErrInvalidJSON,
		}}}
	}
	p := &parser{dec: json.NewDecoder(bytes.NewReader(raw))}
	root, errs, err := p.document()
	if err != nil {
		// Unreachable for json.Valid input; keep a structural error rather than panicking.
		return Rule{}, newError([]validation.FieldError{violation("", "document could not be read")})
	}
	if p.tooMany {
		limit, actual := MaxConditions, p.conds
		errs = append(errs, validation.FieldError{
			Path: "rule", Code: validation.CodeMaxConditionsExceeded,
			Message: "segment rule has more than " + strconv.Itoa(MaxConditions) + " conditions",
			Limit:   &limit, Actual: &actual, Err: ErrMaxConditionsExceeded,
		})
	}
	if len(errs) > 0 {
		return Rule{}, newError(errs)
	}
	return Rule{Root: *root}, nil
}

func newError(fields []validation.FieldError) *validation.Error {
	if len(fields) > maxFieldErrors {
		fields = fields[:maxFieldErrors]
	}
	return &validation.Error{Fields: fields}
}

func violation(path, msg string) validation.FieldError {
	return validation.FieldError{Path: path, Code: validation.CodeSchemaViolation, Message: msg, Err: validation.ErrSchemaViolation}
}

// keyPath appends an unknown property name to path, replacing names that are not short
// identifiers so that untrusted text is never echoed at length.
func keyPath(path, key string) string {
	if len([]rune(key)) > maxKeyRunes || !identifier.MatchString(key) {
		key = invalidKey
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

type parser struct {
	dec     *json.Decoder
	conds   int  // conditions parsed so far
	tooMany bool // more than maxParseConditions conditions: the rest is skipped
}

// result of parsing one node object.
type result struct {
	node    *Node // nil when structurally broken
	group   bool
	errs    []validation.FieldError
	tooDeep bool // the node is a group deeper than maxParseDepth (skipped, no errs)
}

// document parses the root object; errs follow the order unknown document keys (sorted), then
// the rule's errors.
func (p *parser) document() (*Node, []validation.FieldError, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if tok != json.Delim('{') {
		if err := p.skipRest(tok); err != nil {
			return nil, nil, err
		}
		return nil, []validation.FieldError{violation("", "document must be a JSON object with a rule property")}, nil
	}
	var unknown []string
	var rule *result
	for p.dec.More() {
		key, err := p.key()
		if err != nil {
			return nil, nil, err
		}
		if key != "rule" {
			unknown = appendKey(unknown, key)
			if err := p.skipValue(); err != nil {
				return nil, nil, err
			}
			continue
		}
		r, err := p.value("rule", 1)
		if err != nil {
			return nil, nil, err
		}
		rule = &r
	}
	if _, err := p.dec.Token(); err != nil { // '}'
		return nil, nil, err
	}
	slices.Sort(unknown)
	var errs []validation.FieldError
	for _, k := range unknown {
		errs = append(errs, violation(k, "unknown property"))
	}
	if rule == nil {
		return nil, append(errs, violation("rule", "rule is missing")), nil
	}
	errs = append(errs, rule.errs...)
	if rule.node != nil && !rule.group {
		errs = append(errs, violation("rule", "rule root must be a group (all, any or not)"))
	}
	if len(errs) > 0 || rule.node == nil {
		return nil, errs, nil
	}
	return rule.node, nil, nil
}

// value parses the value at path, which must be a group or condition object; depth is the depth
// this node has if it is a group.
func (p *parser) value(path string, depth int) (result, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return result{}, err
	}
	if tok != json.Delim('{') {
		if err := p.skipRest(tok); err != nil {
			return result{}, err
		}
		return result{errs: []validation.FieldError{violation(path, "must be a group or a condition object")}}, nil
	}
	return p.object(path, depth)
}

// object parses one node object after its '{'. The classification (group, condition, mixed,
// unknown keys) is decided on the full key set, exactly as before: children errors only count
// when the object itself is a well-formed group.
func (p *parser) object(path string, depth int) (result, error) {
	var (
		seen               = map[string]bool{}
		unknown            []string
		children           []Node
		childErrs          []validation.FieldError
		childBroken        bool
		notChild           *Node
		groupValueBad      bool // all/any value is not an array
		field, op          string
		fieldOK, opOK      bool
		fieldSeen, opSeen  bool
		value              json.RawMessage
		depthExceeded      bool // a child group is too deep: report this node, skip the rest
		groupKey           string
		tooDeepGroupMarker bool
	)
	for p.dec.More() {
		key, err := p.key()
		if err != nil {
			return result{}, err
		}
		if depthExceeded || tooDeepGroupMarker {
			if err := p.skipValue(); err != nil {
				return result{}, err
			}
			continue
		}
		switch key {
		case "all", "any", "not":
			seen[key] = true
			groupKey = key
			if depth > maxParseDepth {
				// This node is a group nested too deep: do not walk it at all.
				tooDeepGroupMarker = true
				if err := p.skipValue(); err != nil {
					return result{}, err
				}
				continue
			}
			kpath := path + "." + key
			if key == "not" {
				r, err := p.value(kpath, depth+1)
				if err != nil {
					return result{}, err
				}
				switch {
				case r.tooDeep:
					depthExceeded = true
				case r.node == nil:
					childBroken = true
					childErrs = append(childErrs, r.errs...)
				default:
					notChild = r.node
				}
				continue
			}
			nodes, errs, broken, bad, deep, err := p.array(kpath, depth+1)
			if err != nil {
				return result{}, err
			}
			children, childErrs, childBroken, groupValueBad = nodes, append(childErrs, errs...), childBroken || broken, bad
			if deep {
				depthExceeded = true
			}
		case "field", "op":
			seen[key] = true
			s, ok, err := p.stringValue()
			if err != nil {
				return result{}, err
			}
			if key == "field" {
				field, fieldOK, fieldSeen = s, ok, true
			} else {
				op, opOK, opSeen = s, ok, true
			}
		case "value":
			seen[key] = true
			var raw json.RawMessage
			if err := p.dec.Decode(&raw); err != nil {
				return result{}, err
			}
			value = append(json.RawMessage(nil), bytes.TrimSpace(raw)...)
		default:
			unknown = appendKey(unknown, key)
			if err := p.skipValue(); err != nil {
				return result{}, err
			}
		}
	}
	if _, err := p.dec.Token(); err != nil { // '}'
		return result{}, err
	}

	if tooDeepGroupMarker {
		return result{tooDeep: true}, nil
	}
	if depthExceeded {
		limit, actual := MaxDepth, depth
		return result{errs: []validation.FieldError{{
			Path: path, Code: validation.CodeMaxDepthExceeded,
			Message: "segment rule is nested deeper than " + strconv.Itoa(MaxDepth) + " groups",
			Limit:   &limit, Actual: &actual, Err: ErrMaxDepthExceeded,
		}}}, nil
	}

	groupKeys := 0
	for _, k := range []string{"all", "any", "not"} {
		if seen[k] {
			groupKeys++
		}
	}
	condKeys := seen["field"] || seen["op"] || seen["value"]
	switch {
	case groupKeys > 0 && condKeys, groupKeys > 1:
		return result{errs: []validation.FieldError{violation(path, "an object is either a group (exactly one of all, any, not) or a condition")}}, nil
	case len(unknown) > 0:
		slices.Sort(unknown)
		errs := make([]validation.FieldError, 0, len(unknown))
		for _, k := range unknown {
			errs = append(errs, violation(path+"."+k, "unknown property"))
		}
		return result{errs: errs}, nil
	case groupKeys == 1:
		if groupValueBad {
			return result{group: true, errs: []validation.FieldError{violation(path+"."+groupKey, groupKey+" must be an array")}}, nil
		}
		if childBroken {
			return result{group: true, errs: childErrs}, nil
		}
		switch groupKey {
		case "not":
			return result{node: &Node{Not: notChild}, group: true}, nil
		case "all":
			return result{node: &Node{All: children}, group: true}, nil
		default:
			return result{node: &Node{Any: children}, group: true}, nil
		}
	case condKeys:
		var errs []validation.FieldError
		if !fieldSeen {
			errs = append(errs, violation(path+".field", "field is missing"))
		} else if !fieldOK {
			errs = append(errs, violation(path+".field", "field must be a string"))
		}
		if !opSeen {
			errs = append(errs, violation(path+".op", "op is missing"))
		} else if !opOK {
			errs = append(errs, violation(path+".op", "op must be a string"))
		}
		if len(errs) > 0 {
			return result{errs: errs}, nil
		}
		p.conds++
		if p.conds > maxParseConditions {
			p.tooMany = true
		}
		return result{node: &Node{Cond: &Condition{Field: field, Op: op, Value: value}}}, nil
	default:
		return result{errs: []validation.FieldError{violation(path, "must be a group or a condition object")}}, nil
	}
}

// array parses the children of all/any. bad: the value is not an array. deep: a child group is
// nested too deep (the rest of the array is skipped). Once too many conditions were seen, the
// remaining elements are skipped.
func (p *parser) array(path string, childDepth int) (nodes []Node, errs []validation.FieldError, broken, bad, deep bool, err error) {
	tok, err := p.dec.Token()
	if err != nil {
		return nil, nil, false, false, false, err
	}
	if tok != json.Delim('[') {
		return nil, nil, false, true, false, p.skipRest(tok)
	}
	nodes = []Node{}
	for i := 0; p.dec.More(); i++ {
		if deep {
			if err := p.skipValue(); err != nil {
				return nil, nil, false, false, false, err
			}
			continue
		}
		r, err := p.value(path+"["+strconv.Itoa(i)+"]", childDepth)
		if err != nil {
			return nil, nil, false, false, false, err
		}
		switch {
		case r.tooDeep:
			deep = true
		case r.node == nil:
			broken = true
			if len(errs) < maxFieldErrors {
				errs = append(errs, r.errs...)
			}
		case p.tooMany:
			// Past the condition cap: keep walking (to count) but retain nothing.
		default:
			nodes = append(nodes, *r.node)
		}
	}
	if _, err := p.dec.Token(); err != nil { // ']'
		return nil, nil, false, false, false, err
	}
	return nodes, errs, broken, false, deep, nil
}

func (p *parser) key() (string, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return "", err
	}
	k, ok := tok.(string)
	if !ok {
		return "", errors.New("segment: object key expected")
	}
	return k, nil
}

// stringValue reads the next value; ok reports whether it is a JSON string.
func (p *parser) stringValue() (string, bool, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return "", false, err
	}
	if s, ok := tok.(string); ok {
		return s, true, nil
	}
	return "", false, p.skipRest(tok)
}

// skipValue consumes the next value without building it.
func (p *parser) skipValue() error {
	tok, err := p.dec.Token()
	if err != nil {
		return err
	}
	return p.skipRest(tok)
}

// skipRest consumes the remainder of a value whose first token was tok.
func (p *parser) skipRest(tok json.Token) error {
	d, ok := tok.(json.Delim)
	if !ok || d == '}' || d == ']' {
		return nil
	}
	for depth := 1; depth > 0; {
		t, err := p.dec.Token()
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// appendKey records an unknown key once (after replacing an untrusted name by invalidKey),
// keeping at most maxFieldErrors of them.
func appendKey(keys []string, k string) []string {
	k = keyPath("", k)
	if len(keys) >= maxFieldErrors || slices.Contains(keys, k) {
		return keys
	}
	return append(keys, k)
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
