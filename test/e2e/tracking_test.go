package e2e_test

// P5 live tracking (REQ-TRK-01) over HTTP + SSE against the compose.test.yml stack.
//
// Every name below comes from REQ-TRK-01 / REQ-SND-01 in docs/requirements.md:
//   - GET /api/campaigns/{campaign_id}/status -> 200 CampaignStatus {campaign_id, state,
//     counts{total, queued, sending, sent, failed}, by_channel{sms{...}, email{...}},
//     sending_started_at, completed_at, updated_at}; total = queued + sending + sent + failed per
//     channel and in total; counts.x = by_channel.sms.x + by_channel.email.x; unknown campaign 404
//     campaign_not_found.
//   - GET /api/campaigns/{campaign_id}/events: Content-Type "text/event-stream; charset=utf-8";
//     "retry: 3000" once at the start; every event carries "id: <n>" (from 1 per connection),
//     "event:" and one single-line JSON "data:" whose payload is a CampaignStatus. Events: snapshot
//     (first, exactly once), status (only when the result differs from the last one sent,
//     updated_at aside), completed (last; then the server closes the connection; right after the
//     snapshot when the campaign is already completed). sent and failed never decrease. ": keepalive"
//     comments are not events. Last-Event-ID is not supported: a reconnecting client gets a new
//     snapshot. Unknown or non-UUID campaign: no stream, 404 JSON campaign_not_found. An upper-case
//     UUID is valid (REQ-SND-01).
//
// The full-campaign event order is also checked by the delivery suite; this file covers the stream
// format, the reconnect rules, the "only on change" rule, the delivery-less completion and the 404s.
// Helpers carry a trk prefix so they cannot collide with helpers of other E2E files.

