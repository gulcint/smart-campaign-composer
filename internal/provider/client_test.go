package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

const deliveryID = "6f1c2a7e-3b0d-4c55-9a1e-2d7b8c9e0f13"

// setID stores a UUID in Message.DeliveryID. requirements.md names the field but not its type, so a
// string-kinded type and uuid.UUID are both accepted.
func setID(t *testing.T, field any, s string) {
	t.Helper()
	v := reflect.ValueOf(field).Elem()
	switch {
	case v.Kind() == reflect.String:
		v.SetString(s)
	case v.Type() == reflect.TypeFor[uuid.UUID]():
		v.Set(reflect.ValueOf(uuid.MustParse(s)))
	default:
		t.Fatalf("DeliveryID has type %s, want a string type or uuid.UUID", v.Type())
	}
}

func smsMessage(t *testing.T) provider.Message {
	t.Helper()
	m := provider.Message{Channel: provider.ChannelSMS, To: "+905000001234", SMSBody: "Merhaba Ayşe, %30 indirim seni bekliyor"}
	setID(t, &m.DeliveryID, deliveryID)
	return m
}

func emailMessage(t *testing.T) provider.Message {
	t.Helper()
	m := provider.Message{
		Channel: provider.ChannelEmail,
		To:      "ayse@example.com",
		Subject: "Sepetin seni bekliyor",
		Text:    "Merhaba Ayşe,\n\nİndirim seni bekliyor.",
		HTML:    "<p>Merhaba Ayşe,</p><p>İndirim seni bekliyor.</p>",
	}
	setID(t, &m.DeliveryID, deliveryID)
	return m
}

type seenRequest struct {
	method, path, mediaType, key string
	body                         map[string]any
}

// recorder is an httptest handler that records each request and answers with respond.
type recorder struct {
	mu      sync.Mutex
	seen    []seenRequest
	respond http.HandlerFunc
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	rec.mu.Lock()
	rec.seen = append(rec.seen, seenRequest{method: r.Method, path: r.URL.Path, mediaType: mt, key: r.Header.Get("Idempotency-Key"), body: body})
	rec.mu.Unlock()
	rec.respond(w, r)
}

func (rec *recorder) requests() []seenRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]seenRequest(nil), rec.seen...)
}

