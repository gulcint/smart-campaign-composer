package e2e_test

// P5 delivery end to end against the compose.test.yml stack: api + worker + the real mock provider
// (MOCK_PROVIDER_ADMIN=true so /admin/* is available, MOCK_PROVIDER_RATE_LIMIT_RATE=0 so random 429s
// are off; REQ-SND-02). Every campaign here uses the example brief, so all of them reach the same
// customers.
//
// These tests share global state: the mock provider's message store, request log and scripts
// (/admin/reset, /admin/scripts), and the delivery metrics, which are derived from all deliveries in
// the database. They therefore do NOT call t.Parallel. Go runs every sequential top-level test
// before it resumes any parallel one, so no other E2E test sends, scripts or resets while they run,
// and each of them waits until its campaigns are completed before it returns.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// streamClient has no overall timeout: the live status stream stays open until the campaign is
// completed. Each use bounds it with a context deadline instead.
var streamClient = &http.Client{Transport: apiContract}

// completionDeadline bounds the wait for a campaign of the example audience (350 SMS + 368 email at
// <= 100/s per channel, plus scripted retries whose backoff is at most 1+2+4+8 s).
const completionDeadline = 3 * time.Minute

var deliveryChannels = []string{"sms", "email"}

// failureReasons is the closed failure_reason set of REQ-SND-04 (= the reason label set of
// scc_deliveries_failed_total, REQ-TRK-02).
var failureReasons = []string{"provider_rejected", "max_attempts_exceeded", "rate_limit_budget_exhausted", "content_too_long", "recipient_unavailable"}

// lastErrorRE is the closed last_error set of REQ-SND-04.
var lastErrorRE = regexp.MustCompile(`^(http_[0-9]{3}|timeout|connection_error|content_too_long|recipient_unavailable)$`)

// deliveryKeys are the fields every listDeliveries item carries at least (REQ-SND-01).
var deliveryKeys = []string{
	"id", "campaign_id", "content_version_id", "customer_id", "channel", "status", "attempts",
	"failed_attempts", "rate_limited_count", "retry_count", "next_attempt_at", "first_attempt_at",
	"last_attempt_at", "last_http_status", "last_error", "failure_reason", "provider_message_id",
	"sent_at", "failed_at", "created_at", "updated_at",
}

type sendDoc struct {
	CampaignID       string `json:"campaign_id"`
	State            string `json:"state"`
	ContentVersionID string `json:"content_version_id"`
	Deliveries       struct {
		Total int `json:"total"`
		SMS   int `json:"sms"`
		Email int `json:"email"`
	} `json:"deliveries"`
}

type deliveryDoc struct {
	ID                string  `json:"id"`
	CampaignID        string  `json:"campaign_id"`
	ContentVersionID  string  `json:"content_version_id"`
	CustomerID        string  `json:"customer_id"`
	Channel           string  `json:"channel"`
	Status            string  `json:"status"`
	Attempts          int     `json:"attempts"`
	FailedAttempts    int     `json:"failed_attempts"`
	RateLimitedCount  int     `json:"rate_limited_count"`
	RetryCount        int     `json:"retry_count"`
	NextAttemptAt     *string `json:"next_attempt_at"`
	FirstAttemptAt    *string `json:"first_attempt_at"`
	LastAttemptAt     *string `json:"last_attempt_at"`
	LastHTTPStatus    *int    `json:"last_http_status"`
	LastError         *string `json:"last_error"`
	FailureReason     *string `json:"failure_reason"`
	ProviderMessageID *string `json:"provider_message_id"`
	SentAt            *string `json:"sent_at"`
	FailedAt          *string `json:"failed_at"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type statusCounts struct {
	Total   int `json:"total"`
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

type campaignStatusDoc struct {
	CampaignID string       `json:"campaign_id"`
	State      string       `json:"state"`
	Counts     statusCounts `json:"counts"`
	ByChannel  struct {
		SMS   statusCounts `json:"sms"`
		Email statusCounts `json:"email"`
	} `json:"by_channel"`
	SendingStartedAt *string `json:"sending_started_at"`
	CompletedAt      *string `json:"completed_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type statusEvent struct {
	ID   int
	Name string
	Data campaignStatusDoc
}

func str(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

// ---- campaign helpers ----

// approvedCampaign creates an example-brief campaign and takes it to approved with the ok content.
func approvedCampaign(t *testing.T, api string, m manifestDoc) (id, versionID string) {
	t.Helper()
	id = audienceReady(t, api, m)
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: the ok scenario produced no validated version")
	}
	mustApprove(t, api, id, v.ContentVersionID)
	return id, v.ContentVersionID
}

func sendCampaign(t *testing.T, api, id, versionID string) sendDoc {
	t.Helper()
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/send", []byte(`{"content_version_id":"`+versionID+`"}`), nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST send status = %d, want 202; body: %s", status, body)
	}
	var s sendDoc
	mustUnmarshal(t, string(body), &s)
	return s
}

func getStatus(t *testing.T, api, id string) campaignStatusDoc {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/status", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", status, body)
	}
	var s campaignStatusDoc
	mustUnmarshal(t, string(body), &s)
	return s
}

