package campaign

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Store is the persistence port used by the service. Its method set is owned by the
// implementation and is NOT a test seam; it is constructed only by postgres.NewCampaignStore.
type Store interface {
	// CreateCampaign inserts a new draft campaign.
	CreateCampaign(ctx context.Context, c Campaign) error
	// GetCampaign returns the campaign with its segment, latest validated content version of the
	// current segment revision and active approval. Unknown id => ErrCampaignNotFound.
	GetCampaign(ctx context.Context, id string) (Campaign, error)
	// ListCampaigns returns one page, newest first, and the total count.
	ListCampaigns(ctx context.Context, limit, offset int) ([]Campaign, int, error)
	// Longest computes content.Longest over the customers matched by r at ref (before consent).
	Longest(ctx context.Context, r segment.Rule, ref time.Time) (content.Longest, error)
	// SaveSegment stores a generated segment in one transaction: it re-checks the state machine
	// (EventSegmentGenerated) under a row lock, increments the segment revision, revokes the
	// active approval (segment_changed) and moves the campaign to audience_ready.
	// Errors: ErrCampaignNotFound, ErrInvalidState.
	SaveSegment(ctx context.Context, id string, w SegmentWrite) (Campaign, error)

	// ListContentVersions returns every version of the campaign, highest version_number first.
	// Unknown campaign => ErrCampaignNotFound.
	ListContentVersions(ctx context.Context, id string) ([]ContentVersion, error)
	// GetContentVersion returns one version of the campaign. Errors: ErrCampaignNotFound,
	// ErrContentVersionNotFound (missing or another campaign's).
	GetContentVersion(ctx context.Context, id, versionID string) (ContentVersion, error)
	// WriteContentVersion runs ONE transaction under the campaign row lock (SELECT ... FOR UPDATE):
	// it passes the locked campaign to decide and stores the version decide returns with the next
	// version_number (highest of ANY status + 1) and the current segment revision. A non-empty
	// ContentWrite.NextState also moves the campaign to that state, clears the approved version and
	// revokes the active approval ("content_changed"). An error from decide aborts with nothing
	// written. Errors: ErrCampaignNotFound, decide's error.
	WriteContentVersion(ctx context.Context, id string, decide func(LockedCampaign) (ContentWrite, error)) (ContentVersion, error)
	// ApproveVersion runs ONE transaction under the campaign row lock: decide sees the locked
	// campaign and the version and returns whether to write; true inserts the approval row, sets the
	// approved version and the state approved. It returns the campaign after the transaction.
	// Errors: ErrCampaignNotFound, ErrContentVersionNotFound, decide's error.
	ApproveVersion(ctx context.Context, id, versionID string, a ApprovalWrite, decide func(ApprovalCheck) (bool, error)) (Campaign, error)
	// PreviewSample returns one customer matched by r at ref (before consent) with its 0-based index
	// in the matched set ordered by customer id, and the matched count: the customer with id
	// customerID when non-empty, otherwise the index-th one. Not matched / out of range =>
	// ErrCustomerNotFound.
	PreviewSample(ctx context.Context, r segment.Rule, ref time.Time, customerID string, index int) (SampleCustomer, error)
}

// VersionRef identifies a content version by id and number.
type VersionRef struct {
	ID     string
	Number int
}

// LockedCampaign is the campaign row as read under its row lock.
type LockedCampaign struct {
	State             State
	HasSegment        bool
	SegmentRevision   int
	Longest           content.Longest
	Latest            *VersionRef // latest VALIDATED version of the current segment revision
	ApprovedVersionID *string
}

// ContentWrite is one new, immutable content version.
type ContentWrite struct {
	ID               string
	Status           ContentStatus
	Source           ContentSource
	Content          content.Content
	WorstCase        *content.WorstCase
	ValidationErrors []validation.FieldError
	LLMAttempts      *int
	CreatedAt        time.Time
	// NextState: "" leaves the campaign row untouched (stored invalid draft).
	NextState State
}

// ApprovalCheck is what the approval decision sees under the row lock.
type ApprovalCheck struct {
	Campaign LockedCampaign
	Version  ContentVersion
	// Longest recomputes the longest values of the customers matched by the stored rule at the
	// campaign's reference time, inside the transaction.
	Longest func() (content.Longest, error)
}

