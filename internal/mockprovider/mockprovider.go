// Package mockprovider is the mock SMS/email provider (docs/contracts/mock-provider.md): a
// stand-alone HTTP service that accepts messages with idempotency, enforces its own per-channel
// rate limit and injects seeded random errors, latency, 429s and lost responses so that the
// worker's retry logic can be exercised for real. Nothing is ever sent anywhere (REQ-SND-05).
package mockprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

type Config struct {
	Addr               string        // MOCK_PROVIDER_ADDR, default ":8090"
	Seed               int64         // MOCK_PROVIDER_SEED, default 1
	ErrorRate          float64       // MOCK_PROVIDER_ERROR_RATE, default 0.05
	RateLimitRate      float64       // MOCK_PROVIDER_RATE_LIMIT_RATE, default 0.02
	LostResponseRate   float64       // MOCK_PROVIDER_LOST_RESPONSE_RATE, default 0
	LatencyMin         time.Duration // MOCK_PROVIDER_LATENCY_MIN, default 20ms
	LatencyMax         time.Duration // MOCK_PROVIDER_LATENCY_MAX, default 200ms
	RateLimitPerSecond int           // MOCK_PROVIDER_RATE_LIMIT_PER_SECOND, default 100 (per channel)
	RetryAfterSeconds  int           // MOCK_PROVIDER_RETRY_AFTER_SECONDS, default 1
	AdminEnabled       bool          // MOCK_PROVIDER_ADMIN, default false
}

var ErrInvalidConfig = errors.New("mockprovider: invalid config")

// LoadConfig reads the MOCK_PROVIDER_* variables (empty == unset). Invalid values, rates outside
// [0,1], rates summing above 1 or LatencyMin > LatencyMax => ErrInvalidConfig.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Addr: ":8090", Seed: 1, ErrorRate: 0.05, RateLimitRate: 0.02,
		LatencyMin: 20 * time.Millisecond, LatencyMax: 200 * time.Millisecond,
		RateLimitPerSecond: 100, RetryAfterSeconds: 1,
	}
	var errs []error
	bad := func(name string) { errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidConfig, name)) }
	if v := getenv("MOCK_PROVIDER_ADDR"); v != "" {
		c.Addr = v
	}
	if v := getenv("MOCK_PROVIDER_SEED"); v != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			bad("MOCK_PROVIDER_SEED")
		}
		c.Seed = n
	}
	rate := func(name string, dst *float64) {
		if v := getenv(name); v != "" {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || f < 0 || f > 1 {
				bad(name)
				return
			}
			*dst = f
		}
	}
	rate("MOCK_PROVIDER_ERROR_RATE", &c.ErrorRate)
	rate("MOCK_PROVIDER_RATE_LIMIT_RATE", &c.RateLimitRate)
	rate("MOCK_PROVIDER_LOST_RESPONSE_RATE", &c.LostResponseRate)
	duration := func(name string, dst *time.Duration) {
		if v := getenv(name); v != "" {
			d, err := time.ParseDuration(strings.TrimSpace(v))
			if err != nil || d < 0 {
				bad(name)
				return
			}
			*dst = d
		}
	}
	duration("MOCK_PROVIDER_LATENCY_MIN", &c.LatencyMin)
	duration("MOCK_PROVIDER_LATENCY_MAX", &c.LatencyMax)
	positive := func(name string, dst *int) {
		if v := getenv(name); v != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 1 {
				bad(name)
				return
			}
			*dst = n
		}
	}
	positive("MOCK_PROVIDER_RATE_LIMIT_PER_SECOND", &c.RateLimitPerSecond)
	positive("MOCK_PROVIDER_RETRY_AFTER_SECONDS", &c.RetryAfterSeconds)
	if v := getenv("MOCK_PROVIDER_ADMIN"); v != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			bad("MOCK_PROVIDER_ADMIN")
		}
		c.AdminEnabled = b
	}
	if c.ErrorRate+c.RateLimitRate+c.LostResponseRate > 1 {
		bad("rates sum above 1")
	}
	if c.LatencyMin > c.LatencyMax {
		bad("MOCK_PROVIDER_LATENCY_MIN > MOCK_PROVIDER_LATENCY_MAX")
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

var (
	smsTo   = regexp.MustCompile(`^\+90500000[0-9]{4}$`)
	emailTo = regexp.MustCompile(`^[^@\s]+@example\.(com|test)$`)
)

type message struct {
	id, channel, to, key string
	acceptedAt           time.Time
	body                 []byte // the original 202 body (duplicate:false)
}

type requestLog struct {
	Seq            int    `json:"seq"`
	Channel        string `json:"channel"`
	IdempotencyKey string `json:"idempotency_key"`
	To             string `json:"to"`
	ReceivedAt     string `json:"received_at"`
	Status         int    `json:"status"`
	Replayed       bool   `json:"replayed"`
}

type scriptItem struct {
	Status       int    `json:"status"`
	RetryAfter   string `json:"retry_after"`
	DelayMS      int    `json:"delay_ms"`
	LostResponse bool   `json:"lost_response"`
}

type server struct {
	cfg Config
	clk clock.Clock

	mu       sync.Mutex
	messages map[string]*message // by idempotency key
	order    []*message          // acceptance order
	keyCount map[string]int      // requests per idempotency key
	requests []requestLog
	window   map[string][]time.Time  // per channel: times requests reached the rate-limit step
	scripts  map[string][]scriptItem // key: channel + "\x00" + to ("" to = channel-wide queue)
	rngs     map[string]*rand.Rand   // per channel
}

// New returns the HTTP handler of mock-provider.md. Randomness comes only from cfg.Seed;
// latency waits use c.
func New(cfg Config, c clock.Clock) http.Handler {
	if c == nil {
		c = clock.Real()
	}
	s := &server{cfg: cfg, clk: c}
	s.reset()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/sms/messages", func(w http.ResponseWriter, r *http.Request) { s.send(w, r, "sms") })
	mux.HandleFunc("POST /v1/email/messages", func(w http.ResponseWriter, r *http.Request) { s.send(w, r, "email") })
	mux.HandleFunc("/admin/", s.admin)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})
	return mux
}

