package campaign_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// REQ-AUD-05
// NormalizeBrief: NFC + trim, empty => required, more than 2000 runes => too_long (2000 accepted),
// both on path "brief" (go-interfaces.md §campaign, openapi createCampaign).
func TestNormalizeBrief_Limits(t *testing.T) {
	t.Parallel()
	if campaign.BriefMaxRunes != 2000 {
		t.Fatalf("BriefMaxRunes = %d, want 2000", campaign.BriefMaxRunes)
	}
	tests := []struct {
		name     string
		in       string
		want     string
		sentinel error
		code     string
	}{
		{name: "example brief is accepted unchanged", in: exampleBrief, want: exampleBrief},
		{name: "surrounding whitespace is trimmed", in: "  \n\t" + exampleBrief + " \n", want: exampleBrief},
		{name: "1 char is accepted", in: "a", want: "a"},
		{name: "2000 ASCII chars are accepted", in: strings.Repeat("a", 2000), want: strings.Repeat("a", 2000)},
		{name: "2000 Turkish letters count as 2000", in: strings.Repeat("ş", 2000), want: strings.Repeat("ş", 2000)},
		{name: "2000 chars plus surrounding spaces are accepted", in: "   " + strings.Repeat("ğ", 2000) + "   ", want: strings.Repeat("ğ", 2000)},
		{name: "decomposed letters are measured after NFC", in: strings.Repeat("ş", 2000), want: strings.Repeat("ş", 2000)},
		{name: "decomposed I with dot becomes İ", in: "İstanbul", want: "İstanbul"},
		{name: "empty brief is required", in: "", sentinel: validation.ErrRequired, code: "required"},
		{name: "whitespace-only brief is required", in: " \n\t  ", sentinel: validation.ErrRequired, code: "required"},
		{name: "2001 chars are too long", in: strings.Repeat("a", 2001), sentinel: validation.ErrTooLong, code: "too_long"},
		{name: "2001 Turkish letters are too long", in: strings.Repeat("İ", 2001), sentinel: validation.ErrTooLong, code: "too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := campaign.NormalizeBrief(tc.in)
			if tc.sentinel == nil {
				if err != nil {
					t.Fatalf("NormalizeBrief() error = %v, want nil", err)
				}
				if got != tc.want {
					t.Fatalf("NormalizeBrief() = %q, want %q", got, tc.want)
				}
				return
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("NormalizeBrief() error = %v, want %v", err, tc.sentinel)
			}
			var ve *validation.Error
			if !errors.As(err, &ve) {
				t.Fatalf("NormalizeBrief() error = %T, want *validation.Error", err)
			}
			if len(ve.Fields) != 1 || ve.Fields[0].Path != "brief" || ve.Fields[0].Code != tc.code {
				t.Fatalf("field errors = %+v, want exactly one {brief %s}", ve.Fields, tc.code)
			}
		})
	}
}

// REQ-AUD-00
// A stored valid segment moves the campaign to audience_ready from every pre-send state;
// regeneration is refused once sending started (requirements "Kampanya durum makinesi").
func TestTransition_SegmentGenerated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from    campaign.State
		want    campaign.State
		wantErr error
	}{
		{from: campaign.StateDraft, want: campaign.StateAudienceReady},
		{from: campaign.StateAudienceReady, want: campaign.StateAudienceReady},
		{from: campaign.StateContentReady, want: campaign.StateAudienceReady},
		{from: campaign.StateApproved, want: campaign.StateAudienceReady},
		{from: campaign.StateSending, wantErr: campaign.ErrInvalidState},
		{from: campaign.StateCompleted, wantErr: campaign.ErrInvalidState},
	}
	for _, tc := range tests {
		t.Run(string(tc.from), func(t *testing.T) {
			t.Parallel()
			got, err := campaign.Transition(tc.from, campaign.EventSegmentGenerated)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Transition(%s, segment_generated) error = %v, want %v", tc.from, err, tc.wantErr)
			}
			if tc.wantErr == nil && got != tc.want {
				t.Fatalf("Transition(%s, segment_generated) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

// REQ-AUD-00
// The state names are the closed list of the contract.
func TestState_Names(t *testing.T) {
	t.Parallel()
	got := []campaign.State{
		campaign.StateDraft, campaign.StateAudienceReady, campaign.StateContentReady,
		campaign.StateApproved, campaign.StateSending, campaign.StateCompleted,
	}
	want := []string{"draft", "audience_ready", "content_ready", "approved", "sending", "completed"}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("state %d = %q, want %q", i, got[i], want[i])
		}
	}
	if campaign.EventSegmentGenerated != "segment_generated" {
		t.Fatalf("EventSegmentGenerated = %q, want segment_generated", campaign.EventSegmentGenerated)
	}
}

const exampleBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."
