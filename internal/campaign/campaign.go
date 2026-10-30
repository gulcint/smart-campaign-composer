// Package campaign is the campaign use-case layer behind the HTTP API.
//
// It holds the domain types, the pure state machine, the Service port that internal/httpapi
// depends on and its implementation (service.go, content.go). P3 implements campaign creation,
// reading and segment generation (draft -> audience_ready); P4 the content generation, human
// edits, approval and preview; P5 the delivery use cases (accepting a send, status, deliveries);
// P7a the scheduled send (schedule.go, scheduler.go; D62).
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// State is a campaign lifecycle state.
type State string

const (
	StateDraft         State = "draft"
	StateAudienceReady State = "audience_ready"
	StateContentReady  State = "content_ready"
	StateApproved      State = "approved"
	StateScheduled     State = "scheduled" // P7a (D62): approved + planned send, no delivery yet
	StateSending       State = "sending"
	StateCompleted     State = "completed"
)

var (
	ErrCampaignNotFound       = errors.New("campaign: not found")                 // 404 campaign_not_found
	ErrContentVersionNotFound = errors.New("campaign: content version not found") // 404 content_version_not_found
	ErrCustomerNotFound       = errors.New("campaign: customer not in audience")  // 404 customer_not_found
	ErrInvalidState           = errors.New("campaign: invalid state")             // 409 invalid_state
	ErrContentNotValidated    = errors.New("campaign: content not validated")     // 409 content_not_validated
	ErrStaleContentVersion    = errors.New("campaign: stale content version")     // 409 stale_content_version
	ErrGenerationInProgress   = errors.New("campaign: generation in progress")    // 409 generation_in_progress
	ErrContentEditConflict    = errors.New("campaign: content edit conflict")     // 409 content_edit_conflict
)

// ExpectedVersionNone is the X-Expected-Version value meaning "the editor saw no latest validated version".
const ExpectedVersionNone = "none"

// EditConflictError is returned by EditContent when the expected version is not the current latest
// validated version. errors.Is(err, ErrContentEditConflict) is true. Current is the CURRENT latest
// validated version of the current segment revision; both members are nil when there is none.
type EditConflictError struct {
	CurrentID            *string `json:"id"`
	CurrentVersionNumber *int    `json:"version_number"`
}

func (e *EditConflictError) Error() string {
	if e.CurrentID == nil || e.CurrentVersionNumber == nil {
		return ErrContentEditConflict.Error() + ": the campaign has no latest version"
	}
	return ErrContentEditConflict.Error() + ": the latest version is " + *e.CurrentID + " (number " + strconv.Itoa(*e.CurrentVersionNumber) + ")"
}

// Is reports whether target is ErrContentEditConflict.
func (*EditConflictError) Is(target error) bool { return target == ErrContentEditConflict }

// Event is an input of the campaign state machine.
type Event string

const (
	EventSegmentGenerated   Event = "segment_generated"   // valid rule stored
	EventContentValidated   Event = "content_validated"   // new validated version (LLM or edit)
	EventContentInvalid     Event = "content_invalid"     // invalid LLM draft stored (failed (re)generation)
	EventApproved           Event = "approved"            // a validated version was approved
	EventSendAccepted       Event = "send_accepted"       // deliveries created
	EventDeliveriesFinished Event = "deliveries_finished" // no queued/sending delivery left
	EventScheduled          Event = "scheduled"           // plan stored or replaced (P7a)
	EventScheduleCanceled   Event = "schedule_canceled"   // DELETE …/schedule (P7a)
)

// Transition is the pure state machine (requirements "Kampanya durum makinesi").
//
//	segment_generated:   draft|audience_ready|content_ready|approved -> audience_ready
//	content_validated:   audience_ready|content_ready|approved      -> content_ready
//	content_invalid:     audience_ready|content_ready|approved      -> unchanged
//	approved:            content_ready|approved -> approved
//	scheduled:           approved|scheduled -> scheduled
//	schedule_canceled:   scheduled -> approved
//	send_accepted:       approved|scheduled -> sending
//	deliveries_finished: sending -> completed
//
// Any other pair => ErrInvalidState. In particular every event except scheduled,
// schedule_canceled and send_accepted is ErrInvalidState from scheduled (no segment/content change
// or approval while a plan exists; cancel first, D62).
func Transition(from State, ev Event) (State, error) {
	in := func(states ...State) bool { return slices.Contains(states, from) }
	switch ev {
	case EventSegmentGenerated:
		if in(StateDraft, StateAudienceReady, StateContentReady, StateApproved) {
			return StateAudienceReady, nil
		}
	case EventContentValidated:
		if in(StateAudienceReady, StateContentReady, StateApproved) {
			return StateContentReady, nil
		}
	case EventContentInvalid:
		if in(StateAudienceReady, StateContentReady, StateApproved) {
			return from, nil
		}
	case EventApproved:
		if in(StateContentReady, StateApproved) {
			return StateApproved, nil
		}
	case EventScheduled:
		if in(StateApproved, StateScheduled) {
			return StateScheduled, nil
		}
	case EventScheduleCanceled:
		if from == StateScheduled {
			return StateApproved, nil
		}
	case EventSendAccepted:
		if in(StateApproved, StateScheduled) {
			return StateSending, nil
		}
	case EventDeliveriesFinished:
		if from == StateSending {
			return StateCompleted, nil
		}
	}
	return from, fmt.Errorf("%w: %s on %s", ErrInvalidState, ev, from)
}

