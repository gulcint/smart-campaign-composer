package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
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

// listDeliveries: path (404) -> query (422 invalid_value, path query.<name>) -> Service.
func (a *api) listDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := campaign.DeliveryFilter{
		Status: q.Get("status"), Channel: q.Get("channel"), CustomerID: q.Get("customer_id"), Order: q.Get("order"),
	}
	var fields []validation.FieldError
	switch f.Status {
	case "", "queued", "sending", "sent", "failed":
	default:
		fields = append(fields, invalidQuery("status", "status must be one of queued, sending, sent, failed"))
	}
	switch f.Channel {
	case "", "sms", "email":
	default:
		fields = append(fields, invalidQuery("channel", "channel must be sms or email"))
	}
	switch f.Order {
	case "", campaign.DeliveryOrderCustomer, campaign.DeliveryOrderRecent:
	default:
		fields = append(fields, invalidQuery("order", "order must be customer or recent"))
	}
	if f.CustomerID != "" {
		if f.CustomerID, ok = canonicalUUID(f.CustomerID); !ok {
			fields = append(fields, invalidQuery("customer_id", "customer_id must be a UUID"))
		}
	}
	if f.Limit, ok = intQuery(q.Get("limit"), campaign.DefaultPageLimit, 1, campaign.MaxPageLimit); !ok {
		fields = append(fields, invalidQuery("limit", "limit must be an integer between 1 and "+strconv.Itoa(campaign.MaxPageLimit)))
	}
	if f.Offset, ok = intQuery(q.Get("offset"), 0, 0, -1); !ok {
		fields = append(fields, invalidQuery("offset", "offset must be a non-negative integer"))
	}
	if len(fields) > 0 {
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
