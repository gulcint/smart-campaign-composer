package delivery_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
)

// REQ-SND-04, REQ-SND-03, REQ-SND-02
// ProcessOnce turns one provider outcome into the retry table's store write: 2xx -> MarkSent;
// 5xx/408/timeout/connection error -> ScheduleRetry counted as a failure (full-jitter backoff,
// n = 1 -> [0, 1s)); 429 -> ScheduleRetry counted against the 429 budget (Retry-After honoured);
// other 4xx -> MarkFailed provider_rejected; the 5th failure and the 20th 429 -> MarkFailed.
// HTTPStatus is 0 on a transport error and LastError is from the closed last_error set. Every
// delivery makes exactly one provider request with Idempotency-Key = Job.ID, after one permit.
func TestDispatcher_ProcessOnceOutcomes(t *testing.T) {
	t.Parallel()
	now := t0.Add(time.Minute)
	type wantRetry struct {
		u        delivery.RetryUpdate
		min, max time.Duration // NextAttemptAt in [now+min, now+max]
	}
	tests := []struct {
		name      string
		steps     []step
		spec      jobSpec
		timeout   time.Duration
		wantSent  bool
		wantRetry *wantRetry
		wantFail  *delivery.FailedUpdate
	}{
		{name: "202 is sent", steps: []step{{status: 202}}, wantSent: true},
		{
			name: "500 is retried as a failure", steps: []step{{status: 500}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 500, LastError: "http_500"}, max: time.Second - 1},
		},
		{
			name: "503 is retried as a failure", steps: []step{{status: 503}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 503, LastError: "http_503"}, max: time.Second - 1},
		},
		{
			name: "408 is retried as a failure", steps: []step{{status: 408}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 408, LastError: "http_408"}, max: time.Second - 1},
		},
		{
			name: "lost response is retried as connection_error", steps: []step{{lost: true}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 0, LastError: "connection_error"}, max: time.Second - 1},
		},
		{
			// PROVIDER_TIMEOUT is wall-clock time by requirement (not the injected clock), so it must
			// leave room for the dial and the write on a loaded machine: the request has to reach the
			// provider (exactly one request is asserted) before the client gives up.
			name: "provider timeout is retried as timeout", steps: []step{{hang: true}}, timeout: 2 * time.Second,
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 0, LastError: "timeout"}, max: time.Second - 1},
		},
		{
			name: "429 with Retry-After 3 is retried exactly 3 s later", steps: []step{{status: 429, retryAfter: "3"}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountRateLimited, HTTPStatus: 429, LastError: "http_429"}, min: 3 * time.Second, max: 3 * time.Second},
		},
		{
			name: "429 without Retry-After backs off", steps: []step{{status: 429}},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountRateLimited, HTTPStatus: 429, LastError: "http_429"}, max: time.Second - 1},
		},
		{
			name: "third failure backs off below 4 s", steps: []step{{status: 500}}, spec: jobSpec{failed: 2, firstAt: t0},
			wantRetry: &wantRetry{u: delivery.RetryUpdate{Count: delivery.CountFailure, HTTPStatus: 500, LastError: "http_500"}, max: 4*time.Second - 1},
		},
		{
			name: "400 is provider_rejected", steps: []step{{status: 400}},
			wantFail: &delivery.FailedUpdate{Reason: delivery.FailureProviderRejected, HTTPStatus: 400, LastError: "http_400"},
		},
		{
			name: "409 is provider_rejected", steps: []step{{status: 409}},
			wantFail: &delivery.FailedUpdate{Reason: delivery.FailureProviderRejected, HTTPStatus: 409, LastError: "http_409"},
		},
		{
			name: "fifth failure is max_attempts_exceeded", steps: []step{{status: 500}}, spec: jobSpec{failed: 4, firstAt: t0},
			wantFail: &delivery.FailedUpdate{Reason: delivery.FailureMaxAttempts, Count: delivery.CountFailure, HTTPStatus: 500, LastError: "http_500"},
		},
		{
			name: "20th 429 is rate_limit_budget_exhausted", steps: []step{{status: 429, retryAfter: "1"}}, spec: jobSpec{limited: 19, firstAt: t0},
			wantFail: &delivery.FailedUpdate{Reason: delivery.FailureRateLimitBudget, Count: delivery.CountRateLimited, HTTPStatus: 429, LastError: "http_429"},
		},
		{
			name: "429 ten minutes after the first attempt is rate_limit_budget_exhausted", steps: []step{{status: 429, retryAfter: "1"}}, spec: jobSpec{limited: 1, firstAt: now.Add(-10 * time.Minute)},
			wantFail: &delivery.FailedUpdate{Reason: delivery.FailureRateLimitBudget, Count: delivery.CountRateLimited, HTTPStatus: 429, LastError: "http_429"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := clock.NewFake(t0)
			fake.Advance(time.Minute)
			cfg := baseConfig()
			st := newStore(t)
			spec := tc.spec
			spec.n, spec.consent, spec.to, spec.firstName = 1, true, smsTo(1), "Ayşe"
			j := newJob(t, cfg, spec)
			key := j.ID
			st.add(j, now)
			p, srv := newProvider(t)
			p.script(key, tc.steps...)
			lim := &countingLimiter{}
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			d := newDispatcher(t, cfg, st, srv, lim, fake, nil, timeout)

			n, err := d.ProcessOnce(ctxT(t))
			if err != nil || n != 1 {
				t.Fatalf("ProcessOnce() = (%d, %v), want (1, nil)", n, err)
			}
			reqs := p.requests()
			if len(reqs) != 1 || reqs[0].key != key {
				t.Fatalf("provider requests = %+v, want exactly one with Idempotency-Key %s", reqs, key)
			}
			if got := lim.count(); got != 1 {
				t.Fatalf("limiter permits = %d, want 1 per provider request", got)
			}

			sent, retries, failed := st.outcomes(key)
			switch {
			case tc.wantSent:
				if len(sent) != 1 || len(retries) != 0 || len(failed) != 0 {
					t.Fatalf("outcomes sent=%d retry=%d failed=%d, want only one MarkSent", len(sent), len(retries), len(failed))
				}
				if sent[0].HTTPStatus != 202 || sent[0].ProviderMessageID != p.messageID(key) {
					t.Fatalf("SentUpdate = %+v, want HTTPStatus 202 and ProviderMessageID %q", sent[0], p.messageID(key))
				}
				if at, _ := timeOf(sent[0].At); !at.Equal(now) {
					t.Fatalf("SentUpdate.At = %v, want the clock's now %v", sent[0].At, now)
				}
			case tc.wantRetry != nil:
				if len(retries) != 1 || len(sent) != 0 || len(failed) != 0 {
					t.Fatalf("outcomes sent=%d retry=%d failed=%d, want only one ScheduleRetry", len(sent), len(retries), len(failed))
				}
				u, w := retries[0], tc.wantRetry.u
				if u.Count != w.Count || u.HTTPStatus != w.HTTPStatus || u.LastError != w.LastError {
					t.Fatalf("RetryUpdate = %+v, want Count %v HTTPStatus %d LastError %v", u, w.Count, w.HTTPStatus, w.LastError)
				}
				next, _ := timeOf(u.NextAttemptAt)
				if next.Before(now.Add(tc.wantRetry.min)) || next.After(now.Add(tc.wantRetry.max)) {
					t.Fatalf("NextAttemptAt = %v, want in [now+%v, now+%v]", next, tc.wantRetry.min, tc.wantRetry.max)
				}
				if got := st.status(key); got != "queued" {
					t.Fatalf("row status after ScheduleRetry = %s (fake store), want queued", got)
				}
			default:
				if len(failed) != 1 || len(sent) != 0 || len(retries) != 0 {
					t.Fatalf("outcomes sent=%d retry=%d failed=%d, want only one MarkFailed", len(sent), len(retries), len(failed))
				}
				u, w := failed[0], tc.wantFail
				if u.Reason != w.Reason || u.HTTPStatus != w.HTTPStatus || u.LastError != w.LastError {
					t.Fatalf("FailedUpdate = %+v, want Reason %v HTTPStatus %d LastError %v", u, w.Reason, w.HTTPStatus, w.LastError)
				}
				if w.Count != (delivery.FailedUpdate{}).Count && u.Count != w.Count {
					t.Fatalf("FailedUpdate.Count = %v, want %v", u.Count, w.Count)
				}
			}
		})
	}
}

