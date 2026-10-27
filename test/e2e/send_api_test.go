package e2e_test

// P5 send API over HTTP against the compose.test.yml stack (strict OpenAPI client, mock LLM,
// canonical seed of testdata/seed/manifest.json): POST /api/campaigns/{campaign_id}/send,
// GET /api/campaigns/{campaign_id}/status and GET /api/campaigns/{campaign_id}/deliveries.
//
// Scope: REQ-SND-01 (except the generation-lock bullets, covered by test/e2e/send_test.go and the
// campaign integration tests), REQ-SND-03 (second send), REQ-AUD-04 (consent groups at send time),
// REQ-TRK-01 (/status only; the SSE stream and the tracking page are not covered here) and
// REQ-SEC-01 (/deliveries carries no phone or e-mail).
//
// Every test creates its own campaigns and never mutates customers. Only two tests send the example
// campaign (audience.sms + audience.email deliveries each, pushed by the worker to the mock
// provider); the others send an empty audience or are refused before any delivery exists.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var (
	sndAcceptedKeys   = []string{"campaign_id", "content_version_id", "deliveries", "state"}
	sndDeliveriesKeys = []string{"email", "sms", "total"}
	sndStatusKeys     = []string{"by_channel", "campaign_id", "completed_at", "counts", "sending_started_at", "state", "updated_at"}
	sndCountKeys      = []string{"failed", "queued", "sending", "sent", "total"}
	sndChannels       = []string{"email", "sms"}
	sndPageKeys       = []string{"items", "limit", "offset", "total"}
	// The minimum key set of a delivery item (REQ-SND-01).
	sndItemKeys = []string{
		"id", "campaign_id", "content_version_id", "customer_id", "channel", "status", "attempts",
		"failed_attempts", "rate_limited_count", "retry_count", "next_attempt_at", "first_attempt_at",
		"last_attempt_at", "last_http_status", "last_error", "failure_reason", "provider_message_id",
		"sent_at", "failed_at", "created_at", "updated_at",
	}
	// Recipient address keys that must never appear in a delivery item (REQ-SEC-01).
	sndContactKeys      = []string{"to", "phone", "email", "phone_number", "email_address", "recipient", "address"}
	sndDeliveryStatuses = []string{"queued", "sending", "sent", "failed"}
	sndFailureReasons   = []string{"provider_rejected", "max_attempts_exceeded", "rate_limit_budget_exhausted", "recipient_unavailable", "content_too_long"}
	sndLastErrorRE      = regexp.MustCompile(`^(http_[0-9]{3}|timeout|connection_error|content_too_long|recipient_unavailable)$`)
)

type sndAccepted struct {
	CampaignID       string `json:"campaign_id"`
	State            string `json:"state"`
	ContentVersionID string `json:"content_version_id"`
	Deliveries       struct {
		Total int `json:"total"`
		SMS   int `json:"sms"`
		Email int `json:"email"`
	} `json:"deliveries"`
}

