package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
)

// English versions used by the language tests (REQ-LNG-01 C1 shape: en.sms.body, en.email.subject,
// en.email.body). enLongSMS renders to 155 + len(first_name) runes: "Mehmet" makes it 161, while
// the Turkish smsTemplate renders to 156 for the same name.
const (
	enShortSMS = "Hi {{first_name}}, your cart is waiting."
	enSubject  = "{{first_name}}, your cart is waiting"
	enBody     = "Hi {{first_name}},\n\nThe items in your cart are waiting for you."
)

var enLongSMS = "Hi {{first_name}}, " + strings.Repeat("b", 150)

// bilingual is template() plus an English version whose SMS is enSMS.
func bilingual(enSMS string) content.Content {
	c := template()
	c.EN = &content.English{SMS: content.SMS{Body: enSMS}, Email: content.Email{Subject: enSubject, Body: enBody}}
	return c
}

func fill(tmpl, firstName string) string {
	return strings.ReplaceAll(tmpl, "{{first_name}}", firstName)
}

// REQ-LNG-03 C1, C2, C3, C4
// The dispatcher sends every recipient the text of its own language: an `en` recipient gets the
// English SMS / subject / text of an approved version that has `en`, with `<html lang="en">`; a
// `tr` or unset-language recipient, and an `en` recipient of a version without `en`, get the Turkish
// main text with `<html lang="tr">` and the delivery is still sent (fallback, not a rejection). The
// first-name fallback follows the language ("Dear customer" / "Değerli müşterimiz"). The send-time
// length check runs on the SELECTED text: an English SMS over 160 fails with content_too_long without
// a provider call, while the same name in Turkish (156 runes) is sent.
func TestDispatcher_SendsTheRecipientsLanguage(t *testing.T) {
	t.Parallel()
	type want struct {
		failed   bool   // content_too_long, no provider request
		body     string // SMS body
		subject  string // email subject
		text     string // email text
		htmlLang string // `<html lang="…">` of the email html
	}
	tests := []struct {
		name      string
		email     bool
		lang      string
		firstName string
		c         content.Content
		want      want
	}{
		{name: "en recipient gets the English SMS", lang: "en", firstName: "Ayşe", c: bilingual(enShortSMS), want: want{body: fill(enShortSMS, "Ayşe")}},
		{name: "tr recipient gets the Turkish SMS", lang: "tr", firstName: "Ayşe", c: bilingual(enShortSMS), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "unset language is Turkish", lang: "", firstName: "Ayşe", c: bilingual(enShortSMS), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "en recipient of a version without en falls back to Turkish and is sent", lang: "en", firstName: "Ayşe", c: template(), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "en recipient with an empty first name gets Dear customer", lang: "en", firstName: "", c: bilingual(enShortSMS), want: want{body: fill(enShortSMS, content.FirstNameFallbackEN)}},
		{name: "English SMS over 160 after rendering fails content_too_long", lang: "en", firstName: "Mehmet", c: bilingual(enLongSMS), want: want{failed: true}},
		{name: "the same name in Turkish fits and is sent", lang: "tr", firstName: "Mehmet", c: bilingual(enLongSMS), want: want{body: fill(smsTemplate, "Mehmet")}},
		{name: "en recipient gets the English email with lang en", email: true, lang: "en", firstName: "Ayşe", c: bilingual(enShortSMS),
			want: want{subject: fill(enSubject, "Ayşe"), text: fill(enBody, "Ayşe"), htmlLang: "en"}},
		{name: "tr recipient gets the Turkish email with lang tr", email: true, lang: "tr", firstName: "Ayşe", c: bilingual(enShortSMS),
			want: want{subject: fill(subjectTemplate, "Ayşe"), text: fill(template().Email.Body, "Ayşe"), htmlLang: "tr"}},
		// A short name: the send-time check covers the SMS and the subject together (REQ-CNT-05), and
		// the Turkish SMS template with the 18-rune fallback would exceed 160.
		{name: "en recipient of a Turkish-only email gets Turkish with lang tr", email: true, lang: "en", firstName: "Ayşe", c: template(),
			want: want{subject: fill(subjectTemplate, "Ayşe"), text: fill(template().Email.Body, "Ayşe"), htmlLang: "tr"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := clock.NewFake(t0)
			cfg := baseConfig()
			to := smsTo(1)
			if tc.email {
				cfg.Channel = delivery.ChannelEmail
				to = emailTo(1)
			}
			st := newStore(t)
			j := newJob(t, cfg, jobSpec{n: 1, consent: true, to: to, firstName: tc.firstName})
			j.Recipient.Language = tc.lang
			j.Content = tc.c
			st.add(j, t0)
			p, srv := newProvider(t)
			lim := &countingLimiter{}
			d := newDispatcher(t, cfg, st, srv, lim, fake, nil, 10*time.Second)

			if n, err := d.ProcessOnce(ctxT(t)); err != nil || n != 1 {
				t.Fatalf("ProcessOnce() = (%d, %v), want (1, nil)", n, err)
			}
			sent, retries, failed := st.outcomes(j.ID)
			reqs := p.requests()
			if tc.want.failed {
				if len(failed) != 1 || len(sent) != 0 || len(retries) != 0 {
					t.Fatalf("outcomes sent=%d retry=%d failed=%d, want one MarkFailed", len(sent), len(retries), len(failed))
				}
				if u := failed[0]; u.Reason != delivery.FailureContentTooLong || u.LastError != "content_too_long" {
					t.Fatalf("FailedUpdate = %+v, want content_too_long", u)
				}
				if len(reqs) != 0 || lim.count() != 0 {
					t.Fatalf("provider requests/permits = %d/%d, want 0/0", len(reqs), lim.count())
				}
				return
			}
			if len(sent) != 1 || len(failed) != 0 {
				t.Fatalf("outcomes sent=%d failed=%d, want one MarkSent (a language fallback is never a rejection)", len(sent), len(failed))
			}
			if len(reqs) != 1 {
				t.Fatalf("provider requests = %d, want 1", len(reqs))
			}
			r := reqs[0]
			if !tc.email {
				if r.body != tc.want.body {
					t.Fatalf("SMS body = %q, want %q", r.body, tc.want.body)
				}
				return
			}
			if r.subject != tc.want.subject || r.text != tc.want.text {
				t.Fatalf("email subject/text = %q/%q, want %q/%q", r.subject, r.text, tc.want.subject, tc.want.text)
			}
			if wantTag := `<html lang="` + tc.want.htmlLang + `">`; !strings.Contains(r.html, wantTag) {
				t.Fatalf("email html lacks %s: %s", wantTag, r.html)
			}
		})
	}
}
