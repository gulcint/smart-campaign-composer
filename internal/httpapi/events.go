package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/sse"
)

// statusRetryMillis is the EventSource reconnect delay sent once at the start of the stream.
const statusRetryMillis = 3000

// ---- GET /api/campaigns/{campaign_id}/events (SSE) ----

// streamCampaignEvents is the live delivery status stream (sse-events.md §2, REQ-TRK-01):
// Service.Status first (404 JSON, no stream), then `retry: 3000` and the `snapshot` event. Every
// StatusPollInterval on the injected clock the status is read again: a change (ignoring
// updated_at) becomes a `status` event, a completed campaign the final `completed` event, after
// which the stream ends. A keepalive comment goes out every HeartbeatInterval. One goroutine (the
// handler's) does all the writing and returns on client disconnect, so nothing outlives the request.
func (a *api) streamCampaignEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	last, err := a.deps.Service.Status(ctx, id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	sw, err := sse.NewWriter(w)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	if rec, ok := w.(*statusRecorder); ok {
		rec.streaming = true
	}
	w.WriteHeader(http.StatusOK)

	var seq int64
	emit := func(name string, st campaign.CampaignStatus) error {
		seq++
		return sw.EventWithID(seq, name, st)
	}
	if sw.Retry(statusRetryMillis) != nil || emit("snapshot", last) != nil {
		return
	}
	if last.State == campaign.StateCompleted {
		_ = emit("completed", last)
		return
	}

	poll := a.deps.Clock.After(a.deps.StatusPollInterval)
	beat := a.deps.Clock.After(a.deps.HeartbeatInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat:
			if sw.Comment("keepalive") != nil {
				return
			}
			beat = a.deps.Clock.After(a.deps.HeartbeatInterval)
		case <-poll:
			cur, err := a.deps.Service.Status(ctx, id)
			switch {
			case ctx.Err() != nil:
				return
			case err != nil:
				// A failed read (e.g. a database blip) is not a status change: keep the stream
				// open and try again on the next tick; the client still has the last counters.
				a.deps.Logger.Warn("status poll failed", slog.String("request_id", r.Header.Get("X-Request-ID")),
					slog.String("campaign_id", id), slog.String("error", err.Error()))
			case cur.State == campaign.StateCompleted:
				_ = emit("completed", cur)
				return
			case statusChanged(last, cur):
				if emit("status", cur) != nil {
					return
				}
				last = cur
			}
			poll = a.deps.Clock.After(a.deps.StatusPollInterval)
		}
	}
}

// statusChanged reports whether b differs from a in anything but updated_at (sse-events.md §2.1).
func statusChanged(a, b campaign.CampaignStatus) bool {
	return a.State != b.State || a.Counts != b.Counts || a.ByChannel != b.ByChannel ||
		!sameTime(a.SendingStartedAt, b.SendingStartedAt) || !sameTime(a.CompletedAt, b.CompletedAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
