package campaign

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// ---- Scheduled send (P7a, D62) ----

// MaxScheduleAhead is the latest accepted plan relative to now (exactly 720 h, not calendar days).
const MaxScheduleAhead = 30 * 24 * time.Hour

var (
	ErrScheduleTooSoon = errors.New("campaign: scheduled_at too soon") // field code schedule_too_soon (422)
	ErrScheduleTooFar  = errors.New("campaign: scheduled_at too far")  // field code schedule_too_far (422)
)

// scheduledAtForm is the accepted spelling of scheduled_at: RFC 3339 with an explicit offset,
// upper-case T and Z, offset hours 00..23 and minutes 00..59 (time.Parse alone accepts +24:00 and
// +03:60). Field ranges (month, day, hour, ...) are left to time.Parse.
var scheduledAtForm = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)

// ParseScheduledAt parses the request value of scheduled_at. The result is in UTC and truncated to
// microseconds (the precision stored in PostgreSQL). Any other string => *validation.Error with one
// FieldError {Path "scheduled_at", Code "invalid_value", Err validation.ErrInvalidValue}.
func ParseScheduledAt(s string) (time.Time, error) {
	if scheduledAtForm.MatchString(s) {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC().Truncate(time.Microsecond), nil
		}
	}
	return time.Time{}, &validation.Error{Fields: []validation.FieldError{{
		Path: "scheduled_at", Code: validation.CodeInvalidValue,
		Message: "scheduled_at must be an RFC 3339 date-time with an offset", Err: validation.ErrInvalidValue,
	}}}
}

// ValidateScheduledAt is the plan window check; both bounds are inclusive. at < now+minLead =>
// schedule_too_soon (past instants included); at > now+MaxScheduleAhead => schedule_too_far; both
// as *validation.Error with path "scheduled_at". minLead < 0 is treated as 0.
func ValidateScheduledAt(at, now time.Time, minLead time.Duration) error {
	minLead = max(minLead, 0)
	switch {
	case at.Before(now.Add(minLead)):
		return &validation.Error{Fields: []validation.FieldError{{
			Path: "scheduled_at", Code: validation.CodeScheduleTooSoon,
			Message: "scheduled_at must be at least " + minLead.String() + " from now", Err: ErrScheduleTooSoon,
		}}}
	case at.After(now.Add(MaxScheduleAhead)):
		return &validation.Error{Fields: []validation.FieldError{{
			Path: "scheduled_at", Code: validation.CodeScheduleTooFar,
			Message: "scheduled_at must be at most 720h from now", Err: ErrScheduleTooFar,
		}}}
	}
	return nil
}

// checkSendable is the locked-row check shared by Send and Schedule (openapi sendCampaign steps
// 6-8): generation lease -> state machine (ev) -> the approved version.
func (s *service) checkSendable(lc LockedCampaign, versionID string, ev Event) error {
	if err := lc.CheckNoGeneration(s.d.Clock.Now()); err != nil {
		return err
	}
	if _, err := Transition(lc.State, ev); err != nil {
		return err
	}
	if lc.ApprovedVersionID == nil || *lc.ApprovedVersionID != versionID {
		return ErrStaleContentVersion
	}
	return nil
}

// Schedule follows openapi sendCampaign with scheduled_at: window (422, before any database
// access) -> 404 campaign -> 404 version -> (under the row lock) generation lease -> invalid_state
// (neither approved nor scheduled) -> stale. Only the campaign row changes: no delivery is written
// until the Scheduler starts the plan.
func (s *service) Schedule(ctx context.Context, id, versionID string, at time.Time) (SendResult, error) {
	at = at.UTC().Truncate(time.Microsecond)
	if err := ValidateScheduledAt(at, s.d.Clock.Now(), s.d.ScheduleMinLead); err != nil {
		return SendResult{}, err
	}
	v, err := s.GetContentVersion(ctx, id, versionID)
	if err != nil {
		return SendResult{}, err
	}
	id, versionID = v.CampaignID, v.ID // canonical (D47a)
	if err := s.d.Store.ScheduleSend(ctx, id, at, s.now, func(lc LockedCampaign) error {
		return s.checkSendable(lc, versionID, EventScheduled)
	}); err != nil {
		return SendResult{}, err
	}
	s.d.Logger.Info("send scheduled", slog.String("campaign_id", id), slog.String("scheduled_at", at.Format(time.RFC3339Nano)))
	return SendResult{CampaignID: id, State: StateScheduled, ContentVersionID: versionID, ScheduledAt: &at}, nil
}

// CancelSchedule: 404 campaign -> (under the row lock) invalid_state unless scheduled. The
// campaign returns to approved with its approval; a send the worker already started wins (409).
func (s *service) CancelSchedule(ctx context.Context, id string) (CancelScheduleResult, error) {
	id, ok := canonicalID(id)
	if !ok {
		return CancelScheduleResult{}, ErrCampaignNotFound
	}
	if err := s.d.Store.CancelSchedule(ctx, id, s.now, func(lc LockedCampaign) error {
		_, err := Transition(lc.State, EventScheduleCanceled)
		return err
	}); err != nil {
		return CancelScheduleResult{}, err
	}
	s.d.Logger.Info("schedule canceled", slog.String("campaign_id", id))
	return CancelScheduleResult{CampaignID: id, State: StateApproved}, nil
}
