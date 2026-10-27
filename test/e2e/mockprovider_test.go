package e2e_test

// The real mock provider of compose.test.yml (E2E_MOCKPROVIDER_URL), driven directly over HTTP
// (REQ-MOCK-01, REQ-MOCK-02, REQ-SND-05). The admin endpoints are on (MOCK_PROVIDER_ADMIN=true) and
// random 429s are off (MOCK_PROVIDER_RATE_LIMIT_RATE=0). Random 500/503 (MOCK_PROVIDER_ERROR_RATE)
// may be on; such answers are never stored, so the helpers repeat a request until the answer is
// not a random 5xx wherever the expected answer is deterministic. Lost responses are assumed off
// (MOCK_PROVIDER_LOST_RESPONSE_RATE default 0) outside scripts.
//
// Every test resets the mock provider and changes its global state, so none of them calls
// t.Parallel (see delivery_test.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// mpClient talks to the mock provider, which is not part of openapi.yaml. Its transport is the
// testenv network guard installed on http.DefaultTransport.
var mpClient = &http.Client{Timeout: 20 * time.Second}

var msgIDRE = regexp.MustCompile(`^msg_[0-9a-f]{32}$`)

type mpMessage struct {
	ID             string `json:"id"`
	Channel        string `json:"channel"`
	To             string `json:"to"`
	IdempotencyKey string `json:"idempotency_key"`
	AcceptedAt     string `json:"accepted_at"`
	RequestCount   int    `json:"request_count"`
}

type mpRequest struct {
	Seq            int    `json:"seq"`
	Channel        string `json:"channel"`
	IdempotencyKey string `json:"idempotency_key"`
	To             string `json:"to"`
	ReceivedAt     string `json:"received_at"`
	Status         int    `json:"status"`
	Replayed       bool   `json:"replayed"`
}

type mpResponse struct {
	Status int
	Header http.Header
	Body   []byte
	Err    error // transport error: no response (e.g. a lost response)
}

