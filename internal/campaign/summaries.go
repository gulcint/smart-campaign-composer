package campaign

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// ---- Campaign summaries: brief-screen overview panel (P10, D65; openapi listCampaignSummaries) ----

// SummaryLister is a separate read-only port, NOT a method of Service: adding a method to Service
// would break every fake Service of the httpapi tests (D65). The handler of
// GET /api/campaign-summaries is its only HTTP caller (httpapi.Deps.Summaries).
type SummaryLister interface {
	// ListSummaries runs the page query, the per-item delivery counts, total and the overview in ONE
	// read-only REPEATABLE READ transaction (one consistent snapshot). Expects a filter produced by
	// ParseSummaryFilter (a zero Limit means DefaultPageLimit); it does not re-validate. Errors: only
	// infrastructure errors (=> 500); an empty result is not an error.
	ListSummaries(ctx context.Context, f SummaryFilter) (SummaryPage, error)
}

// NewSummaryLister builds the lister on the same Deps as NewService (only Deps.Store and
// Deps.Logger are used).
func NewSummaryLister(d Deps) SummaryLister {
	return NewService(d).(*service)
}

const (
	SummaryOrderRecent      = "recent"       // created_at DESC, id ASC (default)
	SummaryOrderScheduledAt = "scheduled_at" // scheduled_at ASC NULLS LAST, id ASC
)

// allStates is every CampaignState, in openapi order (the whitelist of the state filter).
var allStates = []State{StateDraft, StateAudienceReady, StateContentReady, StateApproved,
	StateScheduled, StateSending, StateCompleted}

// SummaryFilter: States nil or len 0 = no filter (both mean the same). ParseSummaryFilter removes
// duplicates keeping first-occurrence order; order is irrelevant to the result.
type SummaryFilter struct {
	States        []State
	Order         string // SummaryOrderRecent | SummaryOrderScheduledAt
	Limit, Offset int
}

// ParseSummaryFilter turns the query of GET /api/campaign-summaries into a filter (pure). It reads
// only the first value of each parameter; every invalid parameter yields one invalid_value
// FieldError (path query.<name>), collected in the order state, order, limit, offset into ONE
// *validation.Error.
func ParseSummaryFilter(q url.Values) (SummaryFilter, error) {
	f := SummaryFilter{Order: SummaryOrderRecent, Limit: DefaultPageLimit}
	var fields []validation.FieldError
	invalid := func(name, msg string) {
		fields = append(fields, validation.FieldError{Path: "query." + name, Code: validation.CodeInvalidValue,
			Message: msg, Err: validation.ErrInvalidValue})
	}
	if v, ok := q["state"]; ok && len(v) > 0 {
		var states []State
		valid := true
		for _, s := range strings.Split(v[0], ",") {
			st := State(s)
			if !slices.Contains(allStates, st) {
				valid = false
				break
			}
			if !slices.Contains(states, st) {
				states = append(states, st)
			}
		}
		if valid {
			f.States = states
		} else {
			invalid("state", "state must be a comma-separated list of campaign states")
		}
	}
	if v, ok := q["order"]; ok && len(v) > 0 {
		if v[0] != SummaryOrderRecent && v[0] != SummaryOrderScheduledAt {
			invalid("order", "order must be one of "+SummaryOrderRecent+", "+SummaryOrderScheduledAt)
		} else {
			f.Order = v[0]
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPageLimit {
			invalid("limit", "limit must be an integer between 1 and "+strconv.Itoa(MaxPageLimit))
		} else {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			invalid("offset", "offset must be a non-negative integer")
		} else {
			f.Offset = n
		}
	}
	if len(fields) > 0 {
		return SummaryFilter{}, &validation.Error{Fields: fields}
	}
	return f, nil
}

type SummaryAudience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

// Summary is openapi CampaignSummary. Same columns as Campaign; no rule, content or addresses.
type Summary struct {
	ID               string           `json:"id"`
	Brief            string           `json:"brief"`
	State            State            `json:"state"`
	Audience         *SummaryAudience `json:"audience"` // nil iff no segment (draft)
	ScheduledAt      *time.Time       `json:"scheduled_at"`
	SendingStartedAt *time.Time       `json:"sending_started_at"`
	CompletedAt      *time.Time       `json:"completed_at"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	// Deliveries: nil (JSON null) in draft…approved and scheduled; non-nil in sending/completed (all
	// zero for a send without deliveries), equal to Service.Status(id).Counts. Variant-agnostic.
	Deliveries *StatusCounts `json:"deliveries"`
}

// StateCounts is openapi CampaignStateCounts: every state always present; Total = sum of the seven.
type StateCounts struct {
	Total         int `json:"total"`
	Draft         int `json:"draft"`
	AudienceReady int `json:"audience_ready"`
	ContentReady  int `json:"content_ready"`
	Approved      int `json:"approved"`
	Scheduled     int `json:"scheduled"`
	Sending       int `json:"sending"`
	Completed     int `json:"completed"`
}

// Overview is openapi CampaignOverview: over ALL campaigns, independent of the filter and paging.
type Overview struct {
	Campaigns  StateCounts  `json:"campaigns"`
	Deliveries StatusCounts `json:"deliveries"` // sum over every delivery row (both channels, both variants)
}

// SummaryPage is openapi CampaignSummaryPage. Items is never nil (JSON [] past the end).
type SummaryPage struct {
	Items    []Summary `json:"items"`
	Total    int       `json:"total"` // matching the States filter, independent of Limit/Offset
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
	Overview Overview  `json:"overview"`
}

func (s *service) ListSummaries(ctx context.Context, f SummaryFilter) (SummaryPage, error) {
	if f.Limit == 0 {
		f.Limit = DefaultPageLimit
	}
	if f.Order == "" {
		f.Order = SummaryOrderRecent
	}
	page, err := s.d.Store.ListSummaries(ctx, f)
	if err != nil {
		return SummaryPage{}, err
	}
	if page.Items == nil {
		page.Items = []Summary{}
	}
	page.Limit, page.Offset = f.Limit, f.Offset
	return page, nil
}
