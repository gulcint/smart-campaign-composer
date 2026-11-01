package composer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Content fixtures of docs/contracts/llm-tools.md §3.3, verbatim.
const (
	cOKSMS      = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
	cOKSubject  = "Sepetin seni bekliyor: %30 indirim fırsatı"
	cOKBody     = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer"
	cUnknownSMS = "Merhaba {{first_name}}, şifren {{password}} ile giriş yap ve %30 indirimi kap!"
	cHTMLSMS    = "Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!"
	cHTMLSubj   = "<i>Sepetin</i> seni bekliyor"
	cHTMLBody   = "Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>"
)

var (
	cSMS161    = strings.Repeat("İndirim ", 20) + "!"
	cSubject81 = strings.Repeat("Fırsat ", 11) + "son!"
	// manifest example_brief.longest_values: with it the CNT_OK SMS is exactly 160 after substitution.
	cLongest = content.Longest{FirstName: 50, LastName: 8, City: 8}
)

// anyPath marks an expected field error whose path the contract does not fix (content invalid_json).
const anyPath = "\x00any"

// hasPC is containsPathCode with the anyPath wildcard.
func hasPC(list []pathCode, want pathCode) bool {
	for _, pc := range list {
		if pc.Code == want.Code && (want.Path == anyPath || pc.Path == want.Path) {
			return true
		}
	}
	return false
}

func cContent(sms, subject, body string) content.Content {
	return content.Content{SMS: content.SMS{Body: sms}, Email: content.Email{Subject: subject, Body: body}}
}

func cOK() content.Content { return cContent(cOKSMS, cOKSubject, cOKBody) }

func contentRequest() llm.ContentRequest {
	return llm.ContentRequest{Brief: exampleBrief, Rule: json.RawMessage(segOKRule), Longest: cLongest, Attempt: 1}
}

// recorded is the projection of the events a ContentSession emitted.
type recorded struct {
	deltas   []composer.DeltaEvent
	invalids []composer.InvalidEvent
	order    []string // "delta:<attempt>" | "invalid" | "validated" | other names
}

