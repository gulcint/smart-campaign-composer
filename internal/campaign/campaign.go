// Package campaign is the campaign use-case layer behind the HTTP API.
//
// P2 provides the domain types and the Service port that internal/httpapi
// depends on; the state machine and the service implementation arrive with the
// audience, content and delivery slices (P3–P5).
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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
)

// BriefMaxRunes is the brief length limit (REQ-AUD-05).
const BriefMaxRunes = 2000

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
	State            State          `json:"state"` // sending | completed
	ContentVersionID string         `json:"content_version_id"`
	Deliveries       DeliveryTotals `json:"deliveries"`
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
	GenerateSegment(ctx context.Context, id string) (SegmentResult, error)
	// StartContentGeneration performs every pre-stream check (404/409/422 header/502 unavailable)
	// and connects LLM attempt 1. Errors here become JSON responses; after it returns a run,
	// the handler writes HTTP 200 + SSE.
	StartContentGeneration(ctx context.Context, id string) (ContentRun, error)
	EditContent(ctx context.Context, id string, raw []byte) (ContentVersion, error)
	ListContentVersions(ctx context.Context, id string) ([]ContentVersion, error)
	GetContentVersion(ctx context.Context, id, versionID string) (ContentVersion, error)
	Approve(ctx context.Context, id, versionID string) (Campaign, error)
	Send(ctx context.Context, id, versionID string) (SendResult, error)
	Status(ctx context.Context, id string) (CampaignStatus, error)
	Preview(ctx context.Context, id string, q PreviewQuery) (Preview, error)
	ListDeliveries(ctx context.Context, id string, f DeliveryFilter) (DeliveryPage, error)
	CountRule(ctx context.Context, raw []byte) (SegmentCount, error)
}

// ContentRun is a started generation. Stream calls emit for every composer event in order and
// finally exactly once with the terminal event (ValidatedEvent or InvalidEvent{Final:true}),
// persisting the version before emitting it. On ctx cancellation it stores nothing and returns
// ctx.Err().
type ContentRun interface {
	Stream(ctx context.Context, emit func(composer.Event) error) error
}
