package content_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Content fixtures of docs/contracts/llm-tools.md §3.3, verbatim ("\n" is a real line break).
const (
	cntOKSMS       = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
	cntOKSubject   = "Sepetin seni bekliyor: %30 indirim fırsatı"
	cntOKBody      = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer"
	cntUnknownSMS  = "Merhaba {{first_name}}, şifren {{password}} ile giriş yap ve %30 indirimi kap!"
	cntHTMLSMS     = "Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!"
	cntHTMLSubject = "<i>Sepetin</i> seni bekliyor"
	cntHTMLBody    = "Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>"
)

var (
	cntSMS161      = strings.Repeat("İndirim ", 20) + "!"
	cntSubject81   = strings.Repeat("Fırsat ", 11) + "son!"
	exampleLongest = content.Longest{FirstName: 50, LastName: 8, City: 8} // manifest example_brief.longest_values
	emptyLongest   = content.Longest{FirstName: 18}                       // empty matched set (go-interfaces §content)
)

func cntOK() content.Content {
	return content.Content{SMS: content.SMS{Body: cntOKSMS}, Email: content.Email{Subject: cntOKSubject, Body: cntOKBody}}
}

func withSMS(s string) content.Content     { c := cntOK(); c.SMS.Body = s; return c }
func withSubject(s string) content.Content { c := cntOK(); c.Email.Subject = s; return c }
func withBody(s string) content.Content    { c := cntOK(); c.Email.Body = s; return c }

type fieldWant struct {
	Path   string
	Code   string
	Limit  *int
	Actual *int
}

func ip(n int) *int { return &n }

// fieldsOf returns the field errors of err (which must be a *validation.Error).
func fieldsOf(t *testing.T, err error) []validation.FieldError {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want a *validation.Error", err, err)
	}
	if len(ve.Fields) == 0 {
		t.Fatalf("*validation.Error has no field errors")
	}
	for _, f := range ve.Fields {
		if f.Message == "" {
			t.Fatalf("field error %+v has an empty message", f)
		}
	}
	return ve.Fields
}

func project(fs []validation.FieldError, withLengths bool) []fieldWant {
	out := make([]fieldWant, len(fs))
	for i, f := range fs {
		out[i] = fieldWant{Path: f.Path, Code: f.Code}
		if withLengths {
			out[i].Limit, out[i].Actual = f.Limit, f.Actual
		}
	}
	return out
}

// REQ-CNT-01, REQ-CNT-02
// RuneCount counts Unicode code points of the NFC form: Turkish letters are one each and a
// decomposed letter counts once after normalization.
func TestRuneCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "empty", in: "", want: 0},
		{name: "ASCII", in: "abc", want: 3},
		{name: "Turkish letters count one each", in: "ğüşİıöç", want: 7},
		{name: "dotted capital I is one", in: "İ", want: 1},
		{name: "decomposed I + combining dot is one after NFC", in: "İ", want: 1},
		{name: "decomposed s + cedilla is one after NFC", in: "ş", want: 1},
		{name: "160 ş", in: strings.Repeat("ş", 160), want: 160},
		{name: "fixture CNT_OK sms template", in: cntOKSMS, want: 124},
		{name: "fixture CNT_OK subject", in: cntOKSubject, want: 42},
		{name: "fixture CNT_SMS_161", in: cntSMS161, want: 161},
		{name: "fixture CNT_SUBJECT_81", in: cntSubject81, want: 81},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := content.RuneCount(tc.in); got != tc.want {
				t.Fatalf("RuneCount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// trDoc is a structurally valid Turkish document WITHOUT its closing brace, so a test can append
// `,"en":…}` (REQ-LNG-01 C1).
const trDoc = `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"}`

// REQ-AI-01, REQ-APR-02, REQ-CNT-01 (D46), REQ-LNG-01 C1
// Parse checks STRUCTURE only: unknown keys, wrong types and missing keys are schema_violation at
// that path; broken JSON is invalid_json; an empty string is structurally valid (Validate reports it
// as required); strings are NFC-normalized and not trimmed.
func TestParse_Structure(t *testing.T) {
	t.Parallel()
	okDoc := `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":"Konu","body":"Gövde"}}`
	tests := []struct {
		name     string
		raw      string
		sentinel error
		path     *string // nil => do not check the path
		code     string
	}{
		{name: "broken JSON is invalid_json", raw: `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":}`, sentinel: validation.ErrInvalidJSON, code: "invalid_json"},
		{name: "empty input is invalid_json", raw: ``, sentinel: validation.ErrInvalidJSON, code: "invalid_json"},
		{name: "unknown root key is reported at the key itself", raw: `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"html":"<script>alert(1)</script>"}`, sentinel: validation.ErrSchemaViolation, path: sp("html"), code: "schema_violation"},
		{name: "sql key next to the content", raw: `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"sql":"DROP TABLE customers"}`, sentinel: validation.ErrSchemaViolation, path: sp("sql"), code: "schema_violation"},
		{name: "missing sms.body is a schema violation, never required", raw: `{"sms":{},"email":{"subject":"b","body":"c"}}`, sentinel: validation.ErrSchemaViolation, path: sp("sms.body"), code: "schema_violation"},
		{name: "missing email.subject", raw: `{"sms":{"body":"a"},"email":{"body":"c"}}`, sentinel: validation.ErrSchemaViolation, path: sp("email.subject"), code: "schema_violation"},
		{name: "missing email.body", raw: `{"sms":{"body":"a"},"email":{"subject":"b"}}`, sentinel: validation.ErrSchemaViolation, path: sp("email.body"), code: "schema_violation"},
		{name: "missing email object", raw: `{"sms":{"body":"a"}}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "number sms.body is a schema violation", raw: `{"sms":{"body":160},"email":{"subject":"b","body":"c"}}`, sentinel: validation.ErrSchemaViolation, path: sp("sms.body"), code: "schema_violation"},
		{name: "null subject is a schema violation", raw: `{"sms":{"body":"a"},"email":{"subject":null,"body":"c"}}`, sentinel: validation.ErrSchemaViolation, path: sp("email.subject"), code: "schema_violation"},
		{name: "array root is a schema violation", raw: `[]`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "unknown nested key is a schema violation", raw: `{"sms":{"body":"a","html":"<b>x</b>"},"email":{"subject":"b","body":"c"}}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		// REQ-LNG-01 C1: the optional en block has the same strict shape.
		{name: "en null is a schema violation at en", raw: trDoc + `,"en":null}`, sentinel: validation.ErrSchemaViolation, path: sp("en"), code: "schema_violation"},
		{name: "en empty object is a schema violation", raw: trDoc + `,"en":{}}`, sentinel: validation.ErrSchemaViolation, code: "schema_violation"},
		{name: "en string is a schema violation at en", raw: trDoc + `,"en":"hello"}`, sentinel: validation.ErrSchemaViolation, path: sp("en"), code: "schema_violation"},
		{name: "missing en.sms is reported at en.sms", raw: trDoc + `,"en":{"email":{"subject":"b","body":"c"}}}`, sentinel: validation.ErrSchemaViolation, path: sp("en.sms"), code: "schema_violation"},
		{name: "missing en.email.subject is reported at en.email.subject", raw: trDoc + `,"en":{"sms":{"body":"a"},"email":{"body":"c"}}}`, sentinel: validation.ErrSchemaViolation, path: sp("en.email.subject"), code: "schema_violation"},
		{name: "html inside en is reported at en.html", raw: trDoc + `,"en":{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"html":"<b>x</b>"}}`, sentinel: validation.ErrSchemaViolation, path: sp("en.html"), code: "schema_violation"},
		{name: "number en.sms.body is a schema violation", raw: trDoc + `,"en":{"sms":{"body":160},"email":{"subject":"b","body":"c"}}}`, sentinel: validation.ErrSchemaViolation, path: sp("en.sms.body"), code: "schema_violation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := content.Parse([]byte(tc.raw))
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("Parse() error = %v, want %v", err, tc.sentinel)
			}
			fs := fieldsOf(t, err)
			found := false
			for _, f := range fs {
				if f.Code == tc.code && (tc.path == nil || f.Path == *tc.path) {
					found = true
				}
				if f.Code == "required" {
					t.Fatalf("Parse reported %+v; a structural problem is never required", f)
				}
			}
			if !found {
				t.Fatalf("field errors = %+v, want code %s at %v", project(fs, false), tc.code, deref(tc.path))
			}
		})
	}

	t.Run("valid document decodes", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(okDoc))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		want := content.Content{SMS: content.SMS{Body: "Merhaba {{first_name}}"}, Email: content.Email{Subject: "Konu", Body: "Gövde"}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
		}
	})

	// REQ-LNG-01 C1, C8: a document with en decodes into Content.EN; without en, EN stays nil.
	t.Run("valid document with en decodes into EN", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(trDoc + `,"en":{"sms":{"body":"Hi {{first_name}}"},"email":{"subject":"Subject","body":"Body"}}}`))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		want := content.Content{
			SMS:   content.SMS{Body: "a"},
			Email: content.Email{Subject: "b", Body: "c"},
			EN:    &content.English{SMS: content.SMS{Body: "Hi {{first_name}}"}, Email: content.Email{Subject: "Subject", Body: "Body"}},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("empty en strings are structurally valid", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(trDoc + `,"en":{"sms":{"body":""},"email":{"subject":"","body":""}}}`))
		if err != nil {
			t.Fatalf("Parse() error = %v, want nil (empty values are Validate's required)", err)
		}
		if got.EN == nil {
			t.Fatalf("Parse() EN = nil, want an empty English version")
		}
	})

	t.Run("empty strings are structurally valid", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(`{"sms":{"body":""},"email":{"subject":"","body":""}}`))
		if err != nil {
			t.Fatalf("Parse() error = %v, want nil (empty values are Validate's required)", err)
		}
		if diff := cmp.Diff(content.Content{}, got); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("strings are NFC-normalized and not trimmed", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(`{"sms":{"body":"  İstanbul ş  "},"email":{"subject":"\tKonu ","body":"Gövde\n"}}`))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		want := content.Content{SMS: content.SMS{Body: "  İstanbul ş  "}, Email: content.Email{Subject: "\tKonu ", Body: "Gövde\n"}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
		}
	})
}