// assertNoRecipientAddress: listDeliveries never carries a phone number or an e-mail address
// (REQ-SEC-01). Seed addresses are +90500000xxxx and @example.com / @example.test (REQ-SEED-02).
func assertNoRecipientAddress(t *testing.T, what string, body []byte) {
	t.Helper()
	for _, s := range []string{"@example.", "+90500000"} {
		if bytes.Contains(body, []byte(s)) {
			t.Fatalf("%s contains a recipient address (%q): %s", what, s, body)
		}
	}
}

// listDeliveries reads every page (limit 200) of GET /deliveries with the extra query filter.
func listDeliveries(t *testing.T, api, id, filter string) []deliveryDoc {
	t.Helper()
	var all []deliveryDoc
	for offset := 0; ; offset += 200 {
		q := "?limit=200&offset=" + strconv.Itoa(offset)
		if filter != "" {
			q += "&" + filter
		}
		status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/deliveries"+q, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("GET deliveries%s status = %d, want 200; body: %s", q, status, body)
		}
		assertNoRecipientAddress(t, "GET deliveries"+q, body)
		var raw struct {
			Items []map[string]any `json:"items"`
		}
		mustUnmarshal(t, string(body), &raw)
		for i, item := range raw.Items {
			for _, k := range deliveryKeys {
				if _, ok := item[k]; !ok {
					t.Fatalf("deliveries%s items[%d] has no %q: %v", q, i, k, item)
				}
			}
		}
		var page struct {
			Items  []deliveryDoc `json:"items"`
			Total  int           `json:"total"`
			Limit  int           `json:"limit"`
			Offset int           `json:"offset"`
		}
		mustUnmarshal(t, string(body), &page)
		if page.Limit != 200 || page.Offset != offset {
			t.Fatalf("deliveries%s limit/offset = %d/%d, want 200/%d", q, page.Limit, page.Offset, offset)
		}
		all = append(all, page.Items...)
		if len(page.Items) == 0 || len(all) >= page.Total {
			if len(all) != page.Total {
				t.Fatalf("deliveries%s: read %d items, total = %d", q, len(all), page.Total)
			}
			return all
		}
	}
}

// readStatusStream opens GET /events and reads it until the server closes it after `completed`
// (REQ-TRK-01). It checks the framing: one `retry: 3000` before the first event, `id:` from 1,
// one-line JSON data.
func readStatusStream(t *testing.T, api, id string) []statusEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), completionDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/api/campaigns/"+id+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := streamClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Fatalf("GET events status = %d, want 200; body: %s", resp.StatusCode, b.String())
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Fatalf("GET events Content-Type = %q, want %q", ct, "text/event-stream; charset=utf-8")
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		events  []statusEvent
		cur     statusEvent
		data    string
		seen    bool
		retries int
	)
	field := func(line, name string) string { return strings.TrimSpace(strings.TrimPrefix(line, name+":")) }
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if !seen {
				continue
			}
			if cur.Name == "" || data == "" || cur.ID == 0 {
				t.Fatalf("status event without id/event/data: id=%d event=%q data=%q", cur.ID, cur.Name, data)
			}
			mustUnmarshal(t, data, &cur.Data)
			events = append(events, cur)
			cur, data, seen = statusEvent{}, "", false
		case strings.HasPrefix(line, ":"):
			// keepalive comment; not an event
		case strings.HasPrefix(line, "retry:"):
			if retries > 0 || len(events) > 0 || seen {
				t.Fatalf("retry line %q is not the single line before the first event", line)
			}
			if v := field(line, "retry"); v != "3000" {
				t.Fatalf("retry = %q, want 3000", v)
			}
			retries++
		case strings.HasPrefix(line, "id:"):
			n, err := strconv.Atoi(field(line, "id"))
			if err != nil {
				t.Fatalf("id line %q is not a number", line)
			}
			cur.ID, seen = n, true
		case strings.HasPrefix(line, "event:"):
			cur.Name, seen = field(line, "event"), true
		case strings.HasPrefix(line, "data:"):
			if data != "" {
				t.Fatalf("event %q has more than one data line", cur.Name)
			}
			data, seen = field(line, "data"), true
		default:
			t.Fatalf("unexpected status stream line %q", line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read status stream (the campaign did not complete within %s?): %v", completionDeadline, err)
	}
	if seen {
		t.Fatalf("status stream ended inside an event: id=%d event=%q", cur.ID, cur.Name)
	}
	if retries != 1 {
		t.Fatalf("status stream had %d retry lines, want 1", retries)
	}
	return events
}

// assertStatusSums checks total = queued + sending + sent + failed per channel and overall, and
// counts.x = by_channel.sms.x + by_channel.email.x (REQ-TRK-01).
func assertStatusSums(t *testing.T, what string, s campaignStatusDoc) {
	t.Helper()
	for name, c := range map[string]statusCounts{"counts": s.Counts, "sms": s.ByChannel.SMS, "email": s.ByChannel.Email} {
		if c.Total != c.Queued+c.Sending+c.Sent+c.Failed {
			t.Fatalf("%s %s: total %d != queued+sending+sent+failed %+v", what, name, c.Total, c)
		}
	}
	sm, em := s.ByChannel.SMS, s.ByChannel.Email
	want := statusCounts{Total: sm.Total + em.Total, Queued: sm.Queued + em.Queued, Sending: sm.Sending + em.Sending, Sent: sm.Sent + em.Sent, Failed: sm.Failed + em.Failed}
	if s.Counts != want {
		t.Fatalf("%s counts = %+v, want the sum of by_channel %+v", what, s.Counts, want)
	}
}