// REQ-SND-03, REQ-SND-02
// "Accepted but the response was lost" never sends twice: every attempt of a delivery carries the
// same Idempotency-Key (= Job.ID), so after a 500 and a lost response the third request is answered
// as a replay and the delivery is marked sent once. Every provider request, retries included, took
// one permit of the limiter.
func TestDispatcher_SameIdempotencyKeyAcrossRetries(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(t0)
	cfg := baseConfig()
	st := newStore(t)
	j := newJob(t, cfg, jobSpec{n: 7, consent: true, to: smsTo(7), firstName: "Ayşe"})
	key := j.ID
	st.add(j, t0)
	p, srv := newProvider(t)
	p.script(key, step{status: 500}, step{lost: true})
	lim := &countingLimiter{}
	d := newDispatcher(t, cfg, st, srv, lim, fake, nil, 10*time.Second)

	// Backoff caps: n = 1 -> 1 s, n = 2 -> 2 s; advancing past each cap makes the retry due.
	for round, advance := range []time.Duration{0, 2 * time.Second, 3 * time.Second} {
		fake.Advance(advance)
		n, err := d.ProcessOnce(ctxT(t))
		if err != nil || n != 1 {
			t.Fatalf("round %d: ProcessOnce() = (%d, %v), want (1, nil)", round+1, n, err)
		}
	}
	reqs := p.requests()
	if len(reqs) != 3 {
		t.Fatalf("provider requests = %d, want 3 (500, lost response, replay)", len(reqs))
	}
	for i, r := range reqs {
		if r.key != key {
			t.Fatalf("request %d Idempotency-Key = %q, want the delivery id %q", i+1, r.key, key)
		}
	}
	if !reqs[2].replayed {
		t.Fatal("third request was not a replay of the accepted message")
	}
	sent, retries, failed := st.outcomes(key)
	if len(sent) != 1 || len(retries) != 2 || len(failed) != 0 {
		t.Fatalf("outcomes sent=%d retry=%d failed=%d, want 1, 2, 0", len(sent), len(retries), len(failed))
	}
	if got := st.job(key).FailedAttempts; got != 2 {
		t.Fatalf("FailedAttempts = %d, want 2 (500 + connection error)", got)
	}
	if got := lim.count(); got != len(reqs) {
		t.Fatalf("limiter permits = %d, want one per provider request (%d)", got, len(reqs))
	}
}