func (r *recorded) emit(ev composer.Event) error {
	switch e := ev.(type) {
	case composer.DeltaEvent:
		r.deltas = append(r.deltas, e)
		r.order = append(r.order, "delta:"+itoa(e.Attempt))
	case *composer.DeltaEvent:
		r.deltas = append(r.deltas, *e)
		r.order = append(r.order, "delta:"+itoa(e.Attempt))
	case composer.InvalidEvent:
		r.invalids = append(r.invalids, e)
		r.order = append(r.order, "invalid")
	case *composer.InvalidEvent:
		r.invalids = append(r.invalids, *e)
		r.order = append(r.order, "invalid")
	default:
		r.order = append(r.order, ev.Name())
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// fieldText concatenates the delta texts of one attempt for one (channel, field) pair.
func (r *recorded) fieldText(attempt int, channel, field string) string {
	var b strings.Builder
	for _, d := range r.deltas {
		if d.Attempt == attempt && d.Channel == channel && d.Field == field {
			b.WriteString(d.Text)
		}
	}
	return b.String()
}

func runSession(t *testing.T, ctx context.Context, c llm.Client, req llm.ContentRequest) (composer.ContentOutcome, *recorded, error) {
	t.Helper()
	s, err := composer.OpenContent(ctx, c, req)
	if err != nil {
		t.Fatalf("OpenContent() error = %v", err)
	}
	rec := &recorded{}
	out, err := s.Run(rec.emit)
	return out, rec, err
}

func assertDeltaShape(t *testing.T, rec *recorded) {
	t.Helper()
	valid := map[[2]string]bool{{"sms", "body"}: true, {"email", "subject"}: true, {"email", "body"}: true}
	for _, d := range rec.deltas {
		if !valid[[2]string{d.Channel, d.Field}] {
			t.Fatalf("delta with invalid (channel, field) pair: %+v", d)
		}
		if d.Text == "" {
			t.Fatalf("delta with empty text: %+v", d)
		}
		if d.Attempt != 1 && d.Attempt != 2 {
			t.Fatalf("delta attempt = %d, want 1 or 2", d.Attempt)
		}
	}
}

func assertAttemptText(t *testing.T, rec *recorded, attempt int, want content.Content) {
	t.Helper()
	got := cContent(rec.fieldText(attempt, "sms", "body"), rec.fieldText(attempt, "email", "subject"), rec.fieldText(attempt, "email", "body"))
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("attempt %d delta texts mismatch (-want +got):\n%s", attempt, diff)
	}
}

// assertNoTerminal: Run never emits the terminal event (go-interfaces §composer).
func assertNoTerminal(t *testing.T, rec *recorded) {
	t.Helper()
	if len(rec.invalids) > 1 {
		t.Fatalf("Run emitted %d invalid events, want at most one (attempt 1 failure)", len(rec.invalids))
	}
	for _, inv := range rec.invalids {
		if inv.Final {
			t.Fatalf("Run emitted a final invalid event %+v; the caller emits the terminal event", inv)
		}
	}
	for _, n := range rec.order {
		if n == "validated" {
			t.Fatalf("Run emitted a validated event; the caller emits the terminal event")
		}
	}
}

// REQ-CNT-04, REQ-AI-01, REQ-AI-02
// ContentSession.Run over every content mock scenario (llm-tools.md §3.2): deltas stream field by
// field, attempt 1 failure emits one non-final invalid then attempt 2 restarts the deltas, and the
// outcome says whether the result is valid or a storable invalid draft.
func TestContentSession_MockScenarios(t *testing.T) {
	t.Parallel()
	type want struct {
		valid      bool
		storable   bool
		attempts   int
		content    *content.Content // outcome Content (valid or storable)
		worstCase  *content.WorstCase
		noWorst    bool // WorstCase must be nil
		errs       []pathCode
		attempt1   *content.Content // concatenated attempt-1 deltas (nil => not checked)
		attempt2   *content.Content
		repairErrs []pathCode // errors of the non-final invalid event
	}
	ok := cOK()
	sms161 := cContent(cSMS161, cOKSubject, cOKBody)
	subj81 := cContent(cOKSMS, cSubject81, cOKBody)
	unknown := cContent(cUnknownSMS, cOKSubject, cOKBody)
	html := cContent(cHTMLSMS, cHTMLSubj, cHTMLBody)
	tests := []struct {
		scenario llm.Scenario
		want     want
	}{
		{scenario: llm.ScenarioOK, want: want{valid: true, attempts: 1, content: &ok, worstCase: &content.WorstCase{SMSRunes: 160, SubjectRunes: 42}, attempt1: &ok}},
		{scenario: llm.ScenarioSQLInValue, want: want{valid: true, attempts: 1, content: &ok, attempt1: &ok}},
		{scenario: llm.ScenarioHTMLInContent, want: want{valid: true, attempts: 1, content: &html, attempt1: &html}},
		{scenario: llm.ScenarioThenOKAfterRepair, want: want{valid: true, attempts: 2, content: &ok, worstCase: &content.WorstCase{SMSRunes: 160, SubjectRunes: 42},
			attempt1: &sms161, attempt2: &ok, repairErrs: []pathCode{{"sms.body", "sms_too_long"}}}},
		{scenario: llm.ScenarioOverLimitSMS, want: want{storable: true, attempts: 2, content: &sms161, worstCase: &content.WorstCase{SMSRunes: 161, SubjectRunes: 42},
			errs: []pathCode{{"sms.body", "sms_too_long"}}, attempt1: &sms161, attempt2: &sms161, repairErrs: []pathCode{{"sms.body", "sms_too_long"}}}},
		{scenario: llm.ScenarioOverLimitSubject, want: want{storable: true, attempts: 2, content: &subj81, worstCase: &content.WorstCase{SMSRunes: 160, SubjectRunes: 81},
			errs: []pathCode{{"email.subject", "subject_too_long"}}, attempt2: &subj81}},
		{scenario: llm.ScenarioUnknownVariable, want: want{storable: true, attempts: 2, content: &unknown, noWorst: true,
			errs: []pathCode{{"sms.body", "unknown_variable"}}, attempt2: &unknown}},
		{scenario: llm.ScenarioInvalidJSON, want: want{attempts: 2, errs: []pathCode{{anyPath, "invalid_json"}},
			attempt1: &content.Content{SMS: content.SMS{Body: "Merhaba {{first_name}}"}}, repairErrs: []pathCode{{anyPath, "invalid_json"}}}},
		{scenario: llm.ScenarioSchemaViolation, want: want{attempts: 2, errs: []pathCode{{"html", "schema_violation"}}, attempt1: &ok, attempt2: &ok}},
		{scenario: llm.ScenarioInjectionAttempt, want: want{attempts: 2, errs: []pathCode{{"sql", "schema_violation"}}}},
		{scenario: llm.ScenarioTruncatedStream, want: want{attempts: 2, errs: []pathCode{{"", "truncated_output"}},
			attempt1: &content.Content{SMS: content.SMS{Body: string([]rune(cOKSMS)[:40])}}, repairErrs: []pathCode{{"", "truncated_output"}}}},
		{scenario: llm.ScenarioAlwaysInvalid, want: want{attempts: 2, errs: []pathCode{{"html", "schema_violation"}}, repairErrs: []pathCode{{anyPath, "invalid_json"}}}},
	}
	for _, tc := range tests {
		t.Run(string(tc.scenario), func(t *testing.T) {
			t.Parallel()
			m := llm.NewMock(llm.MockOptions{})
			ctx := llm.WithScenario(context.Background(), tc.scenario)
			out, rec, err := runSession(t, ctx, m, contentRequest())
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			w := tc.want
			assertDeltaShape(t, rec)
			assertNoTerminal(t, rec)

			var calls []int
			for _, c := range m.Calls() {
				if c.Operation != "content" {
					t.Fatalf("unexpected mock call %+v", c)
				}
				calls = append(calls, c.Attempt)
			}
			wantCalls := []int{1}
			if w.attempts == 2 {
				wantCalls = []int{1, 2}
			}
			if diff := cmp.Diff(wantCalls, calls); diff != "" {
				t.Fatalf("LLM attempts mismatch (-want +got):\n%s", diff)
			}

			if out.Valid != w.valid || out.Storable != w.storable || out.Attempts != w.attempts {
				t.Fatalf("outcome Valid/Storable/Attempts = %v/%v/%d, want %v/%v/%d", out.Valid, out.Storable, out.Attempts, w.valid, w.storable, w.attempts)
			}
			if w.content != nil {
				if diff := cmp.Diff(*w.content, out.Content); diff != "" {
					t.Fatalf("outcome Content mismatch (-want +got):\n%s", diff)
				}
			}
			if w.worstCase != nil {
				if out.WorstCase == nil {
					t.Fatalf("outcome WorstCase = nil, want %+v", *w.worstCase)
				}
				if diff := cmp.Diff(*w.worstCase, *out.WorstCase); diff != "" {
					t.Fatalf("outcome WorstCase mismatch (-want +got):\n%s", diff)
				}
			}
			if w.noWorst && out.WorstCase != nil {
				t.Fatalf("outcome WorstCase = %+v, want nil (template syntax invalid)", *out.WorstCase)
			}
			if w.valid {
				if len(out.Errors) != 0 {
					t.Fatalf("valid outcome carries errors %+v", out.Errors)
				}
			} else {
				for _, pc := range w.errs {
					if !hasPC(projection(out.Errors), pc) {
						t.Fatalf("outcome errors = %+v, want %+v", projection(out.Errors), pc)
					}
				}
			}

			// Attempt 1 failure => exactly one non-final invalid, after every attempt-1 delta and
			// before every attempt-2 delta.
			if w.attempts == 2 {
				if len(rec.invalids) != 1 {
					t.Fatalf("invalid events = %d, want exactly 1 (end of attempt 1)", len(rec.invalids))
				}
				inv := rec.invalids[0]
				if inv.Attempt != 1 || inv.Final || inv.ContentVersionID != nil || len(inv.Errors) == 0 {
					t.Fatalf("non-final invalid = %+v, want attempt 1, final false, null version id, errors", inv)
				}
				for _, pc := range w.repairErrs {
					if !hasPC(projection(inv.Errors), pc) {
						t.Fatalf("non-final invalid errors = %+v, want %+v", projection(inv.Errors), pc)
					}
				}
				seenInvalid := false
				for _, n := range rec.order {
					switch n {
					case "invalid":
						seenInvalid = true
					case "delta:1":
						if seenInvalid {
							t.Fatalf("attempt-1 delta after the invalid event: %v", rec.order)
						}
					case "delta:2":
						if !seenInvalid {
							t.Fatalf("attempt-2 delta before the invalid event: %v", rec.order)
						}
					}
				}
			} else if len(rec.invalids) != 0 {
				t.Fatalf("invalid events = %+v, want none for a first-attempt success", rec.invalids)
			}
			if w.attempt1 != nil {
				assertAttemptText(t, rec, 1, *w.attempt1)
			}
			if w.attempt2 != nil {
				assertAttemptText(t, rec, 2, *w.attempt2)
			}
		})
	}
}

// REQ-AI-01, REQ-CNT-04
// The mock streams each field in chunks of at most 20 runes, in field order (llm-tools.md §3.4).
func TestContentSession_MockChunking(t *testing.T) {
	t.Parallel()
	_, rec, err := runSession(t, context.Background(), llm.NewMock(llm.MockOptions{}), contentRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rec.deltas) == 0 {
		t.Fatal("no delta events")
	}
	rank := map[string]int{"sms/body": 0, "email/subject": 1, "email/body": 2}
	last := 0
	for _, d := range rec.deltas {
		if n := len([]rune(d.Text)); n > 20 {
			t.Fatalf("delta text has %d runes, want <= 20: %q", n, d.Text)
		}
		r := rank[d.Channel+"/"+d.Field]
		if r < last {
			t.Fatalf("delta for %s/%s after a later field", d.Channel, d.Field)
		}
		last = r
	}
}

// REQ-CNT-01, REQ-CNT-05 (D37)
// Validation applies the length rule AFTER substitution with req.Longest: CNT_OK (160 with a
// 50-char first name) is invalid with a 51-char first name, on both attempts.
func TestContentSession_LongestDrivesTheLengthRule(t *testing.T) {
	t.Parallel()
	req := contentRequest()
	req.Longest = content.Longest{FirstName: 51, LastName: 8, City: 8}
	out, _, err := runSession(t, context.Background(), llm.NewMock(llm.MockOptions{}), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Valid || !out.Storable || out.Attempts != 2 {
		t.Fatalf("outcome Valid/Storable/Attempts = %v/%v/%d, want false/true/2", out.Valid, out.Storable, out.Attempts)
	}
	want := []validation.FieldError{}
	for _, f := range out.Errors {
		if f.Path == "sms.body" && f.Code == "sms_too_long" {
			want = append(want, f)
		}
	}
	if len(want) != 1 || want[0].Actual == nil || *want[0].Actual != 161 || want[0].Limit == nil || *want[0].Limit != 160 {
		t.Fatalf("outcome errors = %+v, want sms.body sms_too_long limit 160 actual 161", out.Errors)
	}
}

// REQ-APR-02, REQ-AI-01 (D46)
// The composer's verdict equals content.Parse + content.Validate(c, req.Longest) on the same raw
// output — the single validator shared with human edits.
func TestContentSession_SameValidatorAsEdits(t *testing.T) {
	t.Parallel()
	raws := map[string]string{
		"over limit sms":   mustJSON(t, cContent(cSMS161, cOKSubject, cOKBody)),
		"unknown variable": mustJSON(t, cContent(cUnknownSMS, cOKSubject, cOKBody)),
		"empty subject":    mustJSON(t, cContent(cOKSMS, "", cOKBody)),
		"malformed body":   mustJSON(t, cContent(cOKSMS, cOKSubject, "Merhaba {{first_name")),
	}
	for name, raw := range raws {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, err := content.Parse([]byte(raw))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			verr := content.Validate(c, cLongest)
			var ve *validation.Error
			if !errors.As(verr, &ve) {
				t.Fatalf("Validate() error = %v, want *validation.Error", verr)
			}
			sc := &scriptedContentClient{steps: []contentStep{{raw: raw}, {raw: raw}}}
			out, _, err := runSession(t, context.Background(), sc, contentRequest())
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if out.Valid {
				t.Fatalf("outcome is valid, Validate says %v", verr)
			}
			if diff := cmp.Diff(projection(ve.Fields), projection(out.Errors)); diff != "" {
				t.Fatalf("composer errors differ from content.Validate (-validate +composer):\n%s", diff)
			}
		})
	}
}