// assertCompletedStatus: completed, nothing queued or sending, sent + failed = total.
func assertCompletedStatus(t *testing.T, what string, s campaignStatusDoc, id string, send sendDoc) {
	t.Helper()
	assertStatusSums(t, what, s)
	if s.CampaignID != id || s.State != "completed" {
		t.Fatalf("%s campaign_id/state = %s/%s, want %s/completed", what, s.CampaignID, s.State, id)
	}
	if s.Counts.Queued != 0 || s.Counts.Sending != 0 || s.Counts.Sent+s.Counts.Failed != s.Counts.Total {
		t.Fatalf("%s counts = %+v, want queued = sending = 0 and sent + failed = total", what, s.Counts)
	}
	if s.Counts.Total != send.Deliveries.Total || s.ByChannel.SMS.Total != send.Deliveries.SMS || s.ByChannel.Email.Total != send.Deliveries.Email {
		t.Fatalf("%s totals = %d (sms %d, email %d), want the send response's %+v", what, s.Counts.Total, s.ByChannel.SMS.Total, s.ByChannel.Email.Total, send.Deliveries)
	}
	if s.CompletedAt == nil || s.SendingStartedAt == nil {
		t.Fatalf("%s completed_at/sending_started_at = %s/%s, want both set", what, str(s.CompletedAt), str(s.SendingStartedAt))
	}
}

// assertLiveStream checks the event order of one connection that stayed open until completion:
// snapshot first and once, ids 1..n, sent and failed never decrease, completed last and once.
func assertLiveStream(t *testing.T, events []statusEvent, id string, send sendDoc) {
	t.Helper()
	if len(events) < 2 {
		t.Fatalf("status stream had %d events, want at least snapshot and completed: %+v", len(events), events)
	}
	for i, ev := range events {
		if ev.ID != i+1 {
			t.Fatalf("event %d has id %d, want %d", i, ev.ID, i+1)
		}
		want := "status"
		switch i {
		case 0:
			want = "snapshot"
		case len(events) - 1:
			want = "completed"
		}
		if ev.Name != want {
			t.Fatalf("event %d (id %d) is %q, want %q; events: %+v", i, ev.ID, ev.Name, want, events)
		}
		assertStatusSums(t, "event "+strconv.Itoa(ev.ID), ev.Data)
		if ev.Data.CampaignID != id {
			t.Fatalf("event %d campaign_id = %s, want %s", ev.ID, ev.Data.CampaignID, id)
		}
		if i > 0 {
			prev := events[i-1].Data.Counts
			if ev.Data.Counts.Sent < prev.Sent || ev.Data.Counts.Failed < prev.Failed {
				t.Fatalf("event %d counts %+v decrease from %+v", ev.ID, ev.Data.Counts, prev)
			}
		}
	}
	assertCompletedStatus(t, "completed event", events[len(events)-1].Data, id, send)
}

// assertDeliveryInvariants checks one terminal delivery (REQ-SND-01, REQ-SND-04).
func assertDeliveryInvariants(t *testing.T, d deliveryDoc, campaignID, versionID string) {
	t.Helper()
	if !uuidRE.MatchString(d.ID) || d.CampaignID != campaignID || d.ContentVersionID != versionID {
		t.Fatalf("delivery %+v: want a UUID id, campaign %s, version %s", d, campaignID, versionID)
	}
	if !slices.Contains(deliveryChannels, d.Channel) {
		t.Fatalf("delivery %s channel = %q", d.ID, d.Channel)
	}
	if d.FailedAttempts < 0 || d.FailedAttempts > 5 || d.RateLimitedCount < 0 || d.RateLimitedCount > 20 || d.RetryCount < 0 || d.Attempts < 0 {
		t.Fatalf("delivery %s counters out of range: failed_attempts %d (0-5), rate_limited_count %d (0-20), retry_count %d, attempts %d",
			d.ID, d.FailedAttempts, d.RateLimitedCount, d.RetryCount, d.Attempts)
	}
	if d.LastError != nil && !lastErrorRE.MatchString(*d.LastError) {
		t.Fatalf("delivery %s last_error = %q, outside the closed set", d.ID, *d.LastError)
	}
	switch d.Status {
	case "sent":
		if d.FailureReason != nil || d.SentAt == nil || d.FailedAt != nil || d.ProviderMessageID == nil || *d.ProviderMessageID == "" {
			t.Fatalf("sent delivery %s: failure_reason %s, sent_at %s, failed_at %s, provider_message_id %s; want null, set, null, set",
				d.ID, str(d.FailureReason), str(d.SentAt), str(d.FailedAt), str(d.ProviderMessageID))
		}
		if d.LastHTTPStatus == nil || *d.LastHTTPStatus < 200 || *d.LastHTTPStatus > 299 {
			t.Fatalf("sent delivery %s last_http_status = %v, want 2xx", d.ID, d.LastHTTPStatus)
		}
	case "failed":
		if d.FailureReason == nil || !slices.Contains(failureReasons, *d.FailureReason) || d.FailedAt == nil || d.SentAt != nil {
			t.Fatalf("failed delivery %s: failure_reason %s, failed_at %s, sent_at %s; want a contracted reason, set, null",
				d.ID, str(d.FailureReason), str(d.FailedAt), str(d.SentAt))
		}
	default:
		t.Fatalf("delivery %s status = %q after completion, want sent or failed", d.ID, d.Status)
	}
}

