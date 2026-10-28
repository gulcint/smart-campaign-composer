package mockprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/mockprovider"
)

// t0 is the test reference time of docs/requirements.md.
var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// baseConfig is LoadConfig's defaults with no latency and no random faults; tests switch on what
// they need. Latency 0 keeps every request independent of the fake clock.
func baseConfig(t *testing.T) mockprovider.Config {
	t.Helper()
	cfg, err := mockprovider.LoadConfig(env(nil))
	if err != nil {
		t.Fatalf("LoadConfig(empty env) error = %v", err)
	}
	cfg.ErrorRate = 0
	cfg.RateLimitRate = 0
	cfg.LostResponseRate = 0
	cfg.LatencyMin = 0
	cfg.LatencyMax = 0
	return cfg
}

type server struct {
	t    *testing.T
	srv  *httptest.Server
	hc   *http.Client
	fake *clock.Fake
}

func start(t *testing.T, cfg mockprovider.Config) *server {
	t.Helper()
	fake := clock.NewFake(t0)
	srv := httptest.NewServer(mockprovider.New(cfg, fake))
	t.Cleanup(srv.Close)
	hc := srv.Client()
	hc.Timeout = 30 * time.Second
	return &server{t: t, srv: srv, hc: hc, fake: fake}
}

// answer is one provider answer; lost is true when the connection closed without a response.
type answer struct {
	status     int
	lost       bool
	retryAfter string
	replayed   string
	code       string
	duplicate  bool
	id         string
}

func (s *server) post(channel, key string, body any) answer {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatalf("marshal body: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.srv.URL+"/v1/"+channel+"/messages", bytes.NewReader(raw))
	if err != nil {
		s.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return answer{lost: true}
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var doc struct {
		ID        string `json:"id"`
		Duplicate bool   `json:"duplicate"`
		Error     struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &doc)
	return answer{
		status:     resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
		replayed:   resp.Header.Get("Idempotent-Replayed"),
		code:       doc.Error.Code,
		duplicate:  doc.Duplicate,
		id:         doc.ID,
	}
}

func smsBody(n int) map[string]string {
	return map[string]string{"to": fmt.Sprintf("+90500000%04d", n%10000), "body": "Merhaba, %30 indirim seni bekliyor"}
}

func emailBody(n int) map[string]string {
	return map[string]string{"to": fmt.Sprintf("musteri%d@example.com", n), "subject": "Sepetin seni bekliyor", "text": "Merhaba", "html": "<p>Merhaba</p>"}
}

// key builds a distinct UUID-shaped Idempotency-Key per (prefix, n).
func key(prefix string, n int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, []byte(prefix)[0])
}

// outcome is the comparable part of an answer for the determinism test.
func outcome(a answer) string {
	if a.lost {
		return "lost"
	}
	return strconv.Itoa(a.status)
}

// REQ-MOCK-01
// With a fixed seed and the same in-channel arrival order the provider makes the same decisions;
// SMS decisions do not change when email requests are interleaved (and vice versa). The run uses
// non-zero 5xx, 429 and lost-response rates, so the decisions are actually random; every random 5xx
// is 500 or 503 and every 429 carries Retry-After = MOCK_PROVIDER_RETRY_AFTER_SECONDS.
func TestHandler_SeededDecisionsAreDeterministic(t *testing.T) {
	t.Parallel()
	const n = 60
	cfg := baseConfig(t)
	cfg.Seed = 42
	cfg.ErrorRate = 0.3
	cfg.RateLimitRate = 0.2
	cfg.LostResponseRate = 0.1
	cfg.RetryAfterSeconds = 2

	run := func(sms, email bool) (smsOut, emailOut []string, all []answer) {
		s := start(t, cfg)
		for i := 0; i < n; i++ {
			if email {
				a := s.post("email", key("e", i), emailBody(i))
				emailOut = append(emailOut, outcome(a))
				all = append(all, a)
			}
			if sms {
				a := s.post("sms", key("s", i), smsBody(i))
				smsOut = append(smsOut, outcome(a))
				all = append(all, a)
			}
			// Keep the rate window empty: only random decisions are under test here.
			s.fake.Advance(time.Second)
		}
		return smsOut, emailOut, all
	}

	smsOnly, _, smsAll := run(true, false)
	_, emailOnly, emailAll := run(false, true)
	smsMixed, emailMixed, _ := run(true, true)
	smsAgain, _, _ := run(true, false)

	eq := func(name string, a, b []string) {
		t.Helper()
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatalf("%s differ:\n%v\n%v", name, a, b)
		}
	}
	eq("two SMS-only runs with the same seed", smsOnly, smsAgain)
	eq("SMS decisions with and without interleaved email", smsOnly, smsMixed)
	eq("email decisions with and without interleaved SMS", emailOnly, emailMixed)

	distinct := map[string]bool{}
	for _, o := range smsOnly {
		distinct[o] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("SMS outcomes %v are all the same; with 30%% 5xx, 20%% 429 and 10%% lost they must vary", smsOnly)
	}
	for _, a := range append(smsAll, emailAll...) {
		switch {
		case a.lost:
		case a.status == 202, a.status == 500, a.status == 503:
		case a.status == 429:
			if a.retryAfter != "2" {
				t.Fatalf("random 429 Retry-After = %q, want %q (RetryAfterSeconds)", a.retryAfter, "2")
			}
			if a.code != "rate_limited" {
				t.Fatalf("random 429 error code = %q, want rate_limited", a.code)
			}
		default:
			t.Fatalf("random decision gave status %d, want 202, 429, 500, 503 or a lost response", a.status)
		}
	}
}