func accepted(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"id":"msg_0123456789abcdef0123456789abcdef","status":"accepted","channel":"sms","idempotency_key":"`+r.Header.Get("Idempotency-Key")+`","duplicate":false}`) //nolint:gosec // httptest stub echoing a test header, not HTML
}

func newClient(t *testing.T, h http.Handler, timeout time.Duration) (provider.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	hc := srv.Client()
	hc.Timeout = timeout
	return provider.NewHTTPClient(srv.URL, hc, clock.NewFake(t0)), srv
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// REQ-MOCK-01, REQ-SND-03
// Send posts SMS to /v1/sms/messages with {"to","body"} and email to /v1/email/messages with
// {"to","subject","text","html"}, as application/json, with the delivery id as Idempotency-Key.
func TestHTTPClient_Request(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		msg      func(*testing.T) provider.Message
		wantPath string
		wantBody map[string]string
	}{
		{
			name:     "sms",
			msg:      smsMessage,
			wantPath: "/v1/sms/messages",
			wantBody: map[string]string{"to": "+905000001234", "body": "Merhaba Ayşe, %30 indirim seni bekliyor"},
		},
		{
			name:     "email",
			msg:      emailMessage,
			wantPath: "/v1/email/messages",
			wantBody: map[string]string{
				"to":      "ayse@example.com",
				"subject": "Sepetin seni bekliyor",
				"text":    "Merhaba Ayşe,\n\nİndirim seni bekliyor.",
				"html":    "<p>Merhaba Ayşe,</p><p>İndirim seni bekliyor.</p>",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{respond: accepted}
			c, _ := newClient(t, rec, 10*time.Second)
			if _, err := c.Send(ctx(t), tc.msg(t)); err != nil {
				t.Fatalf("Send() error = %v, want nil", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("provider received %d requests, want 1", len(reqs))
			}
			got := reqs[0]
			if got.method != http.MethodPost || got.path != tc.wantPath {
				t.Fatalf("request = %s %s, want POST %s", got.method, got.path, tc.wantPath)
			}
			if got.mediaType != "application/json" {
				t.Fatalf("Content-Type media type = %q, want application/json", got.mediaType)
			}
			if got.key != deliveryID {
				t.Fatalf("Idempotency-Key = %q, want the delivery id %q", got.key, deliveryID)
			}
			for k, want := range tc.wantBody {
				if v, _ := got.body[k].(string); v != want {
					t.Fatalf("body[%q] = %#v, want %q (body %v)", k, got.body[k], want, got.body)
				}
			}
		})
	}
}

// REQ-SND-03
// Every Send of the same delivery carries the same Idempotency-Key, whatever the previous answer.
func TestHTTPClient_SameKeyOnEveryAttempt(t *testing.T) {
	t.Parallel()
	statuses := []int{500, 429, 503, 202}
	var mu sync.Mutex
	i := 0
	rec := &recorder{respond: func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		s := statuses[i]
		i++
		mu.Unlock()
		if s == 202 {
			accepted(w, r)
			return
		}
		w.WriteHeader(s)
	}}
	c, _ := newClient(t, rec, 10*time.Second)
	for range statuses {
		if _, err := c.Send(ctx(t), smsMessage(t)); err != nil {
			t.Fatalf("Send() error = %v, want nil for an HTTP answer", err)
		}
	}
	for n, r := range rec.requests() {
		if r.key != deliveryID {
			t.Fatalf("attempt %d Idempotency-Key = %q, want %q", n+1, r.key, deliveryID)
		}
	}
}

// REQ-SND-04, REQ-MOCK-01, REQ-MOCK-02
// Every HTTP answer, whatever its status, comes back with err == nil and its status code;
// Retry-After is parsed (seconds or HTTP date against the injected clock); a 202 carries the
// provider message id and Idempotent-Replayed: true marks a replay.
func TestHTTPClient_Response(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		respond   http.HandlerFunc
		want      provider.Response
		wantRA    time.Duration
		checkID   bool
		checkRepl bool
	}{
		{
			name:    "202 accepted",
			respond: accepted,
			want:    provider.Response{StatusCode: 202, ProviderMessageID: "msg_0123456789abcdef0123456789abcdef"},
			checkID: true, checkRepl: true,
		},
		{
			name: "202 replay",
			respond: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Idempotent-Replayed", "true")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"id":"msg_ffffffffffffffffffffffffffffffff","status":"accepted","channel":"sms","idempotency_key":"`+r.Header.Get("Idempotency-Key")+`","duplicate":true}`) //nolint:gosec // httptest stub echoing a test header, not HTML
			},
			want:    provider.Response{StatusCode: 202, ProviderMessageID: "msg_ffffffffffffffffffffffffffffffff", Replayed: true},
			checkID: true, checkRepl: true,
		},
		{
			name:    "429 with Retry-After seconds",
			respond: status(429, "3"),
			want:    provider.Response{StatusCode: 429, HasRetryAfter: true},
			wantRA:  3 * time.Second,
		},
		{
			name:    "429 with Retry-After HTTP date",
			respond: status(429, t0.Add(7*time.Second).Format(http.TimeFormat)),
			want:    provider.Response{StatusCode: 429, HasRetryAfter: true},
			wantRA:  7 * time.Second,
		},
		{
			name:    "429 with a past Retry-After date waits 0",
			respond: status(429, t0.Add(-time.Minute).Format(http.TimeFormat)),
			want:    provider.Response{StatusCode: 429, HasRetryAfter: true},
			wantRA:  0,
		},
		{name: "429 without Retry-After", respond: status(429, ""), want: provider.Response{StatusCode: 429}},
		{name: "429 with an unparsable Retry-After", respond: status(429, "later"), want: provider.Response{StatusCode: 429}},
		{name: "500", respond: status(500, ""), want: provider.Response{StatusCode: 500}},
		{name: "503", respond: status(503, ""), want: provider.Response{StatusCode: 503}},
		{name: "408", respond: status(408, ""), want: provider.Response{StatusCode: 408}},
		{name: "400", respond: status(400, ""), want: provider.Response{StatusCode: 400}},
		{name: "409", respond: status(409, ""), want: provider.Response{StatusCode: 409}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newClient(t, &recorder{respond: tc.respond}, 10*time.Second)
			got, err := c.Send(ctx(t), smsMessage(t))
			if err != nil {
				t.Fatalf("Send() error = %v, want nil: every HTTP answer returns err == nil", err)
			}
			if got.StatusCode != tc.want.StatusCode {
				t.Fatalf("StatusCode = %d, want %d", got.StatusCode, tc.want.StatusCode)
			}
			if got.HasRetryAfter != tc.want.HasRetryAfter {
				t.Fatalf("HasRetryAfter = %v, want %v", got.HasRetryAfter, tc.want.HasRetryAfter)
			}
			if tc.want.HasRetryAfter && got.RetryAfter != tc.wantRA {
				t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter, tc.wantRA)
			}
			if tc.checkID && got.ProviderMessageID != tc.want.ProviderMessageID {
				t.Fatalf("ProviderMessageID = %q, want %q", got.ProviderMessageID, tc.want.ProviderMessageID)
			}
			if tc.checkRepl && got.Replayed != tc.want.Replayed {
				t.Fatalf("Replayed = %v, want %v", got.Replayed, tc.want.Replayed)
			}
		})
	}
}

