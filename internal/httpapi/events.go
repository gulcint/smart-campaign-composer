package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/sse"
)

// statusRetryMillis is the EventSource reconnect delay sent once at the start of the stream.
const statusRetryMillis = 3000

// maxStatusPollFailures consecutive failed status reads end the stream (D54): the client then
// shows the counters as stale and reconnects with its own backoff instead of looking live.
const maxStatusPollFailures = 5

// ---- GET /api/campaigns/{campaign_id}/events (SSE) ----

// streamCampaignEvents is the live delivery status stream (sse-events.md §2, REQ-TRK-01):
// Service.Status first (404 JSON, no stream), then `retry: 3000` and the `snapshot` event. Every
// StatusPollInterval on the injected clock the status is read again: a change (ignoring
// updated_at) becomes a `status` event, a completed campaign the final `completed` event, after
// which the stream ends. A keepalive comment goes out every HeartbeatInterval. One goroutine (the
// handler's) does all the writing and returns on client disconnect or server shutdown, so nothing
// outlives the request. maxStatusPollFailures failed reads in a row end the stream (D54).
func (a *api) streamCampaignEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	ctx, cancel := a.streamContext(r.Context())
	defer cancel()
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

	log := a.deps.Logger.With(slog.String("request_id", r.Header.Get("X-Request-ID")),
		slog.String("campaign_id", id))
	failures := 0
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
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				// A failed read (e.g. a database blip) is not a status change. Logged once per
				// outage, not once per tick; after maxStatusPollFailures in a row the stream ends.
				failures++
				if failures == 1 {
					log.Warn("status poll failed", slog.String("error", err.Error()))
				}
				if failures >= maxStatusPollFailures {
					log.Error("status stream closed after repeated poll failures",
						slog.Int("failures", failures), slog.String("error", err.Error()))
					return
				}
				poll = a.deps.Clock.After(a.deps.StatusPollInterval)
				continue
			}
			if failures > 0 {
				log.Info("status poll recovered", slog.Int("failures", failures))
				failures = 0
			}
			switch {
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
// The comparison is on the JSON the client receives, so a field added to CampaignStatus counts
// automatically and timestamps compare by their wire form.
func statusChanged(a, b campaign.CampaignStatus) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA != nil || errB != nil || !bytes.Equal(ja, jb)
}