// REQ-SND-03
// A delivery left in `sending` by a stopped worker: ProcessOnce does not reclaim expired leases by
// itself (no provider request, Store.ReclaimExpired not called). After the test calls
// Store.ReclaimExpired explicitly (requirements.md: the lease-expiry test calls it, then
// ProcessOnce), the next ProcessOnce sends again with the same Idempotency-Key, the provider replays
// its earlier acceptance, the delivery is marked sent once and no failed attempt is counted.
func TestDispatcher_LeaseExpiryResendsWithSameKey(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(t0)
	cfg := baseConfig()
	st := newStore(t)
	j := newJob(t, cfg, jobSpec{n: 9, consent: true, to: smsTo(9), firstName: "Ayşe", firstAt: t0})
	j.Attempts = 1
	key := j.ID
	st.addSending(j, t0.Add(cfg.Lease))
	p, srv := newProvider(t)
	p.accept(key) // the stopped worker's request had reached the provider
	lim := &countingLimiter{}
	d := newDispatcher(t, cfg, st, srv, lim, fake, nil, 10*time.Second)

	fake.Advance(cfg.Lease + time.Second)
	n, err := d.ProcessOnce(ctxT(t))
	if err != nil || n != 0 {
		t.Fatalf("ProcessOnce() before reclaim = (%d, %v), want (0, nil)", n, err)
	}
	if len(p.requests()) != 0 || st.reclaimCalls() != 0 {
		t.Fatalf("ProcessOnce sent %d requests and called ReclaimExpired %d times, want 0 and 0", len(p.requests()), st.reclaimCalls())
	}

	reclaimed, err := st.ReclaimExpired(ctxT(t), cfg.Channel, fake.Now())
	if err != nil || reclaimed != 1 {
		t.Fatalf("ReclaimExpired() = (%d, %v), want (1, nil)", reclaimed, err)
	}
	n, err = d.ProcessOnce(ctxT(t))
	if err != nil || n != 1 {
		t.Fatalf("ProcessOnce() after reclaim = (%d, %v), want (1, nil)", n, err)
	}
	reqs := p.requests()
	if len(reqs) != 1 || reqs[0].key != key || !reqs[0].replayed {
		t.Fatalf("provider requests = %+v, want one replayed request with Idempotency-Key %s", reqs, key)
	}
	sent, retries, failed := st.outcomes(key)
	if len(sent) != 1 || len(retries) != 0 || len(failed) != 0 {
		t.Fatalf("outcomes sent=%d retry=%d failed=%d, want 1, 0, 0", len(sent), len(retries), len(failed))
	}
	if got := st.job(key).FailedAttempts; got != 0 {
		t.Fatalf("FailedAttempts = %d, want 0: a lease expiry is not a failed attempt", got)
	}
}