// ApprovalWrite is the approval row to insert.
type ApprovalWrite struct {
	ID         string
	ApprovedAt time.Time
}

// SampleCustomer is one preview recipient.
type SampleCustomer struct {
	Customer PreviewCustomer
	Index    int
	Count    int
}

// SegmentWrite is a validated, counted segment ready to be stored.
type SegmentWrite struct {
	Rule          segment.Rule
	Audience      segment.Audience
	LongestValues content.Longest
	ReferenceTime time.Time
	LLMAttempts   int
	GeneratedAt   time.Time
}

type Deps struct {
	Store    Store
	Audience segment.Counter
	LLM      llm.Client
	Clock    clock.Clock
	// SegmentReferenceTime overrides Clock.Now() as the segment reference time when non-nil.
	SegmentReferenceTime *time.Time
	Logger               *slog.Logger
}

// List page limits (openapi Limit/Offset parameters).
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// errNotInSlice is returned by the use cases that the P5 slice implements (accepting a send,
// status, deliveries). Their HTTP routes are not registered yet, so it is unreachable over HTTP.
var errNotInSlice = errors.New("campaign: operation not implemented in this slice")

// NewService returns the Service implementation.
func NewService(d Deps) Service {
	if d.Clock == nil {
		d.Clock = clock.Real()
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &service{d: d, locks: &generationLocks{held: map[string]time.Time{}}}
}

type service struct {
	d     Deps
	locks *generationLocks
}

// now is the service time, truncated to the database's microsecond precision so that a value
// returned before and after a round trip is identical.
func (s *service) now() time.Time { return s.d.Clock.Now().UTC().Truncate(time.Microsecond) }

func (s *service) referenceTime() time.Time {
	if s.d.SegmentReferenceTime != nil {
		return s.d.SegmentReferenceTime.UTC().Truncate(time.Microsecond)
	}
	return s.now()
}

// canonicalID accepts the 36-character UUID form in any letter case and returns its lower-case
// canonical spelling (D47a): the in-memory generation lock is keyed by it and version ids are
// compared with it, so an upper-case id behaves exactly like the stored (Postgres) one.
func canonicalID(id string) (string, bool) {
	u, err := uuid.Parse(id)
	if err != nil || len(id) != 36 {
		return "", false
	}
	return u.String(), true
}

func (s *service) Create(ctx context.Context, brief string) (Campaign, error) {
	b, err := NormalizeBrief(brief)
	if err != nil {
		return Campaign{}, err
	}
	now := s.now()
	c := Campaign{ID: uuid.NewString(), Brief: b, State: StateDraft, CreatedAt: now, UpdatedAt: now}
	if err := s.d.Store.CreateCampaign(ctx, c); err != nil {
		return Campaign{}, err
	}
	return c, nil
}

func (s *service) Get(ctx context.Context, id string) (Campaign, error) {
	id, ok := canonicalID(id)
	if !ok {
		return Campaign{}, ErrCampaignNotFound
	}
	return s.d.Store.GetCampaign(ctx, id)
}

func (s *service) List(ctx context.Context, limit, offset int) (CampaignPage, error) {
	var fields []validation.FieldError
	if limit < 1 || limit > MaxPageLimit {
		fields = append(fields, validation.FieldError{Path: "query.limit", Code: validation.CodeInvalidValue,
			Message: "limit must be between 1 and " + strconv.Itoa(MaxPageLimit), Err: validation.ErrInvalidValue})
	}
	if offset < 0 {
		fields = append(fields, validation.FieldError{Path: "query.offset", Code: validation.CodeInvalidValue,
			Message: "offset must not be negative", Err: validation.ErrInvalidValue})
	}
	if len(fields) > 0 {
		return CampaignPage{}, &validation.Error{Fields: fields}
	}
	items, total, err := s.d.Store.ListCampaigns(ctx, limit, offset)
	if err != nil {
		return CampaignPage{}, err
	}
	if items == nil {
		items = []Campaign{}
	}
	return CampaignPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// GenerateSegment: 404 -> generation lock (409 generation_in_progress) -> state (409
// invalid_state) -> LLM + validation with one repair -> audience count + longest values at the
// reference time -> store (audience_ready). On any error the campaign is unchanged.
func (s *service) GenerateSegment(ctx context.Context, id string) (SegmentResult, error) {
	id, ok := canonicalID(id)
	if !ok {
		return SegmentResult{}, ErrCampaignNotFound
	}
	c, err := s.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return SegmentResult{}, err
	}
	release, ok := s.locks.acquire(id, s.d.Clock.Now())
	if !ok {
		return SegmentResult{}, ErrGenerationInProgress
	}
	defer release()
	if _, err := Transition(c.State, EventSegmentGenerated); err != nil {
		return SegmentResult{}, err
	}

	ref := s.referenceTime()
	genCtx, cancel := context.WithTimeout(ctx, MaxGenerationDuration)
	defer cancel()
	out, err := composer.GenerateSegment(genCtx, s.d.LLM, c.Brief)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return SegmentResult{}, fmt.Errorf("%w: generation exceeded %s", llm.ErrUnavailable, MaxGenerationDuration)
		}
		s.d.Logger.Info("segment generation rejected", slog.String("campaign_id", id), slog.String("error", err.Error()))
		return SegmentResult{}, err
	}

	aud, err := s.d.Audience.Count(ctx, out.Rule, ref)
	if err != nil {
		return SegmentResult{}, err
	}
	longest, err := s.d.Store.Longest(ctx, out.Rule, ref)
	if err != nil {
		return SegmentResult{}, err
	}
	saved, err := s.d.Store.SaveSegment(ctx, id, SegmentWrite{
		Rule: out.Rule, Audience: aud, LongestValues: longest, ReferenceTime: ref,
		LLMAttempts: out.Attempts, GeneratedAt: s.now(),
	})
	if err != nil {
		return SegmentResult{}, err
	}
	if saved.Segment == nil {
		return SegmentResult{}, errors.New("campaign: stored segment missing")
	}
	s.d.Logger.Info("segment generated", slog.String("campaign_id", id), slog.Int("revision", saved.Segment.Revision),
		slog.Int("llm_attempts", out.Attempts), slog.Int("matched", aud.Matched))
	return SegmentResult{CampaignID: saved.ID, State: saved.State, Segment: *saved.Segment}, nil
}

