// Package content holds the campaign copy model, its single validator and the renderers.
//
// One validator for LLM output and human edits (REQ-APR-02): Parse checks the structure of a
// content.schema.json document, Validate checks the content rules (required, template variables,
// lengths AFTER substituting every variable with the segment's longest values). Render and
// RenderEmailHTML produce the per-recipient texts and the escaped email HTML
// (docs/contracts/go-interfaces.md §content).
package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

const (
	SMSMaxRunes       = 160
	SubjectMaxRunes   = 80
	EmailBodyMaxRunes = 5000
	FirstNameFallback = "Değerli müşterimiz"
	// P8 (D63)
	FirstNameFallbackEN = "Dear customer" // 13 runes
	LangTR              = "tr"
	LangEN              = "en"
)

// AllowedVariables are the only template placeholders (REQ-CNT-03).
var AllowedVariables = []string{"first_name", "last_name", "city"}

// Content is one SMS + email copy (content.schema.json). SMS and Email are the Turkish main copy;
// EN is the optional English version (nil = no English version; P8, D63).
type Content struct {
	SMS   SMS      `json:"sms"`
	Email Email    `json:"email"`
	EN    *English `json:"en,omitempty"`
}

// English is the English version of a Content (P8, D63).
type English struct {
	SMS   SMS   `json:"sms"`
	Email Email `json:"email"`
}

// SMS is the SMS part of a Content.
type SMS struct {
	Body string `json:"body"`
}

// Email is the email part of a Content.
type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// WorstCase are the post-substitution lengths reported on every content version whose template
// syntax is valid (API field ContentVersion.worst_case).
type WorstCase struct {
	SMSRunes     int            `json:"sms_runes"`
	SubjectRunes int            `json:"subject_runes"`
	EN           *WorstCaseLang `json:"en,omitempty"` // P8: nil iff Content.EN == nil
}

// WorstCaseLang are the worst-case lengths of one extra language version (P8).
type WorstCaseLang struct {
	SMSRunes     int `json:"sms_runes"`
	SubjectRunes int `json:"subject_runes"`
}

// Longest holds the longest value lengths (runes) over the customers MATCHED by the segment rule at
// its reference time (before consent filtering). FirstName already includes the fallback.
type Longest struct {
	FirstName int `json:"first_name"`
	LastName  int `json:"last_name"`
	City      int `json:"city"`
}

// Recipient is the personalisation data of one customer.
type Recipient struct {
	FirstName, LastName, City string
	// Language is customers.language ("tr" | "en"; "" is treated as "tr"). P8 (D63).
	Language string
}

// Rendered is a Content with every variable substituted for one recipient.
type Rendered struct {
	SMSText      string
	EmailSubject string
	EmailText    string
	// Language of the rendered text ("tr" | "en") and whether English was wanted but the content has
	// no English version (the Turkish main copy was used). P8 (D63).
	Language string
	Fallback bool
}

// Field errors of the content validator match these sentinels (empty fields match validation.ErrRequired).
var (
	ErrSMSTooLong        = errors.New("content: sms too long")        // "sms_too_long", "sms_too_long_worst_case"
	ErrSubjectTooLong    = errors.New("content: subject too long")    // "subject_too_long", "subject_too_long_worst_case"
	ErrEmailBodyTooLong  = errors.New("content: email body too long") // "email_body_too_long"
	ErrUnknownVariable   = errors.New("content: unknown variable")    // "unknown_variable"
	ErrMalformedVariable = errors.New("content: malformed variable")  // "malformed_variable"
	ErrWorstCaseTooLong  = errors.New("content: worst case too long") // additionally matched by *_worst_case codes
)

// Field paths in the order Validate reports them.
const (
	pathSMSBody      = "sms.body"
	pathEmailSubject = "email.subject"
	pathEmailBody    = "email.body"
)

// RuneCount returns the number of Unicode code points of the NFC form of s.
func RuneCount(s string) int {
	return utf8.RuneCountInString(norm.NFC.String(s))
}

// EffectiveFirstName returns strings.TrimSpace(s), or FirstNameFallback if that is empty.
func EffectiveFirstName(s string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return FirstNameFallback
}

// EffectiveFirstNameLang is EffectiveFirstName with the fallback by language: FirstNameFallbackEN
// for lang == LangEN, FirstNameFallback otherwise.
func EffectiveFirstNameLang(s, lang string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	if lang == LangEN {
		return FirstNameFallbackEN
	}
	return FirstNameFallback
}

// ---- Parse ----