import (
	"bufio"
	"context"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// trkStreamClient reads event streams that may outlive the 10 s timeout of client; it goes through
// the same strict OpenAPI transport.
var trkStreamClient = &http.Client{Transport: apiContract}

// trkStreamDeadline bounds one whole campaign stream (other E2E tests send in parallel and share the
// 100 msg/s per channel limit; retries back off for up to a minute).
const trkStreamDeadline = 4 * time.Minute

func trkEventsURL(api, id string) string { return api + "/api/campaigns/" + id + "/events" }
func trkStatusURL(api, id string) string { return api + "/api/campaigns/" + id + "/status" }

type trkCounts struct {
	Total   int `json:"total"`
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

type trkStatus struct {
	CampaignID string    `json:"campaign_id"`
	State      string    `json:"state"`
	Counts     trkCounts `json:"counts"`
	ByChannel  struct {
		SMS   trkCounts `json:"sms"`
		Email trkCounts `json:"email"`
	} `json:"by_channel"`
	SendingStartedAt *string `json:"sending_started_at"`
	CompletedAt      *string `json:"completed_at"`
	UpdatedAt        string  `json:"updated_at"`
}

// all returns the three counter sets with their names.
func (s trkStatus) all() []struct {
	name string
	c    trkCounts
} {
	return []struct {
		name string
		c    trkCounts
	}{{"counts", s.Counts}, {"by_channel.sms", s.ByChannel.SMS}, {"by_channel.email", s.ByChannel.Email}}
}

type trkEvent struct {
	ID     int
	Name   string
	Data   string
	Status trkStatus
}

type trkSendResult struct {
	CampaignID       string `json:"campaign_id"`
	State            string `json:"state"`
	ContentVersionID string `json:"content_version_id"`
	Deliveries       struct {
		Total int `json:"total"`
		SMS   int `json:"sms"`
		Email int `json:"email"`
	} `json:"deliveries"`
}

// trkCheckStatus checks one CampaignStatus document: the schema of openapi.yaml, the campaign id
// and the sum rules of REQ-TRK-01.
func trkCheckStatus(t *testing.T, where, data, id string) trkStatus {
	t.Helper()
	spec, err := apiContract.Spec()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if problems := spec.ValidateComponent("CampaignStatus", []byte(data)); len(problems) > 0 {
		t.Fatalf("%s does not follow components.schemas.CampaignStatus: %v\ndata: %s", where, problems, data)
	}
	var s trkStatus
	mustUnmarshal(t, data, &s)
	if !strings.EqualFold(s.CampaignID, id) {
		t.Fatalf("%s campaign_id = %q, want %s", where, s.CampaignID, id)
	}
	for _, x := range s.all() {
		c := x.c
		if c.Total < 0 || c.Queued < 0 || c.Sending < 0 || c.Sent < 0 || c.Failed < 0 {
			t.Fatalf("%s %s has a negative counter: %+v", where, x.name, c)
		}
		if c.Total != c.Queued+c.Sending+c.Sent+c.Failed {
			t.Fatalf("%s %s total %d != queued+sending+sent+failed %+v", where, x.name, c.Total, c)
		}
	}
	sms, email := s.ByChannel.SMS, s.ByChannel.Email
	sum := trkCounts{
		Total:   sms.Total + email.Total,
		Queued:  sms.Queued + email.Queued,
		Sending: sms.Sending + email.Sending,
		Sent:    sms.Sent + email.Sent,
		Failed:  sms.Failed + email.Failed,
	}
	if diff := cmp.Diff(sum, s.Counts); diff != "" {
		t.Fatalf("%s counts != by_channel.sms + by_channel.email (-sum +counts):\n%s", where, diff)
	}
	return s
}

// trkWithoutUpdatedAt is the comparison form of the "only on change" rule: the document without
// updated_at.
func trkWithoutUpdatedAt(t *testing.T, data string) map[string]any {
	t.Helper()
	m := decodeObject(t, []byte(data))
	delete(m, "updated_at")
	return m
}

func trkAllZero(s trkStatus) bool {
	return s.Counts == (trkCounts{}) && s.ByChannel.SMS == (trkCounts{}) && s.ByChannel.Email == (trkCounts{})
}

// ---- strict reader of the status stream ----

type trkStream struct {
	id      string
	lines   <-chan string
	errc    <-chan error
	retries int // "retry:" lines seen
	events  int // complete events seen
}

type trkResult int

const (
	trkGot trkResult = iota
	trkEOF
	trkTimeout
)

// trkOpen opens GET /api/campaigns/{urlID}/events and checks the 200 and its Content-Type. id is
// the campaign id the CampaignStatus documents must carry.
func trkOpen(t *testing.T, api, urlID, id string, headers map[string]string) *trkStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trkEventsURL(api, urlID), nil)
	if err != nil {
		cancel()
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := trkStreamClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET events: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	if resp.StatusCode != http.StatusOK {
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			b.WriteString(sc.Text())
		}
		t.Fatalf("GET events status = %d, want 200; body: %s", resp.StatusCode, b.String())
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Fatalf("GET events Content-Type = %q, want %q", ct, "text/event-stream; charset=utf-8")
	}
	lines := make(chan string)
	errc := make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				errc <- nil
				return
			}
		}
		errc <- sc.Err()
	}()
	return &trkStream{id: id, lines: lines, errc: errc}
}