// BriefMaxRunes is the brief length limit (REQ-AUD-05).
const BriefMaxRunes = 2000

// MaxGenerationDuration bounds one segment or content generation (VARSAYIM). The per-campaign
// generation lock is a lease on the campaign row (campaigns.generation_token/generation_expires_at,
// D52), shared by every API process: it is released when the request context is cancelled (client
// disconnect) or the generation ends and, at the latest, expires after this duration (so a crashed
// process cannot block the campaign); a content stream still running then ends with the final
// invalid event, code "llm_unavailable". While the lease is held, GenerateSegment,
// StartContentGeneration, EditContent, Approve, Send and Schedule return ErrGenerationInProgress; each
// checks it under the campaign row lock in the transaction that checks the state.
const MaxGenerationDuration = 120 * time.Second

// NormalizeBrief NFC-normalizes and trims s. Errors (*validation.Error, path "brief"):
// empty => code "required" (matches validation.ErrRequired); > 2000 runes => code "too_long"
// (matches validation.ErrTooLong).
func NormalizeBrief(s string) (string, error) {
	b := strings.TrimSpace(norm.NFC.String(s))
	if b == "" {
		return "", &validation.Error{Fields: []validation.FieldError{{
			Path: "brief", Code: validation.CodeRequired, Message: "brief must not be empty", Err: validation.ErrRequired,
		}}}
	}
	if n := utf8.RuneCountInString(b); n > BriefMaxRunes {
		limit, actual := BriefMaxRunes, n
		return "", &validation.Error{Fields: []validation.FieldError{{
			Path: "brief", Code: validation.CodeTooLong, Message: "brief exceeds " + strconv.Itoa(BriefMaxRunes) + " characters",
			Limit: &limit, Actual: &actual, Err: validation.ErrTooLong,
		}}}
	}
	return b, nil
}

// ---- Domain structs: JSON encoding of each is EXACTLY the OpenAPI schema of the same name ----

type ContentStatus string

const (
	ContentValidated ContentStatus = "validated"
	ContentInvalid   ContentStatus = "invalid"
)

type ContentSource string

const (
	SourceLLM  ContentSource = "llm"
	SourceEdit ContentSource = "edit"
)

type Segment struct {
	Rule          json.RawMessage  `json:"rule"` // root group
	Audience      segment.Audience `json:"audience"`
	LongestValues content.Longest  `json:"longest_values"`
	ReferenceTime time.Time        `json:"reference_time"`
	Revision      int              `json:"revision"`
	LLMAttempts   int              `json:"llm_attempts"`
	GeneratedAt   time.Time        `json:"generated_at"`
}

type ContentVersion struct {
	ID               string                  `json:"id"`
	CampaignID       string                  `json:"campaign_id"`
	VersionNumber    int                     `json:"version_number"`
	SegmentRevision  int                     `json:"segment_revision"`
	Status           ContentStatus           `json:"status"`
	Source           ContentSource           `json:"source"`
	Content          content.Content         `json:"content"`
	WorstCase        *content.WorstCase      `json:"worst_case"` // null iff template syntax invalid
	ValidationErrors []validation.FieldError `json:"validation_errors"`
	LLMAttempts      *int                    `json:"llm_attempts"`
	CreatedAt        time.Time               `json:"created_at"`
}

type Approval struct {
	ID               string    `json:"id"`
	ContentVersionID string    `json:"content_version_id"`
	ApprovedAt       time.Time `json:"approved_at"`
}