// maxKeyRunes / invalidKey: an unknown property name is echoed into a field-error path only if it
// is a short identifier; anything else (it may be model output) becomes invalidKey.
const (
	maxKeyRunes = 64
	invalidKey  = "_invalid_key_"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse decodes content.schema.json documents (LLM tool input and edit request body) against the
// schema's STRUCTURE: unknown keys, wrong JSON types and missing keys are schema_violation at that
// path; an empty string is structurally valid (Validate reports it as required). Strings are
// NFC-normalized and otherwise kept as given. Errors: *validation.Error matching
// validation.ErrInvalidJSON / validation.ErrSchemaViolation.
func Parse(raw []byte) (Content, error) {
	if !json.Valid(raw) {
		return Content{}, &validation.Error{Fields: []validation.FieldError{{
			Path: "", Code: validation.CodeInvalidJSON, Message: "content is not valid JSON", Err: validation.ErrInvalidJSON,
		}}}
	}
	var errs []validation.FieldError
	root, ok := object(raw)
	if !ok {
		return Content{}, &validation.Error{Fields: []validation.FieldError{violation("", "content must be a JSON object")}}
	}
	errs = append(errs, unknownKeys(root, "", "sms", "email", "en")...)

	var c Content
	c.SMS, c.Email = parseCopy(root, "", &errs)
	if _, present := root["en"]; present {
		if en, ok := member(root, "", "en", &errs); ok {
			errs = append(errs, unknownKeys(en, "en", "sms", "email")...)
			var e English
			e.SMS, e.Email = parseCopy(en, "en", &errs)
			c.EN = &e
		}
	}
	if len(errs) > 0 {
		return Content{}, &validation.Error{Fields: errs}
	}
	return c, nil
}

// parseCopy decodes the sms and email objects of one language version under parent.
func parseCopy(m map[string]json.RawMessage, parent string, errs *[]validation.FieldError) (SMS, Email) {
	var sms SMS
	var email Email
	if obj, ok := member(m, parent, "sms", errs); ok {
		*errs = append(*errs, unknownKeys(obj, join(parent, "sms"), "body")...)
		sms.Body, _ = stringMember(obj, join(parent, "sms"), "body", errs)
	}
	if obj, ok := member(m, parent, "email", errs); ok {
		*errs = append(*errs, unknownKeys(obj, join(parent, "email"), "subject", "body")...)
		email.Subject, _ = stringMember(obj, join(parent, "email"), "subject", errs)
		email.Body, _ = stringMember(obj, join(parent, "email"), "body", errs)
	}
	return sms, email
}

func violation(path, msg string) validation.FieldError {
	return validation.FieldError{Path: path, Code: validation.CodeSchemaViolation, Message: msg, Err: validation.ErrSchemaViolation}
}

func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// object decodes raw as a JSON object.
func object(raw []byte) (map[string]json.RawMessage, bool) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return nil, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// unknownKeys reports every key of m outside allowed, in sorted order.
func unknownKeys(m map[string]json.RawMessage, parent string, allowed ...string) []validation.FieldError {
	var keys []string
	for k := range m {
		if !slices.Contains(allowed, k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]validation.FieldError, 0, len(keys))
	for _, k := range keys {
		name := k
		if utf8.RuneCountInString(k) > maxKeyRunes || !identifier.MatchString(k) {
			name = invalidKey
		}
		out = append(out, violation(join(parent, name), "unknown property"))
	}
	return out
}

// member returns the object stored under key; a missing key or a non-object is a violation.
func member(m map[string]json.RawMessage, parent, key string, errs *[]validation.FieldError) (map[string]json.RawMessage, bool) {
	raw, present := m[key]
	if !present {
		*errs = append(*errs, violation(join(parent, key), key+" is missing"))
		return nil, false
	}
	obj, ok := object(raw)
	if !ok {
		*errs = append(*errs, violation(join(parent, key), key+" must be an object"))
		return nil, false
	}
	return obj, true
}

// stringMember returns the NFC-normalized string stored under key; a missing key or a value of
// another JSON type (null included) is a violation.
func stringMember(m map[string]json.RawMessage, parent, key string, errs *[]validation.FieldError) (string, bool) {
	path := join(parent, key)
	raw, present := m[key]
	if !present {
		*errs = append(*errs, violation(path, path+" is missing"))
		return "", false
	}
	var s string
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) || json.Unmarshal(raw, &s) != nil {
		*errs = append(*errs, violation(path, path+" must be a string"))
		return "", false
	}
	return norm.NFC.String(s), true
}

// ---- template syntax ----

// part is one piece of a template: literal text, or a placeholder (variable name).
type part struct {
	text     string
	variable string // "" for text
}

