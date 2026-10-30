package e2e_test

// Scheduled sending (REQ-SCH-01..03) over HTTP against the compose.test.yml stack, where the API runs
// with SCHEDULE_MIN_LEAD=0s and the worker's scheduler starts due plans (REQ-SCH-02 C11).
//
// "Now" is the API's clock: it is read from the Date header of a response (second precision), never
// from the test process. Plans that must not start during the test are set at least one hour ahead
// and canceled in t.Cleanup. Helpers carry a sch prefix.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var schTSI = time.FixedZone("TSİ", 3*3600)

// schServerNow returns the API's current time from the Date header of GET /api/config.
func schServerNow(t *testing.T, api string) time.Time {
	t.Helper()
	status, hdr, body := get(t, api+"/api/config")
	if status != http.StatusOK {
		t.Fatalf("GET /api/config status = %d; body: %s", status, body)
	}
	now, err := http.ParseTime(hdr.Get("Date"))
	if err != nil {
		t.Fatalf("Date header %q: %v", hdr.Get("Date"), err)
	}
	return now.UTC()
}

func schBody(vid, at string) []byte {
	return []byte(`{"content_version_id":"` + vid + `","scheduled_at":"` + at + `"}`)
}

// schParse parses an RFC 3339 UTC ("Z") timestamp of a response.
func schParse(t *testing.T, name string, v any) time.Time {
	t.Helper()
	s, _ := v.(string)
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !strings.HasSuffix(s, "Z") {
		t.Fatalf("%s = %v, want an RFC 3339 UTC timestamp with Z", name, v)
	}
	return ts
}

// schCancelIfScheduled runs in t.Cleanup, where t.Context() is already canceled: it uses its own
// short context derived from context.Background. If the campaign is still scheduled the plan is
// canceled with DELETE /schedule; failures are reported but never panic the cleanup.
func schCancelIfScheduled(t *testing.T, api, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	do := func(method, url string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return 0, nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, b, err
	}
	status, body, err := do(http.MethodGet, api+"/api/campaigns/"+id)
	if err != nil || status != http.StatusOK {
		t.Errorf("cleanup: GET campaign %s = %d, %v; body: %s", id, status, err, body)
		return
	}
	var c struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		t.Errorf("cleanup: decode campaign %s: %v", id, err)
		return
	}
	if c.State != "scheduled" {
		return
	}
	if status, body, err := do(http.MethodDelete, api+"/api/campaigns/"+id+"/schedule"); err != nil || status != http.StatusOK {
		t.Errorf("cleanup: DELETE schedule of %s = %d, %v; body: %s", id, status, err, body)
	}
}

// schMustSchedule plans the campaign at at (sent with the TSİ offset) and checks the 202 body; the
// plan is canceled in t.Cleanup if the campaign is still scheduled.
func schMustSchedule(t *testing.T, api, id, vid string, at time.Time) map[string]any {
	t.Helper()
	status, _, body := sndPost(t, api, id, schBody(vid, at.In(schTSI).Format(time.RFC3339)), nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST send with scheduled_at status = %d, want 202; body: %s", status, body)
	}
	t.Cleanup(func() { schCancelIfScheduled(t, api, id) })
	obj := decodeObject(t, body)
	if diff := cmp.Diff(sndAcceptedKeys, sortedKeys(obj)); diff != "" {
		t.Fatalf("scheduled 202 keys mismatch (-want +got):\n%s", diff)
	}
	if obj["campaign_id"] != id || obj["content_version_id"] != vid || obj["state"] != "scheduled" {
		t.Fatalf("scheduled 202 campaign_id/content_version_id/state = %v/%v/%v, want %s/%s/scheduled", obj["campaign_id"], obj["content_version_id"], obj["state"], id, vid)
	}
	want := map[string]any{"total": float64(0), "sms": float64(0), "email": float64(0)}
	if diff := cmp.Diff(want, obj["deliveries"]); diff != "" {
		t.Fatalf("scheduled 202 deliveries mismatch (-want +got):\n%s", diff)
	}
	if got := schParse(t, "scheduled 202 scheduled_at", obj["scheduled_at"]); !got.Equal(at) {
		t.Fatalf("scheduled 202 scheduled_at = %s, want %s", got, at)
	}
	return obj
}