type Campaign struct {
	ID                   string          `json:"id"`
	Brief                string          `json:"brief"`
	State                State           `json:"state"`
	Segment              *Segment        `json:"segment"`
	LatestContentVersion *ContentVersion `json:"latest_content_version"` // latest VALIDATED, current revision
	Approval             *Approval       `json:"approval"`
	ScheduledAt          *time.Time      `json:"scheduled_at"` // non-nil when scheduled; in sending/completed non-nil if started from a plan
	SendingStartedAt     *time.Time      `json:"sending_started_at"`
	CompletedAt          *time.Time      `json:"completed_at"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type CampaignPage struct {
	Items  []Campaign `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

type SegmentResult struct {
	CampaignID string  `json:"campaign_id"`
	State      State   `json:"state"`
	Segment    Segment `json:"segment"`
}

type SegmentCount struct {
	Audience      segment.Audience `json:"audience"`
	ReferenceTime time.Time        `json:"reference_time"`
}

type DeliveryTotals struct {
	Total int `json:"total"`
	SMS   int `json:"sms"`
	Email int `json:"email"`
}

type SendResult struct {
	CampaignID       string         `json:"campaign_id"`
	State            State          `json:"state"` // sending | completed | scheduled
	ContentVersionID string         `json:"content_version_id"`
	ScheduledAt      *time.Time     `json:"scheduled_at"` // non-nil iff State == scheduled
	Deliveries       DeliveryTotals `json:"deliveries"`   // all zero iff State == scheduled
}

// CancelScheduleResult is the 200 body of DELETE /api/campaigns/{campaign_id}/schedule.
type CancelScheduleResult struct {
	CampaignID  string     `json:"campaign_id"`
	State       State      `json:"state"`        // always approved
	ScheduledAt *time.Time `json:"scheduled_at"` // always nil (JSON null)
}

type StatusCounts struct {
	Total   int `json:"total"`
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

type ChannelCounts struct {
	SMS   StatusCounts `json:"sms"`
	Email StatusCounts `json:"email"`
}

type CampaignStatus struct {
	CampaignID       string        `json:"campaign_id"`
	State            State         `json:"state"`
	Counts           StatusCounts  `json:"counts"`
	ByChannel        ChannelCounts `json:"by_channel"`
	ScheduledAt      *time.Time    `json:"scheduled_at"`
	SendingStartedAt *time.Time    `json:"sending_started_at"`
	CompletedAt      *time.Time    `json:"completed_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type Delivery struct {
	ID                string     `json:"id"`
	CampaignID        string     `json:"campaign_id"`
	ContentVersionID  string     `json:"content_version_id"`
	CustomerID        string     `json:"customer_id"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	FailedAttempts    int        `json:"failed_attempts"`
	RateLimitedCount  int        `json:"rate_limited_count"`
	RetryCount        int        `json:"retry_count"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	FirstAttemptAt    *time.Time `json:"first_attempt_at"`
	LastAttemptAt     *time.Time `json:"last_attempt_at"`
	LastHTTPStatus    *int       `json:"last_http_status"`
	LastError         *string    `json:"last_error"`
	FailureReason     *string    `json:"failure_reason"`
	ProviderMessageID *string    `json:"provider_message_id"`
	SentAt            *time.Time `json:"sent_at"`
	FailedAt          *time.Time `json:"failed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type DeliveryPage struct {
	Items  []Delivery `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// DeliveryFilter: empty string = no filter. Order: "customer" (default) | "recent".
type DeliveryFilter struct {
	Status, Channel, CustomerID, Order string
	Limit, Offset                      int
}

// PreviewQuery: CustomerID wins over SampleIndex when both are set.
type PreviewQuery struct {
	CustomerID       string
	ContentVersionID string
	SampleIndex      int
}

type PreviewCustomer struct {
	ID        string `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	City      string `json:"city"`
}

type PreviewSMS struct {
	Text        string `json:"text"`
	Runes       int    `json:"runes"`
	Limit       int    `json:"limit"`
	WithinLimit bool   `json:"within_limit"`
}

type PreviewEmail struct {
	Subject      string `json:"subject"`
	SubjectRunes int    `json:"subject_runes"`
	SubjectLimit int    `json:"subject_limit"`
	WithinLimit  bool   `json:"within_limit"`
	Text         string `json:"text"`
	HTML         string `json:"html"`
}

type Preview struct {
	CampaignID       string          `json:"campaign_id"`
	ContentVersionID string          `json:"content_version_id"`
	SampleIndex      int             `json:"sample_index"`
	SampleCount      int             `json:"sample_count"`
	Customer         PreviewCustomer `json:"customer"`
	SMS              PreviewSMS      `json:"sms"`
	Email            PreviewEmail    `json:"email"`
}

// ---- Use cases ----

// Service is the use-case layer behind internal/httpapi; httpapi handler tests use a fake Service.
type Service interface {
	Create(ctx context.Context, brief string) (Campaign, error)
	Get(ctx context.Context, id string) (Campaign, error)
	List(ctx context.Context, limit, offset int) (CampaignPage, error)
	// GenerateSegment: ErrCampaignNotFound -> ErrInvalidState (scheduled, sending, completed) ->
	// ErrGenerationInProgress -> llm.ErrUnavailable / composer.ErrInvalidOutput.
	GenerateSegment(ctx context.Context, id string) (SegmentResult, error)
	// StartContentGeneration performs every pre-stream check and connects LLM attempt 1. Errors here
	// become JSON responses; after it returns a run, the handler writes HTTP 200 + SSE.
	// Check order: ErrCampaignNotFound -> ErrInvalidState (draft, scheduled, sending, completed) ->
	// ErrGenerationInProgress -> llm.ErrUnavailable. Acquires the generation lock, which the returned
	// run releases when Stream returns (any outcome) or when ctx is cancelled.
	StartContentGeneration(ctx context.Context, id string) (ContentRun, error)
	// EditContent: ErrCampaignNotFound -> ErrGenerationInProgress -> ErrInvalidState (draft,
	// scheduled, sending, completed) -> *EditConflictError (expectedVersionID, a UUID or ExpectedVersionNone, is not the
	// current latest validated version, compared under the row lock) -> content.Parse +
	// content.Validate against the CURRENT segment's longest values (*validation.Error, nothing
	// stored). Success, in one transaction under the campaign row lock: next version_number, status
	// validated, source edit, state content_ready, active approval revoked ("content_changed").
	EditContent(ctx context.Context, id, expectedVersionID string, raw []byte) (ContentVersion, error)
	ListContentVersions(ctx context.Context, id string) ([]ContentVersion, error)
	GetContentVersion(ctx context.Context, id, versionID string) (ContentVersion, error)
	// Approve: openapi approveContent "Checks, in this order". Idempotent for the already approved
	// version (unchanged Campaign, no new approvals row, the worst-case re-check is not run again).
	Approve(ctx context.Context, id, versionID string) (Campaign, error)
	// Send starts the send NOW: ErrCampaignNotFound -> ErrContentVersionNotFound ->
	// ErrGenerationInProgress -> ErrInvalidState (neither approved nor scheduled; a second send) ->
	// ErrStaleContentVersion (README §5.4). Accepted in approved and in scheduled ("send now", D62:
	// the plan is dropped in the same transaction). Result: ScheduledAt nil, Deliveries = audiences.
	Send(ctx context.Context, id, versionID string) (SendResult, error)
	// Schedule plans (approved) or re-plans (scheduled) the send for at (P7a, D62): the window check
	// ValidateScheduledAt runs FIRST, before any database access, then ErrCampaignNotFound ->
	// ErrContentVersionNotFound -> ErrGenerationInProgress -> ErrInvalidState (neither approved nor
	// scheduled) -> ErrStaleContentVersion. No delivery row is written; the approval is unchanged.
	Schedule(ctx context.Context, id, versionID string, at time.Time) (SendResult, error)
	// CancelSchedule: ErrCampaignNotFound -> ErrInvalidState (not scheduled). Success: state
	// approved, scheduled_at NULL, approval kept. No generation-lease check (none can run in
	// scheduled).
	CancelSchedule(ctx context.Context, id string) (CancelScheduleResult, error)
	Status(ctx context.Context, id string) (CampaignStatus, error)
	Preview(ctx context.Context, id string, q PreviewQuery) (Preview, error)
	ListDeliveries(ctx context.Context, id string, f DeliveryFilter) (DeliveryPage, error)
	CountRule(ctx context.Context, raw []byte) (SegmentCount, error)
}

// ContentRun is a started generation. Stream calls emit for every composer event in order and
// finally exactly once with the terminal event (ValidatedEvent or InvalidEvent{Final:true}),
// persisting the version before emitting it, in ONE transaction under the campaign row lock. ctx is
// checked before that transaction starts: if it is already cancelled nothing is stored and Stream
// returns ctx.Err(); a transaction that has started is completed even if the client disconnects.
type ContentRun interface {
	Stream(ctx context.Context, emit func(composer.Event) error) error
}
