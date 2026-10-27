package e2e_test

// P4 round 2 E2E tests (independent review of PR #50): canonical UUIDs over HTTP (D47 a),
// editContent order with a structurally invalid body (D47 c), getContentVersion and previewContent
// check order (README §5.4). Same stack and rules as content_test.go.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// REQ-CNT-04, REQ-APR-01, REQ-APR-03 (D47 a, README §5.4 step 1, D41)
// An UPPER-CASE campaign UUID in the path and an UPPER-CASE content_version_id behave exactly like
// the lower-case form: while a stalled_stream generation runs on the lower-case id, generateContent,
// editContent and approveContent on the UPPER-CASE id are 409 generation_in_progress; once the lock
// is released, approving the latest version with UPPER-CASE ids is 200 (not stale_content_version)
// and every id in the responses (approve, getContentVersion, previewContent, editContent) is lower
// case.
func TestE2E_UpperCaseIDsAreCanonical(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	v1 := generate(t, api, id, "ok").validated
	if v1 == nil {
		t.Fatal("setup: no validated version")
	}
	upID, upV1 := strings.ToUpper(id), strings.ToUpper(v1.ContentVersionID)
	if upID == id || upV1 == v1.ContentVersionID {
		t.Fatalf("setup: ids %s / %s have no hex letters", id, v1.ContentVersionID)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp := postContent(t, ctx, api, id, nil, scenario("stalled_stream"))
	assertSSEResponse(t, resp)
	sc := newSSEScanner(resp)
	if first, ok := nextEvent(t, sc); !ok || first.Name != "delta" {
		t.Fatalf("first event = %+v, want a delta", first)
	}

	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+upID+"/content", nil, scenario("ok"))
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	status, hdr, body = postEdit(t, api, upID, v1.ContentVersionID, editDoc("büyük harf").json(t))
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	status, hdr, body = approve(t, api, upID, upV1)
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)

	cancel()
	_ = resp.Body.Close()

	// Poll through the UPPER-CASE id until the lock is released (409 while held, 422 once free).
	tooLong := mkContent(strings.Repeat("a", 161), "Konu", "Gövde").json(t)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Second)
	for released := false; !released; {
		select {
		case <-deadline:
			t.Fatal("the generation lock was not released within 15s after the client disconnected")
		case <-ticker.C:
			status, hdr, body := postEdit(t, api, upID, v1.ContentVersionID, tooLong)
			switch status {
			case http.StatusConflict:
				assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
			case http.StatusUnprocessableEntity:
				released = true
			default:
				t.Fatalf("edit after disconnect status = %d; body: %s", status, body)
			}
		}
	}

	c := mustApprove(t, api, upID, upV1)
	if c.ID != id || c.State != "approved" || c.Approval == nil || c.Approval.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("approve(UPPER, UPPER) = id %s state %s approval %+v; want %s, approved, bound to %s", c.ID, c.State, c.Approval, id, v1.ContentVersionID)
	}
	if c.LatestContentVersion == nil || c.LatestContentVersion.ID != v1.ContentVersionID || c.LatestContentVersion.CampaignID != id {
		t.Fatalf("approve response latest_content_version = %+v, want lower-case ids", c.LatestContentVersion)
	}
	again := mustApprove(t, api, upID, upV1)
	if again.Approval == nil || again.Approval.ID != c.Approval.ID || again.Approval.ApprovedAt != c.Approval.ApprovedAt {
		t.Fatalf("second approve(UPPER) = %+v, want the unchanged approval %+v", again.Approval, c.Approval)
	}

	if g := getCampaignDoc(t, api, upID); g.ID != id {
		t.Fatalf("GET campaign(UPPER).id = %s, want %s", g.ID, id)
	}
	if v := getVersion(t, api, upID, upV1); v.ID != v1.ContentVersionID || v.CampaignID != id {
		t.Fatalf("GET content-version(UPPER, UPPER) = id %s campaign %s, want lower-case ids", v.ID, v.CampaignID)
	}
	if list := listVersions(t, api, upID); len(list) != 1 || list[0].ID != v1.ContentVersionID || list[0].CampaignID != id {
		t.Fatalf("GET content-versions(UPPER) = %+v, want v1 with lower-case ids", list)
	}
	if p, _ := preview(t, api, upID, "?content_version_id="+upV1); p.CampaignID != id || p.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("GET preview(UPPER, UPPER version) = campaign %s version %s, want lower-case ids", p.CampaignID, p.ContentVersionID)
	}
	e := mustEdit(t, api, upID, v1.ContentVersionID, editDoc("büyük harf yol"))
	if e.CampaignID != id || e.ID != strings.ToLower(e.ID) {
		t.Fatalf("edit(UPPER) = id %s campaign %s, want lower-case ids", e.ID, e.CampaignID)
	}
}