// assertDeliverySet: every delivery is terminal and valid, (channel, customer) is unique, the
// per-channel counts equal the send response, and sent/failed equal the status endpoint.
func assertDeliverySet(t *testing.T, ds []deliveryDoc, id, versionID string, send sendDoc, st campaignStatusDoc) {
	t.Helper()
	seen := map[string]bool{}
	perChannel := map[string]int{}
	var sent, failed int
	for _, d := range ds {
		assertDeliveryInvariants(t, d, id, versionID)
		k := d.Channel + "/" + d.CustomerID
		if seen[k] {
			t.Fatalf("two deliveries for (%s, %s)", d.Channel, d.CustomerID)
		}
		seen[k] = true
		perChannel[d.Channel]++
		if d.Status == "sent" {
			sent++
		} else {
			failed++
		}
	}
	if perChannel["sms"] != send.Deliveries.SMS || perChannel["email"] != send.Deliveries.Email || len(ds) != send.Deliveries.Total {
		t.Fatalf("listed deliveries per channel = %v (total %d), want the send response's %+v", perChannel, len(ds), send.Deliveries)
	}
	if sent != st.Counts.Sent || failed != st.Counts.Failed {
		t.Fatalf("listed sent/failed = %d/%d, status endpoint %d/%d", sent, failed, st.Counts.Sent, st.Counts.Failed)
	}
}

// runCampaign takes a new example-brief campaign through send to completion and returns what the
// API reports. metricsBefore is scraped right before the send.
type campaignRun struct {
	id, versionID string
	send          sendDoc
	status        campaignStatusDoc
	deliveries    []deliveryDoc
	metricsBefore metricSet
}

func runCampaign(t *testing.T, api string, m manifestDoc) campaignRun {
	t.Helper()
	var r campaignRun
	r.id, r.versionID = approvedCampaign(t, api, m)
	r.metricsBefore = scrapeMetrics(t, api)
	r.send = sendCampaign(t, api, r.id, r.versionID)
	if r.send.CampaignID != r.id || r.send.State != "sending" || r.send.ContentVersionID != r.versionID {
		t.Fatalf("send response = %+v, want campaign %s, state sending, version %s", r.send, r.id, r.versionID)
	}
	a := m.ExampleBrief.Audience
	if r.send.Deliveries.SMS != a.SMS || r.send.Deliveries.Email != a.Email || r.send.Deliveries.Total != a.SMS+a.Email {
		t.Fatalf("send deliveries = %+v, want the manifest audience sms %d email %d", r.send.Deliveries, a.SMS, a.Email)
	}
	assertLiveStream(t, readStatusStream(t, api, r.id), r.id, r.send)
	r.status = getStatus(t, api, r.id)
	assertCompletedStatus(t, "GET status", r.status, r.id, r.send)
	r.deliveries = listDeliveries(t, api, r.id, "")
	assertDeliverySet(t, r.deliveries, r.id, r.versionID, r.send, r.status)
	return r
}

// ---- metrics ----

type metricKey struct{ name, channel, reason string }

type metricSet map[metricKey]float64

var metricLineRE = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+(\S+)`)

// scrapeMetrics reads the scc_deliver* series of GET /metrics (REQ-TRK-02).
func scrapeMetrics(t *testing.T, api string) metricSet {
	t.Helper()
	status, hdr, body := get(t, api+"/metrics")
	if status != http.StatusOK {
		t.Fatalf("GET /metrics status = %d; body: %s", status, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("GET /metrics Content-Type = %q, want %q", ct, "text/plain; version=0.0.4; charset=utf-8")
	}
	out := metricSet{}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "scc_deliver") {
			continue
		}
		mm := metricLineRE.FindStringSubmatch(line)
		if mm == nil {
			t.Fatalf("unparsable metric line %q", line)
		}
		k := metricKey{name: mm[1]}
		for _, pair := range strings.Split(mm[2], ",") {
			if pair == "" {
				continue
			}
			name, value, ok := strings.Cut(pair, "=")
			if !ok {
				t.Fatalf("bad label %q in %q", pair, line)
			}
			value = strings.Trim(value, `"`)
			switch strings.TrimSpace(name) {
			case "channel":
				k.channel = value
			case "reason":
				k.reason = value
			}
		}
		v, err := strconv.ParseFloat(mm[3], 64)
		if err != nil {
			t.Fatalf("metric value in %q: %v", line, err)
		}
		if _, dup := out[k]; dup {
			t.Fatalf("metric series %+v appears twice", k)
		}
		out[k] = v
	}
	for _, c := range deliveryChannels {
		want := []metricKey{
			{name: "scc_deliveries_sent_total", channel: c},
			{name: "scc_delivery_retries_total", channel: c},
			{name: "scc_deliveries_queued", channel: c},
			{name: "scc_deliveries_in_flight", channel: c},
		}
		for _, r := range failureReasons {
			want = append(want, metricKey{name: "scc_deliveries_failed_total", channel: c, reason: r})
		}
		for _, k := range want {
			if _, ok := out[k]; !ok {
				t.Fatalf("GET /metrics lacks %s{channel=%q,reason=%q}; every label combination is published, 0 included", k.name, k.channel, k.reason)
			}
		}
	}
	return out
}