func status(code int, retryAfter string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, `{"error":{"code":"x","message":"scripted"}}`)
	}
}

// REQ-SND-04, REQ-SND-03, REQ-MOCK-01
// Transport failures are the only errors: a connection closed before any answer (the mock
// provider's "lost response") and a refused connection are provider.ErrConnection; exceeding the
// http.Client timeout (PROVIDER_TIMEOUT, wall clock) is provider.ErrTimeout.
func TestHTTPClient_TransportErrors(t *testing.T) {
	t.Parallel()
	t.Run("lost response is ErrConnection", func(t *testing.T) {
		t.Parallel()
		c, _ := newClient(t, &recorder{respond: func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				panic("httptest ResponseWriter is not a Hijacker")
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}}, 10*time.Second)
		_, err := c.Send(ctx(t), smsMessage(t))
		if !errors.Is(err, provider.ErrConnection) {
			t.Fatalf("Send() error = %v, want provider.ErrConnection", err)
		}
	})
	t.Run("refused connection is ErrConnection", func(t *testing.T) {
		t.Parallel()
		c, srv := newClient(t, http.HandlerFunc(accepted), 10*time.Second)
		srv.Close()
		_, err := c.Send(ctx(t), smsMessage(t))
		if !errors.Is(err, provider.ErrConnection) {
			t.Fatalf("Send() error = %v, want provider.ErrConnection", err)
		}
	})
	t.Run("client timeout is ErrTimeout", func(t *testing.T) {
		t.Parallel()
		release := make(chan struct{})
		c, _ := newClient(t, &recorder{respond: func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}}, 100*time.Millisecond)
		_, err := c.Send(ctx(t), smsMessage(t))
		close(release)
		if !errors.Is(err, provider.ErrTimeout) {
			t.Fatalf("Send() error = %v, want provider.ErrTimeout", err)
		}
		if errors.Is(err, provider.ErrConnection) {
			t.Fatalf("Send() error = %v is also ErrConnection, want only ErrTimeout", err)
		}
	})
}