// tokenize splits tmpl into text and placeholders. Grammar: "{{" [spaces] name [spaces] "}}",
// spaces = U+0020 / U+0009, name = [A-Za-z_][A-Za-z0-9_]*. An unclosed or empty "{{", a name with
// other characters and a "}}" that closes nothing are ErrMalformedVariable.
func tokenize(tmpl string) ([]part, error) {
	var parts []part
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, part{text: text.String()})
			text.Reset()
		}
	}
	isSpace := func(b byte) bool { return b == ' ' || b == '\t' }
	isStart := func(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
	isName := func(b byte) bool { return isStart(b) || (b >= '0' && b <= '9') }

	for i := 0; i < len(tmpl); {
		switch {
		case strings.HasPrefix(tmpl[i:], "{{"):
			j := i + 2
			for j < len(tmpl) && isSpace(tmpl[j]) {
				j++
			}
			if j >= len(tmpl) || !isStart(tmpl[j]) {
				return nil, fmt.Errorf("%w at byte %d", ErrMalformedVariable, i)
			}
			start := j
			for j < len(tmpl) && isName(tmpl[j]) {
				j++
			}
			name := tmpl[start:j]
			for j < len(tmpl) && isSpace(tmpl[j]) {
				j++
			}
			if !strings.HasPrefix(tmpl[j:], "}}") {
				return nil, fmt.Errorf("%w at byte %d", ErrMalformedVariable, i)
			}
			flush()
			parts = append(parts, part{variable: name})
			i = j + 2
		case strings.HasPrefix(tmpl[i:], "}}"):
			return nil, fmt.Errorf("%w at byte %d", ErrMalformedVariable, i)
		default:
			text.WriteByte(tmpl[i])
			i++
		}
	}
	flush()
	return parts, nil
}

// Variables returns the placeholder names in tmpl in order of appearance (duplicates kept).
// Names are not checked against AllowedVariables. Broken syntax => ErrMalformedVariable.
func Variables(tmpl string) ([]string, error) {
	parts, err := tokenize(tmpl)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range parts {
		if p.variable != "" {
			names = append(names, p.variable)
		}
	}
	return names, nil
}

// checkedParts tokenizes tmpl and rejects variables outside AllowedVariables (case-sensitive).
func checkedParts(tmpl string) ([]part, error) {
	parts, err := tokenize(tmpl)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		if p.variable != "" && !slices.Contains(AllowedVariables, p.variable) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownVariable, p.variable)
		}
	}
	return parts, nil
}

// worstLength is the rune count of parts with every placeholder replaced by a value of the
// corresponding Longest length.
func worstLength(parts []part, l Longest) int {
	var text strings.Builder
	n := 0
	for _, p := range parts {
		switch p.variable {
		case "":
			text.WriteString(p.text)
		case "first_name":
			n += l.FirstName
		case "last_name":
			n += l.LastName
		case "city":
			n += l.City
		}
	}
	return n + RuneCount(text.String())
}

// ---- Validate ----

type fieldSpec struct {
	path, label  string
	value        string
	limit        int
	code         string
	worstCode    string
	err          error
	recheckAtApp bool // re-checked at approval (CheckWorstCase)
}

// fieldsOf lists the checked fields: the three Turkish ones, then (P8) the English ones with the
// "en." path prefix.
func fieldsOf(c Content) []fieldSpec {
	fields := langFields("", "", c.SMS, c.Email)
	if c.EN != nil {
		fields = append(fields, langFields("en.", "English ", c.EN.SMS, c.EN.Email)...)
	}
	return fields
}

func langFields(prefix, labelPrefix string, sms SMS, email Email) []fieldSpec {
	return []fieldSpec{
		{path: prefix + pathSMSBody, label: labelPrefix + "SMS body", value: sms.Body, limit: SMSMaxRunes, code: validation.CodeSMSTooLong,
			worstCode: validation.CodeSMSTooLongWorstCase, err: ErrSMSTooLong, recheckAtApp: true},
		{path: prefix + pathEmailSubject, label: labelPrefix + "email subject", value: email.Subject, limit: SubjectMaxRunes, code: validation.CodeSubjectTooLong,
			worstCode: validation.CodeSubjectTooLongWorstCase, err: ErrSubjectTooLong, recheckAtApp: true},
		{path: prefix + pathEmailBody, label: labelPrefix + "email body", value: email.Body, limit: EmailBodyMaxRunes, code: validation.CodeEmailBodyTooLong,
			err: ErrEmailBodyTooLong},
	}
}