// poll reads the next complete event. It returns trkEOF when the server closed the stream and
// trkTimeout when deadline fires between events; an event cut by the deadline or by the end of the
// body fails the test, as does any line outside the stream format of REQ-TRK-01.
func (s *trkStream) poll(t *testing.T, deadline <-chan time.Time) (trkEvent, trkResult) {
	t.Helper()
	var ev trkEvent
	var haveID, haveName, haveData bool
	started := func() bool { return haveID || haveName || haveData }
	for {
		select {
		case <-deadline:
			if started() {
				t.Fatalf("deadline inside an event (id %d, event %q)", ev.ID, ev.Name)
			}
			return trkEvent{}, trkTimeout
		case line, open := <-s.lines:
			if !open {
				if err := <-s.errc; err != nil {
					t.Fatalf("read event stream: %v", err)
				}
				if started() {
					t.Fatalf("event stream ended inside an event (no blank line): %+v", ev)
				}
				return trkEvent{}, trkEOF
			}
			switch {
			case line == "":
				if !started() {
					continue
				}
				if !haveID || !haveName || !haveData {
					t.Fatalf("event without id, event or data line: %+v", ev)
				}
				s.events++
				if s.events == 1 && s.retries != 1 {
					t.Fatalf("first event arrived after %d \"retry: 3000\" lines, want exactly 1 at the start of the stream", s.retries)
				}
				if ev.ID != s.events {
					t.Fatalf("event #%d (%s) has id %d, want %d (ids count from 1 per connection)", s.events, ev.Name, ev.ID, s.events)
				}
				switch ev.Name {
				case "snapshot", "status", "completed":
				default:
					t.Fatalf("unknown event %q", ev.Name)
				}
				ev.Status = trkCheckStatus(t, ev.Name+" event #"+strconv.Itoa(ev.ID), ev.Data, s.id)
				return ev, trkGot
			case strings.HasPrefix(line, ":"):
				// comment (": keepalive"); not an event
			case strings.HasPrefix(line, "retry:"):
				s.retries++
				if line != "retry: 3000" {
					t.Fatalf("retry line = %q, want %q", line, "retry: 3000")
				}
				if s.retries > 1 || s.events > 0 {
					t.Fatalf("\"retry: 3000\" must come once, at the start of the stream (seen %d times, after %d events)", s.retries, s.events)
				}
			case strings.HasPrefix(line, "id: "):
				if haveID {
					t.Fatalf("event has two id lines: %q", line)
				}
				n, err := strconv.Atoi(strings.TrimPrefix(line, "id: "))
				if err != nil {
					t.Fatalf("id line %q is not \"id: <n>\"", line)
				}
				ev.ID, haveID = n, true
			case strings.HasPrefix(line, "event: "):
				if haveName {
					t.Fatalf("event has two event lines: %q", line)
				}
				ev.Name, haveName = strings.TrimPrefix(line, "event: "), true
			case strings.HasPrefix(line, "data: "):
				if haveData {
					t.Fatalf("event %q has more than one data line (data must be one-line JSON)", ev.Name)
				}
				ev.Data, haveData = strings.TrimPrefix(line, "data: "), true
			default:
				t.Fatalf("unexpected line in the status stream: %q", line)
			}
		}
	}
}

// next reads the next event and fails the test on end of stream or deadline.
func (s *trkStream) next(t *testing.T, deadline <-chan time.Time, what string) trkEvent {
	t.Helper()
	ev, r := s.poll(t, deadline)
	switch r {
	case trkEOF:
		t.Fatalf("event stream closed while waiting for %s (after %d events)", what, s.events)
	case trkTimeout:
		t.Fatalf("no event before the deadline while waiting for %s (after %d events)", what, s.events)
	}
	return ev
}

// readToCompleted reads events until completed, then checks that the server closes the connection.
func (s *trkStream) readToCompleted(t *testing.T, deadline <-chan time.Time) []trkEvent {
	t.Helper()
	var evs []trkEvent
	for {
		ev := s.next(t, deadline, "the completed event")
		evs = append(evs, ev)
		if ev.Name == "completed" {
			break
		}
	}
	ev, r := s.poll(t, time.After(15*time.Second))
	switch r {
	case trkGot:
		t.Fatalf("event %q (id %d) after completed; completed must be the last event", ev.Name, ev.ID)
	case trkTimeout:
		t.Fatal("the server did not close the stream within 15s after the completed event")
	}
	return evs
}

// trkCheckSequence checks the event grammar of one whole connection: snapshot first and once,
// status events in between, completed last and once; a status event differs from the previous
// event (updated_at aside) and is never in state completed; sent and failed never decrease; the
// completed payload is final (state completed, queued = sending = 0, sent + failed = total).
func trkCheckSequence(t *testing.T, evs []trkEvent) {
	t.Helper()
	if len(evs) < 2 {
		t.Fatalf("got %d events, want at least snapshot and completed", len(evs))
	}
	for i, ev := range evs {
		want := "status"
		switch i {
		case 0:
			want = "snapshot"
		case len(evs) - 1:
			want = "completed"
		}
		if ev.Name != want {
			t.Fatalf("event #%d is %q, want %q (snapshot first, completed last, status in between)", i+1, ev.Name, want)
		}
		if ev.Name == "status" && ev.Status.State == "completed" {
			t.Fatalf("status event #%d carries state completed; that must be the completed event", i+1)
		}
		if i == 0 {
			continue
		}
		prev := evs[i-1]
		if ev.Name == "status" {
			if cmp.Equal(trkWithoutUpdatedAt(t, prev.Data), trkWithoutUpdatedAt(t, ev.Data)) {
				t.Fatalf("status event #%d repeats the previous event (updated_at aside): %s", i+1, ev.Data)
			}
		}
		p, c := prev.Status.all(), ev.Status.all()
		for k := range p {
			if c[k].c.Sent < p[k].c.Sent || c[k].c.Failed < p[k].c.Failed {
				t.Fatalf("%s decreased from event #%d to #%d: sent %d -> %d, failed %d -> %d",
					p[k].name, i, i+1, p[k].c.Sent, c[k].c.Sent, p[k].c.Failed, c[k].c.Failed)
			}
		}
	}
	last := evs[len(evs)-1].Status
	if last.State != "completed" {
		t.Fatalf("completed event state = %q, want completed", last.State)
	}
	for _, x := range last.all() {
		if x.c.Queued != 0 || x.c.Sending != 0 || x.c.Sent+x.c.Failed != x.c.Total {
			t.Fatalf("completed event %s = %+v, want queued = sending = 0 and sent + failed = total", x.name, x.c)
		}
	}
	if last.SendingStartedAt == nil || last.CompletedAt == nil {
		t.Fatalf("completed event sending_started_at/completed_at = %v/%v, want both set", last.SendingStartedAt, last.CompletedAt)
	}
}