// schAssertCampaign checks GET campaign and GET status: state, scheduled_at (nil => null) and the
// timestamps that must be null while nothing was sent.
func schAssertCampaign(t *testing.T, api, id, state string, at *time.Time) map[string]any {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d; body: %s", status, body)
	}
	c := decodeObject(t, body)
	if diff := cmp.Diff(campaignKeys, sortedKeys(c)); diff != "" {
		t.Fatalf("Campaign keys mismatch (-want +got):\n%s", diff)
	}
	s := sndStatusOf(t, api, id)
	_, _, sbody := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/status", nil, nil)
	sraw := decodeObject(t, sbody)
	for name, doc := range map[string]map[string]any{"campaign": c, "status": sraw} {
		if doc["state"] != state {
			t.Fatalf("%s state = %v, want %s", name, doc["state"], state)
		}
		if at == nil {
			if doc["scheduled_at"] != nil {
				t.Fatalf("%s scheduled_at = %v, want null", name, doc["scheduled_at"])
			}
			continue
		}
		if got := schParse(t, name+" scheduled_at", doc["scheduled_at"]); !got.Equal(*at) {
			t.Fatalf("%s scheduled_at = %s, want %s", name, got, *at)
		}
	}
	if state == "scheduled" {
		if c["approval"] == nil || c["sending_started_at"] != nil || c["completed_at"] != nil {
			t.Fatalf("scheduled campaign approval/sending_started_at/completed_at = %v/%v/%v, want set/null/null", c["approval"], c["sending_started_at"], c["completed_at"])
		}
		if s.Counts.Total != 0 {
			t.Fatalf("scheduled campaign status counts.total = %d, want 0", s.Counts.Total)
		}
	}
	return c
}

// REQ-SCH-01 C1, C2, C3, C4; REQ-SCH-03 C1, C3, C7
// Plan, reschedule, repeat and cancel over HTTP: a scheduled_at sent with +03:00 is answered in UTC;
// the campaign is scheduled with no delivery (deliveries total 0, status counts.total 0) and GET
// campaign / status / the live stream carry scheduled_at; rescheduling moves it; repeating the same
// request is a 202 with the same body; DELETE /schedule is 200 {campaign_id, state approved,
// scheduled_at null}, keeps the approval, and a second DELETE is 409 invalid_state; afterwards an
// instant send works as before.
func TestE2E_Schedule_PlanRescheduleCancel(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := sndApprovedEmpty(t, api, m)
	schAssertCampaign(t, api, id, "approved", nil)

	now := schServerNow(t, api)
	at := now.Add(2 * time.Hour).Truncate(time.Second)
	schMustSchedule(t, api, id, vid, at)
	schAssertCampaign(t, api, id, "scheduled", &at)
	sndAssertNoDeliveries(t, api, id, "scheduled")

	t.Run("the live stream carries scheduled_at", func(t *testing.T) {
		s := trkOpen(t, api, id, id, nil)
		ev := s.next(t, time.After(15*time.Second), "the snapshot")
		raw := decodeObject(t, []byte(ev.Data))
		if ev.Name != "snapshot" || raw["state"] != "scheduled" {
			t.Fatalf("first event %s state %v, want snapshot of a scheduled campaign", ev.Name, raw["state"])
		}
		if got := schParse(t, "snapshot scheduled_at", raw["scheduled_at"]); !got.Equal(at) {
			t.Fatalf("snapshot scheduled_at = %s, want %s", got, at)
		}
	})

	later := now.Add(30 * time.Hour).Truncate(time.Second)
	first := schMustSchedule(t, api, id, vid, later)
	schAssertCampaign(t, api, id, "scheduled", &later)
	again := schMustSchedule(t, api, id, vid, later)
	if diff := cmp.Diff(first, again); diff != "" {
		t.Fatalf("repeating the same schedule request changed the body (-first +again):\n%s", diff)
	}
	sndAssertNoDeliveries(t, api, id, "scheduled")
	approval := getCampaignDoc(t, api, id).Approval

	status, _, body := doJSON(t, http.MethodDelete, api+"/api/campaigns/"+id+"/schedule", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("DELETE schedule status = %d, want 200; body: %s", status, body)
	}
	if diff := cmp.Diff(map[string]any{"campaign_id": id, "state": "approved", "scheduled_at": nil}, decodeObject(t, body)); diff != "" {
		t.Fatalf("DELETE schedule body mismatch (-want +got):\n%s", diff)
	}
	schAssertCampaign(t, api, id, "approved", nil)
	if c := getCampaignDoc(t, api, id); c.Approval == nil || approval == nil || c.Approval.ID != approval.ID || c.Approval.ContentVersionID != vid {
		t.Fatalf("approval after cancel = %+v, want the unchanged %+v", c.Approval, approval)
	}
	sndAssertNoDeliveries(t, api, id, "approved")
	status, hdr, body := doJSON(t, http.MethodDelete, api+"/api/campaigns/"+id+"/schedule", nil, nil)
	assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)

	a := sndMustSend(t, api, id, vid)
	if a.State != "completed" {
		t.Fatalf("instant send after cancel state = %s, want completed (empty audience)", a.State)
	}
	schAssertCampaign(t, api, id, "completed", nil)
	status, hdr, body = doJSON(t, http.MethodDelete, api+"/api/campaigns/"+id+"/schedule", nil, nil)
	assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
}

