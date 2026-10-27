package campaign

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
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
	// SaveSegment stores a generated segment in one transaction: under a row lock it checks that the
	// row still carries w.LeaseToken (CheckGenerationOwner) and re-checks the state machine
	// (EventSegmentGenerated), increments the segment revision, revokes the
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

	// BeginGeneration runs ONE transaction under the campaign row lock: it passes the locked
	// campaign to check and, when check returns nil, stores the lease (campaigns.generation_token,
	// generation_expires_at), replacing an expired one. Errors: ErrCampaignNotFound, check's error.
	BeginGeneration(ctx context.Context, id string, lease GenerationLease, check func(LockedCampaign) error) error
	// EndGeneration clears the campaign's lease only while it still carries token, so a holder
	// whose lease expired and was taken over never frees the new holder's lease.
	EndGeneration(ctx context.Context, id, token string) error

	// CreateDeliveries accepts a send in ONE transaction under the campaign row lock: it passes the
	// locked campaign to check and, when check returns nil, reads at := now() (after the lock is
	// held, so updated_at never moves backwards behind a concurrent write) and inserts one queued
	// delivery (next_attempt_at = at) per (channel, customer) of the channel audiences of the stored rule at
	// the stored reference time with the CURRENT consent and contact data (segment-dsl.md §6,
	// REQ-AUD-04), moves the campaign to sending (sending_started_at = at) and, when no delivery was
	// created, on to completed. It returns the created counts and the resulting state. Errors:
	// ErrCampaignNotFound, check's error; nothing is written on any error.
	CreateDeliveries(ctx context.Context, id, versionID string, now func() time.Time, check func(LockedCampaign) error) (DeliveryTotals, State, error)
	// DeliveryStatus returns the campaign's state, timestamps and delivery counters per channel and
	// status, all read in one snapshot. Unknown campaign => ErrCampaignNotFound.
	DeliveryStatus(ctx context.Context, id string) (CampaignStatus, error)
	// ListDeliveries returns one page of the campaign's deliveries matching f (already validated;
	// Order "customer" or "recent") and the filtered total, both from one snapshot. Unknown
	// campaign => ErrCampaignNotFound.
	ListDeliveries(ctx context.Context, id string, f DeliveryFilter) ([]Delivery, int, error)
}

// GenerationLease is the per-campaign generation marker kept on the campaign row (D52): at most
// one segment/content generation per campaign across every API process sharing the database.
type GenerationLease struct {
	Token     string
	ExpiresAt time.Time
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
	Generation        *GenerationLease // nil: no lease on the row
}

// CheckNoGeneration returns ErrGenerationInProgress while the row carries a lease that has not
// expired at now. Every write that must not overlap a generation (segment/content generation
// start, edit, approve, send) calls it on the row it holds locked, inside its own transaction, so
// the check and the write are atomic across API processes (D47 b, D52).
func (lc LockedCampaign) CheckNoGeneration(now time.Time) error {
	if lc.Generation != nil && now.Before(lc.Generation.ExpiresAt) {
		return ErrGenerationInProgress
	}
	return nil
}

// errLeaseLost: the generation's lease was cleared or taken over (it expired) before its result
// was stored; the service reports it like a generation that exceeded MaxGenerationDuration.
var errLeaseLost = errors.New("campaign: generation lease lost")

