package campaign

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// errGenerationTimeout is the cancellation cause of a content generation that exceeded
// MaxGenerationDuration.
var errGenerationTimeout = errors.New("campaign: generation exceeded the maximum duration")

// errSendNotInSlice: Send passed every pre-check of this slice; accepting the send (creating the
// deliveries) is the P5 slice. go-interfaces.md names no sentinel for it, so it maps to 500.
var errSendNotInSlice = fmt.Errorf("%w: accepting a send", errNotInSlice)

// StartContentGeneration: 404 -> invalid_state (draft, sending, completed) -> generation lock ->
// LLM attempt 1 connected (llm.ErrUnavailable otherwise). The lock is held by the returned run.
func (s *service) StartContentGeneration(ctx context.Context, id string) (ContentRun, error) {
	if !validID(id) {
		return nil, ErrCampaignNotFound
	}
	c, err := s.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := Transition(c.State, EventContentValidated); err != nil {
		return nil, err
	}
	if c.Segment == nil {
		return nil, fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
	}
	release, ok := s.locks.acquire(id, s.d.Clock.Now())
	if !ok {
		return nil, ErrGenerationInProgress
	}

	// genCtx carries the request's values (the mock scenario) and ends when the request is
	// cancelled, when the run finishes or after MaxGenerationDuration on the injected clock.
	genCtx, cancel := context.WithCancelCause(ctx)
	context.AfterFunc(genCtx, release)
	timer := s.d.Clock.After(MaxGenerationDuration)
	go func() {
		select {
		case <-timer:
			cancel(errGenerationTimeout)
		case <-genCtx.Done():
		}
	}()

	sess, err := composer.OpenContent(genCtx, s.d.LLM, llm.ContentRequest{
		Brief: c.Brief, Rule: c.Segment.Rule, Longest: c.Segment.LongestValues,
	})
	if err != nil {
		timedOut := errors.Is(context.Cause(genCtx), errGenerationTimeout)
		cancel(nil)
		release()
		if timedOut && ctx.Err() == nil {
			return nil, fmt.Errorf("%w: %w", llm.ErrUnavailable, errGenerationTimeout)
		}
		return nil, err
	}
	return &contentRun{s: s, id: id, genCtx: genCtx, cancel: cancel, release: release, sess: sess}, nil
}

// contentRun is the ContentRun of the service.
type contentRun struct {
	s       *service
	id      string
	genCtx  context.Context
	cancel  context.CancelCauseFunc
	release func() // idempotent; also called by context.AfterFunc when genCtx ends
	sess    *composer.ContentSession
	once    sync.Once
}

// Stream consumes the generation, persists its result and emits the terminal event.
func (r *contentRun) Stream(ctx context.Context, emit func(composer.Event) error) error {
	ran := false
	r.once.Do(func() { ran = true })
	if !ran {
		return errors.New("campaign: content run already streamed")
	}
	defer func() {
		r.cancel(nil)
		r.release() // synchronously, so the next request on this campaign never sees a stale lock
	}()
	stop := context.AfterFunc(ctx, func() { r.cancel(context.Canceled) })
	defer stop()

	attempt := 1
	var emitErr error
	out, err := r.sess.Run(func(ev composer.Event) error {
		switch e := ev.(type) {
		case composer.DeltaEvent:
			attempt = e.Attempt
		case composer.InvalidEvent:
			attempt = e.Attempt + 1
		}
		if err := emit(ev); err != nil {
			emitErr = err
			return err
		}
		return nil
	})
	if emitErr != nil {
		return emitErr
	}
	if ctx.Err() != nil {
		return ctx.Err() // client gone: nothing stored
	}
	if err != nil || r.genCtx.Err() != nil {
		if errors.Is(context.Cause(r.genCtx), errGenerationTimeout) {
			return emit(composer.InvalidEvent{Attempt: attempt, Final: true, Errors: []validation.FieldError{{
				Path: "", Code: validation.CodeLLMUnavailable,
				Message: "the generation did not finish within " + MaxGenerationDuration.String(),
			}}})
		}
		if err == nil {
			err = r.genCtx.Err()
		}
		return err
	}
	return r.finish(ctx, out, emit)
}