// trkAssertCompletedStream opens a new connection to an already completed campaign and expects
// exactly snapshot (id 1) then completed (id 2), then the close; both carry final, equal counters.
// want is the final CampaignStatus (updated_at aside).
func trkAssertCompletedStream(t *testing.T, api, urlID, id string, headers map[string]string, want map[string]any) {
	t.Helper()
	s := trkOpen(t, api, urlID, id, headers)
	evs := s.readToCompleted(t, time.After(30*time.Second))
	if len(evs) != 2 {
		names := make([]string, len(evs))
		for i, ev := range evs {
			names[i] = ev.Name
		}
		t.Fatalf("events of a completed campaign = %v, want [snapshot completed]", names)
	}
	trkCheckSequence(t, evs)
	if evs[0].Status.State != "completed" {
		t.Fatalf("snapshot state = %q, want completed", evs[0].Status.State)
	}
	// campaign_id is compared case-insensitively by trkCheckStatus (the URL may carry the
	// upper-case form); everything else must equal the final status.
	want = maps.Clone(want)
	delete(want, "campaign_id")
	for i, ev := range evs {
		got := trkWithoutUpdatedAt(t, ev.Data)
		delete(got, "campaign_id")
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("%s event (#%d) differs from the final status (-want +got):\n%s", ev.Name, i+1, diff)
		}
	}
}

func trkSend(t *testing.T, api, id, vid string) trkSendResult {
	t.Helper()
	status, _, body := doJSON(t, http.MethodPost, sendURL(api, id), sendBody(t, vid), nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST send status = %d, want 202; body: %s", status, body)
	}
	var r trkSendResult
	mustUnmarshal(t, string(body), &r)
	if r.Deliveries.Total != r.Deliveries.SMS+r.Deliveries.Email {
		t.Fatalf("send deliveries total %d != sms %d + email %d", r.Deliveries.Total, r.Deliveries.SMS, r.Deliveries.Email)
	}
	return r
}

func trkGetStatus(t *testing.T, api, id string) (trkStatus, string) {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, trkStatusURL(api, id), nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status status = %d, want 200; body: %s", status, body)
	}
	return trkCheckStatus(t, "GET status", string(body), id), string(body)
}