// REQ-AUD-04, REQ-CNT-05, REQ-SND-02
// Checks at send time: a delivery whose consent was withdrawn or whose address is gone fails with
// recipient_unavailable, one whose rendered text exceeds the limit (SMS 160, email subject 80) fails
// with content_too_long. Neither calls the provider nor takes a limiter permit; the sendable
// delivery in the same batch still goes out.
func TestDispatcher_UnsendableDeliveriesSkipProviderAndLimiter(t *testing.T) {
	t.Parallel()
	long20 := strings.Repeat("Ş", 20)
	long70 := strings.Repeat("Ğ", 70)
	type want struct {
		reason   delivery.FailedUpdate // Reason and LastError; zero = sent
		sendable bool
	}
	tests := []struct {
		name  string
		email bool
		jobs  []jobSpec
		want  []want
	}{
		{
			name: "sms",
			jobs: []jobSpec{
				{n: 1, consent: false, to: smsTo(1), firstName: "Ayşe"},
				{n: 2, consent: true, to: "", firstName: "Ayşe"},
				{n: 3, consent: true, to: smsTo(3), firstName: long20},
				{n: 4, consent: true, to: smsTo(4), firstName: "Ayşe"},
			},
			want: []want{
				{reason: delivery.FailedUpdate{Reason: delivery.FailureRecipientUnavailable, LastError: "recipient_unavailable"}},
				{reason: delivery.FailedUpdate{Reason: delivery.FailureRecipientUnavailable, LastError: "recipient_unavailable"}},
				{reason: delivery.FailedUpdate{Reason: delivery.FailureContentTooLong, LastError: "content_too_long"}},
				{sendable: true},
			},
		},
		{
			name:  "email",
			email: true,
			jobs: []jobSpec{
				{n: 1, consent: false, to: emailTo(1), firstName: "Ayşe"},
				{n: 2, consent: true, to: "", firstName: "Ayşe"},
				{n: 3, consent: true, to: emailTo(3), firstName: long70},
				{n: 4, consent: true, to: emailTo(4), firstName: "Ayşe"},
			},
			want: []want{
				{reason: delivery.FailedUpdate{Reason: delivery.FailureRecipientUnavailable, LastError: "recipient_unavailable"}},
				{reason: delivery.FailedUpdate{Reason: delivery.FailureRecipientUnavailable, LastError: "recipient_unavailable"}},
				{reason: delivery.FailedUpdate{Reason: delivery.FailureContentTooLong, LastError: "content_too_long"}},
				{sendable: true},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := clock.NewFake(t0)
			cfg := baseConfig()
			if tc.email {
				cfg.Channel = delivery.ChannelEmail
			}
			st := newStore(t)
			keys := make([]string, len(tc.jobs))
			for i, s := range tc.jobs {
				j := newJob(t, cfg, s)
				keys[i] = j.ID
				st.add(j, t0)
			}
			p, srv := newProvider(t)
			lim := &countingLimiter{}
			d := newDispatcher(t, cfg, st, srv, lim, fake, nil, 10*time.Second)

			n, err := d.ProcessOnce(ctxT(t))
			if err != nil || n != len(tc.jobs) {
				t.Fatalf("ProcessOnce() = (%d, %v), want (%d, nil)", n, err, len(tc.jobs))
			}
			sendable := 0
			for i, w := range tc.want {
				sent, retries, failed := st.outcomes(keys[i])
				if w.sendable {
					sendable++
					if len(sent) != 1 {
						t.Fatalf("job %d: MarkSent calls = %d, want 1", i+1, len(sent))
					}
					continue
				}
				if len(failed) != 1 || len(sent) != 0 || len(retries) != 0 {
					t.Fatalf("job %d: outcomes sent=%d retry=%d failed=%d, want one MarkFailed", i+1, len(sent), len(retries), len(failed))
				}
				u := failed[0]
				if u.Reason != w.reason.Reason || u.LastError != w.reason.LastError || u.HTTPStatus != 0 {
					t.Fatalf("job %d: FailedUpdate = %+v, want Reason %v LastError %v HTTPStatus 0", i+1, u, w.reason.Reason, w.reason.LastError)
				}
			}
			reqs := p.requests()
			if len(reqs) != sendable {
				t.Fatalf("provider requests = %d, want %d: unsendable deliveries never reach the provider", len(reqs), sendable)
			}
			if reqs[0].key != keys[len(keys)-1] {
				t.Fatalf("provider request key = %s, want the sendable delivery %s", reqs[0].key, keys[len(keys)-1])
			}
			if got := lim.count(); got != sendable {
				t.Fatalf("limiter permits = %d, want %d: unsendable deliveries take no permit", got, sendable)
			}
		})
	}
}

