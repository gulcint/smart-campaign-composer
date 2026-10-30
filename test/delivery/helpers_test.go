// Package delivery_test is the "delivery" test layer (writing-tests §4): the dispatcher of
// internal/delivery against a fake delivery.Store and a scripted httptest provider, on a fake clock
// with a seeded retry policy. No database, no real network, no time.Sleep.
//
// Only the Go names of docs/requirements.md "Worker ve mock provider birim test yüzeyi" are used,
// with the delivery.Store signatures of D53 (PR #99): TryLockChannel returns a
// delivery.ChannelLock, Claim takes limit int, Requeue exists, ids are string and every update
// struct carries the job's Attempts (a row that is not `sending` or has other attempts is
// delivery.ErrLeaseLost). Whether FirstAttemptAt / NextAttemptAt are time.Time or *time.Time is
// not named, so those fields go through small reflection helpers.
package delivery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

// t0 is the test reference time of docs/requirements.md.
var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// guard bounds waits on other goroutines (wall clock safety net only; no assertion depends on it).
// Every time the tests assert on is fake-clock time, so a slow, loaded machine (-race, parallel
// packages) only needs a generous net: the shared-limiter test drives about 1600 fake-clock steps
// and 160 HTTP round trips.
const guard = 2 * time.Minute

const campaignID = "0b7e4f2a-1c3d-4e5f-8a9b-0c1d2e3f4a5b"

// id returns a distinct, valid UUID string for n.
func id(prefix byte, n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, prefix) }

// ---------------------------------------------------------------------------------------------
// reflection helpers for time fields whose Go type requirements.md does not name

func setTimeValue(v reflect.Value, tm time.Time) error {
	switch v.Type() {
	case reflect.TypeFor[time.Time]():
		v.Set(reflect.ValueOf(tm))
	case reflect.TypeFor[*time.Time]():
		v.Set(reflect.ValueOf(&tm))
	default:
		return fmt.Errorf("time field has type %s, want time.Time or *time.Time", v.Type())
	}
	return nil
}

func setTime(t *testing.T, field any, tm time.Time) {
	t.Helper()
	if err := setTimeValue(reflect.ValueOf(field).Elem(), tm); err != nil {
		t.Fatal(err)
	}
}

