package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
)

// Variant B texts used by the A/B tests (REQ-ABT-01 C1 shape: b.sms.body, b.email.subject,
// b.email.body, b.en.*). bLongSMS renders to 155 + len(first_name) runes: "Mehmet" makes it 161, while
// the variant A smsTemplate renders to 156 for the same name.
const (
	bSMS       = "Son gün {{first_name}}! %30 indirim bugün bitiyor."
	bSubject   = "{{first_name}}, son gün: %30 indirim"
	bBody      = "Merhaba {{first_name}},\n\nİndirim bugün gece yarısı bitiyor."
	bENSMS     = "Last day {{first_name}}! 30% off ends today."
	bENSubject = "{{first_name}}, last day: 30% off"
	bENBody    = "Hi {{first_name}},\n\nThe discount ends at midnight."
)

var bLongSMS = "Son gün {{first_name}}, " + strings.Repeat("b", 145)

// withB is template() plus a variant B whose SMS is sms, and both angles.
func withB(sms string) content.Content {
	c := template()
	c.B = &content.Variant{SMS: content.SMS{Body: sms}, Email: content.Email{Subject: bSubject, Body: bBody}}
	c.Angles = &content.Angles{A: "İndirim oranı", B: "Aciliyet"}
	return c
}

// withBEN is withB(bSMS) with an English version of A (en) and of B (b.en).
func withBEN() content.Content {
	c := withB(bSMS)
	c.EN = &content.English{SMS: content.SMS{Body: enShortSMS}, Email: content.Email{Subject: enSubject, Body: enBody}}
	c.B.EN = &content.English{SMS: content.SMS{Body: bENSMS}, Email: content.Email{Subject: bENSubject, Body: bENBody}}
	return c
}

// REQ-ABT-03 C8
// The dispatcher reads the delivery's variant (Job.Recipient.Variant) and picks the text by variant
// first, then by the recipient's language: a b recipient of a version with B gets B's SMS / subject /
// text; an a, unset or b recipient of a version without B gets A; an English b recipient gets b.en, and
// without b.en the Turkish text of B (still sent). The send-time length check runs on the SELECTED
// text: a B SMS over 160 fails content_too_long without a provider call, while the same name with
// variant A (156 runes) is sent.
func TestDispatcher_SendsTheRecipientsVariant(t *testing.T) {
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
		variant   string
		lang      string
		firstName string
		c         content.Content
		want      want
	}{
		{name: "b recipient gets the B SMS", variant: "b", firstName: "Ayşe", c: withB(bSMS), want: want{body: fill(bSMS, "Ayşe")}},
		{name: "a recipient gets the A SMS", variant: "a", firstName: "Ayşe", c: withB(bSMS), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "unset variant is A", variant: "", firstName: "Ayşe", c: withB(bSMS), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "b recipient of a version without b gets A", variant: "b", firstName: "Ayşe", c: template(), want: want{body: fill(smsTemplate, "Ayşe")}},
		{name: "English b recipient gets the English B SMS", variant: "b", lang: "en", firstName: "Ayşe", c: withBEN(), want: want{body: fill(bENSMS, "Ayşe")}},
		{name: "English a recipient of a bilingual A/B version gets the English A SMS", variant: "a", lang: "en", firstName: "Ayşe", c: withBEN(), want: want{body: fill(enShortSMS, "Ayşe")}},
		{name: "English b recipient without b.en gets the Turkish B SMS and is sent", variant: "b", lang: "en", firstName: "Ayşe", c: withB(bSMS), want: want{body: fill(bSMS, "Ayşe")}},
		{name: "English b recipient with an empty first name gets Dear customer", variant: "b", lang: "en", firstName: "", c: withBEN(), want: want{body: fill(bENSMS, content.FirstNameFallbackEN)}},
		{name: "B SMS over 160 after rendering fails content_too_long", variant: "b", firstName: "Mehmet", c: withB(bLongSMS), want: want{failed: true}},
		{name: "the same name with variant A fits and is sent", variant: "a", firstName: "Mehmet", c: withB(bLongSMS), want: want{body: fill(smsTemplate, "Mehmet")}},
		{name: "b recipient gets the B email with lang tr", email: true, variant: "b", firstName: "Ayşe", c: withB(bSMS),
			want: want{subject: fill(bSubject, "Ayşe"), text: fill(bBody, "Ayşe"), htmlLang: "tr"}},
		{name: "English b recipient gets the English B email with lang en", email: true, variant: "b", lang: "en", firstName: "Ayşe", c: withBEN(),
			want: want{subject: fill(bENSubject, "Ayşe"), text: fill(bENBody, "Ayşe"), htmlLang: "en"}},
		{name: "a recipient gets the A email", email: true, variant: "a", firstName: "Ayşe", c: withB(bSMS),
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
			j.Recipient.Variant = tc.variant
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
				t.Fatalf("outcomes sent=%d failed=%d, want one MarkSent", len(sent), len(failed))
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