func (s *server) reset() {
	s.messages = map[string]*message{}
	s.order = nil
	s.keyCount = map[string]int{}
	s.requests = nil
	s.window = map[string][]time.Time{}
	s.scripts = map[string][]scriptItem{}
	s.rngs = map[string]*rand.Rand{
		"sms":   rand.New(rand.NewPCG(uint64(s.cfg.Seed), 1)), //nolint:gosec // seeded fault injection, not security
		"email": rand.New(rand.NewPCG(uint64(s.cfg.Seed), 2)), //nolint:gosec // seeded fault injection, not security
	}
}

// outcome is what the provider decided for one request.
type outcome struct {
	status     int
	retryAfter string
	delay      time.Duration
	lost       bool
}

func (s *server) send(w http.ResponseWriter, r *http.Request, channel string) {
	now := s.clk.Now()
	key := r.Header.Get("Idempotency-Key")
	to, valid := parseBody(r, channel)

	s.mu.Lock()
	logEntry := func(status int, replayed bool) {
		s.requests = append(s.requests, requestLog{
			Seq: len(s.requests) + 1, Channel: channel, IdempotencyKey: key, To: to,
			ReceivedAt: now.Format(time.RFC3339Nano), Status: status, Replayed: replayed,
		})
	}
	// (1) header and body validation
	switch {
	case key == "":
		logEntry(http.StatusBadRequest, false)
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	case !valid:
		logEntry(http.StatusBadRequest, false)
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid_body", "request body is not valid for this channel")
		return
	case (channel == "sms" && !smsTo.MatchString(to)) || (channel == "email" && !emailTo.MatchString(to)):
		logEntry(http.StatusBadRequest, false)
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid_recipient", "recipient is not in the fictional test range")
		return
	}
	s.keyCount[key]++
	// (2) replay of an accepted message
	if m, ok := s.messages[key]; ok {
		if m.channel != channel || m.to != to {
			logEntry(http.StatusConflict, false)
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "idempotency_key_reused", "idempotency key was used for another message")
			return
		}
		logEntry(http.StatusAccepted, true)
		body := replayBody(m.body)
		s.mu.Unlock()
		w.Header().Set("Idempotent-Replayed", "true")
		writeRaw(w, http.StatusAccepted, body)
		return
	}
	o := s.decide(channel, to, now)
	s.mu.Unlock()

	// (6) latency
	if o.delay > 0 {
		if err := clock.Sleep(r.Context(), s.clk, o.delay); err != nil {
			return // client gone
		}
	}

	// (7) response
	s.mu.Lock()
	if o.status == http.StatusAccepted {
		m, replayed := s.accept(channel, to, key)
		if o.lost {
			logEntry(0, false)
			s.mu.Unlock()
			panic(http.ErrAbortHandler) // accepted and recorded, connection closed without a response
		}
		logEntry(http.StatusAccepted, replayed)
		body := m.body
		if replayed {
			body = replayBody(m.body)
		}
		s.mu.Unlock()
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		writeRaw(w, http.StatusAccepted, body)
		return
	}
	logEntry(o.status, false)
	s.mu.Unlock()
	switch o.status {
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", o.retryAfter)
		writeError(w, o.status, "rate_limited", "rate limit exceeded")
	case http.StatusBadRequest:
		writeError(w, o.status, "invalid_body", "scripted rejection")
	case http.StatusRequestTimeout:
		writeError(w, o.status, "request_timeout", "scripted timeout")
	default:
		writeError(w, o.status, "internal_error", "provider error")
	}
}