// assertMetricDeltas: between a scrape before the send and one after completion, the metrics moved
// exactly by this campaign's deliveries, and nothing is queued or in flight afterwards (REQ-TRK-02).
// Deltas are used because the metrics count every delivery in the shared database; no other
// campaign is sending while the sequential tests run.
func assertMetricDeltas(t *testing.T, before, after metricSet, ds []deliveryDoc) {
	t.Helper()
	for _, c := range deliveryChannels {
		var total, sent, retries int
		failed := map[string]int{}
		for _, d := range ds {
			if d.Channel != c {
				continue
			}
			total++
			retries += d.RetryCount
			if d.Status == "sent" {
				sent++
			} else if d.FailureReason != nil {
				failed[*d.FailureReason]++
			}
		}
		delta := func(k metricKey) int { return int(after[k] - before[k]) }
		got := delta(metricKey{name: "scc_deliveries_sent_total", channel: c})
		if got != sent {
			t.Fatalf("scc_deliveries_sent_total{channel=%q} grew by %d, want %d", c, got, sent)
		}
		sum := got
		for _, r := range failureReasons {
			g := delta(metricKey{name: "scc_deliveries_failed_total", channel: c, reason: r})
			if g != failed[r] {
				t.Fatalf("scc_deliveries_failed_total{channel=%q,reason=%q} grew by %d, want %d", c, r, g, failed[r])
			}
			sum += g
		}
		if sum != total {
			t.Fatalf("channel %s: sent + failed grew by %d, want the %d deliveries", c, sum, total)
		}
		if g := delta(metricKey{name: "scc_delivery_retries_total", channel: c}); g != retries {
			t.Fatalf("scc_delivery_retries_total{channel=%q} grew by %d, want the sum of retry_count %d", c, g, retries)
		}
		for _, name := range []string{"scc_deliveries_queued", "scc_deliveries_in_flight"} {
			if v := after[metricKey{name: name, channel: c}]; v != 0 {
				t.Fatalf("%s{channel=%q} = %v after completion, want 0", name, c, v)
			}
		}
	}
}

// ---- mock provider log ----

func indexMessages(msgs []mpMessage) map[string][]mpMessage {
	out := map[string][]mpMessage{}
	for _, m := range msgs {
		out[m.IdempotencyKey] = append(out[m.IdempotencyKey], m)
	}
	return out
}

func indexRequests(reqs []mpRequest) map[string][]mpRequest {
	out := map[string][]mpRequest{}
	for _, r := range reqs {
		out[r.IdempotencyKey] = append(out[r.IdempotencyKey], r)
	}
	return out
}

// ---- tests ----