// REQ-TRK-01, REQ-SND-01
// A client connected before the send follows the whole campaign without reloading: the stream
// starts with "retry: 3000", then a snapshot of the approved campaign with every counter 0; after
// POST send the counters move through status events (each different from the previous one, sent
// and failed never decreasing) to the completed event, after which the server closes the stream.
// Ids count 1, 2, 3… on the connection. The completed payload equals GET status. A later
// connection (also with Last-Event-ID, also with the upper-case id) gets a new snapshot with id 1
// and the completed event right after it.
func TestE2E_StatusStreamFollowsASend(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	if m.ExampleBrief.Audience.SMS+m.ExampleBrief.Audience.Email == 0 {
		t.Fatal("setup: the example brief audience is empty in the manifest; this test needs deliveries")
	}
	id, vid := approvedCampaign(t, api, m)
	deadline := time.After(trkStreamDeadline)

	s := trkOpen(t, api, id, id, nil)
	first := s.next(t, deadline, "the snapshot")
	if first.Name != "snapshot" {
		t.Fatalf("first event = %q, want snapshot", first.Name)
	}
	if first.Status.State != "approved" || !trkAllZero(first.Status) {
		t.Fatalf("snapshot before the send = %s, want state approved and every counter 0", first.Data)
	}
	if first.Status.SendingStartedAt != nil || first.Status.CompletedAt != nil {
		t.Fatalf("snapshot before the send sending_started_at/completed_at = %v/%v, want null/null", first.Status.SendingStartedAt, first.Status.CompletedAt)
	}

	sent := trkSend(t, api, id, vid)
	if sent.State != "sending" {
		t.Fatalf("send state = %q, want sending (the audience is not empty)", sent.State)
	}

	evs := append([]trkEvent{first}, s.readToCompleted(t, deadline)...)
	trkCheckSequence(t, evs)

	last := evs[len(evs)-1]
	gotTotals := []int{last.Status.Counts.Total, last.Status.ByChannel.SMS.Total, last.Status.ByChannel.Email.Total}
	wantTotals := []int{sent.Deliveries.Total, sent.Deliveries.SMS, sent.Deliveries.Email}
	if diff := cmp.Diff(wantTotals, gotTotals); diff != "" {
		t.Fatalf("completed totals [total sms email] differ from the send response (-want +got):\n%s", diff)
	}
	// Sending one channel at <= 100 msg/s takes longer than the 1 s poll interval here, so the
	// campaign is seen in sending at least once between the snapshot and completed.
	if max(sent.Deliveries.SMS, sent.Deliveries.Email) > 200 {
		seen := false
		for _, ev := range evs[1 : len(evs)-1] {
			seen = seen || ev.Status.State == "sending"
		}
		if !seen {
			t.Fatalf("no status event with state sending between the snapshot and completed (%d events)", len(evs))
		}
	}

	_, body := trkGetStatus(t, api, id)
	final := trkWithoutUpdatedAt(t, body)
	if diff := cmp.Diff(final, trkWithoutUpdatedAt(t, last.Data)); diff != "" {
		t.Fatalf("completed event differs from GET status (-get +event):\n%s", diff)
	}

	reconnects := []struct {
		name    string
		urlID   string
		headers map[string]string
	}{
		{name: "a new connection gets a new snapshot then completed", urlID: id},
		{name: "Last-Event-ID is not supported and still gets a new snapshot", urlID: id, headers: map[string]string{"Last-Event-ID": strconv.Itoa(len(evs))}},
		{name: "an upper-case campaign id is valid", urlID: strings.ToUpper(id)},
	}
	for _, tc := range reconnects {
		t.Run(tc.name, func(t *testing.T) {
			trkAssertCompletedStream(t, api, tc.urlID, id, tc.headers, final)
		})
	}
}

// REQ-TRK-01, REQ-SND-01
// When both channel audiences are empty the send completes the campaign at once (202, state
// completed, no deliveries). Its stream is snapshot (state completed, every counter 0) immediately
// followed by completed, then the server closes the connection; GET status says the same.
func TestE2E_StatusStreamOfCampaignWithoutDeliveries(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	// sql_in_value: a valid rule that matches nobody (REQ-AUD-02).
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("sql_in_value"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d; body: %s", status, body)
	}
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: no validated version")
	}
	if c := mustApprove(t, api, id, v.ContentVersionID); c.State != "approved" {
		t.Fatalf("setup: state after approve = %s, want approved", c.State)
	}

	sent := trkSend(t, api, id, v.ContentVersionID)
	if sent.State != "completed" || sent.Deliveries.Total != 0 {
		t.Fatalf("send of an empty audience = state %q, %d deliveries; want completed, 0", sent.State, sent.Deliveries.Total)
	}

	st, stBody := trkGetStatus(t, api, id)
	if st.State != "completed" || !trkAllZero(st) {
		t.Fatalf("GET status = %s, want state completed and every counter 0", stBody)
	}
	trkAssertCompletedStream(t, api, id, id, nil, trkWithoutUpdatedAt(t, stBody))
}