// REQ-SND-02
// The limit does not depend on how many workers run: two SMS dispatchers working the same queue
// share one delivery.NewTokenBucket (10ms = 100/s). Measured on the fake clock, all their provider
// requests together get at most 100 permits in every half-open 1 s window, every request took one
// permit and every delivery was sent.
func TestDispatcher_SharedLimiterAcrossDispatchers(t *testing.T) {
	t.Parallel()
	const jobs = 160
	pr := newProbe()
	cfg := baseConfig()
	cfg.BatchSize = jobs / 2
	st := newStore(t)
	for i := 0; i < jobs; i++ {
		st.add(newJob(t, cfg, jobSpec{n: i + 1, consent: true, to: smsTo(i + 1), firstName: "Ayşe"}), t0)
	}
	p, srv := newProvider(t)
	shared := delivery.NewTokenBucket(pr, 10*time.Millisecond)

	type result struct {
		n   int
		err error
	}
	results := make(chan result, 2)
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	for w := 0; w < 2; w++ {
		d := newDispatcher(t, cfg, st, srv, probedLimiter{inner: shared, p: pr, who: w}, pr, nil, 10*time.Second)
		go func() {
			n, err := d.ProcessOnce(ctx)
			results <- result{n, err}
		}()
	}
	got := pr.drive(t, time.Millisecond, jobs)
	total := 0
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("ProcessOnce() error = %v", r.err)
			}
			total += r.n
		case <-ctx.Done():
			t.Fatal("a dispatcher did not finish its batch")
		}
	}
	if total != jobs {
		t.Fatalf("deliveries processed = %d, want %d", total, jobs)
	}
	if reqs := len(p.requests()); reqs != jobs {
		t.Fatalf("provider requests = %d, want %d (one permit each)", reqs, jobs)
	}
	ts := sortedTimes(got)
	if count, start := maxPerWindow(ts, time.Second); count > 100 {
		t.Fatalf("%d permits in [%v, +1s) across two dispatchers, want at most 100", count, start)
	}
	for i := 1; i < len(ts); i++ {
		if gap := ts[i].Sub(ts[i-1]); gap < 10*time.Millisecond {
			t.Fatalf("permits %d and %d are %v apart, want at least 10ms", i-1, i, gap)
		}
	}
}