// REQ-SND-01, REQ-SND-02, REQ-SND-03, REQ-SND-04, REQ-SND-05, REQ-TRK-01, REQ-TRK-02, REQ-SEC-01
// A full example-brief campaign through the worker and the real mock provider: the live stream
// goes snapshot -> status* -> completed; the campaign ends completed with every delivery terminal;
// the mock's own 100/s limit never answered 429 (REQ-SND-02 E2E criterion); every delivery was
// sent with its own id as Idempotency-Key and accepted at most once; the metrics moved by exactly
// this campaign; a second send is refused and creates nothing.
func TestE2E_DeliveryFullCampaign(t *testing.T) {
	// Not parallel: see the file comment.
	api := baseURL(t, "E2E_API_URL")
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	m := readManifest(t)
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	r := runCampaign(t, api, m)
	reqs := mpRequests(t, mp)
	msgs := mpMessages(t, mp)
	byKeyMsg := indexMessages(msgs)
	byKeyReq := indexRequests(reqs)

	t.Run("campaign is completed", func(t *testing.T) {
		status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+r.id, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("GET campaign status = %d; body: %s", status, body)
		}
		c := decodeObject(t, body)
		if c["state"] != "completed" || c["completed_at"] == nil || c["sending_started_at"] == nil {
			t.Fatalf("campaign state/completed_at/sending_started_at = %v/%v/%v, want completed and both set", c["state"], c["completed_at"], c["sending_started_at"])
		}
	})

	t.Run("a stream opened after completion is snapshot then completed", func(t *testing.T) {
		events := readStatusStream(t, api, r.id)
		if len(events) != 2 || events[0].Name != "snapshot" || events[1].Name != "completed" || events[0].ID != 1 || events[1].ID != 2 {
			t.Fatalf("events = %+v, want exactly snapshot (id 1) and completed (id 2)", events)
		}
		for _, ev := range events {
			assertCompletedStatus(t, ev.Name, ev.Data, r.id, r.send)
		}
	})

	t.Run("the mock provider never rate-limited the worker", func(t *testing.T) {
		if len(reqs) == 0 {
			t.Fatal("GET /admin/requests is empty after a full campaign")
		}
		for _, q := range reqs {
			if q.Status == http.StatusTooManyRequests {
				t.Fatalf("request %+v got 429: the worker exceeded 100/s on %s (MOCK_PROVIDER_RATE_LIMIT_RATE=0 in compose.test.yml)", q, q.Channel)
			}
		}
	})

	t.Run("every request carries the delivery id and nothing is accepted twice", func(t *testing.T) {
		ids := map[string]deliveryDoc{}
		for _, d := range r.deliveries {
			ids[d.ID] = d
		}
		for _, q := range reqs {
			d, ok := ids[q.IdempotencyKey]
			if !ok {
				t.Fatalf("request %+v: Idempotency-Key is not a delivery id of the campaign", q)
			}
			if q.Channel != d.Channel {
				t.Fatalf("request %+v went to channel %s, delivery %s is %s", q, q.Channel, d.ID, d.Channel)
			}
			if q.Status == http.StatusConflict {
				t.Fatalf("request %+v got 409: the same key was reused with another channel or recipient", q)
			}
		}
		for _, msg := range msgs {
			if _, ok := ids[msg.IdempotencyKey]; !ok {
				t.Fatalf("accepted message %+v has a key that is no delivery id of the campaign", msg)
			}
		}
		for _, d := range r.deliveries {
			got := byKeyMsg[d.ID]
			rq := byKeyReq[d.ID]
			if len(got) > 1 {
				t.Fatalf("delivery %s was accepted %d times: %+v", d.ID, len(got), got)
			}
			if d.Status == "sent" && len(got) != 1 {
				t.Fatalf("sent delivery %s has %d accepted messages, want 1", d.ID, len(got))
			}
			if d.Status == "failed" && len(got) != 0 {
				t.Fatalf("failed delivery %s (%s) has an accepted message %+v", d.ID, str(d.FailureReason), got)
			}
			if len(rq) > d.Attempts {
				t.Fatalf("delivery %s: %d provider requests but attempts = %d", d.ID, len(rq), d.Attempts)
			}
			if len(got) == 1 {
				if got[0].Channel != d.Channel || got[0].RequestCount != len(rq) {
					t.Fatalf("message %+v for delivery %s: want channel %s and request_count = the %d logged requests", got[0], d.ID, d.Channel, len(rq))
				}
				for _, q := range rq {
					if q.To != got[0].To {
						t.Fatalf("delivery %s requests went to %q and %q", d.ID, q.To, got[0].To)
					}
				}
			}
			// A replay is only legitimate after a lost response of the same delivery; any other
			// replay means the same delivery was taken and sent again after it had been accepted.
			lost := false
			for _, q := range rq {
				if q.Replayed && !lost {
					t.Fatalf("delivery %s was sent again after acceptance (replay without a lost response): %+v", d.ID, rq)
				}
				if q.Status == 0 {
					lost = true
				}
			}
		}
	})

	t.Run("metrics moved by exactly this campaign", func(t *testing.T) {
		assertMetricDeltas(t, r.metricsBefore, scrapeMetrics(t, api), r.deliveries)
	})

	t.Run("a second send is 409 invalid_state and creates nothing", func(t *testing.T) {
		status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+r.id+"/send", []byte(`{"content_version_id":"`+r.versionID+`"}`), nil)
		assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
		if n := len(listDeliveries(t, api, r.id, "")); n != r.send.Deliveries.Total {
			t.Fatalf("deliveries after the second send = %d, want %d", n, r.send.Deliveries.Total)
		}
	})
}

type scriptCase struct {
	name      string
	channel   string
	both      bool // the customer must also have a delivery on the other channel
	responses []map[string]any

	wantStatus         string
	wantReason         string // "" = null
	wantFailedAttempts int
	wantRateLimited    int
	wantRetries        int
	wantLastHTTP       int
	wantLastError      string // "" = not checked
	wantRequests       []int  // statuses in /admin/requests for the delivery's key, in order
	wantReplayed       []bool // nil = all false
	wantMessage        bool   // exactly one accepted message (else none)
	minGaps            []time.Duration
}

func scripted(status int) map[string]any { return map[string]any{"status": status} }

func scriptedRetryAfter(status, seconds int) map[string]any {
	return map[string]any{"status": status, "retry_after": seconds}
}

func repeatScripted(n int, item map[string]any) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = item
	}
	return out
}