// syntaxError maps a template error to its field error.
func syntaxError(path, label string, err error) validation.FieldError {
	if errors.Is(err, ErrMalformedVariable) {
		return validation.FieldError{Path: path, Code: validation.CodeMalformedVariable,
			Message: label + " has a malformed {{variable}}", Err: ErrMalformedVariable}
	}
	return validation.FieldError{Path: path, Code: validation.CodeUnknownVariable,
		Message: label + " uses a variable other than {{first_name}}, {{last_name}}, {{city}}", Err: ErrUnknownVariable}
}

func lengthError(path, code, message string, limit, actual int, err error) validation.FieldError {
	return validation.FieldError{Path: path, Code: code, Message: message, Limit: &limit, Actual: &actual, Err: err}
}

// Validate is THE validator for both LLM output and human edits (REQ-APR-02, REQ-CNT-01).
// l = longest values of the campaign's CURRENT segment. Per field, in order sms.body,
// email.subject, email.body: required -> malformed_variable -> unknown_variable -> length (after
// substituting every placeholder with a value of the Longest length); at most one error per field.
// Returns nil or *validation.Error.
func Validate(c Content, l Longest) error {
	var errs []validation.FieldError
	for _, f := range fieldsOf(c) {
		if strings.TrimSpace(f.value) == "" {
			errs = append(errs, validation.FieldError{Path: f.path, Code: validation.CodeRequired,
				Message: f.label + " must not be empty", Err: validation.ErrRequired})
			continue
		}
		parts, err := checkedParts(f.value)
		if err != nil {
			errs = append(errs, syntaxError(f.path, f.label, err))
			continue
		}
		if n := worstLength(parts, l); n > f.limit {
			errs = append(errs, lengthError(f.path, f.code, f.label+" exceeds "+strconv.Itoa(f.limit)+" characters", f.limit, n, f.err))
		}
	}
	if len(errs) > 0 {
		return &validation.Error{Fields: errs}
	}
	return nil
}

// MeasureWorstCase computes WorstCase with the same substitution as Validate.
// Errors: ErrMalformedVariable, ErrUnknownVariable (any of the three Turkish fields or, when
// c.EN != nil, any of the three English ones; the wrapped message names the en.-prefixed path).
func MeasureWorstCase(c Content, l Longest) (WorstCase, error) {
	// fieldsOf order: sms, subject, body per language version.
	fs := fieldsOf(c)
	lengths := make([]int, len(fs))
	for i, f := range fs {
		parts, err := checkedParts(f.value)
		if err != nil {
			return WorstCase{}, fmt.Errorf("%s: %w", f.path, err)
		}
		lengths[i] = worstLength(parts, l)
	}
	wc := WorstCase{SMSRunes: lengths[0], SubjectRunes: lengths[1]}
	if c.EN != nil {
		wc.EN = &WorstCaseLang{SMSRunes: lengths[3], SubjectRunes: lengths[4]}
	}
	return wc, nil
}

// CheckWorstCase is the approval-time RE-check (REQ-CNT-05) with Longest recomputed at approval
// time: only sms.body <= 160 and email.subject <= 80, codes sms_too_long_worst_case /
// subject_too_long_worst_case matching the length sentinel AND ErrWorstCaseTooLong.
func CheckWorstCase(c Content, l Longest) error {
	var errs []validation.FieldError
	for _, f := range fieldsOf(c) {
		if !f.recheckAtApp {
			continue
		}
		parts, err := checkedParts(f.value)
		if err != nil {
			continue // a validated version has a valid syntax; nothing to measure otherwise
		}
		if n := worstLength(parts, l); n > f.limit {
			errs = append(errs, lengthError(f.path, f.worstCode,
				f.label+" exceeds "+strconv.Itoa(f.limit)+" characters with the longest current recipient values",
				f.limit, n, errors.Join(f.err, ErrWorstCaseTooLong)))
		}
	}
	if len(errs) > 0 {
		return &validation.Error{Fields: errs}
	}
	return nil
}

// ---- Render ----

// Render substitutes {{first_name}} (EffectiveFirstNameLang by r.Language), {{last_name}} and {{city}} (raw,
// possibly empty). Substituted values are not processed again. Errors: ErrMalformedVariable,
// ErrUnknownVariable.
func Render(tmpl string, r Recipient) (string, error) {
	parts, err := checkedParts(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.variable {
		case "":
			b.WriteString(p.text)
		case "first_name":
			b.WriteString(EffectiveFirstNameLang(r.FirstName, r.Language))
		case "last_name":
			b.WriteString(r.LastName)
		case "city":
			b.WriteString(r.City)
		}
	}
	return b.String(), nil
}