// CountRule validates an explicit rule (same Parse/Validate/Compile path as LLM output) and counts
// its audience at the reference time. Nothing is stored and the LLM is not called.
func (s *service) CountRule(ctx context.Context, raw []byte) (SegmentCount, error) {
	r, err := segment.Parse(raw)
	if err != nil {
		return SegmentCount{}, err
	}
	if err := segment.Validate(r); err != nil {
		return SegmentCount{}, err
	}
	ref := s.referenceTime()
	aud, err := s.d.Audience.Count(ctx, r, ref)
	if err != nil {
		return SegmentCount{}, err
	}
	return SegmentCount{Audience: aud, ReferenceTime: ref}, nil
}

// ---- P5 slice ----

func (s *service) Status(context.Context, string) (CampaignStatus, error) {
	return CampaignStatus{}, errNotInSlice
}

func (s *service) ListDeliveries(context.Context, string, DeliveryFilter) (DeliveryPage, error) {
	return DeliveryPage{}, errNotInSlice
}

// ---- per-campaign generation lock ----

// generationLocks allows one segment/content generation per campaign in this process. A lock is
// released by the holder, and a lock older than MaxGenerationDuration is considered stale.
type generationLocks struct {
	mu   sync.Mutex
	held map[string]time.Time // campaign id -> acquired at
}

// acquire takes the campaign's lock; release is idempotent.
func (l *generationLocks) acquire(id string, now time.Time) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at, busy := l.held[id]; busy && now.Sub(at) < MaxGenerationDuration {
		return nil, false
	}
	l.held[id] = now
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if at, busy := l.held[id]; busy && at.Equal(now) {
				delete(l.held, id)
			}
		})
	}, true
}

// busy reports whether a generation currently holds the campaign's lock.
func (l *generationLocks) busy(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	at, held := l.held[id]
	return held && now.Sub(at) < MaxGenerationDuration
}