// REQ-SEC-01
// The worker's delivery log records (JSON through the injected *slog.Logger): `delivery sent` (INFO),
// `delivery retry scheduled` (WARN, + next_attempt_at and reason http_<code> / timeout /
// connection_error), `delivery failed` (ERROR, + failure_reason), each with delivery_id,
// campaign_id, customer_id, channel, attempt and http_status. No record has a to / phone / email /
// body key, and no value holds "@example.", "+90500000", the recipient's name or the message text.
func TestDispatcher_DeliveryLogs(t *testing.T) {
	t.Parallel()
	type wantRec struct {
		msg, level  string
		httpStatus  float64
		checkStatus bool
		extra       map[string]string // key -> exact value ("" = present)
	}
	type job struct {
		spec  jobSpec
		steps []step
		want  wantRec
	}
	sent := func(code float64) wantRec {
		return wantRec{msg: "delivery sent", level: "INFO", httpStatus: code, checkStatus: true}
	}
	// code 0 = transport error: http_status must be present, its value is not named by the REQ.
	retry := func(code float64, reason string) wantRec {
		return wantRec{msg: "delivery retry scheduled", level: "WARN", httpStatus: code, checkStatus: code != 0, extra: map[string]string{"next_attempt_at": "", "reason": reason}}
	}
	failed := func(reason string) wantRec {
		return wantRec{msg: "delivery failed", level: "ERROR", extra: map[string]string{"failure_reason": reason}}
	}
	const first, last = "Gülizar", "Karaoğlan"
	tests := []struct {
		name  string
		email bool
		jobs  []job
	}{
		{
			name: "sms",
			jobs: []job{
				{spec: jobSpec{n: 1, consent: true, to: smsTo(1)}, steps: []step{{status: 202}}, want: sent(202)},
				{spec: jobSpec{n: 2, consent: true, to: smsTo(2)}, steps: []step{{status: 500}}, want: retry(500, "http_500")},
				{spec: jobSpec{n: 3, consent: true, to: smsTo(3)}, steps: []step{{lost: true}}, want: retry(0, "connection_error")},
				{spec: jobSpec{n: 4, consent: true, to: smsTo(4)}, steps: []step{{status: 429, retryAfter: "2"}}, want: retry(429, "http_429")},
				{spec: jobSpec{n: 5, consent: true, to: smsTo(5)}, steps: []step{{status: 400}}, want: func() wantRec { w := failed("provider_rejected"); w.httpStatus, w.checkStatus = 400, true; return w }()},
				{spec: jobSpec{n: 6, consent: false, to: smsTo(6)}, want: failed("recipient_unavailable")},
				{spec: jobSpec{n: 7, consent: true, to: smsTo(7), failed: 4, firstAt: t0}, steps: []step{{status: 503}}, want: failed("max_attempts_exceeded")},
			},
		},
		{
			name:  "email",
			email: true,
			jobs: []job{
				{spec: jobSpec{n: 1, consent: true, to: emailTo(1)}, steps: []step{{status: 202}}, want: sent(202)},
				{spec: jobSpec{n: 2, consent: true, to: emailTo(2)}, steps: []step{{status: 503}}, want: retry(503, "http_503")},
				{spec: jobSpec{n: 3, consent: true, to: emailTo(3)}, steps: []step{{status: 422}}, want: failed("provider_rejected")},
				{spec: jobSpec{n: 4, consent: false, to: emailTo(4)}, want: failed("recipient_unavailable")},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := clock.NewFake(t0)
			cfg := baseConfig()
			channel := "sms"
			if tc.email {
				cfg.Channel = delivery.ChannelEmail
				channel = "email"
			}
			st := newStore(t)
			p, srv := newProvider(t)
			wants := map[string]wantRec{}
			customers := map[string]string{}
			for _, jb := range tc.jobs {
				spec := jb.spec
				spec.firstName = first
				j := newJob(t, cfg, spec)
				j.Recipient.LastName = last
				k := j.ID
				st.add(j, t0)
				p.script(k, jb.steps...)
				wants[k] = jb.want
				customers[k] = j.CustomerID
			}
			logger, buf := newLogger()
			d := newDispatcher(t, cfg, st, srv, &countingLimiter{}, fake, logger, 10*time.Second)
			if n, err := d.ProcessOnce(ctxT(t)); err != nil || n != len(tc.jobs) {
				t.Fatalf("ProcessOnce() = (%d, %v), want (%d, nil)", n, err, len(tc.jobs))
			}

			seen := map[string]bool{}
			for _, rec := range records(t, buf) {
				msg, _ := rec["msg"].(string)
				if msg != "delivery sent" && msg != "delivery retry scheduled" && msg != "delivery failed" {
					continue
				}
				checkNoPersonalData(t, rec)
				did, _ := rec["delivery_id"].(string)
				w, ok := wants[did]
				if !ok {
					t.Fatalf("%q record has delivery_id %q, want one of this batch: %v", msg, did, rec)
				}
				if msg != w.msg {
					continue // e.g. a retry record is not this delivery's expected final record
				}
				seen[did] = true
				if rec["level"] != w.level {
					t.Fatalf("%q level = %v, want %s", msg, rec["level"], w.level)
				}
				for _, k := range []string{"delivery_id", "campaign_id", "customer_id", "channel", "attempt", "http_status"} {
					if _, ok := rec[k]; !ok {
						t.Fatalf("%q record lacks %q: %v", msg, k, rec)
					}
				}
				if rec["campaign_id"] != campaignID || rec["customer_id"] != customers[did] || rec["channel"] != channel {
					t.Fatalf("%q record ids = campaign %v customer %v channel %v, want %s %s %s", msg, rec["campaign_id"], rec["customer_id"], rec["channel"], campaignID, customers[did], channel)
				}
				if w.checkStatus && rec["http_status"] != w.httpStatus {
					t.Fatalf("%q http_status = %v, want %v", msg, rec["http_status"], w.httpStatus)
				}
				for k, v := range w.extra {
					got, ok := rec[k]
					if !ok {
						t.Fatalf("%q record lacks %q: %v", msg, k, rec)
					}
					if v != "" && got != v {
						t.Fatalf("%q %s = %v, want %q", msg, k, got, v)
					}
				}
			}
			for k, w := range wants {
				if !seen[k] {
					t.Fatalf("no %q record for delivery %s", w.msg, k)
				}
			}
		})
	}
}

// checkNoPersonalData fails if a delivery log record has a forbidden key at any depth or a value
// with an address, a phone number, the recipient's name or the message text (REQ-SEC-01).
func checkNoPersonalData(t *testing.T, rec map[string]any) {
	t.Helper()
	forbiddenKeys := map[string]bool{"to": true, "phone": true, "email": true, "body": true}
	forbiddenText := []string{"@example.", "+90500000", "Gülizar", "Karaoğlan", "Merhaba", "sepetin seni bekliyor", "Sepetindeki"}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				if forbiddenKeys[k] {
					t.Fatalf("delivery log record has forbidden key %q at %s: %v", k, path, rec)
				}
				walk(path+"."+k, vv)
			}
		case []any:
			for _, vv := range x {
				walk(path+"[]", vv)
			}
		case string:
			for _, s := range forbiddenText {
				if strings.Contains(x, s) {
					t.Fatalf("delivery log value at %s contains %q: %v", path, s, rec)
				}
			}
		}
	}
	walk("$", rec)
}
