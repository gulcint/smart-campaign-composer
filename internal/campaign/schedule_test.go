package campaign_test

// Unit tests of the scheduling helpers of REQ-SCH-01 (pure functions, no database):
// campaign.ParseScheduledAt, campaign.ValidateScheduledAt and campaign.MaxScheduleAhead.

import (
	"errors"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

var schNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// REQ-SCH-01 C3, C5, C9
// ParseScheduledAt accepts YYYY-MM-DDTHH:MM:SS[.fraction](Z|+HH:MM|-HH:MM) with upper-case T and
// Z, returns UTC truncated (not rounded) to microseconds; anything else is a *validation.Error with
// the single field error {scheduled_at, invalid_value} matching validation.ErrInvalidValue.
func TestParseScheduledAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    time.Time
		invalid bool
	}{
		{name: "UTC with Z", in: "2026-01-15T12:30:00Z", want: time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)},
		{name: "positive offset is converted to UTC", in: "2026-01-15T15:30:00+03:00", want: time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)},
		{name: "negative offset is converted to UTC", in: "2026-01-15T07:30:00-05:00", want: time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)},
		{name: "offset crossing midnight", in: "2026-01-16T00:05:00+03:00", want: time.Date(2026, 1, 15, 21, 5, 0, 0, time.UTC)},
		{name: "one fraction digit", in: "2026-01-15T12:30:00.5Z", want: time.Date(2026, 1, 15, 12, 30, 0, 500000000, time.UTC)},
		{name: "microseconds are kept", in: "2026-01-15T12:30:00.123456Z", want: time.Date(2026, 1, 15, 12, 30, 0, 123456000, time.UTC)},
		{name: "finer fraction is truncated to microseconds", in: "2026-01-15T12:30:00.1234567Z", want: time.Date(2026, 1, 15, 12, 30, 0, 123456000, time.UTC)},
		{name: "truncation never rounds up", in: "2026-01-15T12:30:00.9999999Z", want: time.Date(2026, 1, 15, 12, 30, 0, 999999000, time.UTC)},
		{name: "nanoseconds with an offset", in: "2026-01-15T15:30:00.000000999+03:00", want: time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)},
		{name: "local time without offset", in: "2026-01-15T15:30:00", invalid: true},
		{name: "date only", in: "2026-01-15", invalid: true},
		{name: "minutes without seconds", in: "2026-01-15T12:30Z", invalid: true},
		{name: "invalid date 30 February", in: "2026-02-30T10:00:00Z", invalid: true},
		{name: "hour 24", in: "2026-01-15T24:00:00Z", invalid: true},
		{name: "lower-case t", in: "2026-01-15t12:30:00Z", invalid: true},
		{name: "lower-case z", in: "2026-01-15T12:30:00z", invalid: true},
		{name: "space instead of T", in: "2026-01-15 12:30:00Z", invalid: true},
		{name: "offset without colon", in: "2026-01-15T15:30:00+0300", invalid: true},
		{name: "empty string", in: "", invalid: true},
		{name: "free text", in: "yarın öğlen", invalid: true},
		{name: "unix seconds as text", in: "1768480200", invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := campaign.ParseScheduledAt(tc.in)
			if tc.invalid {
				assertSingleScheduleField(t, err, "invalid_value")
				if !errors.Is(err, validation.ErrInvalidValue) {
					t.Fatalf("ParseScheduledAt(%q) error = %v, want it to match validation.ErrInvalidValue", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseScheduledAt(%q) error = %v, want nil", tc.in, err)
			}
			if !got.Equal(tc.want) || got.Nanosecond() != tc.want.Nanosecond() {
				t.Fatalf("ParseScheduledAt(%q) = %s, want %s", tc.in, got.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
			}
			if got.Location() != time.UTC {
				t.Fatalf("ParseScheduledAt(%q) location = %s, want UTC", tc.in, got.Location())
			}
		})
	}
}