// REQ-CNT-01, REQ-CNT-02
// Length limits on templates without variables (Longest has no effect): 160/161 SMS, 80/81 subject,
// 5000/5001 email body, measured in runes; length errors carry limit and actual.
func TestValidate_LengthLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		c        content.Content
		want     *fieldWant
		sentinel error
	}{
		{name: "CNT_OK is valid with the example audience", c: cntOK()},
		{name: "160 ASCII chars SMS is accepted", c: withSMS(strings.Repeat("a", 160))},
		{name: "161 ASCII chars SMS is rejected", c: withSMS(strings.Repeat("a", 161)), want: &fieldWant{"sms.body", "sms_too_long", ip(160), ip(161)}, sentinel: content.ErrSMSTooLong},
		{name: "160 ş SMS is accepted", c: withSMS(strings.Repeat("ş", 160))},
		{name: "161 ş SMS is rejected with actual 161", c: withSMS(strings.Repeat("ş", 161)), want: &fieldWant{"sms.body", "sms_too_long", ip(160), ip(161)}, sentinel: content.ErrSMSTooLong},
		{name: "160 İ SMS is accepted", c: withSMS(strings.Repeat("İ", 160))},
		{name: "fixture CNT_SMS_161 is rejected", c: withSMS(cntSMS161), want: &fieldWant{"sms.body", "sms_too_long", ip(160), ip(161)}, sentinel: content.ErrSMSTooLong},
		{name: "80 chars subject is accepted", c: withSubject(strings.Repeat("ğ", 80))},
		{name: "81 chars subject is rejected", c: withSubject(strings.Repeat("ğ", 81)), want: &fieldWant{"email.subject", "subject_too_long", ip(80), ip(81)}, sentinel: content.ErrSubjectTooLong},
		{name: "fixture CNT_SUBJECT_81 is rejected", c: withSubject(cntSubject81), want: &fieldWant{"email.subject", "subject_too_long", ip(80), ip(81)}, sentinel: content.ErrSubjectTooLong},
		{name: "5000 chars email body is accepted", c: withBody(strings.Repeat("ü", 5000))},
		{name: "5001 chars email body is rejected", c: withBody(strings.Repeat("ü", 5001)), want: &fieldWant{"email.body", "email_body_too_long", ip(5000), ip(5001)}, sentinel: content.ErrEmailBodyTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, exampleLongest)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.sentinel)
			}
			if errors.Is(err, content.ErrWorstCaseTooLong) {
				t.Fatalf("validation-time length error matches ErrWorstCaseTooLong; only the approval re-check codes do")
			}
			if diff := cmp.Diff([]fieldWant{*tc.want}, project(fieldsOf(t, err), true)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-01, REQ-CNT-05 (D37)
// The limit applies AFTER substitution: every placeholder counts as the campaign's longest value
// for that variable (go-interfaces §content length examples).
func TestValidate_LengthAfterSubstitution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		c      content.Content
		l      content.Longest
		actual int // 0 => valid
		path   string
		code   string
	}{
		{name: "first_name 50 pushes a 160-char template to 196", c: withSMS("{{first_name}}" + strings.Repeat("a", 146)), l: content.Longest{FirstName: 50}, actual: 196, path: "sms.body", code: "sms_too_long"},
		{name: "first_name 14 keeps the same template at 160", c: withSMS("{{first_name}}" + strings.Repeat("a", 146)), l: content.Longest{FirstName: 14}},
		{name: "CNT_OK is exactly 160 with the example audience", c: cntOK(), l: exampleLongest},
		{name: "CNT_OK is 161 with a 51-char first name", c: cntOK(), l: content.Longest{FirstName: 51, LastName: 8, City: 8}, actual: 161, path: "sms.body", code: "sms_too_long"},
		{name: "spaces inside the braces do not count", c: withSMS("{{ first_name }}" + strings.Repeat("a", 110)), l: content.Longest{FirstName: 50}},
		{name: "city placeholder counts as the longest city", c: withSMS("{{city}}" + strings.Repeat("a", 150)), l: content.Longest{FirstName: 18, City: 10}},
		{name: "city placeholder one over", c: withSMS("{{city}}" + strings.Repeat("a", 150)), l: content.Longest{FirstName: 18, City: 11}, actual: 161, path: "sms.body", code: "sms_too_long"},
		{name: "last_name placeholder in the subject", c: withSubject("{{last_name}}" + strings.Repeat("b", 70)), l: content.Longest{FirstName: 18, LastName: 10}},
		{name: "last_name placeholder in the subject one over", c: withSubject("{{last_name}}" + strings.Repeat("b", 70)), l: content.Longest{FirstName: 18, LastName: 11}, actual: 81, path: "email.subject", code: "subject_too_long"},
		{name: "a repeated placeholder counts every time", c: withSMS("{{first_name}}{{first_name}}" + strings.Repeat("a", 60)), l: content.Longest{FirstName: 50}, actual: 0},
		{name: "a repeated placeholder one over", c: withSMS("{{first_name}}{{first_name}}" + strings.Repeat("a", 61)), l: content.Longest{FirstName: 50}, actual: 161, path: "sms.body", code: "sms_too_long"},
		{name: "empty audience uses the 18-char fallback", c: withSMS("{{first_name}}" + strings.Repeat("a", 142)), l: emptyLongest},
		{name: "empty audience one over", c: withSMS("{{first_name}}" + strings.Repeat("a", 143)), l: emptyLongest, actual: 161, path: "sms.body", code: "sms_too_long"},
		{name: "email body placeholder counts too", c: withBody("{{city}}" + strings.Repeat("c", 4990)), l: content.Longest{FirstName: 18, City: 11}, actual: 5001, path: "email.body", code: "email_body_too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, tc.l)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			want := []fieldWant{{Path: tc.path, Code: tc.code, Limit: ip(limitFor(tc.path)), Actual: ip(tc.actual)}}
			if diff := cmp.Diff(want, project(fieldsOf(t, err), true)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func limitFor(path string) int {
	switch path {
	case "sms.body":
		return 160
	case "email.subject":
		return 80
	}
	return 5000
}

// REQ-CNT-01 (D46)
// A field that is present but empty (or whitespace only) is `required`, on every field.
func TestValidate_Required(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    content.Content
		path string
	}{
		{name: "empty sms body", c: withSMS(""), path: "sms.body"},
		{name: "whitespace-only sms body", c: withSMS(" \n\t "), path: "sms.body"},
		{name: "empty subject", c: withSubject(""), path: "email.subject"},
		{name: "whitespace-only subject", c: withSubject("   "), path: "email.subject"},
		{name: "empty email body", c: withBody(""), path: "email.body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, exampleLongest)
			if !errors.Is(err, validation.ErrRequired) {
				t.Fatalf("Validate() error = %v, want %v", err, validation.ErrRequired)
			}
			if diff := cmp.Diff([]fieldWant{{Path: tc.path, Code: "required"}}, project(fieldsOf(t, err), false)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-03
// The variable syntax table of go-interfaces §content, through Validate on sms.body: plain text,
// valid allowed variables, malformed_variable and unknown_variable (case-sensitive).
func TestValidate_VariableSyntaxTable(t *testing.T) {
	t.Parallel()
	valid := []string{
		"Merhaba {{first_name}}", "Merhaba {{ first_name }}", "{{city }} indirimi", "{{city\t}} indirimi", "{{\tlast_name}}",
		"{{first_name}}{{first_name}}", "{first_name} düz metin", "%30 indirim", "$ işareti", "tek { parantez", "a } b",
		"{{first_name}} {{last_name}} {{city}}",
	}
	for _, s := range valid {
		t.Run("valid "+s, func(t *testing.T) {
			t.Parallel()
			if err := content.Validate(withSMS(s), exampleLongest); err != nil {
				t.Fatalf("Validate(%q) error = %v, want nil", s, err)
			}
		})
	}
	malformed := []string{
		"Merhaba {{first_name", "{{", "Merhaba {{ }}", "{{}}", "Merhaba }}", "{{first-name}}", "{{1st}}",
		"{{first name}}", "{{{first_name}}}", "{{password}} ve {{first_name",
	}
	for _, s := range malformed {
		t.Run("malformed "+s, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(withSMS(s), exampleLongest)
			if !errors.Is(err, content.ErrMalformedVariable) {
				t.Fatalf("Validate(%q) error = %v, want %v", s, err, content.ErrMalformedVariable)
			}
			if diff := cmp.Diff([]fieldWant{{Path: "sms.body", Code: "malformed_variable"}}, project(fieldsOf(t, err), false)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
	unknown := []string{"{{First_Name}}", "şifren {{password}}", "{{email}} adresine", "{{ phone }}", cntUnknownSMS}
	for _, s := range unknown {
		t.Run("unknown "+s, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(withSMS(s), exampleLongest)
			if !errors.Is(err, content.ErrUnknownVariable) {
				t.Fatalf("Validate(%q) error = %v, want %v", s, err, content.ErrUnknownVariable)
			}
			if errors.Is(err, content.ErrMalformedVariable) {
				t.Fatalf("Validate(%q) also matches ErrMalformedVariable", s)
			}
			if diff := cmp.Diff([]fieldWant{{Path: "sms.body", Code: "unknown_variable"}}, project(fieldsOf(t, err), false)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-01, REQ-CNT-03 (D46)
// At most one error per field with priority required > malformed > unknown > length; a field with
// a variable error gets no length check; the other fields are still checked; order is sms.body,
// email.subject, email.body.
func TestValidate_OneErrorPerFieldInFieldOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    content.Content
		want []fieldWant
	}{
		{
			name: "unknown variable in a 200-char SMS reports only unknown_variable",
			c:    withSMS("{{password}}" + strings.Repeat("a", 200)),
			want: []fieldWant{{Path: "sms.body", Code: "unknown_variable"}},
		},
		{
			name: "malformed variable in a 200-char SMS reports only malformed_variable",
			c:    withSMS("{{first_name" + strings.Repeat("a", 200)),
			want: []fieldWant{{Path: "sms.body", Code: "malformed_variable"}},
		},
		{
			name: "malformed and unknown in one field is malformed",
			c:    withSMS("{{password}} {{first-name}}"),
			want: []fieldWant{{Path: "sms.body", Code: "malformed_variable"}},
		},
		{
			name: "sms variable error does not stop the subject length check",
			c:    content.Content{SMS: content.SMS{Body: cntUnknownSMS}, Email: content.Email{Subject: cntSubject81, Body: cntOKBody}},
			want: []fieldWant{{Path: "sms.body", Code: "unknown_variable"}, {Path: "email.subject", Code: "subject_too_long"}},
		},
		{
			name: "three bad fields are reported in field order",
			c:    content.Content{SMS: content.SMS{Body: cntSMS161}, Email: content.Email{Subject: "  ", Body: "{{x-y}}"}},
			want: []fieldWant{{Path: "sms.body", Code: "sms_too_long"}, {Path: "email.subject", Code: "required"}, {Path: "email.body", Code: "malformed_variable"}},
		},
		{
			name: "all three empty are three required errors",
			c:    content.Content{},
			want: []fieldWant{{Path: "sms.body", Code: "required"}, {Path: "email.subject", Code: "required"}, {Path: "email.body", Code: "required"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, exampleLongest)
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), false)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-03
// EffectiveFirstName trims and falls back to "Değerli müşterimiz" when empty.
func TestEffectiveFirstName(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{in: "Ayşe", want: "Ayşe"},
		{in: "  Ayşe \t", want: "Ayşe"},
		{in: "", want: "Değerli müşterimiz"},
		{in: " \n\t ", want: "Değerli müşterimiz"},
		{in: "Ali Can", want: "Ali Can"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := content.EffectiveFirstName(tc.in); got != tc.want {
				t.Fatalf("EffectiveFirstName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// REQ-CNT-03
// Render fills first_name (trimmed, fallback when empty), last_name and city (raw, possibly
// empty); substituted values are not re-processed; unknown/malformed variables are errors.
func TestRender(t *testing.T) {
	t.Parallel()
	ayse := content.Recipient{FirstName: "  Ayşe ", LastName: "Yılmaz", City: "İzmir"}
	tests := []struct {
		name string
		tmpl string
		r    content.Recipient
		want string
		err  error
	}{
		{name: "first name is trimmed", tmpl: "Merhaba {{first_name}}!", r: ayse, want: "Merhaba Ayşe!"},
		{name: "spaces inside the braces", tmpl: "Merhaba {{ first_name }}!", r: ayse, want: "Merhaba Ayşe!"},
		{name: "empty first name falls back", tmpl: "Merhaba {{first_name}}, %30 indirim", r: content.Recipient{City: "Bursa"}, want: "Merhaba Değerli müşterimiz, %30 indirim"},
		{name: "whitespace first name falls back", tmpl: "Merhaba {{first_name}}", r: content.Recipient{FirstName: "   "}, want: "Merhaba Değerli müşterimiz"},
		{name: "last name and city are filled raw", tmpl: "{{last_name}} - {{city}}", r: ayse, want: "Yılmaz - İzmir"},
		{name: "empty last name and city become empty text", tmpl: "[{{last_name}}][{{city}}]", r: content.Recipient{FirstName: "Ali"}, want: "[][]"},
		{name: "city is not trimmed", tmpl: "[{{city}}]", r: content.Recipient{City: " Konya "}, want: "[ Konya ]"},
		{name: "repeated placeholders", tmpl: "{{first_name}}{{first_name}}", r: ayse, want: "AyşeAyşe"},
		{name: "plain text without variables", tmpl: "{first_name} %30", r: ayse, want: "{first_name} %30"},
		{name: "unknown variable is rejected", tmpl: "şifren {{password}}", r: ayse, err: content.ErrUnknownVariable},
		{name: "variable names are case-sensitive", tmpl: "{{First_Name}}", r: ayse, err: content.ErrUnknownVariable},
		{name: "malformed variable is rejected", tmpl: "Merhaba {{first_name", r: ayse, err: content.ErrMalformedVariable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.Render(tc.tmpl, tc.r)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("Render(%q) error = %v, want %v", tc.tmpl, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render(%q) error = %v", tc.tmpl, err)
			}
			if got != tc.want {
				t.Fatalf("Render(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

// REQ-CNT-03, REQ-CNT-05
// RenderContent renders the three fields with the same rules; CheckRendered is the per-recipient
// send-time check on the rendered SMS and subject.
func TestRenderContentAndCheckRendered(t *testing.T) {
	t.Parallel()
	r := content.Recipient{FirstName: "", LastName: "Kaya", City: "İstanbul"}
	got, err := content.RenderContent(cntOK(), r)
	if err != nil {
		t.Fatalf("RenderContent() error = %v", err)
	}
	want := content.Rendered{
		SMSText:      strings.ReplaceAll(cntOKSMS, "{{first_name}}", "Değerli müşterimiz"),
		EmailSubject: cntOKSubject,
		EmailText:    strings.ReplaceAll(strings.ReplaceAll(cntOKBody, "{{first_name}}", "Değerli müşterimiz"), "{{city}}", "İstanbul"),
		Language:     "tr", // REQ-LNG-03 C3: a recipient without a language gets the Turkish main text
		Variant:      "a",  // REQ-ABT-03 C8: content without b always renders variant A
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("RenderContent() mismatch (-want +got):\n%s", diff)
	}
	if err := content.CheckRendered(got); err != nil {
		t.Fatalf("CheckRendered() error = %v, want nil", err)
	}

	if _, err := content.RenderContent(withSMS(cntUnknownSMS), r); !errors.Is(err, content.ErrUnknownVariable) {
		t.Fatalf("RenderContent(unknown variable) error = %v, want %v", err, content.ErrUnknownVariable)
	}

	checks := []struct {
		name string
		r    content.Rendered
		want []fieldWant
	}{
		{name: "160 rendered SMS chars pass", r: content.Rendered{SMSText: strings.Repeat("ş", 160), EmailSubject: "a", EmailText: "b"}},
		{name: "161 rendered SMS chars fail", r: content.Rendered{SMSText: strings.Repeat("ş", 161), EmailSubject: "a", EmailText: "b"}, want: []fieldWant{{Path: "sms.body", Code: "sms_too_long", Limit: ip(160), Actual: ip(161)}}},
		{name: "81 rendered subject chars fail", r: content.Rendered{SMSText: "a", EmailSubject: strings.Repeat("i", 81), EmailText: "b"}, want: []fieldWant{{Path: "email.subject", Code: "subject_too_long", Limit: ip(80), Actual: ip(81)}}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.CheckRendered(tc.r)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckRendered() error = %v, want nil", err)
				}
				return
			}
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), true)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-05, REQ-LNG-01 C3, C4
// MeasureWorstCase uses the same substitution as Validate; it fails only on template syntax.
func TestMeasureWorstCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    content.Content
		l    content.Longest
		want content.WorstCase
		err  error
	}{
		{name: "CNT_OK with the example audience", c: cntOK(), l: exampleLongest, want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42}},
		{name: "CNT_OK with the empty-audience fallback", c: cntOK(), l: emptyLongest, want: content.WorstCase{SMSRunes: 128, SubjectRunes: 42}},
		{name: "over-limit content is still measured", c: withSMS(cntSMS161), l: exampleLongest, want: content.WorstCase{SMSRunes: 161, SubjectRunes: 42}},
		{name: "subject placeholder", c: withSubject("{{last_name}} için"), l: content.Longest{FirstName: 18, LastName: 12}, want: content.WorstCase{SMSRunes: 160 - 50 + 18, SubjectRunes: 17}},
		{name: "malformed variable", c: withSMS("{{first_name"), l: exampleLongest, err: content.ErrMalformedVariable},
		{name: "unknown variable", c: withSMS(cntUnknownSMS), l: exampleLongest, err: content.ErrUnknownVariable},
		// REQ-LNG-01 C3, C4: the English version is measured with the SAME longest values and
		// reported in EN; the Turkish fields are unchanged; without en there is no EN.
		{name: "English version is measured with the same longest values", c: bi(cntOKSMS, "Hi {{first_name}}, "+strings.Repeat("b", 100), "Your cart: {{city}}"), l: exampleLongest,
			want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42, EN: &content.WorstCaseLang{SMSRunes: 155, SubjectRunes: 19}}},
		{name: "English over-limit content is still measured", c: bi(cntOKSMS, "{{first_name}}"+strings.Repeat("b", 111), "Subject"), l: exampleLongest,
			want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42, EN: &content.WorstCaseLang{SMSRunes: 161, SubjectRunes: 7}}},
		{name: "English empty-audience fallback is measured with the same set", c: bi(cntOKSMS, "{{first_name}}", "{{last_name}}"), l: emptyLongest,
			want: content.WorstCase{SMSRunes: 128, SubjectRunes: 42, EN: &content.WorstCaseLang{SMSRunes: 18, SubjectRunes: 0}}},
		{name: "malformed variable in the English SMS", c: bi(cntOKSMS, "Hi {{first_name", "Subject"), l: exampleLongest, err: content.ErrMalformedVariable},
		{name: "unknown variable in the English subject", c: bi(cntOKSMS, "Hi", "{{password}}"), l: exampleLongest, err: content.ErrUnknownVariable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.MeasureWorstCase(tc.c, tc.l)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("MeasureWorstCase() error = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("MeasureWorstCase() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("MeasureWorstCase() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CNT-05 (D37, D39, D46), REQ-LNG-01 C6
// CheckWorstCase is the approval-time re-check: same substitution, only sms.body <= 160 and
// email.subject <= 80, codes *_worst_case matching both the length sentinel and
// ErrWorstCaseTooLong; email.body is not re-checked.
func TestCheckWorstCase(t *testing.T) {
	t.Parallel()
	longer := content.Longest{FirstName: 51, LastName: 8, City: 8}
	tests := []struct {
		name      string
		c         content.Content
		l         content.Longest
		want      []fieldWant
		sentinels []error
	}{
		{name: "CNT_OK passes at the boundary", c: cntOK(), l: exampleLongest},
		{name: "a longer first name at approval fails the SMS", c: cntOK(), l: longer,
			want:      []fieldWant{{Path: "sms.body", Code: "sms_too_long_worst_case", Limit: ip(160), Actual: ip(161)}},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "a longer last name at approval fails the subject", c: withSubject("{{last_name}}" + strings.Repeat("b", 72)), l: content.Longest{FirstName: 18, LastName: 9},
			want:      []fieldWant{{Path: "email.subject", Code: "subject_too_long_worst_case", Limit: ip(80), Actual: ip(81)}},
			sentinels: []error{content.ErrSubjectTooLong, content.ErrWorstCaseTooLong}},
		{name: "both fields fail in field order", c: content.Content{SMS: content.SMS{Body: cntOKSMS}, Email: content.Email{Subject: "{{city}}" + strings.Repeat("b", 72), Body: cntOKBody}}, l: content.Longest{FirstName: 51, City: 9},
			want: []fieldWant{
				{Path: "sms.body", Code: "sms_too_long_worst_case", Limit: ip(160), Actual: ip(161)},
				{Path: "email.subject", Code: "subject_too_long_worst_case", Limit: ip(80), Actual: ip(81)},
			},
			sentinels: []error{content.ErrSMSTooLong, content.ErrSubjectTooLong, content.ErrWorstCaseTooLong}},
		{name: "email body over 5000 is not re-checked at approval", c: withBody("{{city}}" + strings.Repeat("c", 4995)), l: content.Longest{FirstName: 18, City: 10}},
		// REQ-LNG-01 C6: the approval re-check covers the English version with the same codes,
		// en-prefixed paths and limits 160 / 80.
		{name: "English version at the boundary passes", c: bi(trNoVarSMS, "{{first_name}}"+strings.Repeat("b", 110), "{{last_name}}"+strings.Repeat("b", 72)), l: exampleLongest},
		{name: "a longer first name at approval fails the English SMS", c: bi(trNoVarSMS, "{{first_name}}"+strings.Repeat("b", 110), "Subject"), l: longer,
			want:      []fieldWant{{Path: "en.sms.body", Code: "sms_too_long_worst_case", Limit: ip(160), Actual: ip(161)}},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "a longer last name at approval fails the English subject", c: bi(trNoVarSMS, "Hi", "{{last_name}}"+strings.Repeat("b", 72)), l: content.Longest{FirstName: 18, LastName: 9},
			want:      []fieldWant{{Path: "en.email.subject", Code: "subject_too_long_worst_case", Limit: ip(80), Actual: ip(81)}},
			sentinels: []error{content.ErrSubjectTooLong, content.ErrWorstCaseTooLong}},
		{name: "Turkish and English failures come in field order", c: bi(cntOKSMS, "{{first_name}}"+strings.Repeat("b", 110), "Subject"), l: longer,
			want: []fieldWant{
				{Path: "sms.body", Code: "sms_too_long_worst_case", Limit: ip(160), Actual: ip(161)},
				{Path: "en.sms.body", Code: "sms_too_long_worst_case", Limit: ip(160), Actual: ip(161)},
			},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "English email body over 5000 is not re-checked at approval", c: biBody("{{city}}" + strings.Repeat("c", 4995)), l: content.Longest{FirstName: 18, City: 10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.CheckWorstCase(tc.c, tc.l)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckWorstCase() error = %v, want nil", err)
				}
				return
			}
			for _, s := range tc.sentinels {
				if !errors.Is(err, s) {
					t.Fatalf("CheckWorstCase() error = %v, want it to match %v", err, s)
				}
			}
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), true)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

var tagRE = regexp.MustCompile(`<\s*(/?[A-Za-z!][A-Za-z0-9-]*)`)

// REQ-UI-01
// RenderEmailHTML escapes every text fragment taken from content: no script, no anchor, no
// attribute or tag of the content becomes markup.
func TestRenderEmailHTML_EscapesAndUsesOnlyTheSkeleton(t *testing.T) {
	t.Parallel()
	allowed := []string{"!doctype", "html", "/html", "head", "/head", "meta", "title", "/title", "body", "/body", "p", "/p", "br"}
	tests := []struct {
		name     string
		subject  string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "CNT_HTML fixture is escaped", subject: "<i>Sepetin</i> seni bekliyor",
			body:     "Merhaba Ayşe,\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>",
			contains: []string{"&lt;i&gt;Sepetin&lt;/i&gt; seni bekliyor", "&lt;script&gt;", "&lt;a href="},
			absent:   []string{"<script", "<a ", "<i>", "href=\"javascript"},
		},
		{name: "ampersand and angle brackets are escaped", subject: "A & B \"C\"", body: "x < y & z > w", contains: []string{"A &amp; B", "x &lt; y &amp; z &gt; w"}, absent: []string{"x < y"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := content.RenderEmailHTML(tc.subject, tc.body)
			for _, s := range tc.contains {
				if !strings.Contains(out, s) {
					t.Fatalf("output lacks %q: %s", s, out)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(out, s) {
					t.Fatalf("output contains %q: %s", s, out)
				}
			}
			for _, m := range tagRE.FindAllStringSubmatch(out, -1) {
				if !slices.Contains(allowed, strings.ToLower(m[1])) {
					t.Fatalf("output contains a tag outside the skeleton: <%s in %s", m[1], out)
				}
			}
		})
	}
}

// ---- language (P8) ----

// REQ-LNG-01 C7, C8
// The language constants and the English first-name fallback.
func TestLanguageConstants(t *testing.T) {
	t.Parallel()
	got := []string{content.LangTR, content.LangEN, content.FirstNameFallbackEN}
	if diff := cmp.Diff([]string{"tr", "en", "Dear customer"}, got); diff != "" {
		t.Fatalf("constants mismatch (-want +got):\n%s", diff)
	}
	if n := content.RuneCount(content.FirstNameFallbackEN); n != 13 {
		t.Fatalf("RuneCount(FirstNameFallbackEN) = %d, want 13", n)
	}
}

// REQ-LNG-01 C2, C3
// Validate checks the English version with the same rules as the Turkish one: fields
// en.sms.body, en.email.subject, en.email.body; the same codes, limits and priority (one error per
// field); length after substitution with the campaign's single set of longest values; errors in the
// order sms.body, email.subject, email.body, en.sms.body, en.email.subject, en.email.body.
func TestValidate_English(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		c        content.Content
		l        content.Longest
		want     []fieldWant // nil => valid
		sentinel error
	}{
		{name: "valid English version is accepted", c: bi(cntOKSMS, "Hi {{first_name}}, your cart is waiting.", "Your cart is waiting"), l: exampleLongest},
		{name: "content without en is valid as today", c: cntOK(), l: exampleLongest},
		{name: "160-char English SMS is accepted", c: bi(cntOKSMS, strings.Repeat("a", 160), "Subject"), l: exampleLongest},
		{name: "161-char English SMS is rejected", c: bi(cntOKSMS, strings.Repeat("a", 161), "Subject"), l: exampleLongest,
			want: []fieldWant{{"en.sms.body", "sms_too_long", ip(160), ip(161)}}, sentinel: content.ErrSMSTooLong},
		{name: "80-char English subject is accepted", c: bi(cntOKSMS, "Hi", strings.Repeat("s", 80)), l: exampleLongest},
		{name: "81-char English subject is rejected", c: bi(cntOKSMS, "Hi", strings.Repeat("s", 81)), l: exampleLongest,
			want: []fieldWant{{"en.email.subject", "subject_too_long", ip(80), ip(81)}}, sentinel: content.ErrSubjectTooLong},
		{name: "5000-char English body is accepted", c: biBody(strings.Repeat("e", 5000)), l: exampleLongest},
		{name: "5001-char English body is rejected", c: biBody(strings.Repeat("e", 5001)), l: exampleLongest,
			want: []fieldWant{{"en.email.body", "email_body_too_long", ip(5000), ip(5001)}}, sentinel: content.ErrEmailBodyTooLong},
		{name: "English first_name placeholder at 160 after substitution", c: bi(cntOKSMS, "{{first_name}}"+strings.Repeat("b", 110), "Subject"), l: exampleLongest},
		{name: "English first_name placeholder one over after substitution", c: bi(cntOKSMS, "{{first_name}}"+strings.Repeat("b", 111), "Subject"), l: exampleLongest,
			want: []fieldWant{{"en.sms.body", "sms_too_long", ip(160), ip(161)}}, sentinel: content.ErrSMSTooLong},
		{name: "English city placeholder in the subject one over", c: bi(cntOKSMS, "Hi", "{{city}}"+strings.Repeat("b", 73)), l: exampleLongest,
			want: []fieldWant{{"en.email.subject", "subject_too_long", ip(80), ip(81)}}, sentinel: content.ErrSubjectTooLong},
		{name: "empty English SMS is required", c: bi(cntOKSMS, "", "Subject"), l: exampleLongest,
			want: []fieldWant{{Path: "en.sms.body", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "whitespace-only English subject is required", c: bi(cntOKSMS, "Hi", " \t "), l: exampleLongest,
			want: []fieldWant{{Path: "en.email.subject", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "empty English body is required", c: biBody(""), l: exampleLongest,
			want: []fieldWant{{Path: "en.email.body", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "unknown variable in the English SMS", c: bi(cntOKSMS, "Your password {{password}}", "Subject"), l: exampleLongest,
			want: []fieldWant{{Path: "en.sms.body", Code: "unknown_variable"}}, sentinel: content.ErrUnknownVariable},
		{name: "malformed variable in the English subject", c: bi(cntOKSMS, "Hi", "Hi {{first_name"), l: exampleLongest,
			want: []fieldWant{{Path: "en.email.subject", Code: "malformed_variable"}}, sentinel: content.ErrMalformedVariable},
		{name: "unknown variable in a 200-char English SMS reports only unknown_variable", c: bi(cntOKSMS, "{{password}}"+strings.Repeat("a", 200), "Subject"), l: exampleLongest,
			want: []fieldWant{{Path: "en.sms.body", Code: "unknown_variable"}}, sentinel: content.ErrUnknownVariable},
		{name: "Turkish errors come before English errors",
			c: content.Content{
				SMS:   content.SMS{Body: cntSMS161},
				Email: content.Email{Subject: "  ", Body: cntOKBody},
				EN:    &content.English{SMS: content.SMS{Body: strings.Repeat("a", 161)}, Email: content.Email{Subject: "{{x-y}}", Body: ""}},
			},
			l: exampleLongest,
			want: []fieldWant{
				{Path: "sms.body", Code: "sms_too_long"}, {Path: "email.subject", Code: "required"},
				{Path: "en.sms.body", Code: "sms_too_long"}, {Path: "en.email.subject", Code: "malformed_variable"}, {Path: "en.email.body", Code: "required"},
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, tc.l)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.sentinel)
			}
			withLengths := tc.want[0].Limit != nil
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), withLengths)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-LNG-03 C3, REQ-LNG-01 C7
// RenderContent picks the language: Recipient.Language "en" and an English version => English text,
// Rendered.Language "en"; otherwise Turkish with Language "tr"; "en" without an English version is a
// fallback (Fallback true). The first-name fallback follows the language of the rendered text.
func TestRenderContent_Language(t *testing.T) {
	t.Parallel()
	enSMS, enSubj, enBody := "Hi {{first_name}}, your cart is waiting.", "{{first_name}}, your cart", "Hi {{first_name}},\n\n{{city}} has a 30% discount."
	bilingual := cntOK()
	bilingual.EN = &content.English{SMS: content.SMS{Body: enSMS}, Email: content.Email{Subject: enSubj, Body: enBody}}
	fill := func(tmpl, first, city string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tmpl, "{{first_name}}", first), "{{city}}", city)
	}
	tests := []struct {
		name string
		c    content.Content
		r    content.Recipient
		want content.Rendered
	}{
		// REQ-ABT-03 C8: content without b renders variant A, so every row carries Variant "a".
		{name: "en recipient with an English version gets English", c: bilingual, r: content.Recipient{FirstName: " Ayşe ", City: "İzmir", Language: "en"},
			want: content.Rendered{SMSText: fill(enSMS, "Ayşe", "İzmir"), EmailSubject: fill(enSubj, "Ayşe", "İzmir"), EmailText: fill(enBody, "Ayşe", "İzmir"), Language: "en", Variant: "a"}},
		{name: "en recipient with an empty first name gets Dear customer", c: bilingual, r: content.Recipient{City: "Bursa", Language: "en"},
			want: content.Rendered{SMSText: fill(enSMS, "Dear customer", "Bursa"), EmailSubject: fill(enSubj, "Dear customer", "Bursa"), EmailText: fill(enBody, "Dear customer", "Bursa"), Language: "en", Variant: "a"}},
		{name: "tr recipient gets Turkish", c: bilingual, r: content.Recipient{FirstName: "Ayşe", City: "İzmir", Language: "tr"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ayşe", "İzmir"), EmailSubject: cntOKSubject, EmailText: fill(cntOKBody, "Ayşe", "İzmir"), Language: "tr", Variant: "a"}},
		{name: "unset language gets Turkish", c: bilingual, r: content.Recipient{FirstName: "Ayşe", City: "İzmir"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ayşe", "İzmir"), EmailSubject: cntOKSubject, EmailText: fill(cntOKBody, "Ayşe", "İzmir"), Language: "tr", Variant: "a"}},
		{name: "en recipient without an English version falls back to Turkish", c: cntOK(), r: content.Recipient{FirstName: "", City: "İzmir", Language: "en"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Değerli müşterimiz", "İzmir"), EmailSubject: cntOKSubject, EmailText: fill(cntOKBody, "Değerli müşterimiz", "İzmir"), Language: "tr", Fallback: true, Variant: "a"}},
		{name: "tr recipient of a Turkish-only version is no fallback", c: cntOK(), r: content.Recipient{FirstName: "Ali", City: "Konya", Language: "tr"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ali", "Konya"), EmailSubject: cntOKSubject, EmailText: fill(cntOKBody, "Ali", "Konya"), Language: "tr", Variant: "a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.RenderContent(tc.c, tc.r)
			if err != nil {
				t.Fatalf("RenderContent() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("RenderContent() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-LNG-03 C2
// RenderEmailHTMLLang sets the document language: lang="en" for English, lang="tr" for Turkish;
// RenderEmailHTML is RenderEmailHTMLLang("tr", …) byte for byte. English text is escaped the same way.
func TestRenderEmailHTMLLang(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lang     string
		subject  string
		body     string
		contains []string
		absent   []string
	}{
		{name: "English document", lang: "en", subject: "Your cart", body: "Hi Ayşe,\n\nYour cart is waiting.", contains: []string{`<html lang="en">`}, absent: []string{`lang="tr"`}},
		{name: "Turkish document", lang: "tr", subject: "Sepetin", body: "Merhaba Ayşe", contains: []string{`<html lang="tr">`}, absent: []string{`lang="en"`}},
		{name: "English text is escaped", lang: "en", subject: "<i>Cart</i>", body: "<script>alert('xss')</script> & more",
			contains: []string{`<html lang="en">`, "&lt;i&gt;Cart&lt;/i&gt;", "&lt;script&gt;", "&amp; more"}, absent: []string{"<script", "<i>"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := content.RenderEmailHTMLLang(tc.lang, tc.subject, tc.body)
			for _, s := range tc.contains {
				if !strings.Contains(out, s) {
					t.Fatalf("output lacks %q: %s", s, out)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(out, s) {
					t.Fatalf("output contains %q: %s", s, out)
				}
			}
		})
	}
	t.Run("RenderEmailHTML equals the tr variant byte for byte", func(t *testing.T) {
		t.Parallel()
		subject, body := "Sepetin seni bekliyor", "Merhaba Ayşe,\n\n<b>%30</b> indirim & fırsat"
		if a, b := content.RenderEmailHTML(subject, body), content.RenderEmailHTMLLang("tr", subject, body); a != b {
			t.Fatalf("RenderEmailHTML() != RenderEmailHTMLLang(\"tr\"):\n%s\n%s", a, b)
		}
		if out := content.RenderEmailHTML(subject, body); !strings.Contains(out, `<html lang="tr">`) {
			t.Fatalf("RenderEmailHTML() lacks <html lang=\"tr\">: %s", out)
		}
	})
}

// ---- A/B variants (P9) ----

// Variant B and angle texts of the A/B tests: every B text differs from the CNT_OK text of variant A.
const (
	abBSMS     = "Son gün {{first_name}}! %30 indirim bugün bitiyor."
	abBSubject = "Son gün: %30 indirim"
	abBBody    = "Merhaba {{first_name}},\n\nİndirim bugün bitiyor."
	abAngleA   = "İndirim oranı öne çıkar"
	abAngleB   = "Aciliyet: indirim bugün bitiyor"
)

// abContent is CNT_OK (variant A) plus a valid variant B and both angles (REQ-ABT-01 C1 shape).
func abContent() content.Content {
	c := cntOK()
	c.B = &content.Variant{SMS: content.SMS{Body: abBSMS}, Email: content.Email{Subject: abBSubject, Body: abBBody}}
	c.Angles = &content.Angles{A: abAngleA, B: abAngleB}
	return c
}

// abWith is abContent changed by f.
func abWith(f func(c *content.Content)) content.Content {
	c := abContent()
	f(&c)
	return c
}

// abBilingual is abContent with an English version of A (en) and of B (b.en).
func abBilingual() content.Content {
	c := abContent()
	c.EN = &content.English{SMS: content.SMS{Body: "Hi {{first_name}}, your cart is waiting."}, Email: content.Email{Subject: "Your cart is waiting", Body: "Hi {{first_name}},\n\nYour cart is waiting."}}
	c.B.EN = &content.English{SMS: content.SMS{Body: "Last day {{first_name}}! 30% off ends today."}, Email: content.Email{Subject: "Last day: 30% off", Body: "Hi {{first_name}},\n\nThe discount ends today."}}
	return c
}

// JSON fragments of the A/B structure tests (completed after trDoc).
const (
	abBDoc      = `"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"}}`
	abBENDoc    = `"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"en":{"sms":{"body":"e"},"email":{"subject":"f","body":"g"}}}`
	abENDoc     = `"en":{"sms":{"body":"e"},"email":{"subject":"f","body":"g"}}`
	abAnglesDoc = `"angles":{"a":"A","b":"B"}`
)

// REQ-ABT-01 C10
// The variant constants and the angle limit.
func TestVariantConstants(t *testing.T) {
	t.Parallel()
	if diff := cmp.Diff([]string{"a", "b"}, []string{content.VariantA, content.VariantB}); diff != "" {
		t.Fatalf("variant constants mismatch (-want +got):\n%s", diff)
	}
	if content.AngleMaxRunes != 60 {
		t.Fatalf("AngleMaxRunes = %d, want 60", content.AngleMaxRunes)
	}
}

// REQ-ABT-01 C1, C2
// Parse checks the structure of the optional b and angles blocks: they come together, have the strict
// shape of the top level, and en / b.en come together. Every structural error is schema_violation at the
// key's path; Parse returns ALL of them (none hides another) in the order: unknown keys first, then the
// known keys in schema order (sms, email, en, b, angles), then the pairing rules (b ⇔ angles, en ⇔ b.en).
func TestParse_VariantStructure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		raw   string
		paths []string
		exact bool // paths is the whole list in order; otherwise each path must be present
	}{
		{name: "b without angles is reported at angles", raw: trDoc + `,` + abBDoc + `}`, paths: []string{"angles"}, exact: true},
		{name: "angles without b is reported at b", raw: trDoc + `,` + abAnglesDoc + `}`, paths: []string{"b"}, exact: true},
		{name: "b null is reported at b", raw: trDoc + `,"b":null,` + abAnglesDoc + `}`, paths: []string{"b"}},
		{name: "angles null is reported at angles", raw: trDoc + `,` + abBDoc + `,"angles":null}`, paths: []string{"angles"}},
		{name: "b empty object is reported at b.sms", raw: trDoc + `,"b":{},` + abAnglesDoc + `}`, paths: []string{"b.sms"}},
		{name: "missing b.sms is reported at b.sms", raw: trDoc + `,"b":{"email":{"subject":"y","body":"z"}},` + abAnglesDoc + `}`, paths: []string{"b.sms"}, exact: true},
		{name: "missing b.email.subject is reported at b.email.subject", raw: trDoc + `,"b":{"sms":{"body":"x"},"email":{"body":"z"}},` + abAnglesDoc + `}`, paths: []string{"b.email.subject"}, exact: true},
		{name: "html inside b is reported at b.html", raw: trDoc + `,"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"html":"<b>x</b>"},` + abAnglesDoc + `}`, paths: []string{"b.html"}, exact: true},
		{name: "a third angle is reported at angles.c", raw: trDoc + `,` + abBDoc + `,"angles":{"a":"A","b":"B","c":"C"}}`, paths: []string{"angles.c"}, exact: true},
		{name: "missing angles.a is reported at angles.a", raw: trDoc + `,` + abBDoc + `,"angles":{"b":"B"}}`, paths: []string{"angles.a"}, exact: true},
		{name: "en without b.en is reported at b.en", raw: trDoc + `,` + abENDoc + `,` + abBDoc + `,` + abAnglesDoc + `}`, paths: []string{"b.en"}, exact: true},
		{name: "b.en without en is reported at b.en", raw: trDoc + `,` + abBENDoc + `,` + abAnglesDoc + `}`, paths: []string{"b.en"}, exact: true},
		{name: "the criterion's example reports b.email.body then angles",
			raw:   `{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"b":{"sms":{"body":"x"},"email":{"subject":"y"}}}`,
			paths: []string{"b.email.body", "angles"}, exact: true},
		{name: "unknown keys first, then known keys in schema order, then the pairing rules",
			raw:   `{"x":1,"sms":{"body":"a"},"email":{"subject":"b","body":"c"},` + abENDoc + `,"b":{"sms":{"body":"x"},"email":{"body":"z"}},"angles":{"a":"A","b":"B","c":"C"}}`,
			paths: []string{"x", "b.email.subject", "angles.c", "b.en"}, exact: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := content.Parse([]byte(tc.raw))
			if !errors.Is(err, validation.ErrSchemaViolation) {
				t.Fatalf("Parse() error = %v, want %v", err, validation.ErrSchemaViolation)
			}
			fs := fieldsOf(t, err)
			var got []string
			for _, f := range fs {
				if f.Code != "schema_violation" {
					t.Fatalf("Parse reported %+v; every structural error is schema_violation", f)
				}
				got = append(got, f.Path)
			}
			if tc.exact {
				if diff := cmp.Diff(tc.paths, got); diff != "" {
					t.Fatalf("schema_violation paths mismatch (-want +got):\n%s", diff)
				}
				return
			}
			for _, p := range tc.paths {
				if !slices.Contains(got, p) {
					t.Fatalf("schema_violation paths = %v, want %q among them", got, p)
				}
			}
		})
	}

	t.Run("a document with b, b.en and angles decodes into B and Angles", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(trDoc + `,` + abENDoc + `,` + abBENDoc + `,` + abAnglesDoc + `}`))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		en := &content.English{SMS: content.SMS{Body: "e"}, Email: content.Email{Subject: "f", Body: "g"}}
		want := content.Content{
			SMS: content.SMS{Body: "a"}, Email: content.Email{Subject: "b", Body: "c"}, EN: en,
			B:      &content.Variant{SMS: content.SMS{Body: "x"}, Email: content.Email{Subject: "y", Body: "z"}, EN: en},
			Angles: &content.Angles{A: "A", B: "B"},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("without b and angles B and Angles stay nil", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(trDoc + `}`))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if got.B != nil || got.Angles != nil {
			t.Fatalf("Parse() B/Angles = %+v/%+v, want nil/nil", got.B, got.Angles)
		}
	})

	t.Run("empty b strings and angles are structurally valid", func(t *testing.T) {
		t.Parallel()
		got, err := content.Parse([]byte(trDoc + `,"b":{"sms":{"body":""},"email":{"subject":"","body":""}},"angles":{"a":"","b":""}}`))
		if err != nil {
			t.Fatalf("Parse() error = %v, want nil (empty values are Validate's required)", err)
		}
		if got.B == nil || got.Angles == nil {
			t.Fatalf("Parse() B/Angles = %+v/%+v, want both set", got.B, got.Angles)
		}
	})
}

// REQ-ABT-01 C3, C4, C5, C6
// Validate checks variant B with the rules of variant A at the paths b.sms.body, b.email.subject,
// b.email.body, b.en.sms.body, b.en.email.subject, b.en.email.body (same codes, limits, priority,
// substitution with the campaign's single set of longest values); angles are plain labels (required,
// too_long over 60 runes, no variable check); B equal to A in all three Turkish fields (after NFC) is
// variants_identical at path b, reported in addition to and independently of the field errors. Error
// order: A fields, en fields, b fields, b.en fields, b, angles.a, angles.b.
func TestValidate_VariantB(t *testing.T) {
	t.Parallel()
	identical := func(c *content.Content) { c.B.SMS, c.B.Email = c.SMS, c.Email }
	tests := []struct {
		name     string
		c        content.Content
		want     []fieldWant // nil => valid
		lengths  bool        // compare limit / actual too (nil means "absent")
		sentinel error
	}{
		{name: "valid A/B content is accepted", c: abContent()},
		{name: "valid bilingual A/B content is accepted", c: abBilingual()},
		{name: "160-char b.sms.body is accepted", c: abWith(func(c *content.Content) { c.B.SMS.Body = strings.Repeat("b", 160) })},
		{name: "161-char b.sms.body is rejected", c: abWith(func(c *content.Content) { c.B.SMS.Body = strings.Repeat("b", 161) }),
			want: []fieldWant{{"b.sms.body", "sms_too_long", ip(160), ip(161)}}, lengths: true, sentinel: content.ErrSMSTooLong},
		{name: "b first_name placeholder at 160 after substitution", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 110) })},
		{name: "b first_name placeholder one over after substitution", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 111) }),
			want: []fieldWant{{"b.sms.body", "sms_too_long", ip(160), ip(161)}}, lengths: true, sentinel: content.ErrSMSTooLong},
		{name: "80-char b.email.subject is accepted", c: abWith(func(c *content.Content) { c.B.Email.Subject = strings.Repeat("ğ", 80) })},
		{name: "81-char b.email.subject is rejected", c: abWith(func(c *content.Content) { c.B.Email.Subject = strings.Repeat("ğ", 81) }),
			want: []fieldWant{{"b.email.subject", "subject_too_long", ip(80), ip(81)}}, lengths: true, sentinel: content.ErrSubjectTooLong},
		{name: "5001-char b.email.body is rejected", c: abWith(func(c *content.Content) { c.B.Email.Body = strings.Repeat("ü", 5001) }),
			want: []fieldWant{{"b.email.body", "email_body_too_long", ip(5000), ip(5001)}}, lengths: true, sentinel: content.ErrEmailBodyTooLong},
		{name: "empty b.sms.body is required", c: abWith(func(c *content.Content) { c.B.SMS.Body = "" }),
			want: []fieldWant{{Path: "b.sms.body", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "whitespace-only b.email.subject is required", c: abWith(func(c *content.Content) { c.B.Email.Subject = " \t " }),
			want: []fieldWant{{Path: "b.email.subject", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "unknown variable in b.sms.body", c: abWith(func(c *content.Content) { c.B.SMS.Body = "şifren {{password}}" }),
			want: []fieldWant{{Path: "b.sms.body", Code: "unknown_variable"}}, sentinel: content.ErrUnknownVariable},
		{name: "malformed variable in b.email.body", c: abWith(func(c *content.Content) { c.B.Email.Body = "Merhaba {{first_name" }),
			want: []fieldWant{{Path: "b.email.body", Code: "malformed_variable"}}, sentinel: content.ErrMalformedVariable},
		{name: "unknown variable in a 200-char b.sms.body reports only unknown_variable", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{password}}" + strings.Repeat("a", 200) }),
			want: []fieldWant{{Path: "b.sms.body", Code: "unknown_variable"}}, sentinel: content.ErrUnknownVariable},
		{name: "161-char b.en.sms.body is rejected", c: func() content.Content { c := abBilingual(); c.B.EN.SMS.Body = strings.Repeat("e", 161); return c }(),
			want: []fieldWant{{"b.en.sms.body", "sms_too_long", ip(160), ip(161)}}, lengths: true, sentinel: content.ErrSMSTooLong},
		{name: "81-char b.en.email.subject is rejected", c: func() content.Content { c := abBilingual(); c.B.EN.Email.Subject = strings.Repeat("s", 81); return c }(),
			want: []fieldWant{{"b.en.email.subject", "subject_too_long", ip(80), ip(81)}}, lengths: true, sentinel: content.ErrSubjectTooLong},
		{name: "empty b.en.email.body is required", c: func() content.Content { c := abBilingual(); c.B.EN.Email.Body = ""; return c }(),
			want: []fieldWant{{Path: "b.en.email.body", Code: "required"}}, sentinel: validation.ErrRequired},
		// Angles (C4).
		{name: "60-char angle is accepted", c: abWith(func(c *content.Content) { c.Angles.B = strings.Repeat("a", 60) })},
		{name: "60 Turkish letters angle is accepted", c: abWith(func(c *content.Content) { c.Angles.A = strings.Repeat("ş", 60) })},
		{name: "61-char angles.a is too_long", c: abWith(func(c *content.Content) { c.Angles.A = strings.Repeat("a", 61) }),
			want: []fieldWant{{"angles.a", "too_long", ip(60), ip(61)}}, lengths: true},
		{name: "61-char angles.b is too_long", c: abWith(func(c *content.Content) { c.Angles.B = strings.Repeat("ç", 61) }),
			want: []fieldWant{{"angles.b", "too_long", ip(60), ip(61)}}, lengths: true},
		{name: "empty angles.a is required", c: abWith(func(c *content.Content) { c.Angles.A = "" }),
			want: []fieldWant{{Path: "angles.a", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "whitespace-only angles.b is required", c: abWith(func(c *content.Content) { c.Angles.B = "   " }),
			want: []fieldWant{{Path: "angles.b", Code: "required"}}, sentinel: validation.ErrRequired},
		{name: "an angle is not a template: variables are not checked", c: abWith(func(c *content.Content) { c.Angles.A = "{{password}} ve {{first_name" })},
		// Difference check (C5).
		{name: "B equal to A in all three fields is variants_identical at b", c: abWith(identical),
			want: []fieldWant{{Path: "b", Code: "variants_identical"}}, lengths: true, sentinel: content.ErrVariantsIdentical},
		{name: "B equal to A after NFC is variants_identical", c: abWith(func(c *content.Content) {
			identical(c)
			c.B.SMS.Body = strings.ReplaceAll(cntOKSMS, "ü", "ü")
		}), want: []fieldWant{{Path: "b", Code: "variants_identical"}}, lengths: true, sentinel: content.ErrVariantsIdentical},
		{name: "one differing field is enough", c: abWith(func(c *content.Content) {
			identical(c)
			c.B.Email.Body = "Merhaba {{first_name}}, farklı gövde."
		})},
		{name: "en texts do not enter the difference check", c: func() content.Content {
			c := abBilingual()
			identical(&c)
			return c
		}(), want: []fieldWant{{Path: "b", Code: "variants_identical"}}, lengths: true, sentinel: content.ErrVariantsIdentical},
		{name: "identical B with an invalid A field reports both field errors and variants_identical", c: abWith(func(c *content.Content) {
			c.SMS.Body = cntSMS161
			identical(c)
		}), want: []fieldWant{
			{"sms.body", "sms_too_long", ip(160), ip(161)},
			{"b.sms.body", "sms_too_long", ip(160), ip(161)},
			{Path: "b", Code: "variants_identical"},
		}, lengths: true, sentinel: content.ErrVariantsIdentical},
		// Order (C6).
		{name: "errors come in the order A, en, b, b.en, b, angles.a, angles.b", c: func() content.Content {
			c := abBilingual()
			c.SMS.Body = cntSMS161
			c.EN.SMS.Body = strings.Repeat("e", 161)
			identical(&c)
			c.B.EN.Email.Body = ""
			c.Angles = &content.Angles{A: "", B: strings.Repeat("a", 61)}
			return c
		}(), want: []fieldWant{
			{Path: "sms.body", Code: "sms_too_long"}, {Path: "en.sms.body", Code: "sms_too_long"},
			{Path: "b.sms.body", Code: "sms_too_long"}, {Path: "b.en.email.body", Code: "required"},
			{Path: "b", Code: "variants_identical"}, {Path: "angles.a", Code: "required"}, {Path: "angles.b", Code: "too_long"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.Validate(tc.c, exampleLongest)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.sentinel)
			}
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), tc.lengths)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-ABT-01 C7
// MeasureWorstCase measures every variant × language version with the SAME longest values: A in the
// existing fields, B in WorstCase.B and b.en in WorstCase.B.EN; without b there is no B.
func TestMeasureWorstCase_VariantB(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    content.Content
		l    content.Longest
		want content.WorstCase
		err  error
	}{
		{name: "B is measured with the same longest values", c: abWith(func(c *content.Content) {
			c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 100)
			c.B.Email.Subject = "{{city}} için son gün"
		}), l: exampleLongest, want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42, B: &content.WorstCaseVariant{SMSRunes: 150, SubjectRunes: 21}}},
		{name: "over-limit B is still measured", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 111) }), l: exampleLongest,
			want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42, B: &content.WorstCaseVariant{SMSRunes: 161, SubjectRunes: 20}}},
		{name: "b.en is measured in B.EN, en in EN", c: func() content.Content {
			c := abBilingual()
			c.EN.SMS.Body, c.EN.Email.Subject = "Hi {{first_name}}", "Subject"
			c.B.EN.SMS.Body, c.B.EN.Email.Subject = "{{first_name}}"+strings.Repeat("e", 10), "{{last_name}} x"
			return c
		}(), l: exampleLongest, want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42,
			EN: &content.WorstCaseLang{SMSRunes: 53, SubjectRunes: 7},
			B:  &content.WorstCaseVariant{SMSRunes: 86, SubjectRunes: 20, EN: &content.WorstCaseLang{SMSRunes: 60, SubjectRunes: 10}}}},
		{name: "the empty-audience fallback applies to B too", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" }), l: emptyLongest,
			want: content.WorstCase{SMSRunes: 128, SubjectRunes: 42, B: &content.WorstCaseVariant{SMSRunes: 18, SubjectRunes: 20}}},
		{name: "without b there is no B", c: cntOK(), l: exampleLongest, want: content.WorstCase{SMSRunes: 160, SubjectRunes: 42}},
		{name: "malformed variable in b.sms.body", c: abWith(func(c *content.Content) { c.B.SMS.Body = "{{first_name" }), l: exampleLongest, err: content.ErrMalformedVariable},
		{name: "unknown variable in b.en.email.subject", c: func() content.Content { c := abBilingual(); c.B.EN.Email.Subject = "{{password}}"; return c }(), l: exampleLongest, err: content.ErrUnknownVariable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.MeasureWorstCase(tc.c, tc.l)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("MeasureWorstCase() error = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("MeasureWorstCase() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("MeasureWorstCase() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-ABT-01 C9
// The approval re-check covers every variant × language version: b.sms.body / b.email.subject /
// b.en.sms.body / b.en.email.subject with the *_worst_case codes and limits 160 / 80, after the A
// paths; email bodies are not re-checked.
func TestCheckWorstCase_VariantB(t *testing.T) {
	t.Parallel()
	longer := content.Longest{FirstName: 51, LastName: 8, City: 8}
	// abNoVar is A/B content whose A and en texts have no variables, so only B decides.
	abNoVar := func(f func(c *content.Content)) content.Content {
		c := abBilingual()
		c.SMS.Body, c.Email.Subject = trNoVarSMS, cntOKSubject
		c.EN.SMS.Body, c.EN.Email.Subject = "Hi, your cart is waiting.", "Your cart"
		c.B.SMS.Body, c.B.Email.Subject = "Son gün!", "Son gün"
		c.B.EN.SMS.Body, c.B.EN.Email.Subject = "Last day!", "Last day"
		f(&c)
		return c
	}
	tests := []struct {
		name      string
		c         content.Content
		l         content.Longest
		want      []fieldWant
		sentinels []error
	}{
		{name: "B at the boundary passes", c: abNoVar(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 110) }), l: exampleLongest},
		{name: "a longer first name at approval fails b.sms.body", c: abNoVar(func(c *content.Content) { c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 110) }), l: longer,
			want:      []fieldWant{{"b.sms.body", "sms_too_long_worst_case", ip(160), ip(161)}},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "a longer last name at approval fails b.email.subject", c: abNoVar(func(c *content.Content) { c.B.Email.Subject = "{{last_name}}" + strings.Repeat("b", 72) }), l: content.Longest{FirstName: 18, LastName: 9},
			want:      []fieldWant{{"b.email.subject", "subject_too_long_worst_case", ip(80), ip(81)}},
			sentinels: []error{content.ErrSubjectTooLong, content.ErrWorstCaseTooLong}},
		{name: "a longer first name at approval fails b.en.sms.body", c: abNoVar(func(c *content.Content) { c.B.EN.SMS.Body = "{{first_name}}" + strings.Repeat("e", 110) }), l: longer,
			want:      []fieldWant{{"b.en.sms.body", "sms_too_long_worst_case", ip(160), ip(161)}},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "a longer city at approval fails b.en.email.subject", c: abNoVar(func(c *content.Content) { c.B.EN.Email.Subject = "{{city}}" + strings.Repeat("e", 72) }), l: content.Longest{FirstName: 18, City: 9},
			want:      []fieldWant{{"b.en.email.subject", "subject_too_long_worst_case", ip(80), ip(81)}},
			sentinels: []error{content.ErrSubjectTooLong, content.ErrWorstCaseTooLong}},
		{name: "A failures come before B failures", c: abNoVar(func(c *content.Content) {
			c.SMS.Body = cntOKSMS
			c.B.SMS.Body = "{{first_name}}" + strings.Repeat("b", 110)
		}), l: longer,
			want: []fieldWant{
				{"sms.body", "sms_too_long_worst_case", ip(160), ip(161)},
				{"b.sms.body", "sms_too_long_worst_case", ip(160), ip(161)},
			},
			sentinels: []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong}},
		{name: "b.email.body over 5000 is not re-checked at approval", c: abNoVar(func(c *content.Content) { c.B.Email.Body = "{{city}}" + strings.Repeat("c", 4995) }), l: content.Longest{FirstName: 18, City: 10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.CheckWorstCase(tc.c, tc.l)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckWorstCase() error = %v, want nil", err)
				}
				return
			}
			for _, s := range tc.sentinels {
				if !errors.Is(err, s) {
					t.Fatalf("CheckWorstCase() error = %v, want it to match %v", err, s)
				}
			}
			if diff := cmp.Diff(tc.want, project(fieldsOf(t, err), true)); diff != "" {
				t.Fatalf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-ABT-01 C1, C7, C10
// JSON of the Go types: Content writes b and angles only when set (b.en only when B.EN is set; angles
// keys a and b); WorstCase writes b = {sms_runes, subject_runes} only when B is set, and b.en only when
// B.EN is set. Without b and angles everything is as in P8.
func TestVariantJSONShape(t *testing.T) {
	t.Parallel()
	enWC := &content.WorstCaseLang{SMSRunes: 9, SubjectRunes: 8}
	tests := []struct {
		name string
		v    any
		want string
	}{
		{name: "content without b and angles", v: cntOK(),
			want: `{"sms":{"body":"` + jsonEsc(cntOKSMS) + `"},"email":{"subject":"` + jsonEsc(cntOKSubject) + `","body":"` + jsonEsc(cntOKBody) + `"}}`},
		{name: "content with b and angles", v: content.Content{
			SMS: content.SMS{Body: "a"}, Email: content.Email{Subject: "b", Body: "c"},
			B:      &content.Variant{SMS: content.SMS{Body: "x"}, Email: content.Email{Subject: "y", Body: "z"}},
			Angles: &content.Angles{A: "p", B: "q"},
		}, want: `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"}},"angles":{"a":"p","b":"q"}}`},
		{name: "content with b.en", v: content.Content{
			SMS: content.SMS{Body: "a"}, Email: content.Email{Subject: "b", Body: "c"},
			EN:     &content.English{SMS: content.SMS{Body: "e"}, Email: content.Email{Subject: "f", Body: "g"}},
			B:      &content.Variant{SMS: content.SMS{Body: "x"}, Email: content.Email{Subject: "y", Body: "z"}, EN: &content.English{SMS: content.SMS{Body: "h"}, Email: content.Email{Subject: "i", Body: "j"}}},
			Angles: &content.Angles{A: "p", B: "q"},
		}, want: `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"en":{"sms":{"body":"e"},"email":{"subject":"f","body":"g"}},"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"en":{"sms":{"body":"h"},"email":{"subject":"i","body":"j"}}},"angles":{"a":"p","b":"q"}}`},
		{name: "worst case without B", v: content.WorstCase{SMSRunes: 1, SubjectRunes: 2}, want: `{"sms_runes":1,"subject_runes":2}`},
		{name: "worst case with B", v: content.WorstCase{SMSRunes: 1, SubjectRunes: 2, B: &content.WorstCaseVariant{SMSRunes: 3, SubjectRunes: 4}},
			want: `{"sms_runes":1,"subject_runes":2,"b":{"sms_runes":3,"subject_runes":4}}`},
		{name: "worst case with B and B.EN", v: content.WorstCase{SMSRunes: 1, SubjectRunes: 2, EN: enWC, B: &content.WorstCaseVariant{SMSRunes: 3, SubjectRunes: 4, EN: enWC}},
			want: `{"sms_runes":1,"subject_runes":2,"en":{"sms_runes":9,"subject_runes":8},"b":{"sms_runes":3,"subject_runes":4,"en":{"sms_runes":9,"subject_runes":8}}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got, want any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("decode %s: %v", b, err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatalf("test setup: %v", err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("JSON mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-ABT-03 C1, C2, C3
// VariantFor derives the variant from the first byte of SHA-256(campaign UUID bytes ‖ customer UUID
// bytes): < 128 => a, otherwise b. The ten test vectors of the criterion; upper-case UUIDs give the
// same result; a malformed UUID is an error.
func TestVariantFor(t *testing.T) {
	t.Parallel()
	const c1, c2 = "00000000-0000-4000-8000-000000000001", "3f1c2a4e-0b8d-4c55-9e1a-7d2b6c8f9a01"
	cust := func(n int) string { return "00000000-0000-4000-8000-00000000000" + strconv.Itoa(n) } // n is 1..8
	tests := []struct {
		campaign, customer, want string
	}{
		{c1, cust(1), "a"}, {c1, cust(2), "a"}, {c1, cust(3), "a"}, {c1, cust(4), "b"},
		{c1, cust(5), "a"}, {c1, cust(6), "b"}, {c1, cust(7), "b"}, {c1, cust(8), "b"},
		{c2, cust(4), "a"}, {c2, cust(5), "b"},
	}
	for _, tc := range tests {
		t.Run(tc.campaign+" "+tc.customer, func(t *testing.T) {
			t.Parallel()
			got, err := content.VariantFor(tc.campaign, tc.customer)
			if err != nil || got != tc.want {
				t.Fatalf("VariantFor(%s, %s) = %q, %v; want %q, nil", tc.campaign, tc.customer, got, err, tc.want)
			}
			upper, err := content.VariantFor(strings.ToUpper(tc.campaign), strings.ToUpper(tc.customer))
			if err != nil || upper != tc.want {
				t.Fatalf("VariantFor(upper case) = %q, %v; want %q, nil", upper, err, tc.want)
			}
		})
	}
	for _, bad := range [][2]string{{"not-a-uuid", cust(1)}, {c1, ""}, {c1, cust(1) + "0"}, {"", ""}} {
		t.Run("malformed "+bad[0]+" "+bad[1], func(t *testing.T) {
			t.Parallel()
			if _, err := content.VariantFor(bad[0], bad[1]); err == nil {
				t.Fatalf("VariantFor(%q, %q) error = nil, want an error", bad[0], bad[1])
			}
		})
	}
}

// REQ-ABT-03 C8
// RenderContent picks the variant first, then the language inside it: Recipient.Variant "b" and a
// version with B => B text and Rendered.Variant "b"; otherwise A and Variant "a". An English recipient
// gets the English text of the chosen variant; without b.en (and en) it gets that variant's Turkish text
// (the language fallback works inside the variant).
func TestRenderContent_Variant(t *testing.T) {
	t.Parallel()
	ab := abContent()
	abEN := abBilingual()
	fill := func(tmpl, first string) string { return strings.ReplaceAll(tmpl, "{{first_name}}", first) }
	tests := []struct {
		name string
		c    content.Content
		r    content.Recipient
		want content.Rendered
	}{
		{name: "b recipient gets variant B", c: ab, r: content.Recipient{FirstName: "Ayşe", Variant: "b"},
			want: content.Rendered{SMSText: fill(abBSMS, "Ayşe"), EmailSubject: abBSubject, EmailText: fill(abBBody, "Ayşe"), Language: "tr", Variant: "b"}},
		{name: "a recipient gets variant A", c: ab, r: content.Recipient{FirstName: "Ayşe", Variant: "a"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ayşe"), EmailSubject: cntOKSubject, EmailText: strings.ReplaceAll(fill(cntOKBody, "Ayşe"), "{{city}}", ""), Language: "tr", Variant: "a"}},
		{name: "unset variant is A", c: ab, r: content.Recipient{FirstName: "Ayşe"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ayşe"), EmailSubject: cntOKSubject, EmailText: strings.ReplaceAll(fill(cntOKBody, "Ayşe"), "{{city}}", ""), Language: "tr", Variant: "a"}},
		{name: "b recipient of a version without b gets A", c: cntOK(), r: content.Recipient{FirstName: "Ayşe", Variant: "b"},
			want: content.Rendered{SMSText: fill(cntOKSMS, "Ayşe"), EmailSubject: cntOKSubject, EmailText: strings.ReplaceAll(fill(cntOKBody, "Ayşe"), "{{city}}", ""), Language: "tr", Variant: "a"}},
		{name: "English b recipient gets the English text of B", c: abEN, r: content.Recipient{FirstName: "Ayşe", Language: "en", Variant: "b"},
			want: content.Rendered{SMSText: fill(abEN.B.EN.SMS.Body, "Ayşe"), EmailSubject: abEN.B.EN.Email.Subject, EmailText: fill(abEN.B.EN.Email.Body, "Ayşe"), Language: "en", Variant: "b"}},
		{name: "English b recipient with an empty first name gets Dear customer", c: abEN, r: content.Recipient{Language: "en", Variant: "b"},
			want: content.Rendered{SMSText: fill(abEN.B.EN.SMS.Body, "Dear customer"), EmailSubject: abEN.B.EN.Email.Subject, EmailText: fill(abEN.B.EN.Email.Body, "Dear customer"), Language: "en", Variant: "b"}},
		{name: "English b recipient without b.en gets the Turkish text of B", c: ab, r: content.Recipient{FirstName: "Ayşe", Language: "en", Variant: "b"},
			want: content.Rendered{SMSText: fill(abBSMS, "Ayşe"), EmailSubject: abBSubject, EmailText: fill(abBBody, "Ayşe"), Language: "tr", Fallback: true, Variant: "b"}},
		{name: "English a recipient of a bilingual A/B version gets the English text of A", c: abEN, r: content.Recipient{FirstName: "Ayşe", Language: "en", Variant: "a"},
			want: content.Rendered{SMSText: fill(abEN.EN.SMS.Body, "Ayşe"), EmailSubject: abEN.EN.Email.Subject, EmailText: fill(abEN.EN.Email.Body, "Ayşe"), Language: "en", Variant: "a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.RenderContent(tc.c, tc.r)
			if err != nil {
				t.Fatalf("RenderContent() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("RenderContent() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// jsonEsc is s as the inside of a JSON string literal.
func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// ---- helpers ----

// trNoVarSMS is a Turkish SMS without variables: its length does not depend on Longest.
const trNoVarSMS = "Merhaba, sepetindeki ürünler seni bekliyor."

// bi is a bilingual content: Turkish SMS trSMS with the CNT_OK email, and an English version with
// enSMS, enSubject and a short English body.
func bi(trSMS, enSMS, enSubject string) content.Content {
	c := withSMS(trSMS)
	c.EN = &content.English{SMS: content.SMS{Body: enSMS}, Email: content.Email{Subject: enSubject, Body: "Hi {{first_name}}, your cart is waiting."}}
	return c
}

// biBody is CNT_OK with a valid English SMS and subject and the English email body enBody.
func biBody(enBody string) content.Content {
	c := bi(cntOKSMS, "Hi {{first_name}}", "Your cart")
	c.EN.Email.Body = enBody
	return c
}

func sp(s string) *string { return &s }

func deref(p *string) string {
	if p == nil {
		return "(any)"
	}
	return *p
}