// timeOf reads a time.Time or *time.Time; ok is false for nil or the zero time.
func timeOf(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case *time.Time:
		if x == nil {
			return time.Time{}, false
		}
		return *x, !x.IsZero()
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------------------------------
// fake delivery.Store

type row struct {
	job        delivery.Job
	status     string // queued | sending | sent | failed
	next       time.Time
	leaseUntil time.Time
}

type fakeStore struct {
	t        *testing.T
	mu       sync.Mutex
	rows     map[string]*row
	order    []string
	sent     map[string][]delivery.SentUpdate
	retries  map[string][]delivery.RetryUpdate
	failed   map[string][]delivery.FailedUpdate
	reclaims int
	requeues int
}

var _ delivery.Store = (*fakeStore)(nil)

func newStore(t *testing.T) *fakeStore {
	return &fakeStore{
		t:       t,
		rows:    map[string]*row{},
		sent:    map[string][]delivery.SentUpdate{},
		retries: map[string][]delivery.RetryUpdate{},
		failed:  map[string][]delivery.FailedUpdate{},
	}
}

// add queues j, due at next.
func (s *fakeStore) add(j delivery.Job, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[j.ID] = &row{job: j, status: "queued", next: next}
	s.order = append(s.order, j.ID)
}

// addSending stores j as an in-flight row whose lease ends at leaseUntil (a worker took it and
// stopped before writing an outcome).
func (s *fakeStore) addSending(j delivery.Job, leaseUntil time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[j.ID] = &row{job: j, status: "sending", leaseUntil: leaseUntil}
	s.order = append(s.order, j.ID)
}

// heldLock is a ChannelLock that is always held (ProcessOnce takes no lock).
type heldLock struct{}

func (heldLock) Held(context.Context) (bool, error) { return true, nil }
func (heldLock) Release()                           {}

func (s *fakeStore) TryLockChannel(_ context.Context, _ delivery.Channel) (delivery.ChannelLock, bool, error) {
	return heldLock{}, true, nil
}

func (s *fakeStore) Claim(_ context.Context, ch delivery.Channel, now time.Time, limit int, leaseUntil time.Time) ([]delivery.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []delivery.Job
	for _, k := range s.order {
		if len(out) >= limit {
			break
		}
		r := s.rows[k]
		if r.status != "queued" || r.job.Channel != ch || r.next.After(now) {
			continue
		}
		r.status = "sending"
		r.leaseUntil = leaseUntil
		r.job.Attempts++
		if _, ok := timeOf(r.job.FirstAttemptAt); !ok {
			if err := setTimeValue(reflect.ValueOf(&r.job.FirstAttemptAt).Elem(), now); err != nil {
				s.t.Error(err)
			}
		}
		out = append(out, r.job)
	}
	return out, nil
}

func (s *fakeStore) ReclaimExpired(_ context.Context, ch delivery.Channel, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reclaims++
	n := 0
	for _, k := range s.order {
		r := s.rows[k]
		if r.status == "sending" && r.job.Channel == ch && r.leaseUntil.Before(now) {
			r.status = "queued"
			r.next = now
			n++
		}
	}
	return n, nil
}

// Requeue returns claimed jobs whose provider request never started to queued, without changing
// any counter.
func (s *fakeStore) Requeue(_ context.Context, jobs []delivery.Job, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requeues++
	n := 0
	for _, j := range jobs {
		if r, ok := s.rows[j.ID]; ok && r.status == "sending" {
			r.status = "queued"
			r.next = now
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) take(id string, attempts int) (*row, error) {
	r, ok := s.rows[id]
	if !ok || r.status != "sending" || r.job.Attempts != attempts {
		return nil, delivery.ErrLeaseLost
	}
	return r, nil
}

func (s *fakeStore) MarkSent(_ context.Context, id string, u delivery.SentUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.take(id, u.Attempts)
	if err != nil {
		return err
	}
	r.status = "sent"
	s.sent[id] = append(s.sent[id], u)
	return nil
}

func (s *fakeStore) ScheduleRetry(_ context.Context, id string, u delivery.RetryUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.take(id, u.Attempts)
	if err != nil {
		return err
	}
	next, ok := timeOf(u.NextAttemptAt)
	if !ok {
		s.t.Errorf("ScheduleRetry(%s) without NextAttemptAt: %+v", id, u)
	}
	r.status = "queued"
	r.next = next
	switch u.Count {
	case delivery.CountFailure:
		r.job.FailedAttempts++
	case delivery.CountRateLimited:
		r.job.RateLimited++
	}
	s.retries[id] = append(s.retries[id], u)
	return nil
}

func (s *fakeStore) MarkFailed(_ context.Context, id string, u delivery.FailedUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.take(id, u.Attempts)
	if err != nil {
		return err
	}
	r.status = "failed"
	s.failed[id] = append(s.failed[id], u)
	return nil
}

func (s *fakeStore) job(k string) delivery.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[k].job
}

func (s *fakeStore) status(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[k].status
}

func (s *fakeStore) outcomes(k string) ([]delivery.SentUpdate, []delivery.RetryUpdate, []delivery.FailedUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]delivery.SentUpdate(nil), s.sent[k]...),
		append([]delivery.RetryUpdate(nil), s.retries[k]...),
		append([]delivery.FailedUpdate(nil), s.failed[k]...)
}

func (s *fakeStore) reclaimCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaims
}

// ---------------------------------------------------------------------------------------------
// scripted httptest provider (the mock provider's HTTP surface of REQ-MOCK-01, replay of REQ-MOCK-02)

type step struct {
	status     int
	retryAfter string
	lost       bool // accept and store, then close the connection without an answer
	hang       bool // never answer (client timeout)
}

// provReq is one request the provider saw. body is the SMS text; subject, text and html are the
// email fields (provider bodies of REQ-MOCK-01: {"to","body"} and {"to","subject","text","html"}).
type provReq struct {
	key, path, to             string
	body, subject, text, html string
	replayed                  bool
}

type fakeProvider struct {
	mu       sync.Mutex
	scripts  map[string][]step
	accepted map[string]string // Idempotency-Key -> provider message id
	reqs     []provReq
	nextID   int
}

// newProvider starts the scripted provider with keep-alives off: every request arrives on its own
// connection. Otherwise whether a request reuses an idle connection depends on goroutine timing
// (CPU load), and net/http's Transport silently replays a request that carries an Idempotency-Key
// when a REUSED connection closes without an answer, so a scripted "lost response" could turn into
// an extra, unscripted replay on a loaded machine. With one connection per request the provider sees
// exactly the requests the dispatcher made, every run.
func newProvider(t *testing.T) (*fakeProvider, *httptest.Server) {
	t.Helper()
	p := &fakeProvider{scripts: map[string][]step{}, accepted: map[string]string{}}
	srv := httptest.NewUnstartedServer(p)
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	t.Cleanup(srv.Close)
	return p, srv
}