// REQ-SCH-01 C5, C6; REQ-SCH-03 C2, C6
// Refused schedule requests and their order: malformed path UUID 404 › body 422 (format) › 422
// window › 404 campaign_not_found › 404 content_version_not_found › 409 invalid_state › 409
// stale_content_version. An unknown campaign with a past scheduled_at is 422 schedule_too_soon.
// After the send started a valid future scheduled_at is 409 invalid_state, a past one 422. No refusal
// creates a delivery or changes state / scheduled_at; DELETE on a malformed or unknown id is 404.
func TestE2E_Schedule_Refusals(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	draftID, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	readyID := audienceReady(t, api, m)
	rv := generate(t, api, readyID, "ok").validated
	if rv == nil {
		t.Fatal("setup: no validated version for the content_ready campaign")
	}
	apprID := audienceReady(t, api, m)
	v1 := generate(t, api, apprID, "ok").validated
	if v1 == nil {
		t.Fatal("setup: no validated version")
	}
	v2 := mustEdit(t, api, apprID, v1.ContentVersionID, editDoc("plan"))
	mustApprove(t, api, apprID, v2.ID)
	av := v2.ID
	doneID, doneV := sndApprovedEmpty(t, api, m)
	sndMustSend(t, api, doneID, doneV)

	now := schServerNow(t, api)
	future := now.Add(3 * time.Hour).UTC().Format(time.RFC3339)
	past := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	tooFar := now.Add(721 * time.Hour).UTC().Format(time.RFC3339)
	vidField := `"content_version_id":"` + av + `"`

	type refusal struct {
		name   string
		id     string
		body   []byte
		status int
		code   string
		fields []fieldWant // all must be present; nil => no fields
	}
	tests := []refusal{
		{name: "malformed path UUID before a bad body", id: "not-a-uuid", body: []byte(`{` + vidField + `,"scheduled_at":"x"}`), status: 404, code: "campaign_not_found"},
		{name: "local time without offset", id: apprID, body: []byte(`{` + vidField + `,"scheduled_at":"2026-01-15T15:30:00"}`), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "invalid_value"}}},
		{name: "date only", id: apprID, body: []byte(`{` + vidField + `,"scheduled_at":"2026-01-15"}`), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "invalid_value"}}},
		{name: "invalid date", id: apprID, body: []byte(`{` + vidField + `,"scheduled_at":"2026-02-30T10:00:00Z"}`), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "invalid_value"}}},
		{name: "null", id: apprID, body: []byte(`{` + vidField + `,"scheduled_at":null}`), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schema_violation"}}},
		{name: "not a string", id: apprID, body: []byte(`{` + vidField + `,"scheduled_at":1768480200}`), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schema_violation"}}},
		{name: "all body errors in one 422", id: apprID, body: []byte(`{"force":true,"content_version_id":"nope","scheduled_at":"x"}`), status: 422, code: "validation_failed",
			fields: []fieldWant{{"force", "schema_violation"}, {"content_version_id", "invalid_value"}, {"scheduled_at", "invalid_value"}}},
		{name: "a body error hides the window", id: apprID, body: []byte(`{"content_version_id":"nope","scheduled_at":"` + past + `"}`), status: 422, code: "validation_failed", fields: []fieldWant{{"content_version_id", "invalid_value"}}},
		{name: "past instant", id: apprID, body: schBody(av, past), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schedule_too_soon"}}},
		{name: "beyond 720h", id: apprID, body: schBody(av, tooFar), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schedule_too_far"}}},
		{name: "unknown campaign with a past instant is 422", id: unknownCampaignID, body: schBody(av, past), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schedule_too_soon"}}},
		{name: "unknown campaign", id: unknownCampaignID, body: schBody(av, future), status: 404, code: "campaign_not_found"},
		{name: "unknown version", id: apprID, body: schBody(unknownVerID, future), status: 404, code: "content_version_not_found"},
		{name: "another campaign's version", id: apprID, body: schBody(rv.ContentVersionID, future), status: 404, code: "content_version_not_found"},
		{name: "draft campaign", id: draftID, body: schBody(unknownVerID, future), status: 404, code: "content_version_not_found"},
		{name: "content_ready campaign", id: readyID, body: schBody(rv.ContentVersionID, future), status: 409, code: "invalid_state"},
		{name: "older version than the approved one", id: apprID, body: schBody(v1.ContentVersionID, future), status: 409, code: "stale_content_version"},
		{name: "send already started: valid future instant", id: doneID, body: schBody(doneV, future), status: 409, code: "invalid_state"},
		{name: "send already started: past instant", id: doneID, body: schBody(doneV, past), status: 422, code: "validation_failed", fields: []fieldWant{{"scheduled_at", "schedule_too_soon"}}},
	}
	t.Run("refused", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := sndPost(t, api, tc.id, tc.body, nil)
				e := assertError(t, status, hdr, body, tc.status, tc.code, tc.fields != nil)
				if tc.fields == nil {
					return
				}
				// Exactly these field errors, in this order (one 422 collects all body errors).
				var got []fieldWant
				for _, f := range e.Fields {
					got = append(got, fieldWant{f.Path, f.Code})
				}
				if diff := cmp.Diff(tc.fields, got); diff != "" {
					t.Fatalf("error.fields (path, code) mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})
	t.Run("cancel refused", func(t *testing.T) {
		for _, tc := range []struct {
			name, id string
			status   int
			code     string
		}{
			{name: "malformed id", id: "not-a-uuid", status: 404, code: "campaign_not_found"},
			{name: "unknown campaign", id: unknownCampaignID, status: 404, code: "campaign_not_found"},
			{name: "approved campaign", id: apprID, status: 409, code: "invalid_state"},
			{name: "completed campaign", id: doneID, status: 409, code: "invalid_state"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				status, hdr, body := doJSON(t, http.MethodDelete, api+"/api/campaigns/"+tc.id+"/schedule", nil, nil)
				assertError(t, status, hdr, body, tc.status, tc.code, false)
			})
		}
	})

	schAssertCampaign(t, api, apprID, "approved", nil)
	sndAssertNoDeliveries(t, api, apprID, "approved")
	sndAssertNoDeliveries(t, api, readyID, "content_ready")
	sndAssertNoDeliveries(t, api, draftID, "draft")
	if c := getCampaignDoc(t, api, doneID); c.State != "completed" {
		t.Fatalf("completed campaign state after refusals = %s", c.State)
	}
}

// REQ-SCH-03 C4, C6
// "Send now": an instant send on a scheduled campaign is the REQ-SND-01 response (state
// sending|completed, channel audiences) with scheduled_at null; the plan drops; a second instant
// send is 409 invalid_state.
func TestE2E_Schedule_SendNow(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := sndApprovedEmpty(t, api, m)
	at := schServerNow(t, api).Add(4 * time.Hour).Truncate(time.Second)
	schMustSchedule(t, api, id, vid, at)

	a := sndMustSend(t, api, id, vid) // checks scheduled_at = null
	if a.State != "completed" || a.Deliveries.Total != 0 {
		t.Fatalf("send now = %+v, want completed with 0 deliveries (empty audience)", a)
	}
	schAssertCampaign(t, api, id, "completed", nil)
	status, hdr, body := sndPost(t, api, id, sndBody(vid), nil)
	assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
}

// REQ-SCH-03 C5
// While scheduled: content edit, segment generation, content generation and approval (even of the
// approved version) are 409 invalid_state and store nothing; the campaign stays scheduled at the
// same instant; preview answers 200.
func TestE2E_Schedule_ScheduledCampaignIsLocked(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	// The example campaign (matched customers), like TestE2E_Preview: an empty audience has no sample
	// customer to preview. It is never sent: the plan is 5 h ahead and canceled in t.Cleanup.
	id, vid := sndApprovedExample(t, api, m)
	at := schServerNow(t, api).Add(5 * time.Hour).Truncate(time.Second)
	schMustSchedule(t, api, id, vid, at)
	versions := len(listVersions(t, api, id))
	before := getCampaignDoc(t, api, id)

	t.Run("edit", func(t *testing.T) {
		status, hdr, body := postEdit(t, api, id, vid, editDoc("planlı").json(t))
		assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
	})
	t.Run("segment", func(t *testing.T) {
		status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
		assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
	})
	t.Run("content", func(t *testing.T) {
		resp := postContent(t, t.Context(), api, id, nil, scenario("ok"))
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read POST content body: %v", err)
		}
		assertError(t, resp.StatusCode, resp.Header, b, http.StatusConflict, "invalid_state", false)
	})
	t.Run("approve the approved version", func(t *testing.T) {
		status, hdr, body := approve(t, api, id, vid)
		assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
	})
	t.Run("preview works", func(t *testing.T) {
		preview(t, api, id, "")
	})

	if n := len(listVersions(t, api, id)); n != versions {
		t.Fatalf("content versions = %d, want %d", n, versions)
	}
	after := getCampaignDoc(t, api, id)
	if after.Segment == nil || before.Segment == nil || after.Segment.Revision != before.Segment.Revision {
		t.Fatalf("segment after the refusals = %+v, want revision %d", after.Segment, before.Segment.Revision)
	}
	if after.Approval == nil || after.Approval.ID != before.Approval.ID {
		t.Fatalf("approval after the refusals = %+v, want %+v", after.Approval, before.Approval)
	}
	schAssertCampaign(t, api, id, "scheduled", &at)
}

// REQ-SCH-02 C1, C2, C11; REQ-SCH-01 C4
// The worker starts a plan a few seconds ahead by itself: the campaign leaves scheduled, gets one
// delivery per (channel, customer) equal to the segment's channel audiences, keeps scheduled_at
// unchanged with sending_started_at >= scheduled_at, and reaches completed.
func TestE2E_Schedule_WorkerStartsThePlan(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := sndApprovedExample(t, api, m)
	aud := getCampaignDoc(t, api, id).Segment.Audience
	at := schServerNow(t, api).Add(3 * time.Second)
	schMustSchedule(t, api, id, vid, at)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(trkStreamDeadline)
	s := sndStatusOf(t, api, id)
	for s.State != "completed" {
		select {
		case <-deadline:
			t.Fatalf("scheduled campaign not completed within %s; last status %+v", trkStreamDeadline, s)
		case <-ticker.C:
			s = sndStatusOf(t, api, id)
		}
	}
	c := schAssertCampaign(t, api, id, "completed", &at)
	started := schParse(t, "sending_started_at", c["sending_started_at"])
	if started.Before(at) {
		t.Fatalf("sending_started_at %s is before scheduled_at %s", started, at)
	}
	schParse(t, "completed_at", c["completed_at"])
	if s.ByChannel.SMS.Total != aud.SMS || s.ByChannel.Email.Total != aud.Email {
		t.Fatalf("deliveries sms/email = %d/%d, want the segment audience %d/%d", s.ByChannel.SMS.Total, s.ByChannel.Email.Total, aud.SMS, aud.Email)
	}
	seen := map[string]bool{}
	for _, d := range sndAll(t, api, id) {
		key := d.Channel + "/" + d.CustomerID
		if seen[key] {
			t.Fatalf("two deliveries for (%s, %s)", d.Channel, d.CustomerID)
		}
		seen[key] = true
		sndCheckItem(t, d, id, vid)
	}
	if len(seen) != aud.SMS+aud.Email {
		t.Fatalf("listed %d deliveries, want %d", len(seen), aud.SMS+aud.Email)
	}
}

// REQ-SCH-01 C7
// GET /api/config gives the UI the window: limits.schedule_min_lead_seconds (the test stack runs
// SCHEDULE_MIN_LEAD=0s => 0) and limits.schedule_max_ahead_seconds = 2592000.
func TestE2E_Schedule_ConfigLimits(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	status, _, body := get(t, api+"/api/config")
	if status != http.StatusOK {
		t.Fatalf("GET /api/config status = %d; body: %s", status, body)
	}
	var cfg struct {
		Limits struct {
			MinLead  *float64 `json:"schedule_min_lead_seconds"`
			MaxAhead *float64 `json:"schedule_max_ahead_seconds"`
		} `json:"limits"`
	}
	mustUnmarshal(t, string(body), &cfg)
	if cfg.Limits.MinLead == nil || *cfg.Limits.MinLead != 0 {
		t.Fatalf("limits.schedule_min_lead_seconds = %v, want 0 (SCHEDULE_MIN_LEAD=0s); body: %s", cfg.Limits.MinLead, body)
	}
	if cfg.Limits.MaxAhead == nil || *cfg.Limits.MaxAhead != 2592000 {
		t.Fatalf("limits.schedule_max_ahead_seconds = %v, want 2592000; body: %s", cfg.Limits.MaxAhead, body)
	}
}
