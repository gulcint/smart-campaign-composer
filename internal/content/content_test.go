package content_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
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

// REQ-CNT-01, REQ-CNT-02, REQ-CNT-03
// The contracted constants (requirements "Sabitler", go-interfaces §content).
func TestConstants(t *testing.T) {
	t.Parallel()
	if content.SMSMaxRunes != 160 || content.SubjectMaxRunes != 80 || content.EmailBodyMaxRunes != 5000 {
		t.Fatalf("limits = %d/%d/%d, want 160/80/5000", content.SMSMaxRunes, content.SubjectMaxRunes, content.EmailBodyMaxRunes)
	}
	if content.FirstNameFallback != "Değerli müşterimiz" {
		t.Fatalf("FirstNameFallback = %q", content.FirstNameFallback)
	}
	if content.RuneCount(content.FirstNameFallback) != 18 {
		t.Fatalf("RuneCount(FirstNameFallback) = %d, want 18", content.RuneCount(content.FirstNameFallback))
	}
	if diff := cmp.Diff([]string{"first_name", "last_name", "city"}, content.AllowedVariables); diff != "" {
		t.Fatalf("AllowedVariables mismatch (-want +got):\n%s", diff)
	}
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

// REQ-AI-01, REQ-APR-02, REQ-CNT-01 (D46)
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

	t.Run("the LLM fixtures with a valid structure parse", func(t *testing.T) {
		t.Parallel()
		for _, c := range []content.Content{cntOK(), withSMS(cntSMS161), withSubject(cntSubject81), withSMS(cntUnknownSMS),
			{SMS: content.SMS{Body: cntHTMLSMS}, Email: content.Email{Subject: cntHTMLSubject, Body: cntHTMLBody}}} {
			raw := marshalContent(t, c)
			got, err := content.Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%s) error = %v", raw, err)
			}
			if diff := cmp.Diff(c, got); diff != "" {
				t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
			}
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
// Variables returns placeholder names in order (duplicates kept); names are not checked against
// the allowed list; broken syntax is ErrMalformedVariable.
func TestVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tmpl string
		want []string
		err  error
	}{
		{tmpl: "düz metin", want: nil},
		{tmpl: "{first_name} %30 $ { }", want: nil},
		{tmpl: "Merhaba {{first_name}}", want: []string{"first_name"}},
		{tmpl: "{{ first_name }} {{city\t}}", want: []string{"first_name", "city"}},
		{tmpl: "{{first_name}}{{first_name}}{{last_name}}", want: []string{"first_name", "first_name", "last_name"}},
		{tmpl: "{{First_Name}} {{password}}", want: []string{"First_Name", "password"}},
		{tmpl: "{{_x9}}", want: []string{"_x9"}},
		{tmpl: "{{first_name", err: content.ErrMalformedVariable},
		{tmpl: "{{", err: content.ErrMalformedVariable},
		{tmpl: "{{ }}", err: content.ErrMalformedVariable},
		{tmpl: "{{}}", err: content.ErrMalformedVariable},
		{tmpl: "}}", err: content.ErrMalformedVariable},
		{tmpl: "{{first-name}}", err: content.ErrMalformedVariable},
		{tmpl: "{{1st}}", err: content.ErrMalformedVariable},
		{tmpl: "{{first name}}", err: content.ErrMalformedVariable},
		{tmpl: "{{{first_name}}}", err: content.ErrMalformedVariable},
	}
	for _, tc := range tests {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := content.Variables(tc.tmpl)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("Variables(%q) error = %v, want %v", tc.tmpl, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Variables(%q) error = %v", tc.tmpl, err)
			}
			if len(tc.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("Variables(%q) = %q, want none", tc.tmpl, got)
				}
				return
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("Variables(%q) mismatch (-want +got):\n%s", tc.tmpl, diff)
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
		{name: "a value that looks like a template stays plain text", tmpl: "Merhaba {{first_name}}", r: content.Recipient{FirstName: "{{city}}", City: "Ankara"}, want: "Merhaba {{city}}"},
		{name: "HTML in a value is kept as text", tmpl: "Merhaba {{first_name}}", r: content.Recipient{FirstName: "<b>x</b>"}, want: "Merhaba <b>x</b>"},
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

// REQ-CNT-05
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

// REQ-CNT-05 (D37, D39, D46)
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
// RenderEmailHTML escapes every text fragment and emits no element beyond the fixed skeleton:
// no script, no anchor, no attribute taken from content.
func TestRenderEmailHTML_EscapesAndUsesOnlyTheSkeleton(t *testing.T) {
	t.Parallel()
	allowed := []string{"!doctype", "html", "/html", "head", "/head", "meta", "title", "/title", "body", "/body", "p", "/p", "br"}
	tests := []struct {
		name     string
		subject  string
		body     string
		contains []string
		absent   []string
		paras    int
	}{
		{
			name: "CNT_HTML fixture is escaped", subject: "<i>Sepetin</i> seni bekliyor",
			body:     "Merhaba Ayşe,\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>",
			contains: []string{"&lt;i&gt;Sepetin&lt;/i&gt; seni bekliyor", "&lt;script&gt;", "&lt;a href="},
			absent:   []string{"<script", "<a ", "<i>", "href=\"javascript"},
			paras:    3,
		},
		{name: "blank lines separate paragraphs", subject: "Konu", body: "Birinci paragraf\n\nİkinci paragraf", contains: []string{"<p>Birinci paragraf</p>", "<p>İkinci paragraf</p>"}, paras: 2},
		{name: "several blank lines are one separator", subject: "Konu", body: "a\n\n\n\nb", contains: []string{"<p>a</p>", "<p>b</p>"}, paras: 2},
		{name: "a single newline becomes br", subject: "Konu", body: "Sevgiler,\nSmart Campaign Composer", contains: []string{"<p>Sevgiler,<br>Smart Campaign Composer</p>"}, paras: 1},
		{name: "ampersand and quotes are escaped", subject: "A & B \"C\"", body: "x < y & z > w", contains: []string{"A &amp; B", "x &lt; y &amp; z &gt; w"}, absent: []string{"x < y"}, paras: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := content.RenderEmailHTML(tc.subject, tc.body)
			if !strings.HasPrefix(strings.ToLower(out), "<!doctype html>") {
				t.Fatalf("output does not start with <!doctype html>: %s", out)
			}
			for _, s := range []string{`<html lang="tr">`, `<meta charset="utf-8">`, `Content-Security-Policy`, `default-src 'none'`, "<title>", "</body></html>"} {
				if !strings.Contains(out, s) {
					t.Fatalf("output lacks %q: %s", s, out)
				}
			}
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
			if got := strings.Count(out, "<p>"); got != tc.paras {
				t.Fatalf("paragraph count = %d, want %d: %s", got, tc.paras, out)
			}
		})
	}
}

// ---- helpers ----

func sp(s string) *string { return &s }

func deref(p *string) string {
	if p == nil {
		return "(any)"
	}
	return *p
}

func marshalContent(t *testing.T, c content.Content) []byte {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return b
}