// REQ-AI-01, REQ-CNT-04 (llm-tools.md §3.5)
// Outcomes without a mock scenario, through a scripted llm.Client: storable drafts (empty value,
// malformed variable, email body over 5000), a missing key that is not storable, and no_tool_call
// triggering the single repair.
func TestContentSession_ScriptedOutputs(t *testing.T) {
	t.Parallel()
	missingBody := `{"sms":{"body":"Merhaba"},"email":{"subject":"Konu"}}`
	longBody := cContent(cOKSMS, cOKSubject, strings.Repeat("ü", 5001))
	tests := []struct {
		name      string
		steps     []contentStep
		valid     bool
		storable  bool
		attempts  int
		errs      []pathCode
		noWorst   bool
		wantCalls int
	}{
		{name: "empty sms body is a storable required draft", steps: []contentStep{{raw: mustJSON(t, cContent("", cOKSubject, cOKBody))}, {raw: mustJSON(t, cContent("", cOKSubject, cOKBody))}},
			storable: true, attempts: 2, errs: []pathCode{{"sms.body", "required"}}, wantCalls: 2},
		{name: "malformed variable is a storable draft without worst case", steps: []contentStep{{raw: mustJSON(t, cContent("{{first_name", cOKSubject, cOKBody))}, {raw: mustJSON(t, cContent("{{first_name", cOKSubject, cOKBody))}},
			storable: true, attempts: 2, errs: []pathCode{{"sms.body", "malformed_variable"}}, noWorst: true, wantCalls: 2},
		{name: "email body over 5000 is a storable draft", steps: []contentStep{{raw: mustJSON(t, longBody)}, {raw: mustJSON(t, longBody)}},
			storable: true, attempts: 2, errs: []pathCode{{"email.body", "email_body_too_long"}}, wantCalls: 2},
		{name: "a missing key is a schema violation and not storable", steps: []contentStep{{raw: missingBody}, {raw: missingBody}},
			attempts: 2, errs: []pathCode{{"email.body", "schema_violation"}}, wantCalls: 2},
		{name: "no tool call twice is invalid output", steps: []contentStep{{endErr: llm.ErrNoToolCall}, {endErr: llm.ErrNoToolCall}},
			attempts: 2, errs: []pathCode{{"", "no_tool_call"}}, wantCalls: 2},
		{name: "no tool call then valid content succeeds on attempt 2", steps: []contentStep{{endErr: llm.ErrNoToolCall}, {raw: mustJSON(t, cOK())}},
			valid: true, attempts: 2, wantCalls: 2},
		{name: "truncated then valid content succeeds on attempt 2", steps: []contentStep{{chunks: []llm.ContentChunk{{Path: "sms.body", Text: "Merhaba"}}, endErr: llm.ErrTruncated}, {raw: mustJSON(t, cOK())}},
			valid: true, attempts: 2, wantCalls: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sc := &scriptedContentClient{steps: tc.steps}
			out, rec, err := runSession(t, context.Background(), sc, contentRequest())
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			assertNoTerminal(t, rec)
			if out.Valid != tc.valid || out.Storable != tc.storable || out.Attempts != tc.attempts {
				t.Fatalf("outcome Valid/Storable/Attempts = %v/%v/%d, want %v/%v/%d (errors %+v)", out.Valid, out.Storable, out.Attempts, tc.valid, tc.storable, tc.attempts, out.Errors)
			}
			for _, pc := range tc.errs {
				if !containsPathCode(projection(out.Errors), pc) {
					t.Fatalf("outcome errors = %+v, want %+v", projection(out.Errors), pc)
				}
			}
			if tc.noWorst && out.WorstCase != nil {
				t.Fatalf("WorstCase = %+v, want nil", *out.WorstCase)
			}
			if n := len(sc.requests()); n != tc.wantCalls {
				t.Fatalf("StreamContent calls = %d, want %d", n, tc.wantCalls)
			}
		})
	}
}

