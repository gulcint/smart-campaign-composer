package campaign_test

// Query parsing of GET /api/campaign-summaries (REQ-DSH-01) through the pure Go surface
// campaign.ParseSummaryFilter(q url.Values) (campaign.SummaryFilter, error): defaults, the state
// list (comma or %2C, duplicates once in first-seen order), the two orders, the limit/offset
// bounds, "only the first value of a repeated parameter" and the 422 field order query.state,
// query.order, query.limit, query.offset. No database.

import (
	"errors"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// dshState converts a CampaignState value of REQ-DSH-01 to campaign.State.
func dshState(s string) campaign.State { return campaign.State(s) }

func dshStates(ss ...string) []campaign.State {
	out := make([]campaign.State, 0, len(ss))
	for _, s := range ss {
		out = append(out, dshState(s))
	}
	return out
}

func parseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("test setup: url.ParseQuery(%q): %v", raw, err)
	}
	return q
}

// REQ-DSH-01 C6, C9
// Valid queries: no parameter means no state filter (States empty), order recent, limit 50, offset
// 0; an empty limit= / offset= is the default; state is a comma list (also %2C) where a duplicate
// counts once in first-seen order; order recent | scheduled_at; limit 1..200 and offset >= 0 are
// taken as given; a repeated parameter uses only its first value.
func TestParseSummaryFilter_Valid(t *testing.T) {
	t.Parallel()
	def := func(f campaign.SummaryFilter) campaign.SummaryFilter {
		if f.Order == "" {
			f.Order = campaign.SummaryOrderRecent
		}
		if f.Limit == 0 {
			f.Limit = 50
		}
		return f
	}
	tests := []struct {
		name  string
		query string
		want  campaign.SummaryFilter
	}{
		{name: "no parameter: no filter, recent, 50, 0", query: "", want: def(campaign.SummaryFilter{})},
		{name: "empty limit= is the default 50", query: "limit=", want: def(campaign.SummaryFilter{})},
		{name: "empty offset= is the default 0", query: "offset=", want: def(campaign.SummaryFilter{})},
		{name: "state=scheduled", query: "state=scheduled", want: def(campaign.SummaryFilter{States: dshStates("scheduled")})},
		{name: "state=sending,completed keeps the order", query: "state=sending,completed", want: def(campaign.SummaryFilter{States: dshStates("sending", "completed")})},
		{name: "an encoded comma %2C is a separator", query: "state=sending%2Ccompleted", want: def(campaign.SummaryFilter{States: dshStates("sending", "completed")})},
		{name: "state=completed,completed counts once", query: "state=completed,completed", want: def(campaign.SummaryFilter{States: dshStates("completed")})},
		{name: "duplicates keep the first-seen order", query: "state=sending,completed,sending", want: def(campaign.SummaryFilter{States: dshStates("sending", "completed")})},
		{name: "all seven states", query: "state=draft,audience_ready,content_ready,approved,scheduled,sending,completed",
			want: def(campaign.SummaryFilter{States: dshStates("draft", "audience_ready", "content_ready", "approved", "scheduled", "sending", "completed")})},
		{name: "order=recent", query: "order=recent", want: def(campaign.SummaryFilter{Order: campaign.SummaryOrderRecent})},
		{name: "order=scheduled_at", query: "order=scheduled_at", want: def(campaign.SummaryFilter{Order: campaign.SummaryOrderScheduledAt})},
		{name: "limit=1 is the lower bound", query: "limit=1", want: def(campaign.SummaryFilter{Limit: 1})},
		{name: "limit=200 is the upper bound", query: "limit=200", want: def(campaign.SummaryFilter{Limit: 200})},
		{name: "offset=0", query: "offset=0", want: def(campaign.SummaryFilter{})},
		{name: "limit=5&offset=5", query: "limit=5&offset=5", want: def(campaign.SummaryFilter{Limit: 5, Offset: 5})},
		{name: "every parameter at once", query: "state=scheduled&order=scheduled_at&limit=5&offset=0",
			want: def(campaign.SummaryFilter{States: dshStates("scheduled"), Order: campaign.SummaryOrderScheduledAt, Limit: 5})},
		{name: "repeated state: only the first value is read", query: "state=scheduled&state=sent", want: def(campaign.SummaryFilter{States: dshStates("scheduled")})},
		{name: "repeated order: only the first value is read", query: "order=scheduled_at&order=oldest", want: def(campaign.SummaryFilter{Order: campaign.SummaryOrderScheduledAt})},
		{name: "repeated limit: only the first value is read", query: "limit=5&limit=0", want: def(campaign.SummaryFilter{Limit: 5})},
		{name: "repeated offset: only the first value is read", query: "offset=7&offset=-1", want: def(campaign.SummaryFilter{Offset: 7})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := campaign.ParseSummaryFilter(parseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("ParseSummaryFilter(%q) error = %v, want nil", tc.query, err)
			}
			// "state yoksa States boş (nil veya uzunluk 0)".
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("ParseSummaryFilter(%q) mismatch (-want +got):\n%s", tc.query, diff)
			}
		})
	}
}