// REQ-MOCK-02
// The rate limit is a sliding window per channel measured on the injected clock: when the requests
// that reached the limit step in (now - 1s, now] number RateLimitPerSecond or more, the answer is
// 429 rate_limited with Retry-After = RetryAfterSeconds. Rate-limited requests count too; the other
// channel and replays are not affected.
func TestHandler_RateLimitWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		perSecond  int
		retryAfter int
	}{
		{name: "default 100 per second", perSecond: 100, retryAfter: 1},
		{name: "3 per second, Retry-After 2", perSecond: 3, retryAfter: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			cfg.RateLimitPerSecond = tc.perSecond
			cfg.RetryAfterSeconds = tc.retryAfter
			s := start(t, cfg)
			wantRA := strconv.Itoa(tc.retryAfter)
			n := 0
			next := func() int { n++; return n }

			for i := 0; i < tc.perSecond; i++ {
				if a := s.post("sms", key("s", next()), smsBody(n)); a.status != http.StatusAccepted {
					t.Fatalf("SMS #%d at t0 = %d (%s), want 202 below the limit", i+1, a.status, a.code)
				}
			}
			over := s.post("sms", key("s", next()), smsBody(n))
			if over.status != http.StatusTooManyRequests || over.code != "rate_limited" || over.retryAfter != wantRA {
				t.Fatalf("SMS over the limit = %d %q Retry-After %q, want 429 rate_limited Retry-After %q", over.status, over.code, over.retryAfter, wantRA)
			}
			if a := s.post("email", key("e", 1), emailBody(1)); a.status != http.StatusAccepted {
				t.Fatalf("email while SMS is limited = %d (%s), want 202: channels are independent", a.status, a.code)
			}
			replay := s.post("sms", key("s", 1), smsBody(1))
			if replay.status != http.StatusAccepted || replay.replayed != "true" || !replay.duplicate {
				t.Fatalf("replay while limited = %d, Idempotent-Replayed %q, duplicate %v; want 202, true, true", replay.status, replay.replayed, replay.duplicate)
			}

			s.fake.Advance(time.Second - time.Millisecond)
			if a := s.post("sms", key("s", next()), smsBody(n)); a.status != http.StatusTooManyRequests {
				t.Fatalf("SMS at t0+999ms = %d, want 429: the t0 requests are still inside (now-1s, now]", a.status)
			}
			s.fake.Advance(time.Millisecond)
			if a := s.post("sms", key("s", next()), smsBody(n)); a.status != http.StatusAccepted {
				t.Fatalf("SMS at t0+1s = %d (%s), want 202: only the t0+999ms request is left in the window", a.status, a.code)
			}
		})
	}
}

