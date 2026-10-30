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

// StartContentGeneration: 404 -> invalid_state (draft, scheduled, sending, completed) -> generation lease,
// all under the campaign row lock (D52) -> LLM attempt 1 connected (llm.ErrUnavailable otherwise).
// The lease is held by the returned run.
func (s *service) StartContentGeneration(ctx context.Context, id string) (ContentRun, error) {
	id, ok := canonicalID(id)
	if !ok {
		return nil, ErrCampaignNotFound
	}
	lease, release, err := s.beginGeneration(ctx, id, func(lc LockedCampaign) error {
		if _, err := Transition(lc.State, EventContentValidated); err != nil {
			return err
		}
		if !lc.HasSegment {
			return fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
		}
		return lc.CheckNoGeneration(s.d.Clock.Now())
	})
	if err != nil {
		return nil, err
	}

	// genCtx carries the request's values (the mock scenario) and ends when the request is
	// cancelled, when the run finishes or at the lease's deadline (MaxGenerationDuration after
	// the instant the lease was stamped with) on the injected clock.
	genCtx, cancel := context.WithCancelCause(ctx)
	// Until Stream takes over, the lease ends with genCtx (e.g. the handler never streams). Stream
	// stops this before persisting, so a timeout can never release the lease mid-transaction.
	stopRelease := context.AfterFunc(genCtx, release)
	timer := s.d.Clock.After(lease.ExpiresAt.Sub(s.d.Clock.Now()))
	go func() {
		select {
		case <-timer:
			cancel(errGenerationTimeout)
		case <-genCtx.Done():
		}
	}()

	// Read after the lease is held: no segment generation can change the campaign meanwhile.
	c, err := s.d.Store.GetCampaign(genCtx, id)
	if err == nil && c.Segment == nil {
		err = fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
	}
	if err != nil {
		cancel(nil)
		release()
		return nil, err
	}

	sess, err := composer.OpenContent(genCtx, s.d.LLM, llm.ContentRequest{
		Brief: c.Brief, Rule: c.Segment.Rule, Longest: c.Segment.LongestValues,
		// P8 (D63): English only when the current audience has English-speaking customers.
		IncludeEnglish: c.Segment.Audience.WantsEnglish(),
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
	return &contentRun{s: s, id: id, token: lease.Token, genCtx: genCtx, cancel: cancel, release: release, stopRelease: stopRelease, sess: sess}, nil
}

// contentRun is the ContentRun of the service.
type contentRun struct {
	s       *service
	id      string
	token   string // the run's generation lease
	genCtx  context.Context
	cancel  context.CancelCauseFunc
	release func() // idempotent; also called by context.AfterFunc when genCtx ends
	// stopRelease detaches release from genCtx; false means it already ran (genCtx ended).
	stopRelease func() bool
	sess        *composer.ContentSession
	once        sync.Once
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
	// From here on only Stream's defer releases the lock: a timeout firing while finish is inside
	// the save transaction must not let another generation or edit in. If the release already ran,
	// genCtx has ended and the run is treated as cancelled/timed out.
	released := !r.stopRelease()
	if err != nil || released || r.genCtx.Err() != nil {
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
		// A run whose lease expired and was taken over (by any process) stores nothing (D52).
		if err := lc.CheckGenerationOwner(r.token); err != nil {
			return ContentWrite{}, err
		}
		next, err := Transition(lc.State, ev)
		if err != nil {
			return ContentWrite{}, err
		}
		if out.Valid {
			w.NextState = next
		}
		return w, nil
	})
	if errors.Is(err, errLeaseLost) {
		r.s.d.Logger.Info("content version not stored: generation lease lost", slog.String("campaign_id", r.id))
		return emit(composer.InvalidEvent{Attempt: out.Attempts, Final: true, Errors: []validation.FieldError{{
			Path: "", Code: validation.CodeLLMUnavailable,
			Message: "the generation did not finish within " + MaxGenerationDuration.String(),
		}}})
	}
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

// EditContent: (under the row lock) 404 -> generation lease -> invalid_state -> edit conflict ->
// content.Parse + content.Validate with the current segment's longest values -> new validated
// version, state content_ready, active approval revoked.
func (s *service) EditContent(ctx context.Context, id, expectedVersionID string, raw []byte) (ContentVersion, error) {
	id, ok := canonicalID(id)
	if !ok {
		return ContentVersion{}, ErrCampaignNotFound
	}
	if v, ok := canonicalID(expectedVersionID); ok {
		expectedVersionID = v
	}
	return s.d.Store.WriteContentVersion(ctx, id, func(lc LockedCampaign) (ContentWrite, error) {
		// Under the row lock (D47b, D52): a generation lease on the row, from any process, refuses
		// the edit; a generation cannot start until this transaction ends.
		if err := lc.CheckNoGeneration(s.d.Clock.Now()); err != nil {
			return ContentWrite{}, err
		}
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
	id, ok := canonicalID(id)
	if !ok {
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
	id, ok := canonicalID(id)
	if !ok {
		return ContentVersion{}, ErrCampaignNotFound
	}
	versionID, ok = canonicalID(versionID)
	if !ok {
		if _, err := s.d.Store.GetCampaign(ctx, id); err != nil {
			return ContentVersion{}, err
		}
		return ContentVersion{}, ErrContentVersionNotFound
	}
	return s.d.Store.GetContentVersion(ctx, id, versionID)
}

// Approve follows openapi approveContent: 404 campaign -> 404 version -> (under the row lock)
// generation lease -> content_not_validated -> invalid_state -> stale -> idempotent re-approval
// or worst-case re-check with the current customers -> approval row, state approved.
func (s *service) Approve(ctx context.Context, id, versionID string) (Campaign, error) {
	v, err := s.GetContentVersion(ctx, id, versionID)
	if err != nil {
		return Campaign{}, err
	}
	id, versionID = v.CampaignID, v.ID // canonical (D47a)
	a := ApprovalWrite{ID: uuid.NewString(), ApprovedAt: s.now()}
	return s.d.Store.ApproveVersion(ctx, id, versionID, a, func(chk ApprovalCheck) (bool, error) {
		// Under the row lock (D47b, D52): no approval while a generation lease is on the row.
		if err := chk.Campaign.CheckNoGeneration(s.d.Clock.Now()); err != nil {
			return false, err
		}
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

// Send follows openapi sendCampaign without scheduled_at (README §5.4): 404 campaign -> 404
// version -> (under the row lock) generation lease -> invalid_state (neither approved nor
// scheduled, second send) -> stale (not the approved version). The checks and the delivery rows
// share ONE transaction on the locked row (D52), so a send and a generation start never both win
// and a refused send writes nothing. From scheduled ("send now", D62) the plan is dropped in that
// same transaction.
func (s *service) Send(ctx context.Context, id, versionID string) (SendResult, error) {
	v, err := s.GetContentVersion(ctx, id, versionID)
	if err != nil {
		return SendResult{}, err
	}
	id, versionID = v.CampaignID, v.ID // canonical (D47a)
	totals, state, err := s.d.Store.CreateDeliveries(ctx, id, versionID, s.now, func(lc LockedCampaign) error {
		return s.checkSendable(lc, versionID, EventSendAccepted)
	})
	if err != nil {
		return SendResult{}, err
	}
	s.d.Logger.Info("send accepted", slog.String("campaign_id", id), slog.String("state", string(state)),
		slog.Int("sms", totals.SMS), slog.Int("email", totals.Email))
	return SendResult{CampaignID: id, State: state, ContentVersionID: versionID, Deliveries: totals}, nil
}

// Preview renders a validated version (default: the latest) for one sample customer matched by
// the rule at the campaign's reference time.
func (s *service) Preview(ctx context.Context, id string, q PreviewQuery) (Preview, error) {
	id, ok := canonicalID(id)
	if !ok {
		return Preview{}, ErrCampaignNotFound
	}
	if q.Language != "" && q.Language != content.LangTR && q.Language != content.LangEN {
		return Preview{}, &validation.Error{Fields: []validation.FieldError{{
			Path: "query.language", Code: validation.CodeInvalidValue, Message: "language must be tr or en",
		}}}
	}
	c, err := s.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	// README §5.4: 404 content_version_not_found comes before 409 invalid_state.
	var v ContentVersion
	if q.ContentVersionID != "" {
		if v, err = s.GetContentVersion(ctx, id, q.ContentVersionID); err != nil {
			return Preview{}, err
		}
	}
	if c.Segment == nil {
		return Preview{}, fmt.Errorf("%w: campaign has no segment", ErrInvalidState)
	}
	if q.ContentVersionID != "" {
		if v.Status != ContentValidated {
			return Preview{}, ErrContentNotValidated
		}
	} else {
		if c.LatestContentVersion == nil {
			return Preview{}, fmt.Errorf("%w: campaign has no validated content version", ErrInvalidState)
		}
		v = *c.LatestContentVersion
	}
	customerID := q.CustomerID
	if customerID != "" {
		if customerID, ok = canonicalID(customerID); !ok {
			return Preview{}, ErrCustomerNotFound
		}
	}
	if q.SampleIndex < 0 {
		return Preview{}, ErrCustomerNotFound
	}
	rule, err := segment.Parse(append(append([]byte(`{"rule":`), c.Segment.Rule...), '}'))
	if err != nil {
		return Preview{}, fmt.Errorf("campaign: stored segment rule: %w", err)
	}
	sample, err := s.d.Store.PreviewSample(ctx, rule, c.Segment.ReferenceTime, customerID, q.SampleIndex)
	if err != nil {
		return Preview{}, err
	}
	cu := sample.Customer
	// P8 (D63): the requested language, or the sample customer's own.
	lang := q.Language
	if lang == "" {
		lang = cu.Language
	}
	r, err := content.RenderContent(v.Content, content.Recipient{FirstName: cu.FirstName, LastName: cu.LastName, City: cu.City, Language: lang})
	if err != nil {
		return Preview{}, fmt.Errorf("campaign: render validated version: %w", err)
	}
	smsRunes, subjectRunes := content.RuneCount(r.SMSText), content.RuneCount(r.EmailSubject)
	return Preview{
		CampaignID: id, ContentVersionID: v.ID, SampleIndex: sample.Index, SampleCount: sample.Count, Customer: cu,
		Language: r.Language, Fallback: r.Fallback,
		SMS: PreviewSMS{Text: r.SMSText, Runes: smsRunes, Limit: content.SMSMaxRunes, WithinLimit: smsRunes <= content.SMSMaxRunes},
		Email: PreviewEmail{
			Subject: r.EmailSubject, SubjectRunes: subjectRunes, SubjectLimit: content.SubjectMaxRunes,
			WithinLimit: subjectRunes <= content.SubjectMaxRunes, Text: r.EmailText,
			HTML: content.RenderEmailHTMLLang(r.Language, r.EmailSubject, r.EmailText),
		},
	}, nil
}