// decide runs steps (3)–(5) of mock-provider.md §1.1. Caller holds s.mu.
func (s *server) decide(channel, to string, now time.Time) outcome {
	defaultRetry := strconv.Itoa(s.cfg.RetryAfterSeconds)
	// (3) scripted responses: channel+to queue first, then the channel-wide queue
	for _, qk := range []string{channel + "\x00" + to, channel + "\x00"} {
		if q := s.scripts[qk]; len(q) > 0 {
			it := q[0]
			s.scripts[qk] = q[1:]
			ra := it.RetryAfter
			if ra == "" {
				ra = defaultRetry
			}
			return outcome{status: it.Status, retryAfter: ra, delay: time.Duration(it.DelayMS) * time.Millisecond, lost: it.LostResponse}
		}
	}
	// (4) own sliding-window rate limit: requests that reached this step in (now-1s, now]
	win := s.window[channel][:0]
	for _, t := range s.window[channel] {
		if t.After(now.Add(-time.Second)) {
			win = append(win, t)
		}
	}
	limited := len(win) >= s.cfg.RateLimitPerSecond
	s.window[channel] = append(win, now)
	if limited {
		return outcome{status: http.StatusTooManyRequests, retryAfter: defaultRetry}
	}
	// (5) random decision: one draw for the kind, one for the latency
	rng := s.rngs[channel]
	u := rng.Float64()
	o := outcome{status: http.StatusAccepted, retryAfter: defaultRetry}
	switch e, rl, lost := s.cfg.ErrorRate, s.cfg.RateLimitRate, s.cfg.LostResponseRate; {
	case u < e/2:
		o.status = http.StatusInternalServerError
	case u < e:
		o.status = http.StatusServiceUnavailable
	case u < e+rl:
		o.status = http.StatusTooManyRequests
	case u < e+rl+lost:
		o.lost = true
	}
	span := s.cfg.LatencyMax - s.cfg.LatencyMin
	o.delay = s.cfg.LatencyMin + time.Duration(rng.Float64()*float64(span))
	return o
}

// accept records the message unless a concurrent request with the same key won. Caller holds s.mu.
func (s *server) accept(channel, to, key string) (*message, bool) {
	if m, ok := s.messages[key]; ok {
		return m, true
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(s.cfg.Seed, 10) + "/" + key))
	m := &message{id: "msg_" + hex.EncodeToString(sum[:16]), channel: channel, to: to, key: key, acceptedAt: s.clk.Now()}
	m.body, _ = json.Marshal(acceptBody{ID: m.id, Status: "accepted", Channel: channel, IdempotencyKey: key, Duplicate: false})
	s.messages[key] = m
	s.order = append(s.order, m)
	return m, false
}

type acceptBody struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Channel        string `json:"channel"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
}

func replayBody(orig []byte) []byte {
	var b acceptBody
	_ = json.Unmarshal(orig, &b) // produced by accept
	b.Duplicate = true
	out, _ := json.Marshal(b)
	return out
}

// parseBody validates the JSON body for the channel and returns its "to".
func parseBody(r *http.Request, channel string) (string, bool) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return "", false
	}
	var b struct {
		To      *string `json:"to"`
		Body    *string `json:"body"`
		Subject *string `json:"subject"`
		Text    *string `json:"text"`
		HTML    *string `json:"html"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&b); err != nil || b.To == nil {
		return "", false
	}
	if channel == "sms" {
		return *b.To, b.Body != nil && *b.Body != ""
	}
	return *b.To, b.Subject != nil && b.Text != nil && b.HTML != nil
}

// ---- admin (scripted mode), only with MOCK_PROVIDER_ADMIN=true ----

func (s *server) admin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AdminEnabled {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch {
	case r.URL.Path == "/admin/scripts" && r.Method == http.MethodPost:
		var req struct {
			Channel   string       `json:"channel"`
			To        string       `json:"to"`
			Responses []scriptItem `json:"responses"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil ||
			(req.Channel != "sms" && req.Channel != "email") || len(req.Responses) == 0 {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid script")
			return
		}
		for _, it := range req.Responses {
			switch it.Status {
			case 202, 400, 408, 429, 500, 503:
			default:
				writeError(w, http.StatusBadRequest, "invalid_body", "unsupported scripted status")
				return
			}
		}
		s.mu.Lock()
		qk := req.Channel + "\x00" + req.To
		s.scripts[qk] = append(s.scripts[qk], req.Responses...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/admin/scripts" && r.Method == http.MethodDelete:
		s.mu.Lock()
		s.scripts = map[string][]scriptItem{}
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/admin/messages" && r.Method == http.MethodGet:
		type item struct {
			ID             string `json:"id"`
			Channel        string `json:"channel"`
			To             string `json:"to"`
			IdempotencyKey string `json:"idempotency_key"`
			AcceptedAt     string `json:"accepted_at"`
			RequestCount   int    `json:"request_count"`
		}
		s.mu.Lock()
		items := make([]item, 0, len(s.order))
		for _, m := range s.order {
			items = append(items, item{m.id, m.channel, m.to, m.key, m.acceptedAt.Format(time.RFC3339Nano), s.keyCount[m.key]})
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case r.URL.Path == "/admin/requests" && r.Method == http.MethodGet:
		s.mu.Lock()
		items := append([]requestLog{}, s.requests...)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case r.URL.Path == "/admin/reset" && r.Method == http.MethodPost:
		s.mu.Lock()
		s.reset()
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

// ---- responses ----

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v) // plain maps/structs always marshal
	writeRaw(w, status, b)
}

func writeRaw(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b) // the client may have gone
}