// CheckGenerationOwner fails unless the row still carries the lease with token. A generation's
// final save calls it under the row lock, so a generation whose lease expired and was taken over
// (possibly by another process) never stores a result (D52).
func (lc LockedCampaign) CheckGenerationOwner(token string) error {
	if lc.Generation == nil || lc.Generation.Token != token {
		return errLeaseLost
	}
	return nil
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
	// LeaseToken is the generation lease the write requires: SaveSegment refuses (nothing written)
	// when the locked row no longer carries it (LockedCampaign.CheckGenerationOwner).
	LeaseToken string
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

// NewService returns the Service implementation.
func NewService(d Deps) Service {
	if d.Clock == nil {
		d.Clock = clock.Real()
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &service{d: d}
}

type service struct {
	d Deps
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
// canonical spelling (D47a): version ids are compared with it, so an upper-case id behaves exactly like the stored (Postgres) one.
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

// GenerateSegment: 404 -> generation lease (409 generation_in_progress) -> state (409
// invalid_state), both under the campaign row lock -> LLM + validation with one repair -> audience count + longest values at the
// reference time -> store (audience_ready). On any error the campaign is unchanged.
func (s *service) GenerateSegment(ctx context.Context, id string) (SegmentResult, error) {
	id, ok := canonicalID(id)
	if !ok {
		return SegmentResult{}, ErrCampaignNotFound
	}
	// The lease and the state are checked together under the campaign row lock (D52).
	lease, release, err := s.beginGeneration(ctx, id, func(lc LockedCampaign) error {
		if err := lc.CheckNoGeneration(s.d.Clock.Now()); err != nil {
			return err
		}
		_, err := Transition(lc.State, EventSegmentGenerated)
		return err
	})
	if err != nil {
		return SegmentResult{}, err
	}
	defer release()

	// The work after the lease is taken (LLM, count, longest values) ends at the lease's deadline.
	// The save is not cancelled by it (a deadline during COMMIT could report an error for a stored
	// segment); SaveSegment instead refuses under the row lock once the lease is no longer ours.
	workCtx, cancel := context.WithTimeout(ctx, lease.ExpiresAt.Sub(s.d.Clock.Now()))
	defer cancel()
	expired := func(err error) error {
		if errors.Is(err, errLeaseLost) || (ctx.Err() == nil && workCtx.Err() != nil) {
			return fmt.Errorf("%w: generation exceeded %s", llm.ErrUnavailable, MaxGenerationDuration)
		}
		return err
	}
	c, err := s.d.Store.GetCampaign(workCtx, id)
	if err != nil {
		return SegmentResult{}, expired(err)
	}

	ref := s.referenceTime()
	out, err := composer.GenerateSegment(workCtx, s.d.LLM, c.Brief)
	if err != nil {
		if ctx.Err() == nil && workCtx.Err() != nil {
			return SegmentResult{}, expired(err)
		}
		s.d.Logger.Info("segment generation rejected", slog.String("campaign_id", id), slog.String("error", err.Error()))
		return SegmentResult{}, err
	}

	aud, err := s.d.Audience.Count(workCtx, out.Rule, ref)
	if err != nil {
		return SegmentResult{}, expired(err)
	}
	longest, err := s.d.Store.Longest(workCtx, out.Rule, ref)
	if err != nil {
		return SegmentResult{}, expired(err)
	}
	saved, err := s.d.Store.SaveSegment(context.WithoutCancel(ctx), id, SegmentWrite{
		Rule: out.Rule, Audience: aud, LongestValues: longest, ReferenceTime: ref,
		LLMAttempts: out.Attempts, GeneratedAt: s.now(), LeaseToken: lease.Token,
	})
	if err != nil {
		return SegmentResult{}, expired(err)
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

// ---- delivery tracking ----

// Delivery filter values (openapi listDeliveries): the single source for the HTTP handler and the
// service.
const (
	DeliveryOrderCustomer = "customer"
	DeliveryOrderRecent   = "recent"
)

var (
	deliveryStatuses = []string{"queued", "sending", "sent", "failed"}
	deliveryChannels = []string{"sms", "email"}
	deliveryOrders   = []string{DeliveryOrderCustomer, DeliveryOrderRecent}
)

func (s *service) Status(ctx context.Context, id string) (CampaignStatus, error) {
	id, ok := canonicalID(id)
	if !ok {
		return CampaignStatus{}, ErrCampaignNotFound
	}
	return s.d.Store.DeliveryStatus(ctx, id)
}

// ListDeliveries validates the filter (422 invalid_value, paths query.<name>) before looking the
// campaign up (README §5.4: query before use case). Zero Limit and empty Order take the defaults.
func (s *service) ListDeliveries(ctx context.Context, id string, f DeliveryFilter) (DeliveryPage, error) {
	id, ok := canonicalID(id)
	if !ok {
		return DeliveryPage{}, ErrCampaignNotFound
	}
	if f.Order == "" {
		f.Order = DeliveryOrderCustomer
	}
	if f.Limit == 0 {
		f.Limit = DefaultPageLimit
	}
	if fields := DeliveryFilterErrors(&f); len(fields) > 0 {
		return DeliveryPage{}, &validation.Error{Fields: fields}
	}
	items, total, err := s.d.Store.ListDeliveries(ctx, id, f)
	if err != nil {
		return DeliveryPage{}, err
	}
	if items == nil {
		items = []Delivery{}
	}
	return DeliveryPage{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// DeliveryFilterErrors checks f against openapi listDeliveries (empty Status, Channel, CustomerID
// = no filter; Order must be set; Limit 1..MaxPageLimit; Offset >= 0) and canonicalizes
// f.CustomerID (D47a). Errors are invalid_value with paths query.<name>, in parameter order.
func DeliveryFilterErrors(f *DeliveryFilter) []validation.FieldError {
	var fields []validation.FieldError
	invalid := func(name, msg string) {
		fields = append(fields, validation.FieldError{Path: "query." + name, Code: validation.CodeInvalidValue,
			Message: msg, Err: validation.ErrInvalidValue})
	}
	if f.Status != "" && !slices.Contains(deliveryStatuses, f.Status) {
		invalid("status", "status must be one of "+strings.Join(deliveryStatuses, ", "))
	}
	if f.Channel != "" && !slices.Contains(deliveryChannels, f.Channel) {
		invalid("channel", "channel must be one of "+strings.Join(deliveryChannels, ", "))
	}
	if !slices.Contains(deliveryOrders, f.Order) {
		invalid("order", "order must be one of "+strings.Join(deliveryOrders, ", "))
	}
	if f.CustomerID != "" {
		cid, ok := canonicalID(f.CustomerID)
		if !ok {
			invalid("customer_id", "customer_id must be a UUID")
		}
		f.CustomerID = cid
	}
	if f.Limit < 1 || f.Limit > MaxPageLimit {
		invalid("limit", "limit must be an integer between 1 and "+strconv.Itoa(MaxPageLimit))
	}
	if f.Offset < 0 {
		invalid("offset", "offset must be a non-negative integer")
	}
	return fields
}

// ---- per-campaign generation lease ----

// beginGeneration takes the campaign's generation lease under the row lock after check accepted
// the locked row. The lease expires MaxGenerationDuration after now on the injected clock, rounded
// up to the database's microsecond precision so it never ends early. release is idempotent, safe
// for concurrent use, and frees only this lease even after ctx is cancelled. Callers bound their
// work by lease.ExpiresAt.
func (s *service) beginGeneration(ctx context.Context, id string, check func(LockedCampaign) error) (lease GenerationLease, release func(), err error) {
	expires := s.d.Clock.Now().UTC().Add(MaxGenerationDuration)
	if t := expires.Truncate(time.Microsecond); !t.Equal(expires) {
		expires = t.Add(time.Microsecond)
	}
	lease = GenerationLease{Token: uuid.NewString(), ExpiresAt: expires}
	end := func() {
		// Best effort: if this fails the lease still expires at lease.ExpiresAt.
		ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.d.Store.EndGeneration(ectx, id, lease.Token); err != nil {
			s.d.Logger.Error("generation lease not released", slog.String("campaign_id", id), slog.String("error", err.Error()))
		}
	}
	accepted := false
	err = s.d.Store.BeginGeneration(ctx, id, lease, func(lc LockedCampaign) error {
		err := check(lc)
		accepted = err == nil
		return err
	})
	if err != nil {
		if accepted {
			end() // the write or commit failed after check: the lease may have been stored anyway
		}
		return GenerationLease{}, nil, err
	}
	var once sync.Once
	return lease, func() { once.Do(end) }, nil
}
