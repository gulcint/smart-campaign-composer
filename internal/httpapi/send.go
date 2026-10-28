package httpapi

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// ---- POST /api/campaigns/{campaign_id}/send ----

// sendCampaign: path (404) -> body (400/413/422, scheduled_at format included) -> without
// scheduled_at Service.Send, with it Service.Schedule (422 window › 404 campaign/version ›
// 409 generation_in_progress › invalid_state › stale_content_version) -> 202 (REQ-SND-01,
// REQ-SCH-01). The window check is the service's (its clock and ScheduleMinLead).
func (a *api) sendCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	req, ok := readSendRequest(w, r)
	if !ok {
		return
	}
	var (
		res campaign.SendResult
		err error
	)
	if req.scheduledAt != nil {
		res, err = a.deps.Service.Schedule(r.Context(), id, req.versionID, *req.scheduledAt)
	} else {
		res, err = a.deps.Service.Send(r.Context(), id, req.versionID)
	}
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

type sendRequest struct {
	versionID   string
	scheduledAt *time.Time // nil: send now
}

// readSendRequest reads the SendRequest body {"content_version_id": "<uuid>", "scheduled_at":
// "<RFC 3339>"?}; every field error is collected into one 422 (unknown properties,
// content_version_id, scheduled_at).
func readSendRequest(w http.ResponseWriter, r *http.Request) (sendRequest, bool) {
	body, ok := readBody(w, r, true)
	if !ok {
		return sendRequest{}, false
	}
	obj, ok := decodeObject(w, r, body)
	if !ok {
		return sendRequest{}, false
	}
	var req sendRequest
	vid, fields := versionRefFields(obj, "scheduled_at")
	req.versionID = vid
	if raw, present := obj["scheduled_at"]; present {
		var s string
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) || json.Unmarshal(raw, &s) != nil {
			fields = append(fields, schemaViolation("scheduled_at", "scheduled_at must be a string"))
		} else if at, err := campaign.ParseScheduledAt(s); err != nil {
			var ve *validation.Error
			if errors.As(err, &ve) {
				fields = append(fields, ve.Fields...)
			}
		} else {
			req.scheduledAt = &at
		}
	}
	if len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return sendRequest{}, false
	}
	return req, true
}

// ---- DELETE /api/campaigns/{campaign_id}/schedule ----

// cancelSchedule: path (404) -> Service.CancelSchedule (404 campaign › 409 invalid_state) -> 200
// (REQ-SCH-03). No body is defined; one that is sent is not read.
func (a *api) cancelSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	res, err := a.deps.Service.CancelSchedule(r.Context(), id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- GET /api/campaigns/{campaign_id}/status ----

func (a *api) getCampaignStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	st, err := a.deps.Service.Status(r.Context(), id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ---- GET /api/campaigns/{campaign_id}/deliveries ----

// listDeliveries: path (404) -> query (422 invalid_value, path query.<name>) -> Service. The
// value rules are campaign.DeliveryFilterErrors, shared with the service.
func (a *api) listDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := campaign.DeliveryFilter{
		Status: q.Get("status"), Channel: q.Get("channel"), CustomerID: q.Get("customer_id"),
		Order: cmp.Or(q.Get("order"), campaign.DeliveryOrderCustomer),
		Limit: queryInt(q.Get("limit"), campaign.DefaultPageLimit), Offset: queryInt(q.Get("offset"), 0),
	}
	if fields := campaign.DeliveryFilterErrors(&f); len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	page, err := a.deps.Service.ListDeliveries(r.Context(), id, f)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// queryInt parses an optional integer query value; a non-integer becomes -1, which is out of range
// for both limit and offset, so the shared check reports it.
func queryInt(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}