// finish persists the outcome (one transaction, completed even if the client goes away) and
// emits the terminal event.
func (r *contentRun) finish(ctx context.Context, out composer.ContentOutcome, emit func(composer.Event) error) error {
	if !out.Valid && !out.Storable {
		return emit(composer.InvalidEvent{Attempt: out.Attempts, Final: true, Errors: out.Errors})
	}
	attempts := out.Attempts
	w := ContentWrite{
		ID: uuid.NewString(), Source: SourceLLM, Content: out.Content, WorstCase: out.WorstCase,
		ValidationErrors: []validation.FieldError{}, LLMAttempts: &attempts, CreatedAt: r.s.now(),
	}
	ev := EventContentInvalid
	if out.Valid {
		w.Status, ev = ContentValidated, EventContentValidated
	} else {
		w.Status, w.ValidationErrors = ContentInvalid, out.Errors
	}
	v, err := r.s.d.Store.WriteContentVersion(context.WithoutCancel(ctx), r.id, func(lc LockedCampaign) (ContentWrite, error) {
		next, err := Transition(lc.State, ev)
		if err != nil {
			return ContentWrite{}, err
		}
		if out.Valid {
			w.NextState = next
		}
		return w, nil
	})
	if err != nil {
		r.s.d.Logger.Error("content version not stored", slog.String("campaign_id", r.id), slog.String("error", err.Error()))
		return emit(composer.InvalidEvent{Attempt: out.Attempts, Final: true, Errors: []validation.FieldError{{
			Path: "", Code: validation.CodeInternalError, Message: "the generated content could not be stored",
		}}})
	}
	r.s.d.Logger.Info("content version stored", slog.String("campaign_id", r.id), slog.Int("version_number", v.VersionNumber),
		slog.String("status", string(v.Status)), slog.Int("llm_attempts", attempts))
	if out.Valid {
		return emit(composer.ValidatedEvent{Attempt: out.Attempts, ContentVersionID: v.ID, VersionNumber: v.VersionNumber,
			Content: v.Content, WorstCase: *out.WorstCase})
	}
	return emit(composer.InvalidEvent{Attempt: out.Attempts, Final: true, Errors: out.Errors, ContentVersionID: &v.ID})
}

// EditContent: 404 -> generation lock -> (under the row lock) invalid_state -> edit conflict ->
// content.Parse + content.Validate with the current segment's longest values -> new validated
// version, state content_ready, active approval revoked.
func (s *service) EditContent(ctx context.Context, id, expectedVersionID string, raw []byte) (ContentVersion, error) {
	if !validID(id) {
		return ContentVersion{}, ErrCampaignNotFound
	}
	if _, err := s.d.Store.GetCampaign(ctx, id); err != nil {
		return ContentVersion{}, err
	}
	if s.locks.busy(id, s.d.Clock.Now()) {
		return ContentVersion{}, ErrGenerationInProgress
	}
	return s.d.Store.WriteContentVersion(ctx, id, func(lc LockedCampaign) (ContentWrite, error) {
		next, err := Transition(lc.State, EventContentValidated)
		if err != nil {
			return ContentWrite{}, err
		}
		if !lc.HasSegment {
			return ContentWrite{}, fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
		}
		if !expectedMatches(expectedVersionID, lc.Latest) {
			ce := &EditConflictError{}
			if lc.Latest != nil {
				vid, n := lc.Latest.ID, lc.Latest.Number
				ce.CurrentID, ce.CurrentVersionNumber = &vid, &n
			}
			return ContentWrite{}, ce
		}
		c, err := content.Parse(raw)
		if err != nil {
			return ContentWrite{}, err
		}
		if err := content.Validate(c, lc.Longest); err != nil {
			return ContentWrite{}, err
		}
		wc, err := content.MeasureWorstCase(c, lc.Longest)
		if err != nil {
			return ContentWrite{}, err // unreachable: Validate already accepted the template syntax
		}
		return ContentWrite{
			ID: uuid.NewString(), Status: ContentValidated, Source: SourceEdit, Content: c, WorstCase: &wc,
			ValidationErrors: []validation.FieldError{}, CreatedAt: s.now(), NextState: next,
		}, nil
	})
}

func expectedMatches(expected string, latest *VersionRef) bool {
	if latest == nil {
		return expected == ExpectedVersionNone
	}
	return expected == latest.ID
}

func (s *service) ListContentVersions(ctx context.Context, id string) ([]ContentVersion, error) {
	if !validID(id) {
		return nil, ErrCampaignNotFound
	}
	vs, err := s.d.Store.ListContentVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	if vs == nil {
		vs = []ContentVersion{}
	}
	return vs, nil
}

func (s *service) GetContentVersion(ctx context.Context, id, versionID string) (ContentVersion, error) {
	if !validID(id) {
		return ContentVersion{}, ErrCampaignNotFound
	}
	if !validID(versionID) {
		if _, err := s.d.Store.GetCampaign(ctx, id); err != nil {
			return ContentVersion{}, err
		}
		return ContentVersion{}, ErrContentVersionNotFound
	}
	return s.d.Store.GetContentVersion(ctx, id, versionID)
}