// REQ-APR-01, REQ-APR-02, REQ-APR-03 (D47 c, README §5.4 step 3 exception, openapi editContent)
// content.Parse runs inside the use case: {"sms":{}} with a stale X-Expected-Version is 409
// content_edit_conflict, to an unknown campaign 404 campaign_not_found, and only on the current base
// 422 schema_violation; a malformed X-Expected-Version still wins over all of them (422
// invalid_value, path header.X-Expected-Version). Nothing is stored.
func TestE2E_EditStructurallyInvalidBodyOrder(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	v1 := generate(t, api, id, "ok").validated
	if v1 == nil {
		t.Fatal("setup: no validated version")
	}
	v2 := mustEdit(t, api, id, v1.ContentVersionID, editDoc("v2"))
	body := []byte(`{"sms":{}}`)

	status, hdr, b := postEdit(t, api, id, v1.ContentVersionID, body)
	assertEditConflict(t, status, hdr, b, v2.ID, v2.VersionNumber)

	status, hdr, b = postEdit(t, api, unknownCampaignID, "none", body)
	assertError(t, status, hdr, b, http.StatusNotFound, "campaign_not_found", false)

	for _, target := range []string{id, unknownCampaignID} {
		status, hdr, b = postEdit(t, api, target, "v1", body)
		e := assertError(t, status, hdr, b, http.StatusUnprocessableEntity, "validation_failed", true)
		if !e.has(fieldWant{"header.X-Expected-Version", "invalid_value"}) {
			t.Fatalf("campaign %s: error.fields = %+v, want header.X-Expected-Version invalid_value", target, e.Fields)
		}
	}

	status, hdr, b = postEdit(t, api, id, v2.ID, body)
	e := assertError(t, status, hdr, b, http.StatusUnprocessableEntity, "validation_failed", true)
	if !e.has(fieldWant{"sms.body", "schema_violation"}) {
		t.Fatalf("error.fields = %+v, want sms.body schema_violation", e.Fields)
	}
	if n := len(listVersions(t, api, id)); n != 2 {
		t.Fatalf("versions = %d, want 2 (refused edits store nothing)", n)
	}
}

// REQ-APR-01, REQ-APR-03 (README §5.4 steps 1 and 4, openapi getContentVersion, previewContent)
// Check order on unknown things: a malformed version id on an UNKNOWN campaign is 404
// campaign_not_found (not content_version_not_found); previewContent with an unknown
// content_version_id on a campaign without a segment is 404 content_version_not_found (the 404s come
// before 409 invalid_state).
func TestE2E_NotFoundOrder(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	draftID, _ := createCampaign(t, api, m.ExampleBrief.Brief)

	status, hdr, b := doJSON(t, http.MethodGet, api+"/api/campaigns/"+unknownCampaignID+"/content-versions/not-a-uuid", nil, nil)
	assertError(t, status, hdr, b, http.StatusNotFound, "campaign_not_found", false)

	status, hdr, b = doJSON(t, http.MethodGet, api+"/api/campaigns/"+draftID+"/preview?content_version_id="+unknownVerID, nil, nil)
	assertError(t, status, hdr, b, http.StatusNotFound, "content_version_not_found", false)
}