// REQ-MOCK-02
// A second request with an accepted Idempotency-Key is not accepted again: it gets the first
// answer with "duplicate": true and Idempotent-Replayed: true. The same key with another channel or
// another "to" is 409 idempotency_key_reused.
func TestHandler_Idempotency(t *testing.T) {
	t.Parallel()
	s := start(t, baseConfig(t))
	k := key("i", 1)
	first := s.post("sms", k, smsBody(1))
	if first.status != http.StatusAccepted || first.duplicate || first.replayed == "true" {
		t.Fatalf("first request = %d duplicate %v replayed %q, want 202 not duplicate", first.status, first.duplicate, first.replayed)
	}
	again := s.post("sms", k, smsBody(1))
	if again.status != http.StatusAccepted || !again.duplicate || again.replayed != "true" || again.id != first.id {
		t.Fatalf("replay = %d duplicate %v replayed %q id %q, want 202, true, true, %q", again.status, again.duplicate, again.replayed, again.id, first.id)
	}
	if a := s.post("sms", k, smsBody(2)); a.status != http.StatusConflict || a.code != "idempotency_key_reused" {
		t.Fatalf("same key, other to = %d %q, want 409 idempotency_key_reused", a.status, a.code)
	}
	if a := s.post("email", k, emailBody(1)); a.status != http.StatusConflict || a.code != "idempotency_key_reused" {
		t.Fatalf("same key, other channel = %d %q, want 409 idempotency_key_reused", a.status, a.code)
	}
}

// REQ-MOCK-01, REQ-SND-05
// Request checks: a missing Idempotency-Key, a broken body and a non-fictional recipient are 400.
func TestHandler_RequestValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		channel  string
		key      string
		body     any
		wantCode string
	}{
		{name: "no Idempotency-Key", channel: "sms", body: smsBody(1), wantCode: "missing_idempotency_key"},
		{name: "sms without body field", channel: "sms", key: key("v", 1), body: map[string]string{"to": "+905000000001"}, wantCode: "invalid_body"},
		{name: "email without subject", channel: "email", key: key("v", 2), body: map[string]string{"to": "a@example.com", "text": "x", "html": "x"}, wantCode: "invalid_body"},
		{name: "sms to a real-looking number", channel: "sms", key: key("v", 3), body: map[string]string{"to": "+905321234567", "body": "x"}, wantCode: "invalid_recipient"},
		{name: "email to a real domain", channel: "email", key: key("v", 4), body: map[string]string{"to": "a@gmail.com", "subject": "x", "text": "x", "html": "x"}, wantCode: "invalid_recipient"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := start(t, baseConfig(t))
			a := s.post(tc.channel, tc.key, tc.body)
			if a.status != http.StatusBadRequest || a.code != tc.wantCode {
				t.Fatalf("answer = %d %q, want 400 %q", a.status, a.code, tc.wantCode)
			}
		})
	}
}

// REQ-MOCK-01
// GET /healthz is 200 {"status":"ok"}; with AdminEnabled false every /admin/* path is 404.
func TestHandler_HealthAndAdminOff(t *testing.T) {
	t.Parallel()
	s := start(t, baseConfig(t))
	get := func(path string) (int, map[string]any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.srv.URL+path, nil)
		resp, err := s.hc.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		var doc map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&doc)
		return resp.StatusCode, doc
	}
	if code, doc := get("/healthz"); code != http.StatusOK || doc["status"] != "ok" {
		t.Fatalf("GET /healthz = %d %v, want 200 {\"status\":\"ok\"}", code, doc)
	}
	for _, p := range []string{"/admin/messages", "/admin/requests"} {
		if code, _ := get(p); code != http.StatusNotFound {
			t.Fatalf("GET %s with admin off = %d, want 404", p, code)
		}
	}
}