func repeatInt(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// REQ-SND-04, REQ-SND-03, REQ-TRK-01, REQ-TRK-02, REQ-MOCK-01
// The retry table against forced provider answers: /admin/scripts queues an exact response
// sequence for one recipient (channel + to), which bypasses the mock's random decisions. A first
// campaign learns each customer's address from /admin/messages; a second campaign then runs with
// the scripts. For every scripted delivery the counters, failure_reason, last_http_status/last_error
// and the request sequence logged under its id (= Idempotency-Key on every attempt) are checked.
// The campaign is completed although some deliveries failed.
func TestE2E_DeliveryScriptedProviderResponses(t *testing.T) {
	// Not parallel: see the file comment.
	api := baseURL(t, "E2E_API_URL")
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	m := readManifest(t)
	mpReset(t, mp)
	t.Cleanup(func() { mpResetCleanup(t, mp) })

	cases := []scriptCase{
		{
			name: "500 and 503 are retried, then 202 is sent", channel: "sms",
			responses:  []map[string]any{scripted(500), scripted(503), scripted(202)},
			wantStatus: "sent", wantFailedAttempts: 2, wantRetries: 2, wantLastHTTP: 202,
			wantRequests: []int{500, 503, 202}, wantMessage: true,
		},
		{
			name: "408 is retried as a failed attempt", channel: "sms",
			responses:  []map[string]any{scripted(408), scripted(202)},
			wantStatus: "sent", wantFailedAttempts: 1, wantRetries: 1, wantLastHTTP: 202,
			wantRequests: []int{408, 202}, wantMessage: true,
		},
		{
			name: "400 is not retried and fails as provider_rejected", channel: "sms", both: true,
			responses:  []map[string]any{scripted(400)},
			wantStatus: "failed", wantReason: "provider_rejected", wantLastHTTP: 400, wantLastError: "http_400",
			wantRequests: []int{400},
		},
		{
			name: "the fifth failed attempt fails as max_attempts_exceeded", channel: "sms",
			responses:  repeatScripted(5, scripted(500)),
			wantStatus: "failed", wantReason: "max_attempts_exceeded", wantFailedAttempts: 5, wantRetries: 4,
			wantLastHTTP: 500, wantLastError: "http_500", wantRequests: repeatInt(5, 500),
		},
		{
			name: "429 waits for Retry-After and is not a failed attempt", channel: "sms",
			responses:  []map[string]any{scriptedRetryAfter(429, 1), scriptedRetryAfter(429, 2), scripted(202)},
			wantStatus: "sent", wantRateLimited: 2, wantRetries: 2, wantLastHTTP: 202,
			wantRequests: []int{429, 429, 202}, wantMessage: true,
			minGaps: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name: "the 20th 429 fails as rate_limit_budget_exhausted", channel: "sms",
			responses:  repeatScripted(20, scriptedRetryAfter(429, 0)),
			wantStatus: "failed", wantReason: "rate_limit_budget_exhausted", wantRateLimited: 20, wantRetries: 19,
			wantLastHTTP: 429, wantLastError: "http_429", wantRequests: repeatInt(20, 429),
		},
		{
			name: "a Retry-After beyond the 10 minute window fails without waiting", channel: "sms",
			responses:  []map[string]any{scriptedRetryAfter(429, 601)},
			wantStatus: "failed", wantReason: "rate_limit_budget_exhausted", wantRateLimited: 1,
			wantLastHTTP: 429, wantLastError: "http_429", wantRequests: []int{429},
		},
		{
			name: "429 and 5xx are counted in separate budgets", channel: "sms",
			responses:  []map[string]any{scriptedRetryAfter(429, 0), scripted(500), scriptedRetryAfter(429, 0), scripted(503), scripted(202)},
			wantStatus: "sent", wantFailedAttempts: 2, wantRateLimited: 2, wantRetries: 4, wantLastHTTP: 202,
			wantRequests: []int{429, 500, 429, 503, 202}, wantMessage: true,
		},
		{
			name: "a lost response is retried with the same key and not sent twice", channel: "sms",
			responses:  []map[string]any{{"status": 202, "lost_response": true}},
			wantStatus: "sent", wantFailedAttempts: 1, wantRetries: 1, wantLastHTTP: 202,
			wantRequests: []int{0, 202}, wantReplayed: []bool{false, true}, wantMessage: true,
		},
		{
			name: "email 503 is retried, then 202 is sent", channel: "email",
			responses:  []map[string]any{scripted(503), scripted(202)},
			wantStatus: "sent", wantFailedAttempts: 1, wantRetries: 1, wantLastHTTP: 202,
			wantRequests: []int{503, 202}, wantMessage: true,
		},
		{
			name: "email 400 fails as provider_rejected", channel: "email",
			responses:  []map[string]any{scripted(400)},
			wantStatus: "failed", wantReason: "provider_rejected", wantLastHTTP: 400, wantLastError: "http_400",
			wantRequests: []int{400},
		},
	}

	// 1. Learn each customer's address: delivery id -> customer, message key -> to. Only customers
	// whose address is unique in the campaign are used (shared phones/e-mails exist in the seed).
	base := runCampaign(t, api, m)
	baseMsgs := indexMessages(mpMessages(t, mp))
	addr := map[string]map[string]string{"sms": {}, "email": {}} // channel -> customer -> to
	owners := map[string]int{}
	for _, d := range base.deliveries {
		if got := baseMsgs[d.ID]; len(got) == 1 {
			addr[d.Channel][d.CustomerID] = got[0].To
			owners[d.Channel+"|"+got[0].To]++
		}
	}
	usable := func(channel, customer string) bool {
		to, ok := addr[channel][customer]
		return ok && owners[channel+"|"+to] == 1
	}
	var customers []string
	for c := range addr["sms"] {
		customers = append(customers, c)
	}
	for c := range addr["email"] {
		if _, ok := addr["sms"][c]; !ok {
			customers = append(customers, c)
		}
	}
	slices.Sort(customers)
	used := map[string]bool{}
	picked := make([]string, len(cases))
	for i, tc := range cases {
		other := "email"
		if tc.channel == "email" {
			other = "sms"
		}
		for _, c := range customers {
			if used[c] || !usable(tc.channel, c) {
				continue
			}
			if _, inOther := addr[other][c]; tc.both && !inOther {
				continue
			}
			picked[i], used[c] = c, true
			break
		}
		if picked[i] == "" {
			t.Fatalf("setup: no customer with a unique %s address left for %q", tc.channel, tc.name)
		}
	}

	// 2. Script every case, then run the second campaign.
	mpReset(t, mp)
	for i, tc := range cases {
		mpScript(t, mp, tc.channel, addr[tc.channel][picked[i]], tc.responses)
	}
	run := runCampaign(t, api, m)
	reqs := indexRequests(mpRequests(t, mp))
	msgs := indexMessages(mpMessages(t, mp))

	failingIDs := map[string]bool{}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := listDeliveries(t, api, run.id, "customer_id="+picked[i]+"&channel="+tc.channel)
			if len(ds) != 1 {
				t.Fatalf("deliveries for customer %s on %s = %d, want 1", picked[i], tc.channel, len(ds))
			}
			d := ds[0]
			if d.CustomerID != picked[i] || d.Channel != tc.channel {
				t.Fatalf("filtered delivery = %+v, want customer %s channel %s", d, picked[i], tc.channel)
			}
			if d.Status == "failed" {
				failingIDs[d.ID] = true
			}
			wantReason := "<null>"
			if tc.wantReason != "" {
				wantReason = tc.wantReason
			}
			if d.Status != tc.wantStatus || str(d.FailureReason) != wantReason {
				t.Fatalf("status/failure_reason = %s/%s, want %s/%q", d.Status, str(d.FailureReason), tc.wantStatus, tc.wantReason)
			}
			if d.FailedAttempts != tc.wantFailedAttempts || d.RateLimitedCount != tc.wantRateLimited || d.RetryCount != tc.wantRetries || d.Attempts != len(tc.wantRequests) {
				t.Fatalf("failed_attempts/rate_limited_count/retry_count/attempts = %d/%d/%d/%d, want %d/%d/%d/%d",
					d.FailedAttempts, d.RateLimitedCount, d.RetryCount, d.Attempts, tc.wantFailedAttempts, tc.wantRateLimited, tc.wantRetries, len(tc.wantRequests))
			}
			if d.LastHTTPStatus == nil || *d.LastHTTPStatus != tc.wantLastHTTP {
				t.Fatalf("last_http_status = %v, want %d", d.LastHTTPStatus, tc.wantLastHTTP)
			}
			if tc.wantLastError != "" && str(d.LastError) != tc.wantLastError {
				t.Fatalf("last_error = %s, want %s", str(d.LastError), tc.wantLastError)
			}

			rq := reqs[d.ID]
			var statuses []int
			var replayed []bool
			for _, q := range rq {
				statuses = append(statuses, q.Status)
				replayed = append(replayed, q.Replayed)
				if q.To != addr[tc.channel][picked[i]] || q.Channel != tc.channel {
					t.Fatalf("request %+v: want channel %s and the customer's address", q, tc.channel)
				}
			}
			if !slices.Equal(statuses, tc.wantRequests) {
				t.Fatalf("statuses logged under Idempotency-Key %s = %v, want %v (every attempt carries the delivery id)", d.ID, statuses, tc.wantRequests)
			}
			wantReplayed := tc.wantReplayed
			if wantReplayed == nil {
				wantReplayed = make([]bool, len(tc.wantRequests))
			}
			if !slices.Equal(replayed, wantReplayed) {
				t.Fatalf("replayed flags = %v, want %v", replayed, wantReplayed)
			}
			for g, minGap := range tc.minGaps {
				t0 := parseTime(t, "received_at", rq[g].ReceivedAt)
				t1 := parseTime(t, "received_at", rq[g+1].ReceivedAt)
				// 50 ms tolerance for timestamp precision only; Retry-After is a lower bound.
				if gap := t1.Sub(t0); gap < minGap-50*time.Millisecond {
					t.Fatalf("request %d came %s after request %d, want at least the Retry-After of %s", g+2, gap, g+1, minGap)
				}
			}
			got := msgs[d.ID]
			switch {
			case tc.wantMessage && (len(got) != 1 || got[0].RequestCount != len(tc.wantRequests)):
				t.Fatalf("accepted messages for %s = %+v, want one with request_count %d", d.ID, got, len(tc.wantRequests))
			case !tc.wantMessage && len(got) != 0:
				t.Fatalf("accepted messages for %s = %+v, want none", d.ID, got)
			}
		})
	}

	t.Run("a rejected SMS does not touch the customer's email", func(t *testing.T) {
		for i, tc := range cases {
			if !tc.both {
				continue
			}
			ds := listDeliveries(t, api, run.id, "customer_id="+picked[i]+"&channel=email")
			if len(ds) != 1 || ds[0].Status != "sent" || ds[0].FailureReason != nil {
				t.Fatalf("email delivery of customer %s = %+v, want one sent delivery", picked[i], ds)
			}
		}
	})

	t.Run("completed with failed deliveries visible", func(t *testing.T) {
		if run.status.State != "completed" || run.status.Counts.Failed < len(failingIDs) {
			t.Fatalf("status = %+v, want completed with at least %d failed", run.status, len(failingIDs))
		}
		failed := listDeliveries(t, api, run.id, "status=failed")
		if len(failed) != run.status.Counts.Failed {
			t.Fatalf("status=failed lists %d deliveries, status endpoint says %d", len(failed), run.status.Counts.Failed)
		}
		found := map[string]bool{}
		for _, d := range failed {
			if d.Status != "failed" {
				t.Fatalf("status=failed returned %+v", d)
			}
			found[d.ID] = true
		}
		for id := range failingIDs {
			if !found[id] {
				t.Fatalf("failed delivery %s is missing from status=failed", id)
			}
		}
	})

	t.Run("metrics moved by exactly this campaign", func(t *testing.T) {
		assertMetricDeltas(t, run.metricsBefore, scrapeMetrics(t, api), run.deliveries)
	})
}

func parseTime(t *testing.T, name, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %q is not RFC 3339: %v", name, s, err)
	}
	return v
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return b
}