// RenderContent renders the three fields of c for r in r's language: r.Language == "en" and
// c.EN != nil => the English version (Language "en"); otherwise the Turkish main copy (Language
// "tr"), with Fallback = true when English was wanted but c has no English version. The fallback
// first name follows the language of the text used.
func RenderContent(c Content, r Recipient) (Rendered, error) {
	lang, prefix, sms0, email0, fallback := LangTR, "", c.SMS, c.Email, false
	if r.Language == LangEN {
		if c.EN != nil {
			lang, prefix, sms0, email0 = LangEN, "en.", c.EN.SMS, c.EN.Email
		} else {
			fallback = true
		}
	}
	r.Language = lang
	sms, err := Render(sms0.Body, r)
	if err != nil {
		return Rendered{}, fmt.Errorf("%s: %w", prefix+pathSMSBody, err)
	}
	subject, err := Render(email0.Subject, r)
	if err != nil {
		return Rendered{}, fmt.Errorf("%s: %w", prefix+pathEmailSubject, err)
	}
	body, err := Render(email0.Body, r)
	if err != nil {
		return Rendered{}, fmt.Errorf("%s: %w", prefix+pathEmailBody, err)
	}
	return Rendered{SMSText: sms, EmailSubject: subject, EmailText: body, Language: lang, Fallback: fallback}, nil
}

// CheckRendered is the per-recipient send-time check (REQ-CNT-05): sms_too_long /
// subject_too_long on the rendered text. nil or *validation.Error. The error paths are always the
// unprefixed "sms.body" / "email.subject", even when r is the English version (r.Language "en"):
// at send time only the failure reason content_too_long is recorded, never the path.
func CheckRendered(r Rendered) error {
	var errs []validation.FieldError
	if n := RuneCount(r.SMSText); n > SMSMaxRunes {
		errs = append(errs, lengthError(pathSMSBody, validation.CodeSMSTooLong,
			"rendered SMS body exceeds "+strconv.Itoa(SMSMaxRunes)+" characters", SMSMaxRunes, n, ErrSMSTooLong))
	}
	if n := RuneCount(r.EmailSubject); n > SubjectMaxRunes {
		errs = append(errs, lengthError(pathEmailSubject, validation.CodeSubjectTooLong,
			"rendered email subject exceeds "+strconv.Itoa(SubjectMaxRunes)+" characters", SubjectMaxRunes, n, ErrSubjectTooLong))
	}
	if len(errs) > 0 {
		return &validation.Error{Fields: errs}
	}
	return nil
}

// emailBodyStyle is the skeleton's only styling: a constant inline style on <body> (allowed by the
// document's own CSP style-src 'unsafe-inline'; no <style> element, no font download) that mirrors
// the editor field (DESIGN.md text-body 16/24, --ink-900 on --surface-card, 16px side padding).
// Figtree is used only where it is installed locally; otherwise the system sans-serif stack.
const emailBodyStyle = "margin:0;padding:0 16px;font-family:'Figtree Variable',Figtree,system-ui," +
	"-apple-system,'Segoe UI','Helvetica Neue',Arial,sans-serif;font-size:16px;line-height:24px;" +
	"color:#241c20;background:#fcfcf9;overflow-wrap:anywhere"

// RenderEmailHTML builds the escaped HTML document used for the provider and the preview.
// Paragraphs are separated by one or more blank lines; a single newline inside a paragraph becomes
// "<br>". Every text fragment goes through html.EscapeString; the only tags are the skeleton's.
// It is RenderEmailHTMLLang(LangTR, subject, bodyText).
func RenderEmailHTML(subject, bodyText string) string {
	return RenderEmailHTMLLang(LangTR, subject, bodyText)
}

// RenderEmailHTMLLang is RenderEmailHTML with <html lang="..."> by language: "en" for LangEN, "tr"
// for anything else (P8, D63). The lang value is a constant, never taken from content.
func RenderEmailHTMLLang(lang, subject, bodyText string) string {
	htmlLang := LangTR
	if lang == LangEN {
		htmlLang = LangEN
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + htmlLang + `"><head><meta charset="utf-8">`)
	b.WriteString(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`)
	b.WriteString("<title>")
	b.WriteString(html.EscapeString(subject))
	b.WriteString("</title></head><body style=\"" + emailBodyStyle + "\">")
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>")
		for i, line := range para {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(html.EscapeString(line))
		}
		b.WriteString("</p>")
		para = para[:0]
	}
	for _, line := range strings.Split(strings.ReplaceAll(bodyText, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, line)
	}
	flush()
	b.WriteString("</body></html>")
	return b.String()
}