func (p *fakeProvider) script(key string, steps ...step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scripts[key] = append(p.scripts[key], steps...)
}

// accept records key as already accepted (a previous worker's request reached the provider).
func (p *fakeProvider) accept(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	p.accepted[key] = fmt.Sprintf("msg_%032x", p.nextID)
}

func (p *fakeProvider) requests() []provReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provReq(nil), p.reqs...)
}

func (p *fakeProvider) messageID(key string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted[key]
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		To      string `json:"to"`
		Body    string `json:"body"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
		HTML    string `json:"html"`
	}
	_ = json.Unmarshal(raw, &body)
	key := r.Header.Get("Idempotency-Key")
	channel := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "/messages")
	seen := provReq{key: key, path: r.URL.Path, to: body.To, body: body.Body, subject: body.Subject, text: body.Text, html: body.HTML}

	p.mu.Lock()
	if mid, ok := p.accepted[key]; ok {
		seen.replayed = true
		p.reqs = append(p.reqs, seen)
		p.mu.Unlock()
		w.Header().Set("Idempotent-Replayed", "true")
		writeAccepted(w, mid, channel, key, true)
		return
	}
	p.reqs = append(p.reqs, seen)
	st := step{status: http.StatusAccepted}
	if q := p.scripts[key]; len(q) > 0 {
		st, p.scripts[key] = q[0], q[1:]
	}
	var mid string
	if st.lost || st.status == http.StatusAccepted {
		p.nextID++
		mid = fmt.Sprintf("msg_%032x", p.nextID)
		p.accepted[key] = mid
	}
	p.mu.Unlock()

	switch {
	case st.hang:
		<-r.Context().Done()
	case st.lost:
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	case st.status == http.StatusAccepted:
		writeAccepted(w, mid, channel, key, false)
	default:
		if st.retryAfter != "" {
			w.Header().Set("Retry-After", st.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st.status)
		_, _ = io.WriteString(w, `{"error":{"code":"scripted","message":"scripted answer"}}`)
	}
}

func writeAccepted(w http.ResponseWriter, mid, channel, key string, duplicate bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": mid, "status": "accepted", "channel": channel, "idempotency_key": key, "duplicate": duplicate,
	})
}

// ---------------------------------------------------------------------------------------------
// limiters

// countingLimiter is a delivery.RateLimiter that never blocks and counts permits.
type countingLimiter struct {
	mu sync.Mutex
	n  int
}

func (l *countingLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return ctx.Err()
}

func (l *countingLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// probedLimiter forwards to a shared limiter and lets the probe see every caller inside Wait and
// stamp every permit.
type probedLimiter struct {
	inner delivery.RateLimiter
	p     *probe
	who   int
}

func (l probedLimiter) Wait(ctx context.Context) error {
	l.p.enter()
	err := l.inner.Wait(ctx)
	if err == nil {
		l.p.grant(l.who)
	}
	l.p.exit()
	return err
}

// ---------------------------------------------------------------------------------------------
// logs

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLogger() (*slog.Logger, *syncBuffer) {
	b := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})), b
}

// records returns the JSON log records of b.
func records(t *testing.T, b *syncBuffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// dispatcher wiring

func baseConfig() delivery.DispatcherConfig {
	return delivery.DispatcherConfig{
		Channel:           delivery.ChannelSMS,
		BatchSize:         50,
		Concurrency:       32,
		PollInterval:      200 * time.Millisecond,
		Lease:             60 * time.Second,
		LockRetryInterval: time.Second,
	}
}

func seededPolicy() *delivery.RetryPolicy {
	return delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), rand.New(rand.NewPCG(20260115, 7))) //nolint:gosec // seeded jitter for deterministic tests
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	t.Cleanup(cancel)
	return ctx
}

// smsTemplate renders to 150 + len(first_name) runes: "Ayşe" fits in 160, a 20-rune name does not.
var smsTemplate = "Merhaba {{first_name}}, " + strings.Repeat("a", 140)

// subjectTemplate renders to 24 + len(first_name) runes: a 70-rune name exceeds 80.
const subjectTemplate = "{{first_name}}, sepetin seni bekliyor"

func template() content.Content {
	return content.Content{
		SMS:   content.SMS{Body: smsTemplate},
		Email: content.Email{Subject: subjectTemplate, Body: "Merhaba {{first_name}},\n\nSepetindeki ürünler seni bekliyor."},
	}
}

type jobSpec struct {
	n         int
	consent   bool
	to        string
	firstName string
	failed    int
	limited   int
	firstAt   time.Time // zero = set by the store at the first claim
}

func newJob(t *testing.T, cfg delivery.DispatcherConfig, s jobSpec) delivery.Job {
	t.Helper()
	j := delivery.Job{
		ID:             id('d', s.n),
		CampaignID:     campaignID,
		CustomerID:     id('c', s.n),
		Channel:        cfg.Channel,
		To:             s.to,
		Consent:        s.consent,
		Recipient:      content.Recipient{FirstName: s.firstName, LastName: "Yılmaz", City: "İstanbul"},
		Content:        template(),
		FailedAttempts: s.failed,
		RateLimited:    s.limited,
	}
	if !s.firstAt.IsZero() {
		setTime(t, &j.FirstAttemptAt, s.firstAt)
	}
	return j
}

func smsTo(n int) string   { return fmt.Sprintf("+90500000%04d", n%10000) }
func emailTo(n int) string { return fmt.Sprintf("musteri%d@example.com", n) }

func newDispatcher(t *testing.T, cfg delivery.DispatcherConfig, st delivery.Store, srv *httptest.Server, lim delivery.RateLimiter, c clock.Clock, logger *slog.Logger, timeout time.Duration) *delivery.Dispatcher {
	t.Helper()
	hc := srv.Client()
	hc.Timeout = timeout
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	d, err := delivery.NewDispatcher(cfg, delivery.DispatcherDeps{
		Store:    st,
		Provider: provider.NewHTTPClient(srv.URL, hc, c),
		Limiter:  lim,
		Policy:   seededPolicy(),
		Clock:    c,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	return d
}

// ---------------------------------------------------------------------------------------------
// fake-clock driver for a real TokenBucket shared by several dispatchers (same idea as
// internal/delivery's unit tests)

type grant struct {
	who int
	at  time.Time
}

// probe is the clock.Clock of the shared limiter and the dispatchers. A caller is "active" while
// inside the limiter's Wait; it stamps its permit with the clock's time before leaving. The driver
// moves time only when every active caller is parked on a fake timer, so time never moves past an
// unstamped permit. It does not assume how many permits follow a fired timer: woken callers may
// re-arm for a later slot.
type probe struct {
	fake   *clock.Fake
	mu     sync.Mutex
	active int
	grants []grant
	notify chan struct{}
}

func newProbe() *probe { return &probe{fake: clock.NewFake(t0), notify: make(chan struct{}, 1)} }

func (p *probe) signal() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *probe) Now() time.Time { return p.fake.Now() }

func (p *probe) After(d time.Duration) <-chan time.Time {
	ch := p.fake.After(d)
	p.signal()
	return ch
}

var _ clock.Clock = (*probe)(nil)

func (p *probe) enter() {
	p.mu.Lock()
	p.active++
	p.mu.Unlock()
	p.signal()
}

func (p *probe) exit() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	p.signal()
}

func (p *probe) grant(who int) {
	p.mu.Lock()
	p.grants = append(p.grants, grant{who: who, at: p.fake.Now()})
	p.mu.Unlock()
	p.signal()
}

// drive advances the fake clock in steps of step, only while every active caller is parked on the
// clock, until total permits have been given.
func (p *probe) drive(t *testing.T, step time.Duration, total int) []grant {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	for {
		p.mu.Lock()
		if len(p.grants) >= total {
			out := append([]grant(nil), p.grants...)
			p.mu.Unlock()
			return out
		}
		settled := p.active > 0 && p.fake.Waiters() >= p.active
		if settled {
			p.fake.Advance(step)
		}
		given, active := len(p.grants), p.active
		p.mu.Unlock()
		if settled {
			continue
		}
		select {
		case <-p.notify:
		case <-ctx.Done():
			t.Fatalf("stuck at %v: %d of %d permits, %d callers in Wait, %d parked on the clock", p.fake.Now(), given, total, active, p.fake.Waiters())
		}
	}
}

func sortedTimes(gs []grant) []time.Time {
	out := make([]time.Time, len(gs))
	for i, g := range gs {
		out[i] = g.at
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func maxPerWindow(ts []time.Time, w time.Duration) (int, time.Time) {
	best, start, j := 0, time.Time{}, 0
	for i := range ts {
		if j < i {
			j = i
		}
		for j < len(ts) && ts[j].Before(ts[i].Add(w)) {
			j++
		}
		if j-i > best {
			best, start = j-i, ts[i]
		}
	}
	return best, start
}