// REQ-TRK-01
// Nothing changes on a draft campaign, so after the snapshot (state draft, every counter 0,
// sending_started_at and completed_at null, equal to GET status) no status event is sent across
// several 1 s polls, and the stream stays open (no completed event, no close).
func TestE2E_StatusStreamSendsNothingWithoutChange(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)

	_, body := trkGetStatus(t, api, id)
	s := trkOpen(t, api, id, id, nil)
	first := s.next(t, time.After(30*time.Second), "the snapshot")
	if first.Name != "snapshot" {
		t.Fatalf("first event = %q, want snapshot", first.Name)
	}
	if first.Status.State != "draft" || !trkAllZero(first.Status) || first.Status.SendingStartedAt != nil || first.Status.CompletedAt != nil {
		t.Fatalf("snapshot of a draft = %s, want state draft, every counter 0, null times", first.Data)
	}
	if diff := cmp.Diff(trkWithoutUpdatedAt(t, body), trkWithoutUpdatedAt(t, first.Data)); diff != "" {
		t.Fatalf("snapshot differs from GET status (-get +snapshot):\n%s", diff)
	}

	ev, r := s.poll(t, time.After(3500*time.Millisecond))
	switch r {
	case trkGot:
		t.Fatalf("event %q (id %d) on an unchanged draft campaign: %s", ev.Name, ev.ID, ev.Data)
	case trkEOF:
		t.Fatal("the server closed the stream of a draft campaign; only completed ends the stream")
	}
}

// REQ-TRK-01, REQ-SND-01
// No stream opens for an unknown campaign or a non-UUID campaign_id: 404 JSON campaign_not_found,
// also when the client asks for text/event-stream. GET status behaves the same.
func TestE2E_TrackingUnknownCampaignIs404(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	sse := map[string]string{"Accept": "text/event-stream"}
	tests := []struct {
		name    string
		url     string
		headers map[string]string
	}{
		{name: "events of an unknown campaign", url: trkEventsURL(api, unknownCampaignID), headers: sse},
		{name: "events of an unknown campaign without Accept", url: trkEventsURL(api, unknownCampaignID)},
		{name: "events of an unknown upper-case UUID", url: trkEventsURL(api, strings.ToUpper("0000000a-0000-4000-8000-00000000000b")), headers: sse},
		{name: "events of a non-UUID id", url: trkEventsURL(api, "not-a-uuid"), headers: sse},
		{name: "events of a numeric id", url: trkEventsURL(api, "12345"), headers: sse},
		{name: "events of a UUID with an extra character", url: trkEventsURL(api, unknownCampaignID+"0"), headers: sse}, // gitleaks:allow (test UUID, not a secret)
		{name: "status of an unknown campaign", url: trkStatusURL(api, unknownCampaignID)},
		{name: "status of a non-UUID id", url: trkStatusURL(api, "not-a-uuid")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodGet, tc.url, nil, tc.headers)
			if strings.HasPrefix(hdr.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("a stream was opened (status %d); want 404 JSON", status)
			}
			assertError(t, status, hdr, body, http.StatusNotFound, "campaign_not_found", false)
		})
	}
}

// trkStatusKeys is the key set of CampaignStatus named in REQ-TRK-01.
var trkStatusKeys = []string{"by_channel", "campaign_id", "completed_at", "counts", "sending_started_at", "state", "updated_at"}

// REQ-TRK-01
// GET status of a campaign without deliveries (draft, audience_ready, approved): 200 CampaignStatus
// with exactly the documented keys, every counter 0, both channels present, null times.
func TestE2E_StatusBeforeSend(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	draft, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	ready := audienceReady(t, api, m)
	approved, _ := approvedCampaign(t, api, m)
	tests := []struct {
		name, id, state string
	}{
		{name: "draft", id: draft, state: "draft"},
		{name: "audience_ready", id: ready, state: "audience_ready"},
		{name: "approved", id: approved, state: "approved"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, body := trkGetStatus(t, api, tc.id)
			raw := decodeObject(t, []byte(body))
			if diff := cmp.Diff(trkStatusKeys, sortedKeys(raw)); diff != "" {
				t.Fatalf("CampaignStatus keys mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{"email", "sms"}, sortedKeys(asObject(t, raw["by_channel"]))); diff != "" {
				t.Fatalf("by_channel keys mismatch (-want +got):\n%s", diff)
			}
			if st.State != tc.state || !trkAllZero(st) || st.SendingStartedAt != nil || st.CompletedAt != nil {
				t.Fatalf("GET status = %s, want state %s, every counter 0, null times", body, tc.state)
			}
		})
	}
}
