package httpapi

import (
	"cmp"
	"net/http"
	"strconv"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
)

// ---- POST /api/campaigns/{campaign_id}/send ----

// sendCampaign: path (404) -> body (400/413/422) -> Service.Send (404 campaign/version ›
// 409 generation_in_progress › invalid_state › stale_content_version) -> 202 (REQ-SND-01).
func (a *api) sendCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	vid, ok := readVersionRef(w, r)
	if !ok {
		return
	}
	res, err := a.deps.Service.Send(r.Context(), id, vid)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
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