// REQ-DSH-01 C6
// The two order values are the strings "recent" and "scheduled_at".
func TestParseSummaryFilter_OrderConstants(t *testing.T) {
	t.Parallel()
	if campaign.SummaryOrderRecent != "recent" || campaign.SummaryOrderScheduledAt != "scheduled_at" {
		t.Fatalf("SummaryOrderRecent/SummaryOrderScheduledAt = %q/%q, want recent/scheduled_at", campaign.SummaryOrderRecent, campaign.SummaryOrderScheduledAt)
	}
}

// REQ-DSH-01 C7, C9
// Invalid parameters are one *validation.Error (errors.As) whose Fields are invalid_value on
// query.state, query.order, query.limit, query.offset, in that order, whatever the order of the
// parameters in the query string.
func TestParseSummaryFilter_Invalid(t *testing.T) {
	t.Parallel()
	type pc struct{ Path, Code string }
	st := pc{"query.state", "invalid_value"}
	or := pc{"query.order", "invalid_value"}
	li := pc{"query.limit", "invalid_value"}
	of := pc{"query.offset", "invalid_value"}
	tests := []struct {
		name  string
		query string
		want  []pc
	}{
		{name: "state= (empty value)", query: "state=", want: []pc{st}},
		{name: "state=sending, (empty trailing item)", query: "state=sending,", want: []pc{st}},
		{name: "state=,sending (empty leading item)", query: "state=,sending", want: []pc{st}},
		{name: "state=sending,,completed (empty middle item)", query: "state=sending,,completed", want: []pc{st}},
		{name: "state=sent (unknown state)", query: "state=sent", want: []pc{st}},
		{name: "state=Sending (upper case)", query: "state=Sending", want: []pc{st}},
		{name: "state=sending,%20completed (item with a space)", query: "state=sending,%20completed", want: []pc{st}},
		{name: "state with one valid and one unknown item", query: "state=scheduled,x", want: []pc{st}},
		{name: "order=oldest", query: "order=oldest", want: []pc{or}},
		{name: "order=Recent (upper case)", query: "order=Recent", want: []pc{or}},
		{name: "order= (empty value)", query: "order=", want: []pc{or}},
		{name: "limit=0", query: "limit=0", want: []pc{li}},
		{name: "limit=201", query: "limit=201", want: []pc{li}},
		{name: "limit=abc", query: "limit=abc", want: []pc{li}},
		{name: "offset=-1", query: "offset=-1", want: []pc{of}},
		{name: "offset=abc", query: "offset=abc", want: []pc{of}},
		{name: "state=x&limit=0 (example of the REQ)", query: "state=x&limit=0", want: []pc{st, li}},
		{name: "all four, given in reverse order", query: "offset=-1&limit=0&order=oldest&state=sent", want: []pc{st, or, li, of}},
		{name: "order and offset", query: "offset=abc&order=", want: []pc{or, of}},
		{name: "a repeated parameter is judged by its first value", query: "limit=0&limit=5", want: []pc{li}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := campaign.ParseSummaryFilter(parseQuery(t, tc.query))
			var ve *validation.Error
			if !errors.As(err, &ve) {
				t.Fatalf("ParseSummaryFilter(%q) error = %v (%T), want a *validation.Error", tc.query, err, err)
			}
			got := make([]pc, 0, len(ve.Fields))
			for _, f := range ve.Fields {
				got = append(got, pc{f.Path, f.Code})
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("ParseSummaryFilter(%q) fields mismatch (-want +got):\n%s", tc.query, diff)
			}
		})
	}
}