// REQ-SCH-01 C5, C9
// ValidateScheduledAt: at < now + minLead => campaign.ErrScheduleTooSoon (schedule_too_soon),
// at > now + 720h => campaign.ErrScheduleTooFar (schedule_too_far), both bounds inclusive; the error
// is a *validation.Error on path scheduled_at; minLead 0 accepts now itself and a negative minLead
// counts as 0. campaign.MaxScheduleAhead is 720h.
func TestValidateScheduledAt_Window(t *testing.T) {
	t.Parallel()
	if campaign.MaxScheduleAhead != 720*time.Hour {
		t.Fatalf("MaxScheduleAhead = %s, want 720h", campaign.MaxScheduleAhead)
	}
	tests := []struct {
		name     string
		at       time.Time
		minLead  time.Duration
		wantErr  error
		wantCode string
	}{
		{name: "exactly now + min lead is accepted", at: schNow.Add(time.Minute), minLead: time.Minute},
		{name: "1s before now + min lead is too soon", at: schNow.Add(time.Minute - time.Second), minLead: time.Minute, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "now with the default lead is too soon", at: schNow, minLead: time.Minute, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "a past instant is too soon", at: schNow.Add(-2 * time.Hour), minLead: time.Minute, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "one hour ahead is accepted", at: schNow.Add(time.Hour), minLead: time.Minute},
		{name: "exactly now + 720h is accepted", at: schNow.Add(720 * time.Hour), minLead: time.Minute},
		{name: "1s after now + 720h is too far", at: schNow.Add(720*time.Hour + time.Second), minLead: time.Minute, wantErr: campaign.ErrScheduleTooFar, wantCode: "schedule_too_far"},
		{name: "a year ahead is too far", at: schNow.AddDate(1, 0, 0), minLead: time.Minute, wantErr: campaign.ErrScheduleTooFar, wantCode: "schedule_too_far"},
		{name: "min lead 0 accepts now", at: schNow, minLead: 0},
		{name: "min lead 0 refuses 1s before now", at: schNow.Add(-time.Second), minLead: 0, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "negative min lead counts as 0 and accepts now", at: schNow, minLead: -5 * time.Minute},
		{name: "negative min lead counts as 0 and refuses the past", at: schNow.Add(-time.Second), minLead: -5 * time.Minute, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "24h min lead: exactly the bound is accepted", at: schNow.Add(24 * time.Hour), minLead: 24 * time.Hour},
		{name: "24h min lead: 1s earlier is too soon", at: schNow.Add(24*time.Hour - time.Second), minLead: 24 * time.Hour, wantErr: campaign.ErrScheduleTooSoon, wantCode: "schedule_too_soon"},
		{name: "an instant in another zone is compared as an instant", at: schNow.Add(time.Hour).In(time.FixedZone("TSİ", 3*3600)), minLead: time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := campaign.ValidateScheduledAt(tc.at, schNow, tc.minLead)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateScheduledAt(%s) error = %v, want nil", tc.at.Format(time.RFC3339), err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateScheduledAt(%s) error = %v, want %v", tc.at.Format(time.RFC3339), err, tc.wantErr)
			}
			other := campaign.ErrScheduleTooFar
			if tc.wantErr == campaign.ErrScheduleTooFar {
				other = campaign.ErrScheduleTooSoon
			}
			if errors.Is(err, other) {
				t.Fatalf("ValidateScheduledAt(%s) error = %v also matches %v", tc.at.Format(time.RFC3339), err, other)
			}
			assertSingleScheduleField(t, err, tc.wantCode)
		})
	}
}

// assertSingleScheduleField: err is a *validation.Error whose only field error is
// {Path: "scheduled_at", Code: code}.
func assertSingleScheduleField(t *testing.T, err error, code string) {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want a *validation.Error", err, err)
	}
	if len(ve.Fields) != 1 || ve.Fields[0].Path != "scheduled_at" || ve.Fields[0].Code != code {
		t.Fatalf("field errors = %+v, want exactly one {scheduled_at %s}", ve.Fields, code)
	}
}