type sndCounts struct {
	Total   int `json:"total"`
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

type sndStatus struct {
	CampaignID string    `json:"campaign_id"`
	State      string    `json:"state"`
	Counts     sndCounts `json:"counts"`
	ByChannel  struct {
		SMS   sndCounts `json:"sms"`
		Email sndCounts `json:"email"`
	} `json:"by_channel"`
	SendingStartedAt *string `json:"sending_started_at"`
	CompletedAt      *string `json:"completed_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type sndDelivery struct {
	ID               string  `json:"id"`
	CampaignID       string  `json:"campaign_id"`
	ContentVersionID string  `json:"content_version_id"`
	CustomerID       string  `json:"customer_id"`
	Channel          string  `json:"channel"`
	Status           string  `json:"status"`
	Attempts         int     `json:"attempts"`
	FailedAttempts   int     `json:"failed_attempts"`
	RateLimitedCount int     `json:"rate_limited_count"`
	RetryCount       int     `json:"retry_count"`
	FirstAttemptAt   *string `json:"first_attempt_at"`
	LastError        *string `json:"last_error"`
	FailureReason    *string `json:"failure_reason"`
	SentAt           *string `json:"sent_at"`
	FailedAt         *string `json:"failed_at"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type sndPage struct {
	Items  []sndDelivery `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

type sndGroupsDoc struct {
	Groups map[string]struct {
		CustomerIDs         []string `json:"customer_ids"`
		MatchesExampleBrief bool     `json:"matches_example_brief"`
		InSMSAudience       bool     `json:"in_sms_audience"`
		InEmailAudience     bool     `json:"in_email_audience"`
	} `json:"groups"`
}

func sndReadGroups(t *testing.T) sndGroupsDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(manifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var g sndGroupsDoc
	mustUnmarshal(t, string(b), &g)
	return g
}

// ---- request helpers ----

func sndBody(vid string) []byte { return []byte(`{"content_version_id":"` + vid + `"}`) }

// sndPadded returns a valid send body of exactly n bytes (JSON whitespace before the closing brace).
func sndPadded(t *testing.T, vid string, n int) []byte {
	t.Helper()
	head := `{"content_version_id":"` + vid + `"`
	pad := n - len(head) - 1
	if pad < 0 {
		t.Fatalf("test setup: %d bytes is too small for a send body", n)
	}
	b := []byte(head + strings.Repeat(" ", pad) + "}")
	if len(b) != n {
		t.Fatalf("test setup: body is %d bytes, want %d", len(b), n)
	}
	return b
}

func sndPost(t *testing.T, api, id string, body []byte, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	return doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/send", body, headers)
}

// sndMustSend sends the approved version and checks the 202 body shape.
func sndMustSend(t *testing.T, api, id, vid string) sndAccepted {
	t.Helper()
	status, _, body := sndPost(t, api, id, sndBody(vid), nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST send status = %d, want 202; body: %s", status, body)
	}
	obj := decodeObject(t, body)
	if diff := cmp.Diff(sndAcceptedKeys, sortedKeys(obj)); diff != "" {
		t.Fatalf("send 202 body keys mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(sndDeliveriesKeys, sortedKeys(asObject(t, obj["deliveries"]))); diff != "" {
		t.Fatalf("send 202 deliveries keys mismatch (-want +got):\n%s", diff)
	}
	var a sndAccepted
	mustUnmarshal(t, string(body), &a)
	if a.CampaignID != id || a.ContentVersionID != vid {
		t.Fatalf("send 202 campaign_id/content_version_id = %s/%s, want %s/%s", a.CampaignID, a.ContentVersionID, id, vid)
	}
	if a.Deliveries.Total != a.Deliveries.SMS+a.Deliveries.Email {
		t.Fatalf("send 202 deliveries = %+v, want total = sms + email", a.Deliveries)
	}
	return a
}

// sndApprovedExample returns an approved campaign of the example brief (SEG_OK segment, CNT_OK
// content) and its approved version.
func sndApprovedExample(t *testing.T, api string, m manifestDoc) (id, vid string) {
	t.Helper()
	id = audienceReady(t, api, m)
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: content generation did not validate")
	}
	mustApprove(t, api, id, v.ContentVersionID)
	return id, v.ContentVersionID
}

// sndApprovedEmpty returns an approved campaign whose segment matches nobody (mock scenario
// sql_in_value: the SQL text is only a value, REQ-AUD-02), so both channel audiences are 0.
func sndApprovedEmpty(t *testing.T, api string, m manifestDoc) (id, vid string) {
	t.Helper()
	id, _ = createCampaign(t, api, m.ExampleBrief.Brief)
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("sql_in_value"))
	if status != http.StatusOK {
		t.Fatalf("POST segment (sql_in_value) status = %d; body: %s", status, body)
	}
	c := getCampaignDoc(t, api, id)
	if c.Segment == nil || c.Segment.Audience != (audienceDoc{}) {
		t.Fatalf("setup: sql_in_value segment = %+v, want an empty audience", c.Segment)
	}
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: content generation did not validate")
	}
	mustApprove(t, api, id, v.ContentVersionID)
	return id, v.ContentVersionID
}

// sndStatusOf reads GET /status, checks the exact CampaignStatus shape and the counter invariants.
func sndStatusOf(t *testing.T, api, id string) sndStatus {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/status", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status status = %d, want 200; body: %s", status, body)
	}
	obj := decodeObject(t, body)
	if diff := cmp.Diff(sndStatusKeys, sortedKeys(obj)); diff != "" {
		t.Fatalf("CampaignStatus keys mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(sndCountKeys, sortedKeys(asObject(t, obj["counts"]))); diff != "" {
		t.Fatalf("CampaignStatus.counts keys mismatch (-want +got):\n%s", diff)
	}
	bc := asObject(t, obj["by_channel"])
	if diff := cmp.Diff(sndChannels, sortedKeys(bc)); diff != "" {
		t.Fatalf("CampaignStatus.by_channel keys mismatch (-want +got):\n%s", diff)
	}
	for _, ch := range sndChannels {
		if diff := cmp.Diff(sndCountKeys, sortedKeys(asObject(t, bc[ch]))); diff != "" {
			t.Fatalf("CampaignStatus.by_channel.%s keys mismatch (-want +got):\n%s", ch, diff)
		}
	}
	var s sndStatus
	mustUnmarshal(t, string(body), &s)
	if s.CampaignID != id {
		t.Fatalf("CampaignStatus.campaign_id = %s, want %s", s.CampaignID, id)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err != nil {
		t.Fatalf("CampaignStatus.updated_at = %q: %v", s.UpdatedAt, err)
	}
	sndCheckCounts(t, s)
	return s
}

// sndCheckCounts: total = queued + sending + sent + failed for each channel and overall, and the
// overall counts are the sum of the channels.
func sndCheckCounts(t *testing.T, s sndStatus) {
	t.Helper()
	for name, c := range map[string]sndCounts{"counts": s.Counts, "by_channel.sms": s.ByChannel.SMS, "by_channel.email": s.ByChannel.Email} {
		if c.Total < 0 || c.Queued < 0 || c.Sending < 0 || c.Sent < 0 || c.Failed < 0 {
			t.Fatalf("%s = %+v has a negative counter", name, c)
		}
		if c.Total != c.Queued+c.Sending+c.Sent+c.Failed {
			t.Fatalf("%s = %+v, want total = queued + sending + sent + failed", name, c)
		}
	}
	sms, email := s.ByChannel.SMS, s.ByChannel.Email
	sum := sndCounts{Total: sms.Total + email.Total, Queued: sms.Queued + email.Queued, Sending: sms.Sending + email.Sending, Sent: sms.Sent + email.Sent, Failed: sms.Failed + email.Failed}
	if s.Counts != sum {
		t.Fatalf("counts = %+v, want the sum of by_channel %+v", s.Counts, sum)
	}
}

// sndAssertNoDeliveries is the observation of "no delivery row was created" (REQ-SND-01): /deliveries
// total 0 with empty items, /status counts.total 0, and the campaign state unchanged.
func sndAssertNoDeliveries(t *testing.T, api, id, wantState string) {
	t.Helper()
	p, _ := sndList(t, api, id, "")
	if p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("deliveries of %s: total %d, %d items; want 0 and none", id, p.Total, len(p.Items))
	}
	s := sndStatusOf(t, api, id)
	if s.Counts.Total != 0 || s.State != wantState {
		t.Fatalf("status of %s: counts.total %d state %s; want 0 and %s", id, s.Counts.Total, s.State, wantState)
	}
	if c := getCampaignDoc(t, api, id); c.State != wantState {
		t.Fatalf("campaign %s state = %s, want %s", id, c.State, wantState)
	}
}

// sndList reads one /deliveries page, checks the page and item shapes and that no recipient address
// is exposed (REQ-SEC-01).
func sndList(t *testing.T, api, id, query string) (sndPage, []byte) {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/deliveries"+query, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET deliveries%s status = %d, want 200; body: %s", query, status, body)
	}
	obj := decodeObject(t, body)
	if diff := cmp.Diff(sndPageKeys, sortedKeys(obj)); diff != "" {
		t.Fatalf("deliveries page keys mismatch (-want +got):\n%s", diff)
	}
	items, ok := obj["items"].([]any)
	if !ok {
		t.Fatalf("deliveries items = %v (%T), want a JSON array", obj["items"], obj["items"])
	}
	for i, it := range items {
		o := asObject(t, it)
		for _, k := range sndItemKeys {
			if _, ok := o[k]; !ok {
				t.Fatalf("deliveries items[%d] lacks %q: %v", i, k, o)
			}
		}
		for _, k := range sndContactKeys {
			if _, ok := o[k]; ok {
				t.Fatalf("deliveries items[%d] exposes the recipient key %q: %v", i, k, o)
			}
		}
	}
	for _, s := range []string{"@example.", "+90500000", "+90 500 000"} {
		if strings.Contains(string(body), s) {
			t.Fatalf("deliveries response contains a recipient address fragment %q", s)
		}
	}
	var p sndPage
	mustUnmarshal(t, string(body), &p)
	return p, body
}

// sndAll pages through every delivery of the campaign in the default customer order.
func sndAll(t *testing.T, api, id string) []sndDelivery {
	t.Helper()
	var out []sndDelivery
	total := -1
	for offset := 0; ; {
		p, _ := sndList(t, api, id, "?order=customer&limit=200&offset="+strconv.Itoa(offset))
		if total >= 0 && p.Total != total {
			t.Fatalf("deliveries total changed while paging: %d then %d", total, p.Total)
		}
		total = p.Total
		out = append(out, p.Items...)
		offset += len(p.Items)
		if len(p.Items) == 0 || offset >= p.Total {
			break
		}
	}
	if len(out) != total {
		t.Fatalf("paged %d deliveries, want total %d", len(out), total)
	}
	return out
}

// sndCheckItem checks one delivery item against REQ-SND-01/REQ-SND-04 field semantics.
func sndCheckItem(t *testing.T, d sndDelivery, id, vid string) {
	t.Helper()
	if d.ID == "" || d.CampaignID != id || d.ContentVersionID != vid {
		t.Fatalf("delivery %+v, want a non-empty id, campaign %s, version %s", d, id, vid)
	}
	if !uuidRE.MatchString(d.CustomerID) {
		t.Fatalf("delivery %s customer_id = %q, want a lower-case UUID", d.ID, d.CustomerID)
	}
	if !slices.Contains(sndChannels, d.Channel) || !slices.Contains(sndDeliveryStatuses, d.Status) {
		t.Fatalf("delivery %s channel/status = %s/%s", d.ID, d.Channel, d.Status)
	}
	if d.Attempts < 0 || d.FailedAttempts < 0 || d.FailedAttempts > 5 || d.RateLimitedCount < 0 || d.RateLimitedCount > 20 || d.RetryCount < 0 {
		t.Fatalf("delivery %s counters attempts/failed/rate_limited/retry = %d/%d/%d/%d out of range", d.ID, d.Attempts, d.FailedAttempts, d.RateLimitedCount, d.RetryCount)
	}
	// Every delivery is born queued; only the dispatcher moves it.
	if d.Attempts == 0 && (d.Status != "queued" || d.FirstAttemptAt != nil) {
		t.Fatalf("delivery %s was never taken (attempts 0) but is %s / first_attempt_at %v; want queued", d.ID, d.Status, d.FirstAttemptAt)
	}
	switch d.Status {
	case "sent":
		if d.FailureReason != nil || d.SentAt == nil || d.FailedAt != nil {
			t.Fatalf("sent delivery %s failure_reason/sent_at/failed_at = %v/%v/%v, want null/set/null", d.ID, d.FailureReason, d.SentAt, d.FailedAt)
		}
	case "failed":
		if d.FailureReason == nil || !slices.Contains(sndFailureReasons, *d.FailureReason) || d.FailedAt == nil || d.SentAt != nil {
			t.Fatalf("failed delivery %s failure_reason/failed_at/sent_at = %v/%v/%v, want a contracted reason/set/null", d.ID, d.FailureReason, d.FailedAt, d.SentAt)
		}
	default:
		if d.SentAt != nil || d.FailedAt != nil {
			t.Fatalf("%s delivery %s has sent_at/failed_at %v/%v, want both null", d.Status, d.ID, d.SentAt, d.FailedAt)
		}
	}
	if d.LastError != nil && !sndLastErrorRE.MatchString(*d.LastError) {
		t.Fatalf("delivery %s last_error = %q, outside the closed set", d.ID, *d.LastError)
	}
	for name, ts := range map[string]string{"created_at": d.CreatedAt, "updated_at": d.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Fatalf("delivery %s %s = %q: %v", d.ID, name, ts, err)
		}
	}
}

// sndMonotonic: the totals never change after the send and sent/failed never decrease.
func sndMonotonic(t *testing.T, prev, cur sndStatus) {
	t.Helper()
	pairs := map[string][2]sndCounts{
		"counts":           {prev.Counts, cur.Counts},
		"by_channel.sms":   {prev.ByChannel.SMS, cur.ByChannel.SMS},
		"by_channel.email": {prev.ByChannel.Email, cur.ByChannel.Email},
	}
	for name, p := range pairs {
		a, b := p[0], p[1]
		if b.Total != a.Total || b.Sent < a.Sent || b.Failed < a.Failed {
			t.Fatalf("%s went from %+v to %+v; want the same total and non-decreasing sent/failed", name, a, b)
		}
	}
	if prev.State == "completed" && cur.State != "completed" {
		t.Fatalf("state went from completed back to %s", cur.State)
	}
}

func sndCustomerOrder(t *testing.T, items []sndDelivery) {
	t.Helper()
	for i := 1; i < len(items); i++ {
		a, b := items[i-1], items[i]
		if a.CustomerID > b.CustomerID || (a.CustomerID == b.CustomerID && a.Channel >= b.Channel) {
			t.Fatalf("items[%d] (%s, %s) does not come after items[%d] (%s, %s); want customer_id ascending, then channel", i, b.CustomerID, b.Channel, i-1, a.CustomerID, a.Channel)
		}
	}
}

// ---- tests ----

// REQ-SND-01, REQ-SND-03, REQ-AUD-04, REQ-TRK-01, REQ-SEC-01
// The example campaign end to end over the send API: before the send /status is all zeros; the
// send is 202 with the contracted body (state sending, per-channel counts equal to the segment
// audience and the manifest); exactly one delivery exists per (channel, customer), created for the
// approved version and born queued; the consent groups of the manifest decide the channels; the
// /deliveries filters, paging, ordering and 422s work on the real rows; a second send is 409
// invalid_state and changes nothing; the campaign reaches completed with sent + failed = total.
func TestE2E_SendAPI_ExampleCampaign(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	groups := sndReadGroups(t)
	id, vid := sndApprovedExample(t, api, m)
	aud := getCampaignDoc(t, api, id).Segment.Audience

	before := sndStatusOf(t, api, id)
	if before.State != "approved" || before.Counts != (sndCounts{}) || before.SendingStartedAt != nil || before.CompletedAt != nil {
		t.Fatalf("status before the send = %+v, want approved, all counters 0, null timestamps", before)
	}

	a := sndMustSend(t, api, id, vid)
	if a.State != "sending" {
		t.Fatalf("send 202 state = %s, want sending", a.State)
	}
	if a.Deliveries.SMS != aud.SMS || a.Deliveries.Email != aud.Email {
		t.Fatalf("send 202 deliveries sms/email = %d/%d, want the segment audience %d/%d", a.Deliveries.SMS, a.Deliveries.Email, aud.SMS, aud.Email)
	}
	if a.Deliveries.SMS != m.ExampleBrief.Audience.SMS || a.Deliveries.Email != m.ExampleBrief.Audience.Email {
		t.Fatalf("send 202 deliveries sms/email = %d/%d, want the manifest's %d/%d", a.Deliveries.SMS, a.Deliveries.Email, m.ExampleBrief.Audience.SMS, m.ExampleBrief.Audience.Email)
	}

	t.Run("campaign is sending", func(t *testing.T) {
		status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("GET campaign status = %d; body: %s", status, body)
		}
		c := decodeObject(t, body)
		if st := c["state"]; st != "sending" && st != "completed" {
			t.Fatalf("campaign state after the send = %v, want sending (or completed once every delivery ended)", st)
		}
		if s, _ := c["sending_started_at"].(string); s == "" {
			t.Fatalf("campaign sending_started_at = %v, want a timestamp after the send", c["sending_started_at"])
		}
	})

	t.Run("status counts the created deliveries", func(t *testing.T) {
		s := sndStatusOf(t, api, id)
		if s.State != "sending" && s.State != "completed" {
			t.Fatalf("status state = %s, want sending or completed", s.State)
		}
		if s.Counts.Total != a.Deliveries.Total || s.ByChannel.SMS.Total != a.Deliveries.SMS || s.ByChannel.Email.Total != a.Deliveries.Email {
			t.Fatalf("status totals %d/%d/%d, want the send's %d/%d/%d", s.Counts.Total, s.ByChannel.SMS.Total, s.ByChannel.Email.Total, a.Deliveries.Total, a.Deliveries.SMS, a.Deliveries.Email)
		}
		if s.SendingStartedAt == nil {
			t.Fatal("status sending_started_at is null after the send")
		}
		if s.State == "sending" && s.CompletedAt != nil {
			t.Fatalf("status completed_at = %s while sending, want null", *s.CompletedAt)
		}
		sndMonotonic(t, s, sndStatusOf(t, api, id))
	})

	var all []sndDelivery
	t.Run("one delivery per channel and customer", func(t *testing.T) {
		all = sndAll(t, api, id)
		if len(all) != a.Deliveries.Total {
			t.Fatalf("listed %d deliveries, want %d", len(all), a.Deliveries.Total)
		}
		sndCustomerOrder(t, all)
		seen := map[string]bool{}
		ids := map[string]bool{}
		perChannel := map[string]int{}
		for _, d := range all {
			sndCheckItem(t, d, id, vid)
			key := d.Channel + "/" + d.CustomerID
			if seen[key] {
				t.Fatalf("two deliveries for (%s, %s)", d.Channel, d.CustomerID)
			}
			if ids[d.ID] {
				t.Fatalf("delivery id %s listed twice", d.ID)
			}
			seen[key], ids[d.ID] = true, true
			perChannel[d.Channel]++
		}
		if perChannel["sms"] != a.Deliveries.SMS || perChannel["email"] != a.Deliveries.Email {
			t.Fatalf("listed per channel = %v, want sms %d email %d", perChannel, a.Deliveries.SMS, a.Deliveries.Email)
		}
	})

	t.Run("consent groups decide the channels", func(t *testing.T) {
		if all == nil {
			t.Fatal("the full listing failed above")
		}
		channels := map[string][]string{}
		for _, d := range all {
			channels[d.CustomerID] = append(channels[d.CustomerID], d.Channel)
		}
		for _, name := range []string{"istanbul_no_consent", "istanbul_sms_only", "istanbul_email_only", "sms_consent_missing_phone", "email_consent_missing_email", "shared_contact"} {
			if len(groups.Groups[name].CustomerIDs) == 0 {
				t.Fatalf("manifest group %s is missing or empty", name)
			}
		}
		for name, g := range groups.Groups {
			var want []string
			if g.InEmailAudience {
				want = append(want, "email")
			}
			if g.InSMSAudience {
				want = append(want, "sms")
			}
			for _, cid := range g.CustomerIDs {
				got := slices.Sorted(slices.Values(channels[cid]))
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("group %s customer %s channels mismatch (-want +got):\n%s", name, cid, diff)
				}
			}
		}
	})

	t.Run("customer_id filter", func(t *testing.T) {
		smsOnly := groups.Groups["istanbul_sms_only"].CustomerIDs[0]
		emailOnly := groups.Groups["istanbul_email_only"].CustomerIDs[0]
		noConsent := groups.Groups["istanbul_no_consent"].CustomerIDs[0]
		shared := groups.Groups["shared_contact"].CustomerIDs
		tests := []struct {
			name  string
			query string
			want  []string // channels, in order
		}{
			{name: "sms-only customer has only sms", query: "?customer_id=" + smsOnly, want: []string{"sms"}},
			{name: "email-only customer has only email", query: "?customer_id=" + emailOnly, want: []string{"email"}},
			{name: "customer without consent has none", query: "?customer_id=" + noConsent},
			{name: "sms-only customer filtered on email has none", query: "?customer_id=" + smsOnly + "&channel=email"},
			{name: "shared contact first customer has both", query: "?customer_id=" + shared[0], want: []string{"email", "sms"}},
			{name: "shared contact second customer has both", query: "?customer_id=" + shared[1], want: []string{"email", "sms"}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				p, _ := sndList(t, api, id, tc.query)
				var got []string
				for _, d := range p.Items {
					got = append(got, d.Channel)
				}
				if p.Total != len(tc.want) || !slices.Equal(got, tc.want) {
					t.Fatalf("total %d channels %v, want %d %v", p.Total, got, len(tc.want), tc.want)
				}
			})
		}
	})

	t.Run("filters paging and ordering", func(t *testing.T) {
		t.Run("defaults are limit 50 offset 0 in customer order", func(t *testing.T) {
			t.Parallel()
			p, _ := sndList(t, api, id, "")
			if p.Limit != 50 || p.Offset != 0 || p.Total != a.Deliveries.Total || len(p.Items) != min(50, a.Deliveries.Total) {
				t.Fatalf("default page limit/offset/total/items = %d/%d/%d/%d", p.Limit, p.Offset, p.Total, len(p.Items))
			}
			sndCustomerOrder(t, p.Items)
		})
		for _, ch := range sndChannels {
			t.Run("channel "+ch, func(t *testing.T) {
				t.Parallel()
				want := a.Deliveries.SMS
				if ch == "email" {
					want = a.Deliveries.Email
				}
				p, _ := sndList(t, api, id, "?channel="+ch+"&limit=200")
				if p.Total != want {
					t.Fatalf("channel=%s total = %d, want %d", ch, p.Total, want)
				}
				for _, d := range p.Items {
					if d.Channel != ch {
						t.Fatalf("channel=%s returned a %s delivery", ch, d.Channel)
					}
				}
				sndCustomerOrder(t, p.Items)
			})
		}
		for _, st := range sndDeliveryStatuses {
			t.Run("status "+st, func(t *testing.T) {
				t.Parallel()
				p, _ := sndList(t, api, id, "?status="+st+"&limit=200")
				if p.Total < 0 || p.Total > a.Deliveries.Total || len(p.Items) > 200 {
					t.Fatalf("status=%s total %d items %d, want within [0, %d]", st, p.Total, len(p.Items), a.Deliveries.Total)
				}
				for _, d := range p.Items {
					if d.Status != st {
						t.Fatalf("status=%s returned a %s delivery", st, d.Status)
					}
				}
			})
		}
		t.Run("order recent is updated_at descending", func(t *testing.T) {
			t.Parallel()
			p, _ := sndList(t, api, id, "?order=recent&limit=200")
			var prev time.Time
			for i, d := range p.Items {
				ts, err := time.Parse(time.RFC3339Nano, d.UpdatedAt)
				if err != nil {
					t.Fatalf("items[%d].updated_at = %q: %v", i, d.UpdatedAt, err)
				}
				if i > 0 && ts.After(prev) {
					t.Fatalf("items[%d].updated_at %s is newer than items[%d] %s; want updated_at descending", i, ts, i-1, prev)
				}
				prev = ts
			}
		})
		paging := []struct {
			name      string
			query     string
			wantLimit int
			wantOff   int
			wantItems int
		}{
			{name: "limit 1", query: "?limit=1", wantLimit: 1, wantItems: 1},
			{name: "limit 200", query: "?limit=200", wantLimit: 200, wantItems: min(200, a.Deliveries.Total)},
			{name: "offset at the last item", query: "?offset=" + strconv.Itoa(a.Deliveries.Total-1), wantLimit: 50, wantOff: a.Deliveries.Total - 1, wantItems: 1},
			{name: "offset at total is empty", query: "?offset=" + strconv.Itoa(a.Deliveries.Total), wantLimit: 50, wantOff: a.Deliveries.Total, wantItems: 0},
		}
		for _, tc := range paging {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				p, _ := sndList(t, api, id, tc.query)
				if p.Limit != tc.wantLimit || p.Offset != tc.wantOff || len(p.Items) != tc.wantItems || p.Total != a.Deliveries.Total {
					t.Fatalf("limit/offset/items/total = %d/%d/%d/%d, want %d/%d/%d/%d", p.Limit, p.Offset, len(p.Items), p.Total, tc.wantLimit, tc.wantOff, tc.wantItems, a.Deliveries.Total)
				}
			})
		}
		rejected := []struct {
			name  string
			query string
			path  string
		}{
			{name: "unknown status", query: "?status=done", path: "query.status"},
			{name: "unknown channel", query: "?channel=push", path: "query.channel"},
			{name: "unknown order", query: "?order=oldest", path: "query.order"},
			{name: "customer_id not a UUID", query: "?customer_id=abc", path: "query.customer_id"},
			{name: "limit 0", query: "?limit=0", path: "query.limit"},
			{name: "limit 201", query: "?limit=201", path: "query.limit"},
			{name: "limit not a number", query: "?limit=ten", path: "query.limit"},
			{name: "negative offset", query: "?offset=-1", path: "query.offset"},
			{name: "offset not a number", query: "?offset=x", path: "query.offset"},
		}
		for _, tc := range rejected {
			t.Run("rejects "+tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/deliveries"+tc.query, nil, nil)
				e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
				if !e.has(fieldWant{tc.path, "invalid_value"}) {
					t.Fatalf("error.fields = %+v, want {%s invalid_value}", e.Fields, tc.path)
				}
			})
		}
	})

	t.Run("second send is 409 and changes nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			body   []byte
			status int
			code   string
		}{
			{name: "same version again", body: sndBody(vid), status: http.StatusConflict, code: "invalid_state"},
			{name: "unknown version is checked before the state", body: sndBody(unknownVerID), status: http.StatusNotFound, code: "content_version_not_found"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				status, hdr, body := sndPost(t, api, id, tc.body, nil)
				assertError(t, status, hdr, body, tc.status, tc.code, false)
			})
		}
		p, _ := sndList(t, api, id, "?limit=1")
		s := sndStatusOf(t, api, id)
		if p.Total != a.Deliveries.Total || s.Counts.Total != a.Deliveries.Total {
			t.Fatalf("after a second send deliveries total %d / status total %d, want %d", p.Total, s.Counts.Total, a.Deliveries.Total)
		}
	})

	t.Run("campaign reaches completed", func(t *testing.T) {
		prev := sndStatusOf(t, api, id)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(120 * time.Second)
		for prev.State != "completed" {
			select {
			case <-deadline:
				t.Fatalf("campaign not completed within 120s; last status %+v", prev)
			case <-ticker.C:
				cur := sndStatusOf(t, api, id)
				sndMonotonic(t, prev, cur)
				prev = cur
			}
		}
		for name, c := range map[string]sndCounts{"counts": prev.Counts, "by_channel.sms": prev.ByChannel.SMS, "by_channel.email": prev.ByChannel.Email} {
			if c.Queued != 0 || c.Sending != 0 || c.Sent+c.Failed != c.Total {
				t.Fatalf("completed %s = %+v, want queued = sending = 0 and sent + failed = total", name, c)
			}
		}
		if prev.Counts.Total != a.Deliveries.Total || prev.CompletedAt == nil || prev.SendingStartedAt == nil {
			t.Fatalf("completed status = %+v, want total %d and both timestamps set", prev, a.Deliveries.Total)
		}
		if c := getCampaignDoc(t, api, id); c.State != "completed" {
			t.Fatalf("campaign state = %s after /status said completed", c.State)
		}
		status, hdr, body := sndPost(t, api, id, sndBody(vid), nil)
		assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
	})
}

// REQ-SND-03, REQ-SND-01
// Eight send requests for the same approved campaign racing: exactly one is 202, every other one is
// 409 invalid_state, and the campaign has exactly audience.sms + audience.email deliveries.
func TestE2E_SendAPI_ConcurrentSendsAcceptOne(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := sndApprovedExample(t, api, m)

	type result struct {
		status int
		hdr    http.Header
		body   []byte
		err    error
	}
	const n = 8
	results := make([]result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i].status, results[i].hdr, results[i].body, results[i].err = sndRawPost(t.Context(), api+"/api/campaigns/"+id+"/send", sndBody(vid))
		}(i)
	}
	close(start)
	wg.Wait()

	accepted := 0
	var a sndAccepted
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("send %d: %v", i, r.err)
		}
		if r.status == http.StatusAccepted {
			accepted++
			mustUnmarshal(t, string(r.body), &a)
			continue
		}
		assertError(t, r.status, r.hdr, r.body, http.StatusConflict, "invalid_state", false)
	}
	if accepted != 1 {
		t.Fatalf("%d sends accepted, want exactly 1", accepted)
	}
	want := m.ExampleBrief.Audience.SMS + m.ExampleBrief.Audience.Email
	if a.Deliveries.Total != want {
		t.Fatalf("accepted send deliveries.total = %d, want %d", a.Deliveries.Total, want)
	}
	p, _ := sndList(t, api, id, "?limit=1")
	s := sndStatusOf(t, api, id)
	if p.Total != want || s.Counts.Total != want {
		t.Fatalf("after racing sends deliveries total %d / status total %d, want %d", p.Total, s.Counts.Total, want)
	}
}

func sndRawPost(ctx context.Context, url string, body []byte) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

// REQ-SND-01, REQ-TRK-01
// When both channel audiences are empty the send completes in the same operation: 202 with state
// completed and zero deliveries; /status is completed with all counters 0; the listing is empty;
// another send is 409 invalid_state.
func TestE2E_SendAPI_EmptyAudienceCompletesImmediately(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := sndApprovedEmpty(t, api, m)

	a := sndMustSend(t, api, id, vid)
	if a.State != "completed" || a.Deliveries.Total != 0 || a.Deliveries.SMS != 0 || a.Deliveries.Email != 0 {
		t.Fatalf("send 202 = %+v, want state completed and 0/0/0 deliveries", a)
	}
	s := sndStatusOf(t, api, id)
	if s.State != "completed" || s.Counts != (sndCounts{}) || s.ByChannel.SMS != (sndCounts{}) || s.ByChannel.Email != (sndCounts{}) || s.CompletedAt == nil {
		t.Fatalf("status = %+v, want completed, all counters 0, completed_at set", s)
	}
	if c := getCampaignDoc(t, api, id); c.State != "completed" {
		t.Fatalf("campaign state = %s, want completed", c.State)
	}
	p, _ := sndList(t, api, id, "")
	if p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("deliveries total %d items %d, want 0", p.Total, len(p.Items))
	}
	status, hdr, body := sndPost(t, api, id, sndBody(vid), nil)
	assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
}

// REQ-SND-01, REQ-APR-01
// Refused sends and their order. Body errors (400 malformed_request, 413 payload_too_large, 422
// validation_failed) come before every campaign check, even for an unknown campaign; then 404
// campaign_not_found › 404 content_version_not_found › 409 invalid_state › 409
// stale_content_version. No refused send creates a delivery or changes a state.
func TestE2E_SendAPI_RefusalsCreateNothing(t *testing.T) {
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
		t.Fatal("setup: no validated version for the approved campaign")
	}
	v2 := mustEdit(t, api, apprID, v1.ContentVersionID, editDoc("gönderim"))
	mustApprove(t, api, apprID, v2.ID)
	av := v2.ID

	type refusal struct {
		name    string
		id      string
		body    []byte
		headers map[string]string
		status  int
		code    string
		field   *fieldWant // nil: no fields; Path "" matches the code on any path
	}
	var tests []refusal
	for _, target := range []struct{ name, id string }{{"unknown campaign", unknownCampaignID}, {"approved campaign", apprID}} {
		tests = append(tests,
			refusal{name: target.name + ": no body", id: target.id, status: 400, code: "malformed_request"},
			refusal{name: target.name + ": empty body", id: target.id, body: []byte{}, status: 400, code: "malformed_request"},
			refusal{name: target.name + ": body is not JSON", id: target.id, body: []byte("content_version_id=" + av), status: 400, code: "malformed_request"},
			refusal{name: target.name + ": Content-Type is not JSON", id: target.id, body: sndBody(av), headers: map[string]string{"Content-Type": "text/plain"}, status: 400, code: "malformed_request"},
			refusal{name: target.name + ": body of 65537 bytes", id: target.id, body: sndPadded(t, av, 65537), status: 413, code: "payload_too_large"},
			refusal{name: target.name + ": content_version_id missing", id: target.id, body: []byte(`{}`), status: 422, code: "validation_failed", field: &fieldWant{"", "schema_violation"}},
			refusal{name: target.name + ": unknown field", id: target.id, body: []byte(`{"content_version_id":"` + av + `","force":true}`), status: 422, code: "validation_failed", field: &fieldWant{"", "schema_violation"}},
			refusal{name: target.name + ": content_version_id not a UUID", id: target.id, body: []byte(`{"content_version_id":"not-a-uuid"}`), status: 422, code: "validation_failed", field: &fieldWant{"content_version_id", "invalid_value"}},
		)
	}
	tests = append(tests,
		refusal{name: "body of exactly 65536 bytes passes the size check", id: unknownCampaignID, body: sndPadded(t, av, 65536), status: 404, code: "campaign_not_found"},
		refusal{name: "unknown campaign", id: unknownCampaignID, body: sndBody(av), status: 404, code: "campaign_not_found"},
		refusal{name: "unknown campaign is checked before the version", id: unknownCampaignID, body: sndBody(unknownVerID), status: 404, code: "campaign_not_found"},
		refusal{name: "draft: unknown version", id: draftID, body: sndBody(unknownVerID), status: 404, code: "content_version_not_found"},
		refusal{name: "draft: another campaign's version", id: draftID, body: sndBody(rv.ContentVersionID), status: 404, code: "content_version_not_found"},
		refusal{name: "content_ready: its own validated version is not approved", id: readyID, body: sndBody(rv.ContentVersionID), status: 409, code: "invalid_state"},
		refusal{name: "content_ready: another campaign's version", id: readyID, body: sndBody(av), status: 404, code: "content_version_not_found"},
		refusal{name: "approved: unknown version", id: apprID, body: sndBody(unknownVerID), status: 404, code: "content_version_not_found"},
		refusal{name: "approved: another campaign's version", id: apprID, body: sndBody(rv.ContentVersionID), status: 404, code: "content_version_not_found"},
		refusal{name: "approved: a version older than the approved one is stale", id: apprID, body: sndBody(v1.ContentVersionID), status: 409, code: "stale_content_version"},
	)
	// The group returns only after its parallel subtests finished.
	t.Run("refused", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := sndPost(t, api, tc.id, tc.body, tc.headers)
				e := assertError(t, status, hdr, body, tc.status, tc.code, tc.field != nil)
				if tc.field != nil && !e.hasLoose(*tc.field) {
					t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
				}
			})
		}
	})

	sndAssertNoDeliveries(t, api, apprID, "approved")
	sndAssertNoDeliveries(t, api, readyID, "content_ready")
	sndAssertNoDeliveries(t, api, draftID, "draft")
	if c := getCampaignDoc(t, api, apprID); c.Approval == nil || c.Approval.ContentVersionID != av {
		t.Fatalf("approval after refused sends = %+v, want the approval of %s", c.Approval, av)
	}
}

// REQ-TRK-01
// Before any send /status answers 200 for every pre-send state with the campaign's state, every
// counter 0 and null sending_started_at / completed_at.
func TestE2E_CampaignStatus_BeforeSending(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	draftID, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	readyID := audienceReady(t, api, m)
	contentID := audienceReady(t, api, m)
	if generate(t, api, contentID, "ok").validated == nil {
		t.Fatal("setup: no validated version")
	}
	apprID, _ := sndApprovedExample(t, api, m)

	tests := []struct {
		state string
		id    string
	}{
		{state: "draft", id: draftID},
		{state: "audience_ready", id: readyID},
		{state: "content_ready", id: contentID},
		{state: "approved", id: apprID},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()
			s := sndStatusOf(t, api, tc.id)
			if s.State != tc.state {
				t.Fatalf("status state = %s, want %s", s.State, tc.state)
			}
			if s.Counts != (sndCounts{}) || s.ByChannel.SMS != (sndCounts{}) || s.ByChannel.Email != (sndCounts{}) {
				t.Fatalf("status counters = %+v, want all 0 before a send", s)
			}
			if s.SendingStartedAt != nil || s.CompletedAt != nil {
				t.Fatalf("sending_started_at/completed_at = %v/%v, want null before a send", s.SendingStartedAt, s.CompletedAt)
			}
		})
	}
}

// REQ-TRK-01, REQ-SND-01
// An unknown campaign is 404 campaign_not_found on /status, /deliveries and /send.
func TestE2E_SendAPI_UnknownCampaign(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "status", method: http.MethodGet, path: "/status"},
		{name: "deliveries", method: http.MethodGet, path: "/deliveries"},
		{name: "deliveries with a valid filter", method: http.MethodGet, path: "/deliveries?channel=sms&limit=10"},
		{name: "send", method: http.MethodPost, path: "/send", body: sndBody(unknownVerID)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, tc.method, api+"/api/campaigns/"+unknownCampaignID+tc.path, tc.body, nil)
			assertError(t, status, hdr, body, http.StatusNotFound, "campaign_not_found", false)
		})
	}
}
