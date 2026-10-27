package campaign_test

import (
	"errors"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
)

// REQ-APR-01, REQ-APR-03, REQ-CNT-04
// The P4 events of the pure state machine (go-interfaces §campaign Transition): a validated
// version moves audience_ready|content_ready|approved to content_ready (an edit after approval
// drops it back), a stored invalid draft changes nothing, approval needs content_ready|approved,
// and nothing changes once sending started.
func TestTransition_ContentAndApprovalEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from    campaign.State
		ev      campaign.Event
		want    campaign.State
		wantErr error
	}{
		{from: campaign.StateAudienceReady, ev: campaign.EventContentValidated, want: campaign.StateContentReady},
		{from: campaign.StateContentReady, ev: campaign.EventContentValidated, want: campaign.StateContentReady},
		{from: campaign.StateApproved, ev: campaign.EventContentValidated, want: campaign.StateContentReady},
		{from: campaign.StateDraft, ev: campaign.EventContentValidated, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateSending, ev: campaign.EventContentValidated, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateCompleted, ev: campaign.EventContentValidated, wantErr: campaign.ErrInvalidState},

		{from: campaign.StateAudienceReady, ev: campaign.EventContentInvalid, want: campaign.StateAudienceReady},
		{from: campaign.StateContentReady, ev: campaign.EventContentInvalid, want: campaign.StateContentReady},
		{from: campaign.StateApproved, ev: campaign.EventContentInvalid, want: campaign.StateApproved},
		{from: campaign.StateDraft, ev: campaign.EventContentInvalid, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateSending, ev: campaign.EventContentInvalid, wantErr: campaign.ErrInvalidState},

		{from: campaign.StateContentReady, ev: campaign.EventApproved, want: campaign.StateApproved},
		{from: campaign.StateApproved, ev: campaign.EventApproved, want: campaign.StateApproved},
		{from: campaign.StateDraft, ev: campaign.EventApproved, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateAudienceReady, ev: campaign.EventApproved, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateSending, ev: campaign.EventApproved, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateCompleted, ev: campaign.EventApproved, wantErr: campaign.ErrInvalidState},

		{from: campaign.StateApproved, ev: campaign.EventSendAccepted, want: campaign.StateSending},
		{from: campaign.StateContentReady, ev: campaign.EventSendAccepted, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateAudienceReady, ev: campaign.EventSendAccepted, wantErr: campaign.ErrInvalidState},
	}
	for _, tc := range tests {
		t.Run(string(tc.from)+" "+string(tc.ev), func(t *testing.T) {
			t.Parallel()
			got, err := campaign.Transition(tc.from, tc.ev)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Transition(%s, %s) error = %v, want %v", tc.from, tc.ev, err, tc.wantErr)
			}
			if tc.wantErr == nil && got != tc.want {
				t.Fatalf("Transition(%s, %s) = %s, want %s", tc.from, tc.ev, got, tc.want)
			}
		})
	}
}

// REQ-APR-03, REQ-CNT-04
// Event names, the X-Expected-Version literal and the generation bound of the contract.
func TestCampaign_P4Names(t *testing.T) {
	t.Parallel()
	got := []string{
		string(campaign.EventContentValidated), string(campaign.EventContentInvalid), string(campaign.EventApproved),
		string(campaign.EventSendAccepted), string(campaign.EventDeliveriesFinished),
		string(campaign.ContentValidated), string(campaign.ContentInvalid), string(campaign.SourceLLM), string(campaign.SourceEdit),
		campaign.ExpectedVersionNone,
	}
	want := []string{
		"content_validated", "content_invalid", "approved", "send_accepted", "deliveries_finished",
		"validated", "invalid", "llm", "edit", "none",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
	if campaign.MaxGenerationDuration.Seconds() != 120 {
		t.Fatalf("MaxGenerationDuration = %s, want 120s", campaign.MaxGenerationDuration)
	}
}

// REQ-APR-03 (D40)
// EditConflictError matches ErrContentEditConflict (and nothing else) and carries the current
// version; both members are nil when there is no validated version.
func TestEditConflictError_Is(t *testing.T) {
	t.Parallel()
	id, n := "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11", 3
	var err error = &campaign.EditConflictError{CurrentID: &id, CurrentVersionNumber: &n}
	if !errors.Is(err, campaign.ErrContentEditConflict) {
		t.Fatalf("errors.Is(EditConflictError, ErrContentEditConflict) = false")
	}
	for _, other := range []error{campaign.ErrStaleContentVersion, campaign.ErrInvalidState, campaign.ErrGenerationInProgress} {
		if errors.Is(err, other) {
			t.Fatalf("EditConflictError matches %v", other)
		}
	}
	var ce *campaign.EditConflictError
	if !errors.As(err, &ce) || *ce.CurrentID != id || *ce.CurrentVersionNumber != 3 {
		t.Fatalf("errors.As did not recover the current version: %+v", ce)
	}
	if (&campaign.EditConflictError{}).Error() == "" {
		t.Fatalf("EditConflictError.Error() is empty")
	}
}