// REQ-AI-01
// The repair attempt carries the brief, the rule, the same Longest, the previous raw output and
// the field errors; attempt 1 has no previous output (llm-tools.md §2 step 4).
func TestContentSession_RepairRequestCarriesFeedback(t *testing.T) {
	t.Parallel()
	raw1 := mustJSON(t, cContent(cSMS161, cOKSubject, cOKBody))
	sc := &scriptedContentClient{steps: []contentStep{{raw: raw1}, {raw: mustJSON(t, cOK())}}}
	out, _, err := runSession(t, context.Background(), sc, contentRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !out.Valid || out.Attempts != 2 {
		t.Fatalf("outcome Valid/Attempts = %v/%d, want true/2", out.Valid, out.Attempts)
	}
	reqs := sc.requests()
	if len(reqs) != 2 {
		t.Fatalf("StreamContent calls = %d, want 2", len(reqs))
	}
	first, second := reqs[0], reqs[1]
	if first.Attempt != 1 || first.Previous != nil {
		t.Fatalf("attempt 1 request = {Attempt:%d Previous:%v}, want {1 nil}", first.Attempt, first.Previous)
	}
	if second.Attempt != 2 || second.Previous == nil {
		t.Fatalf("attempt 2 request = {Attempt:%d Previous:%v}, want {2 non-nil}", second.Attempt, second.Previous)
	}
	for i, r := range reqs {
		if r.Brief != exampleBrief || r.Longest != cLongest {
			t.Fatalf("request %d Brief/Longest = %q/%+v, want the session's", i+1, r.Brief, r.Longest)
		}
	}
	assertRawOutput(t, second.Previous.Output, raw1)
	if !containsPathCode(projection(second.Previous.Errors), pathCode{"sms.body", "sms_too_long"}) {
		t.Fatalf("repair feedback = %+v, want sms.body sms_too_long", projection(second.Previous.Errors))
	}
}

// REQ-CNT-04, REQ-AI-01
// LLM access lost AFTER the stream opened (attempt 1 or 2) never triggers a repair: the outcome is
// !Valid, !Storable with the single error {"" llm_unavailable}.
func TestContentSession_UnavailableAfterOpen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		steps     []contentStep
		wantCalls int
	}{
		{name: "mid attempt 1", steps: []contentStep{{chunks: []llm.ContentChunk{{Path: "sms.body", Text: "Merhaba {{first_"}}, endErr: llm.ErrUnavailable}, {raw: mustJSON(t, cOK())}}, wantCalls: 1},
		{name: "at the start of the repair attempt", steps: []contentStep{{raw: mustJSON(t, cContent(cSMS161, cOKSubject, cOKBody))}, {startErr: llm.ErrUnavailable}}, wantCalls: 2},
		{name: "mid repair attempt", steps: []contentStep{{raw: `{"sms":`}, {chunks: []llm.ContentChunk{{Path: "sms.body", Text: "Mer"}}, endErr: llm.ErrUnavailable}}, wantCalls: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sc := &scriptedContentClient{steps: tc.steps}
			out, rec, err := runSession(t, context.Background(), sc, contentRequest())
			if err != nil {
				t.Fatalf("Run() error = %v, want nil (the outcome reports llm_unavailable)", err)
			}
			assertNoTerminal(t, rec)
			if out.Valid || out.Storable {
				t.Fatalf("outcome Valid/Storable = %v/%v, want false/false", out.Valid, out.Storable)
			}
			if diff := cmp.Diff([]pathCode{{"", "llm_unavailable"}}, projection(out.Errors)); diff != "" {
				t.Fatalf("outcome errors mismatch (-want +got):\n%s", diff)
			}
			if n := len(sc.requests()); n != tc.wantCalls {
				t.Fatalf("StreamContent calls = %d, want %d (no repair after ErrUnavailable)", n, tc.wantCalls)
			}
		})
	}
}