type mpAccepted struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Channel        string `json:"channel"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
}

// mpPost never fails the test itself, so it can run in goroutines.
func mpPost(url, contentType, key string, body []byte) mpResponse {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return mpResponse{Err: err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := mpClient.Do(req)
	if err != nil {
		return mpResponse{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return mpResponse{Status: resp.StatusCode, Header: resp.Header, Body: b, Err: err}
}

func smsPayload(to string) map[string]string {
	return map[string]string{"to": to, "body": "Merhaba, sepetin seni bekliyor."}
}

func emailPayload(to string) map[string]string {
	return map[string]string{"to": to, "subject": "Sepetin seni bekliyor", "text": "Merhaba", "html": "<p>Merhaba</p>"}
}

func mpSend(t *testing.T, mp, channel, key string, payload any) mpResponse {
	t.Helper()
	return mpPost(mp+"/v1/"+channel+"/messages", "application/json", key, mustJSON(t, payload))
}

// mpSettled repeats a request while the answer is a random 500/503 and returns the first other
// answer. A transport error is fatal (no lost responses outside scripts).
func mpSettled(t *testing.T, url, contentType, key string, body []byte) mpResponse {
	t.Helper()
	for range 40 {
		r := mpPost(url, contentType, key, body)
		if r.Err != nil {
			t.Fatalf("POST %s (key %q): %v", url, key, r.Err)
		}
		if r.Status != http.StatusInternalServerError && r.Status != http.StatusServiceUnavailable {
			return r
		}
	}
	t.Fatalf("POST %s (key %q) answered 5xx 40 times in a row", url, key)
	return mpResponse{}
}

func mpSendSettled(t *testing.T, mp, channel, key string, payload any) mpResponse {
	t.Helper()
	return mpSettled(t, mp+"/v1/"+channel+"/messages", "application/json", key, mustJSON(t, payload))
}

func mpAdmin(t *testing.T, method, url string, body []byte, wantStatus int) []byte {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, rd)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := mpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, url, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body: %s", method, url, resp.StatusCode, wantStatus, b)
	}
	return b
}

func mpReset(t *testing.T, mp string) {
	t.Helper()
	mpAdmin(t, http.MethodPost, mp+"/admin/reset", nil, http.StatusNoContent)
}

// mpResetCleanup runs from t.Cleanup, after t.Context() is cancelled.
func mpResetCleanup(t *testing.T, mp string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, mp+"/admin/reset", nil)
	if err != nil {
		t.Errorf("cleanup reset: %v", err)
		return
	}
	resp, err := mpClient.Do(req)
	if err != nil {
		t.Errorf("cleanup reset: %v", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("cleanup POST /admin/reset status = %d, want 204", resp.StatusCode)
	}
}

// mpScript queues responses for channel (+ to when non-empty).
func mpScript(t *testing.T, mp, channel, to string, responses []map[string]any) {
	t.Helper()
	body := map[string]any{"channel": channel, "responses": responses}
	if to != "" {
		body["to"] = to
	}
	mpAdmin(t, http.MethodPost, mp+"/admin/scripts", mustJSON(t, body), http.StatusNoContent)
}

func mpMessages(t *testing.T, mp string) []mpMessage {
	t.Helper()
	b := mpAdmin(t, http.MethodGet, mp+"/admin/messages", nil, http.StatusOK)
	var page struct {
		Items []mpMessage `json:"items"`
	}
	mustUnmarshal(t, string(b), &page)
	return page.Items
}

// mpRequests returns the request log; seq starts at 1 after a reset and grows by one.
func mpRequests(t *testing.T, mp string) []mpRequest {
	t.Helper()
	b := mpAdmin(t, http.MethodGet, mp+"/admin/requests", nil, http.StatusOK)
	var page struct {
		Items []mpRequest `json:"items"`
	}
	mustUnmarshal(t, string(b), &page)
	for i, r := range page.Items {
		if r.Seq != i+1 {
			t.Fatalf("/admin/requests items[%d].seq = %d, want %d (arrival order from 1)", i, r.Seq, i+1)
		}
	}
	return page.Items
}

func requestsFor(reqs []mpRequest, key string) []mpRequest {
	var out []mpRequest
	for _, r := range reqs {
		if r.IdempotencyKey == key {
			out = append(out, r)
		}
	}
	return out
}

// assertAccepted checks a 202 body {"id","status","channel","idempotency_key","duplicate"} and the
// Idempotent-Replayed header of a replay.
func assertAccepted(t *testing.T, r mpResponse, channel, key string, duplicate bool) mpAccepted {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("no response: %v", r.Err)
	}
	if r.Status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", r.Status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if diff := cmp.Diff([]string{"channel", "duplicate", "id", "idempotency_key", "status"}, sortedKeys(decodeObject(t, r.Body))); diff != "" {
		t.Fatalf("202 body keys mismatch (-want +got):\n%s", diff)
	}
	var a mpAccepted
	mustUnmarshal(t, string(r.Body), &a)
	if !msgIDRE.MatchString(a.ID) || a.Status != "accepted" || a.Channel != channel || a.IdempotencyKey != key || a.Duplicate != duplicate {
		t.Fatalf("202 body = %+v, want id msg_<32 hex>, status accepted, channel %s, key %s, duplicate %v", a, channel, key, duplicate)
	}
	if got := r.Header.Get("Idempotent-Replayed"); (got == "true") != duplicate {
		t.Fatalf("Idempotent-Replayed = %q for duplicate=%v", got, duplicate)
	}
	return a
}

// assertMPError checks status and the exact body {"error":{"code","message"}}.
func assertMPError(t *testing.T, r mpResponse, wantStatus int, wantCode string) {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("no response: %v", r.Err)
	}
	if r.Status != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", r.Status, wantStatus, r.Body)
	}
	var env map[string]map[string]any
	if err := json.Unmarshal(r.Body, &env); err != nil || len(env) != 1 || env["error"] == nil {
		t.Fatalf("body is not {\"error\":{...}} (%v): %s", err, r.Body)
	}
	if diff := cmp.Diff([]string{"code", "message"}, sortedKeys(env["error"])); diff != "" {
		t.Fatalf("error keys mismatch (-want +got):\n%s", diff)
	}
	if env["error"]["code"] != wantCode {
		t.Fatalf("error.code = %v, want %s", env["error"]["code"], wantCode)
	}
	if msg, _ := env["error"]["message"].(string); msg == "" {
		t.Fatalf("error.message is empty: %s", r.Body)
	}
}

// ---- tests ----

// REQ-MOCK-01
// Both send endpoints accept a valid message with 202 and the contracted body; missing
// Idempotency-Key and broken or incomplete bodies are 400 with their codes and are not stored.
func TestE2E_MockProviderSendEndpoints(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	t.Run("sms is accepted", func(t *testing.T) {
		assertAccepted(t, mpSendSettled(t, mp, "sms", "send-sms-1", smsPayload("+905000000101")), "sms", "send-sms-1", false)
	})
	t.Run("email is accepted", func(t *testing.T) {
		assertAccepted(t, mpSendSettled(t, mp, "email", "send-email-1", emailPayload("send-1@example.com")), "email", "send-email-1", false)
	})

	rejected := []struct {
		name        string
		channel     string
		key         string
		contentType string
		body        string
		code        string
	}{
		{name: "sms without Idempotency-Key", channel: "sms", body: `{"to":"+905000000102","body":"Merhaba"}`, code: "missing_idempotency_key"},
		{name: "email without Idempotency-Key", channel: "email", body: `{"to":"a@example.com","subject":"K","text":"M","html":"<p>M</p>"}`, code: "missing_idempotency_key"},
		{name: "broken JSON", channel: "sms", key: "send-bad-1", body: `{"to":"+905000000103",`, code: "invalid_body"},
		{name: "sms without body", channel: "sms", key: "send-bad-2", body: `{"to":"+905000000104"}`, code: "invalid_body"},
		{name: "sms without to", channel: "sms", key: "send-bad-3", body: `{"body":"Merhaba"}`, code: "invalid_body"},
		{name: "email without subject", channel: "email", key: "send-bad-4", body: `{"to":"a@example.com","text":"M","html":"<p>M</p>"}`, code: "invalid_body"},
		{name: "email without text", channel: "email", key: "send-bad-5", body: `{"to":"a@example.com","subject":"K","html":"<p>M</p>"}`, code: "invalid_body"},
		{name: "email without html", channel: "email", key: "send-bad-6", body: `{"to":"a@example.com","subject":"K","text":"M"}`, code: "invalid_body"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			r := mpSettled(t, mp+"/v1/"+tc.channel+"/messages", "application/json", tc.key, []byte(tc.body))
			assertMPError(t, r, http.StatusBadRequest, tc.code)
		})
	}

	t.Run("only the accepted messages are stored", func(t *testing.T) {
		var keys []string
		for _, m := range mpMessages(t, mp) {
			keys = append(keys, m.IdempotencyKey)
		}
		slices.Sort(keys)
		if diff := cmp.Diff([]string{"send-email-1", "send-sms-1"}, keys); diff != "" {
			t.Fatalf("/admin/messages keys mismatch (-want +got):\n%s", diff)
		}
	})
}

// REQ-SND-05
// The mock provider only accepts fictional recipients: SMS to ^\+90500000[0-9]{4}$, e-mail ending
// in @example.com or @example.test; anything else is 400 invalid_recipient and is not stored.
func TestE2E_MockProviderRejectsRealRecipients(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	tests := []struct {
		name    string
		channel string
		to      string
		ok      bool
	}{
		{name: "fictional block number", channel: "sms", to: "+905000000000", ok: true},
		{name: "last number of the block", channel: "sms", to: "+905000009999", ok: true},
		{name: "real Turkish mobile number", channel: "sms", to: "+905321234567"},
		{name: "three digits after the block", channel: "sms", to: "+90500000123"},
		{name: "five digits after the block", channel: "sms", to: "+9050000012345"},
		{name: "block number without plus", channel: "sms", to: "905000001234"},
		{name: "letters after the block", channel: "sms", to: "+90500000abcd"},
		{name: "empty phone", channel: "sms", to: ""},
		{name: "example.com address", channel: "email", to: "ayse@example.com", ok: true},
		{name: "example.test address", channel: "email", to: "ayse@example.test", ok: true},
		{name: "real mail provider", channel: "email", to: "ayse@gmail.com"},
		{name: "example.com as a subdomain", channel: "email", to: "ayse@example.com.tr"},
		{name: "domain that merely ends in example.com", channel: "email", to: "ayse@notexample.com"},
		{name: "example.org", channel: "email", to: "ayse@example.org"},
	}
	var accepted []string
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("recipient-%02d", i)
			payload := smsPayload(tc.to)
			if tc.channel == "email" {
				payload = emailPayload(tc.to)
			}
			r := mpSendSettled(t, mp, tc.channel, key, payload)
			if tc.ok {
				assertAccepted(t, r, tc.channel, key, false)
				accepted = append(accepted, key)
				return
			}
			assertMPError(t, r, http.StatusBadRequest, "invalid_recipient")
		})
	}
	t.Run("rejected recipients are not stored", func(t *testing.T) {
		var keys []string
		for _, m := range mpMessages(t, mp) {
			keys = append(keys, m.IdempotencyKey)
		}
		slices.Sort(keys)
		slices.Sort(accepted)
		if diff := cmp.Diff(accepted, keys); diff != "" {
			t.Fatalf("/admin/messages keys mismatch (-want +got):\n%s", diff)
		}
	})
}

// REQ-MOCK-02, REQ-SND-03
// Idempotency: a second request with an accepted key is a replay (202, the first body with
// duplicate true, Idempotent-Replayed: true) that stores nothing new and only raises request_count;
// a replay is not subject to scripts; the same key with another recipient or channel is 409
// idempotency_key_reused; a key whose request was not accepted is evaluated again.
func TestE2E_MockProviderIdempotency(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const to = "+905000000201"
	first := mpSendSettled(t, mp, "sms", "idem-1", smsPayload(to))
	a1 := assertAccepted(t, first, "sms", "idem-1", false)

	// A script queued now must not be consumed by the replay (replays skip scripts).
	mpScript(t, mp, "sms", to, []map[string]any{scripted(503)})

	t.Run("replay returns the first result marked duplicate", func(t *testing.T) {
		r := mpSend(t, mp, "sms", "idem-1", smsPayload(to))
		a2 := assertAccepted(t, r, "sms", "idem-1", true)
		want := a1
		want.Duplicate = true
		if diff := cmp.Diff(want, a2); diff != "" {
			t.Fatalf("replay body mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the replay left the script in place", func(t *testing.T) {
		r := mpSend(t, mp, "sms", "idem-2", smsPayload(to))
		assertMPError(t, r, http.StatusServiceUnavailable, "internal_error")
	})

	reused := []struct {
		name    string
		channel string
		payload any
	}{
		{name: "another recipient", channel: "sms", payload: smsPayload("+905000000202")},
		{name: "another channel", channel: "email", payload: emailPayload("idem@example.com")},
	}
	for _, tc := range reused {
		t.Run("same key with "+tc.name+" is 409 idempotency_key_reused", func(t *testing.T) {
			assertMPError(t, mpSendSettled(t, mp, tc.channel, "idem-1", tc.payload), http.StatusConflict, "idempotency_key_reused")
		})
	}

	t.Run("one message, request_count counts every request with the key", func(t *testing.T) {
		msgs := mpMessages(t, mp)
		if len(msgs) != 1 {
			t.Fatalf("/admin/messages = %+v, want only idem-1", msgs)
		}
		got := msgs[0]
		n := len(requestsFor(mpRequests(t, mp), "idem-1"))
		if got.IdempotencyKey != "idem-1" || got.ID != a1.ID || got.Channel != "sms" || got.To != to || got.RequestCount != n || n < 4 || got.AcceptedAt == "" {
			t.Fatalf("message = %+v, want idem-1 / %s / sms / %s with request_count = %d logged requests (>= 4)", got, a1.ID, to, n)
		}
	})

	t.Run("a key that got 500 is evaluated again", func(t *testing.T) {
		const to2 = "+905000000203"
		mpScript(t, mp, "sms", to2, []map[string]any{scripted(500), scripted(202)})
		assertMPError(t, mpSend(t, mp, "sms", "idem-3", smsPayload(to2)), http.StatusInternalServerError, "internal_error")
		assertAccepted(t, mpSend(t, mp, "sms", "idem-3", smsPayload(to2)), "sms", "idem-3", false)
	})
}

// REQ-MOCK-01, REQ-MOCK-02
// /admin/scripts forces an exact answer sequence for channel + to (no rate limit or random decision
// applies): 429 with the scripted Retry-After, 500, 503, 202, and the replay that follows does not
// consume the next item. /admin/requests logs every evaluated request in arrival order with its
// status and replay flag; /admin/messages has the one accepted message.
func TestE2E_MockProviderScriptedSequence(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const to = "+905000000301"
	mpScript(t, mp, "sms", to, []map[string]any{scriptedRetryAfter(429, 3), scripted(500), scripted(503), scripted(202), scripted(503)})

	r := mpSend(t, mp, "sms", "seq-1", smsPayload(to))
	assertMPError(t, r, http.StatusTooManyRequests, "rate_limited")
	if got := r.Header.Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After = %q, want the scripted 3", got)
	}
	assertMPError(t, mpSend(t, mp, "sms", "seq-1", smsPayload(to)), http.StatusInternalServerError, "internal_error")
	assertMPError(t, mpSend(t, mp, "sms", "seq-1", smsPayload(to)), http.StatusServiceUnavailable, "internal_error")
	acc := assertAccepted(t, mpSend(t, mp, "sms", "seq-1", smsPayload(to)), "sms", "seq-1", false)
	assertAccepted(t, mpSend(t, mp, "sms", "seq-1", smsPayload(to)), "sms", "seq-1", true)
	assertMPError(t, mpSend(t, mp, "sms", "seq-2", smsPayload(to)), http.StatusServiceUnavailable, "internal_error")

	reqs := mpRequests(t, mp)
	type row struct {
		Channel, Key, To string
		Status           int
		Replayed         bool
	}
	var got []row
	for _, q := range reqs {
		got = append(got, row{q.Channel, q.IdempotencyKey, q.To, q.Status, q.Replayed})
	}
	want := []row{
		{"sms", "seq-1", to, 429, false},
		{"sms", "seq-1", to, 500, false},
		{"sms", "seq-1", to, 503, false},
		{"sms", "seq-1", to, 202, false},
		{"sms", "seq-1", to, 202, true},
		{"sms", "seq-2", to, 503, false},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("/admin/requests mismatch (-want +got):\n%s", diff)
	}
	for i := 1; i < len(reqs); i++ {
		if parseTime(t, "received_at", reqs[i].ReceivedAt).Before(parseTime(t, "received_at", reqs[i-1].ReceivedAt)) {
			t.Fatalf("received_at goes back between seq %d and %d", reqs[i-1].Seq, reqs[i].Seq)
		}
	}
	msgs := mpMessages(t, mp)
	if len(msgs) != 1 || msgs[0].ID != acc.ID || msgs[0].IdempotencyKey != "seq-1" || msgs[0].RequestCount != 5 {
		t.Fatalf("/admin/messages = %+v, want only seq-1 (%s) with request_count 5", msgs, acc.ID)
	}
}

// REQ-MOCK-01
// Script queues: a channel + to queue is used before the channel-only queue, which serves every
// other recipient of that channel; scripts of one channel never answer the other; DELETE
// /admin/scripts removes what is left.
func TestE2E_MockProviderScriptQueues(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const a, b = "+905000000401", "+905000000402"
	mpScript(t, mp, "sms", "", []map[string]any{scripted(500), scripted(400), scripted(400)})
	mpScript(t, mp, "sms", a, []map[string]any{scripted(503)})

	if r := mpSend(t, mp, "sms", "q-1", smsPayload(a)); r.Err != nil || r.Status != http.StatusServiceUnavailable {
		t.Fatalf("first request to the scripted recipient = %d (%v), want its own 503", r.Status, r.Err)
	}
	if r := mpSend(t, mp, "sms", "q-2", smsPayload(a)); r.Err != nil || r.Status != http.StatusInternalServerError {
		t.Fatalf("second request to the scripted recipient = %d (%v), want the channel queue's 500", r.Status, r.Err)
	}
	if r := mpSend(t, mp, "sms", "q-3", smsPayload(b)); r.Err != nil || r.Status != http.StatusBadRequest {
		t.Fatalf("request to another recipient = %d (%v), want the channel queue's 400", r.Status, r.Err)
	}

	r := mpSendSettled(t, mp, "email", "q-4", emailPayload("queue@example.com"))
	assertAccepted(t, r, "email", "q-4", false)

	mpAdmin(t, http.MethodDelete, mp+"/admin/scripts", nil, http.StatusNoContent)
	assertAccepted(t, mpSendSettled(t, mp, "sms", "q-5", smsPayload(b)), "sms", "q-5", false)
	for _, q := range requestsFor(mpRequests(t, mp), "q-5") {
		if q.Status == http.StatusBadRequest {
			t.Fatalf("a deleted script still answered: %+v", q)
		}
	}
}

// REQ-MOCK-01, REQ-MOCK-02
// A lost response: the message is accepted and stored but the connection closes without an answer
// (logged with status 0); the retry with the same key is a replay, not a second message.
func TestE2E_MockProviderLostResponse(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const to = "+905000000501"
	mpScript(t, mp, "sms", to, []map[string]any{{"status": 202, "lost_response": true}})
	if r := mpSend(t, mp, "sms", "lost-1", smsPayload(to)); r.Err == nil {
		t.Fatalf("lost response answered %d: %s; want a connection error", r.Status, r.Body)
	}
	msgs := mpMessages(t, mp)
	if len(msgs) != 1 || msgs[0].IdempotencyKey != "lost-1" || msgs[0].RequestCount != 1 || !msgIDRE.MatchString(msgs[0].ID) {
		t.Fatalf("/admin/messages after a lost response = %+v, want lost-1 stored with request_count 1", msgs)
	}
	if reqs := mpRequests(t, mp); len(reqs) != 1 || reqs[0].Status != 0 || reqs[0].Replayed {
		t.Fatalf("/admin/requests = %+v, want one entry with status 0", reqs)
	}

	a := assertAccepted(t, mpSend(t, mp, "sms", "lost-1", smsPayload(to)), "sms", "lost-1", true)
	if a.ID != msgs[0].ID {
		t.Fatalf("replay id = %s, want the stored %s", a.ID, msgs[0].ID)
	}
	msgs = mpMessages(t, mp)
	if len(msgs) != 1 || msgs[0].RequestCount != 2 {
		t.Fatalf("/admin/messages after the retry = %+v, want one message with request_count 2", msgs)
	}
	reqs := mpRequests(t, mp)
	if len(reqs) != 2 || reqs[1].Status != http.StatusAccepted || !reqs[1].Replayed {
		t.Fatalf("/admin/requests = %+v, want the retry logged as 202 replayed", reqs)
	}
}

// REQ-MOCK-01
// POST /admin/reset clears messages, the request log (seq restarts at 1), the idempotency store and
// the scripts.
func TestE2E_MockProviderReset(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const to = "+905000000601"
	assertAccepted(t, mpSendSettled(t, mp, "sms", "reset-1", smsPayload(to)), "sms", "reset-1", false)
	mpScript(t, mp, "sms", to, []map[string]any{scripted(400)})
	mpReset(t, mp)

	if msgs := mpMessages(t, mp); len(msgs) != 0 {
		t.Fatalf("/admin/messages after reset = %+v, want empty", msgs)
	}
	if reqs := mpRequests(t, mp); len(reqs) != 0 {
		t.Fatalf("/admin/requests after reset = %+v, want empty", reqs)
	}
	assertAccepted(t, mpSendSettled(t, mp, "sms", "reset-1", smsPayload(to)), "sms", "reset-1", false)
	for _, q := range mpRequests(t, mp) {
		if q.Status == http.StatusBadRequest {
			t.Fatalf("a script survived the reset: %+v", q)
		}
	}
}

// REQ-MOCK-01
// Determinism: after a reset (generators re-seeded from MOCK_PROVIDER_SEED) the same in-channel
// arrival order gives the same decision sequence, and SMS and e-mail decisions are independent:
// interleaving the two channels changes neither sequence.
func TestE2E_MockProviderDeterministicDecisions(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const n = 60
	type step struct{ channel, key string }
	send := func(t *testing.T, s step, i int) int {
		t.Helper()
		var r mpResponse
		if s.channel == "sms" {
			r = mpSend(t, mp, "sms", s.key, smsPayload(fmt.Sprintf("+90500000%04d", 7000+i)))
		} else {
			r = mpSend(t, mp, "email", s.key, emailPayload(fmt.Sprintf("det-%02d@example.com", i)))
		}
		if r.Err != nil {
			return 0 // lost response
		}
		return r.Status
	}
	run := func(t *testing.T, plan []step) map[string][]int {
		t.Helper()
		mpReset(t, mp)
		out := map[string][]int{}
		idx := map[string]int{}
		for _, s := range plan {
			out[s.channel] = append(out[s.channel], send(t, s, idx[s.channel]))
			idx[s.channel]++
		}
		return out
	}
	var smsOnly, emailOnly, mixed []step
	for i := range n {
		sms := step{"sms", fmt.Sprintf("det-sms-%02d", i)}
		email := step{"email", fmt.Sprintf("det-email-%02d", i)}
		smsOnly = append(smsOnly, sms)
		emailOnly = append(emailOnly, email)
		mixed = append(mixed, sms, email)
	}

	a := run(t, smsOnly)
	again := run(t, smsOnly)
	b := run(t, emailOnly)
	c := run(t, mixed)
	if diff := cmp.Diff(a["sms"], again["sms"]); diff != "" {
		t.Fatalf("the same SMS sequence after a reset decided differently (-first +second):\n%s", diff)
	}
	if diff := cmp.Diff(a["sms"], c["sms"]); diff != "" {
		t.Fatalf("interleaved e-mail requests changed the SMS decisions (-alone +mixed):\n%s", diff)
	}
	if diff := cmp.Diff(b["email"], c["email"]); diff != "" {
		t.Fatalf("interleaved SMS requests changed the e-mail decisions (-alone +mixed):\n%s", diff)
	}
}

// REQ-MOCK-02, REQ-SND-02
// The mock's own sliding-window limit: with random 429 off, a burst of 150 concurrent SMS requests
// that all arrive within one second gets 429 rate_limited with an integer Retry-After for
// everything above 100, and never more than 100 non-429 answers in any window of the mock's own
// received_at; the e-mail counter is independent, so an e-mail sent right after is not limited.
func TestE2E_MockProviderRateLimit(t *testing.T) {
	// Not parallel: see the file comment.
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	const burst = 150
	results := make([]mpResponse, burst)
	var wg sync.WaitGroup
	for i := range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := json.Marshal(smsPayload(fmt.Sprintf("+90500000%04d", 8000+i)))
			if err != nil {
				results[i] = mpResponse{Err: err}
				return
			}
			results[i] = mpPost(mp+"/v1/sms/messages", "application/json", "burst-"+strconv.Itoa(i), body)
		}()
	}
	wg.Wait()

	email := mpSendSettled(t, mp, "email", "burst-email", emailPayload("burst@example.com"))
	assertAccepted(t, email, "email", "burst-email", false)

	limited := 0
	retryAfter := ""
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("burst request %d: %v", i, r.Err)
		}
		if r.Status != http.StatusTooManyRequests {
			continue
		}
		limited++
		assertMPError(t, r, http.StatusTooManyRequests, "rate_limited")
		ra := r.Header.Get("Retry-After")
		if _, err := strconv.Atoi(ra); err != nil {
			t.Fatalf("429 Retry-After = %q, want whole seconds", ra)
		}
		if retryAfter != "" && ra != retryAfter {
			t.Fatalf("429 Retry-After values differ: %q and %q (one MOCK_PROVIDER_RETRY_AFTER_SECONDS)", retryAfter, ra)
		}
		retryAfter = ra
	}

	var all, passed []time.Time
	for _, q := range mpRequests(t, mp) {
		if q.Channel != "sms" {
			continue
		}
		ts := parseTime(t, "received_at", q.ReceivedAt)
		all = append(all, ts)
		if q.Status != http.StatusTooManyRequests {
			passed = append(passed, ts)
		}
	}
	if len(all) != burst {
		t.Fatalf("/admin/requests has %d sms entries, want %d", len(all), burst)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Before(all[j]) })
	sort.Slice(passed, func(i, j int) bool { return passed[i].Before(passed[j]) })
	if all[len(all)-1].Sub(all[0]) < time.Second {
		if limited < burst-100 {
			t.Fatalf("%d requests arrived within one second but only %d got 429, want at least %d", burst, limited, burst-100)
		}
	} else {
		t.Logf("the burst spread over %s; only the window bound below applies", all[len(all)-1].Sub(all[0]))
	}
	// 20 ms tolerance for the gap between stamping received_at and the limiter step.
	for i := 0; i+100 < len(passed); i++ {
		if span := passed[i+100].Sub(passed[i]); span < time.Second-20*time.Millisecond {
			t.Fatalf("101 sms requests passed the limiter within %s, want at most 100 per second", span)
		}
	}
}