// Approve follows openapi approveContent: 404 campaign -> 404 version -> generation lock ->
// (under the row lock) content_not_validated -> invalid_state -> stale -> idempotent re-approval
// or worst-case re-check with the current customers -> approval row, state approved.
func (s *service) Approve(ctx context.Context, id, versionID string) (Campaign, error) {
	if _, err := s.GetContentVersion(ctx, id, versionID); err != nil {
		return Campaign{}, err
	}
	if s.locks.busy(id, s.d.Clock.Now()) {
		return Campaign{}, ErrGenerationInProgress
	}
	a := ApprovalWrite{ID: uuid.NewString(), ApprovedAt: s.now()}
	return s.d.Store.ApproveVersion(ctx, id, versionID, a, func(chk ApprovalCheck) (bool, error) {
		if chk.Version.Status != ContentValidated {
			return false, ErrContentNotValidated
		}
		if _, err := Transition(chk.Campaign.State, EventApproved); err != nil {
			return false, err
		}
		latest := chk.Campaign.Latest
		if latest == nil || latest.ID != versionID || chk.Version.SegmentRevision != chk.Campaign.SegmentRevision {
			return false, ErrStaleContentVersion
		}
		if chk.Campaign.State == StateApproved && chk.Campaign.ApprovedVersionID != nil && *chk.Campaign.ApprovedVersionID == versionID {
			return false, nil // idempotent: no new row, approved_at unchanged, no re-check
		}
		l, err := chk.Longest()
		if err != nil {
			return false, err
		}
		if err := content.CheckWorstCase(chk.Version.Content, l); err != nil {
			return false, err
		}
		return true, nil
	})
}

// Send performs the pre-checks of sendCampaign (README §5.4): 404 campaign -> 404 version ->
// generation lock -> invalid_state (not approved) -> stale (not the approved version). Accepting
// the send is the P5 slice.
func (s *service) Send(ctx context.Context, id, versionID string) (SendResult, error) {
	if _, err := s.GetContentVersion(ctx, id, versionID); err != nil {
		return SendResult{}, err
	}
	if s.locks.busy(id, s.d.Clock.Now()) {
		return SendResult{}, ErrGenerationInProgress
	}
	c, err := s.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return SendResult{}, err
	}
	if _, err := Transition(c.State, EventSendAccepted); err != nil {
		return SendResult{}, err
	}
	if c.Approval == nil || c.Approval.ContentVersionID != versionID {
		return SendResult{}, ErrStaleContentVersion
	}
	return SendResult{}, errSendNotInSlice
}

// Preview renders a validated version (default: the latest) for one sample customer matched by
// the rule at the campaign's reference time.
func (s *service) Preview(ctx context.Context, id string, q PreviewQuery) (Preview, error) {
	if !validID(id) {
		return Preview{}, ErrCampaignNotFound
	}
	c, err := s.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	if c.Segment == nil {
		return Preview{}, fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
	}
	var v ContentVersion
	if q.ContentVersionID != "" {
		if v, err = s.GetContentVersion(ctx, id, q.ContentVersionID); err != nil {
			return Preview{}, err
		}
		if v.Status != ContentValidated {
			return Preview{}, ErrContentNotValidated
		}
	} else {
		if c.LatestContentVersion == nil {
			return Preview{}, fmt.Errorf("%w: campaign has no validated content version", ErrInvalidState)
		}
		v = *c.LatestContentVersion
	}
	if q.SampleIndex < 0 || (q.CustomerID != "" && !validID(q.CustomerID)) {
		return Preview{}, ErrCustomerNotFound
	}
	rule, err := segment.Parse(append(append([]byte(`{"rule":`), c.Segment.Rule...), '}'))
	if err != nil {
		return Preview{}, fmt.Errorf("campaign: stored segment rule: %w", err)
	}
	sample, err := s.d.Store.PreviewSample(ctx, rule, c.Segment.ReferenceTime, q.CustomerID, q.SampleIndex)
	if err != nil {
		return Preview{}, err
	}
	cu := sample.Customer
	r, err := content.RenderContent(v.Content, content.Recipient{FirstName: cu.FirstName, LastName: cu.LastName, City: cu.City})
	if err != nil {
		return Preview{}, fmt.Errorf("campaign: render validated version: %w", err)
	}
	smsRunes, subjectRunes := content.RuneCount(r.SMSText), content.RuneCount(r.EmailSubject)
	return Preview{
		CampaignID: id, ContentVersionID: v.ID, SampleIndex: sample.Index, SampleCount: sample.Count, Customer: cu,
		SMS: PreviewSMS{Text: r.SMSText, Runes: smsRunes, Limit: content.SMSMaxRunes, WithinLimit: smsRunes <= content.SMSMaxRunes},
		Email: PreviewEmail{
			Subject: r.EmailSubject, SubjectRunes: subjectRunes, SubjectLimit: content.SubjectMaxRunes,
			WithinLimit: subjectRunes <= content.SubjectMaxRunes, Text: r.EmailText,
			HTML: content.RenderEmailHTML(r.EmailSubject, r.EmailText),
		},
	}, nil
}