// REQ-CNT-04, REQ-AI-01
// An LLM that is unreachable before the stream opens is OpenContent's error (=> HTTP 502).
func TestOpenContent_UnavailableAtStart(t *testing.T) {
	t.Parallel()
	ctx := llm.WithScenario(context.Background(), llm.ScenarioLLMUnavailable)
	_, err := composer.OpenContent(ctx, llm.NewMock(llm.MockOptions{}), contentRequest())
	if !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("OpenContent() error = %v, want %v", err, llm.ErrUnavailable)
	}
	sc := &scriptedContentClient{steps: []contentStep{{startErr: llm.ErrUnavailable}}}
	if _, err := composer.OpenContent(context.Background(), sc, contentRequest()); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("OpenContent(scripted) error = %v, want %v", err, llm.ErrUnavailable)
	}
}

// REQ-CNT-04
// Run returns the error returned by emit, and a context error when the stream is cancelled
// (stalled_stream: first delta, then waits for ctx).
func TestContentSession_EmitErrorAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("emit error is returned", func(t *testing.T) {
		t.Parallel()
		errStop := errors.New("client went away")
		s, err := composer.OpenContent(context.Background(), llm.NewMock(llm.MockOptions{}), contentRequest())
		if err != nil {
			t.Fatalf("OpenContent() error = %v", err)
		}
		_, err = s.Run(func(composer.Event) error { return errStop })
		if !errors.Is(err, errStop) {
			t.Fatalf("Run() error = %v, want %v", err, errStop)
		}
	})
	t.Run("cancellation during a stalled stream", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(llm.WithScenario(context.Background(), llm.ScenarioStalledStream))
		defer cancel()
		s, err := composer.OpenContent(ctx, llm.NewMock(llm.MockOptions{}), contentRequest())
		if err != nil {
			t.Fatalf("OpenContent() error = %v", err)
		}
		firstDelta := make(chan struct{})
		var once sync.Once
		done := make(chan error, 1)
		go func() {
			_, err := s.Run(func(ev composer.Event) error {
				if ev.Name() == "delta" {
					once.Do(func() { close(firstDelta) })
				}
				return nil
			})
			done <- err
		}()
		select {
		case <-firstDelta:
		case <-time.After(10 * time.Second):
			t.Fatal("no delta from the stalled stream within 10s")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v, want context.Canceled", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return within 10s after cancellation")
		}
	})
}

// REQ-CNT-04
// Event names are the SSE event names (sse-events.md §1.1).
func TestEvent_Names(t *testing.T) {
	t.Parallel()
	got := []string{composer.DeltaEvent{}.Name(), composer.InvalidEvent{}.Name(), composer.ValidatedEvent{}.Name()}
	if diff := cmp.Diff([]string{"delta", "invalid", "validated"}, got); diff != "" {
		t.Fatalf("event names mismatch (-want +got):\n%s", diff)
	}
}

// REQ-CNT-04
// The JSON of the three events has exactly the sse-events.md §1.1 keys; content_version_id is
// present (null) on a non-final invalid.
func TestEvent_JSONShape(t *testing.T) {
	t.Parallel()
	id := "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11"
	tests := []struct {
		name string
		ev   any
		want string
	}{
		{name: "delta", ev: composer.DeltaEvent{Attempt: 1, Channel: "sms", Field: "body", Text: "Merhaba {{first_"},
			want: `{"attempt":1,"channel":"sms","field":"body","text":"Merhaba {{first_"}`},
		{name: "non-final invalid", ev: composer.InvalidEvent{Attempt: 1, Final: false, Errors: []validation.FieldError{{Path: "", Code: "invalid_json", Message: "m"}}},
			want: `{"attempt":1,"final":false,"errors":[{"path":"","code":"invalid_json","message":"m"}],"content_version_id":null}`},
		{name: "final invalid with a stored draft", ev: composer.InvalidEvent{Attempt: 2, Final: true, Errors: []validation.FieldError{{Path: "sms.body", Code: "sms_too_long", Message: "m", Limit: ip(160), Actual: ip(161)}}, ContentVersionID: &id},
			want: `{"attempt":2,"final":true,"errors":[{"path":"sms.body","code":"sms_too_long","message":"m","limit":160,"actual":161}],"content_version_id":"` + id + `"}`},
		{name: "validated", ev: composer.ValidatedEvent{Attempt: 1, ContentVersionID: id, VersionNumber: 3, Content: cContent("a", "b", "c"), WorstCase: content.WorstCase{SMSRunes: 1, SubjectRunes: 1}},
			want: `{"attempt":1,"content_version_id":"` + id + `","version_number":3,"content":{"sms":{"body":"a"},"email":{"subject":"b","body":"c"}},"worst_case":{"sms_runes":1,"subject_runes":1}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tc.ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got, want any
			_ = json.Unmarshal(b, &got)
			_ = json.Unmarshal([]byte(tc.want), &want)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("JSON mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ---- language (P8) ----

// bilingualContent is CNT_OK plus an English version with the given SMS.
func bilingualContent(enSMS string) content.Content {
	c := cOK()
	c.EN = &content.English{SMS: content.SMS{Body: enSMS}, Email: content.Email{Subject: "Your cart is waiting", Body: "Hi {{first_name}},\n\nYour cart is waiting."}}
	return c
}

// deltaJSON is the wire form of one delta event (sse-events keys plus the optional language).
type deltaJSON struct {
	Attempt  int     `json:"attempt"`
	Channel  string  `json:"channel"`
	Field    string  `json:"field"`
	Text     string  `json:"text"`
	Language *string `json:"language"`
}

// REQ-LNG-02 C4, C5, C7
// With IncludeEnglish the mock returns CNT_OK_EN and the session streams the English fields too:
// Turkish deltas first (no `language` key), then English deltas with `language: "en"`; the English
// deltas concatenate to the outcome's English version, which is validated and measured (WorstCase.EN).
// Without IncludeEnglish no delta carries a language and the outcome has no English version.
func TestContentSession_EnglishDeltasFollowTurkish(t *testing.T) {
	t.Parallel()
	for _, includeEnglish := range []bool{true, false} {
		t.Run("IncludeEnglish="+strconv.FormatBool(includeEnglish), func(t *testing.T) {
			t.Parallel()
			req := contentRequest()
			req.IncludeEnglish = includeEnglish
			out, rec, err := runSession(t, context.Background(), llm.NewMock(llm.MockOptions{}), req)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !out.Valid || out.Attempts != 1 {
				t.Fatalf("outcome Valid/Attempts = %v/%d, want true/1 (errors %+v)", out.Valid, out.Attempts, out.Errors)
			}
			en := map[string]string{}
			seenEnglish := false
			for i, d := range rec.deltas {
				b, err := json.Marshal(d)
				if err != nil {
					t.Fatalf("marshal delta: %v", err)
				}
				var w deltaJSON
				if err := json.Unmarshal(b, &w); err != nil {
					t.Fatalf("decode delta %s: %v", b, err)
				}
				if w.Language == nil {
					if seenEnglish {
						t.Fatalf("delta %d is Turkish after an English delta: Turkish fields stream first", i)
					}
					continue
				}
				if *w.Language != "en" {
					t.Fatalf("delta %d language = %q, want en or no key", i, *w.Language)
				}
				seenEnglish = true
				en[w.Channel+"."+w.Field] += w.Text
			}
			if !includeEnglish {
				if seenEnglish || out.Content.EN != nil || (out.WorstCase != nil && out.WorstCase.EN != nil) {
					t.Fatalf("Turkish-only generation produced English (deltas %v, EN %+v)", seenEnglish, out.Content.EN)
				}
				if diff := cmp.Diff(cOK(), out.Content); diff != "" {
					t.Fatalf("Turkish-only outcome differs from CNT_OK (-want +got):\n%s", diff)
				}
				return
			}
			if !seenEnglish || out.Content.EN == nil {
				t.Fatalf("bilingual generation: English deltas %v, outcome EN %+v; want both", seenEnglish, out.Content.EN)
			}
			if out.Content.SMS != cOK().SMS || out.Content.Email != cOK().Email {
				t.Fatalf("Turkish part of CNT_OK_EN = %+v/%+v, want CNT_OK", out.Content.SMS, out.Content.Email)
			}
			want := map[string]string{"sms.body": out.Content.EN.SMS.Body, "email.subject": out.Content.EN.Email.Subject, "email.body": out.Content.EN.Email.Body}
			if diff := cmp.Diff(want, en); diff != "" {
				t.Fatalf("English delta concatenation differs from the outcome's EN (-want +got):\n%s", diff)
			}
			if out.WorstCase == nil || out.WorstCase.EN == nil {
				t.Fatalf("outcome WorstCase = %+v, want an EN measurement", out.WorstCase)
			}
		})
	}
}

// REQ-LNG-02 C1, C5, C7, C8
// An error in the English version triggers the single repair like a Turkish one: the non-final
// invalid carries the en-prefixed path, attempt 2 regenerates both versions (same IncludeEnglish:
// the decision is not changed by the repair), at most 2 attempts.
func TestContentSession_EnglishErrorTriggersRepair(t *testing.T) {
	t.Parallel()
	bad := bilingualContent(strings.Repeat("a", 161))
	good := bilingualContent("Hi {{first_name}}, your cart is waiting.")
	tests := []struct {
		name     string
		steps    []contentStep
		valid    bool
		storable bool
		content  content.Content
	}{
		{name: "English SMS 161 then valid bilingual content", steps: []contentStep{{raw: mustJSON(t, bad)}, {raw: mustJSON(t, good)}}, valid: true, content: good},
		{name: "English SMS 161 twice is a storable draft", steps: []contentStep{{raw: mustJSON(t, bad)}, {raw: mustJSON(t, bad)}}, storable: true, content: bad},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sc := &scriptedContentClient{steps: tc.steps}
			req := contentRequest()
			req.IncludeEnglish = true
			out, rec, err := runSession(t, context.Background(), sc, req)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			assertNoTerminal(t, rec)
			if out.Valid != tc.valid || out.Storable != tc.storable || out.Attempts != 2 {
				t.Fatalf("outcome Valid/Storable/Attempts = %v/%v/%d, want %v/%v/2 (errors %+v)", out.Valid, out.Storable, out.Attempts, tc.valid, tc.storable, out.Errors)
			}
			if diff := cmp.Diff(tc.content, out.Content); diff != "" {
				t.Fatalf("outcome Content mismatch (-want +got):\n%s", diff)
			}
			if !tc.valid && !containsPathCode(projection(out.Errors), pathCode{"en.sms.body", "sms_too_long"}) {
				t.Fatalf("outcome errors = %+v, want en.sms.body sms_too_long", projection(out.Errors))
			}
			if len(rec.invalids) != 1 || !containsPathCode(projection(rec.invalids[0].Errors), pathCode{"en.sms.body", "sms_too_long"}) {
				t.Fatalf("non-final invalid events = %+v, want one with en.sms.body sms_too_long", rec.invalids)
			}
			reqs := sc.requests()
			if len(reqs) != 2 {
				t.Fatalf("StreamContent calls = %d, want 2 (at most one repair)", len(reqs))
			}
			for i, r := range reqs {
				if !r.IncludeEnglish {
					t.Fatalf("request %d IncludeEnglish = false; the decision is made once and kept for the repair", i+1)
				}
			}
			if reqs[1].Previous == nil || !containsPathCode(projection(reqs[1].Previous.Errors), pathCode{"en.sms.body", "sms_too_long"}) {
				t.Fatalf("repair feedback = %+v, want en.sms.body sms_too_long", reqs[1].Previous)
			}
		})
	}
}

func ip(n int) *int { return &n }

// ---- scripted content client ----

// contentStep scripts one StreamContent call: startErr fails the call; otherwise the stream yields
// chunks (or, when chunks is nil and raw is set, one chunk per field of raw if it decodes), then
// endErr (nil => io.EOF with Raw() == raw).
type contentStep struct {
	startErr error
	chunks   []llm.ContentChunk
	raw      string
	endErr   error
}

type scriptedContentClient struct {
	steps []contentStep
	mu    sync.Mutex
	reqs  []llm.ContentRequest
}

func (s *scriptedContentClient) GenerateSegment(context.Context, llm.SegmentRequest) (json.RawMessage, error) {
	return json.RawMessage(segOK), nil
}

func (s *scriptedContentClient) StreamContent(ctx context.Context, req llm.ContentRequest) (llm.ContentStream, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	n := len(s.reqs)
	s.mu.Unlock()
	if n > len(s.steps) {
		return nil, errors.New("scriptedContentClient: more calls than scripted")
	}
	step := s.steps[n-1]
	if step.startErr != nil {
		return nil, step.startErr
	}
	chunks := step.chunks
	if chunks == nil && step.raw != "" {
		var c content.Content
		if json.Unmarshal([]byte(step.raw), &c) == nil {
			parts := []llm.ContentChunk{{Path: "sms.body", Text: c.SMS.Body}, {Path: "email.subject", Text: c.Email.Subject}, {Path: "email.body", Text: c.Email.Body}}
			if c.EN != nil { // REQ-LNG-02 C5: English chunks use the en.* paths, after the Turkish ones
				parts = append(parts, llm.ContentChunk{Path: "en.sms.body", Text: c.EN.SMS.Body}, llm.ContentChunk{Path: "en.email.subject", Text: c.EN.Email.Subject}, llm.ContentChunk{Path: "en.email.body", Text: c.EN.Email.Body})
			}
			for _, p := range parts {
				if p.Text != "" {
					chunks = append(chunks, p)
				}
			}
		}
	}
	return &scriptedStream{ctx: ctx, chunks: chunks, raw: step.raw, endErr: step.endErr}, nil
}

func (s *scriptedContentClient) requests() []llm.ContentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.ContentRequest(nil), s.reqs...)
}

type scriptedStream struct {
	ctx    context.Context
	chunks []llm.ContentChunk
	i      int
	raw    string
	endErr error
}

func (s *scriptedStream) Recv() (llm.ContentChunk, error) {
	if err := s.ctx.Err(); err != nil {
		return llm.ContentChunk{}, err
	}
	if s.i < len(s.chunks) {
		c := s.chunks[s.i]
		s.i++
		return c, nil
	}
	if s.endErr != nil {
		return llm.ContentChunk{}, s.endErr
	}
	return llm.ContentChunk{}, io.EOF
}

func (s *scriptedStream) Raw() json.RawMessage { return json.RawMessage(s.raw) }
func (s *scriptedStream) Close() error         { return nil }

var (
	_ llm.Client        = (*scriptedContentClient)(nil)
	_ llm.ContentStream = (*scriptedStream)(nil)
)

func mustJSON(t *testing.T, c content.Content) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
