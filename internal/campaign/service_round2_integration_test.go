package campaign_test

// P4 round 2 service integration tests (independent review of PR #50): canonical UUIDs (D47 a),
// the generation lock re-checked under the row lock (D47 b), editContent use-case order with a
// structurally invalid body (D47 c), the client-cancel lock release and previewContent order.
// Same harness and database rules as service_integration_test.go.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// stall starts a stalled_stream generation on id and returns once its first delta was emitted.
// stop cancels the client and waits for Stream to return (no terminal event expected).
func (h *harness) stall(t *testing.T, id string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(scen(llm.ScenarioStalledStream))
	run, err := h.svc.StartContentGeneration(ctx, id)
	if err != nil {
		cancel()
		t.Fatalf("StartContentGeneration(stalled_stream) error = %v", err)
	}
	firstDelta := make(chan struct{})
	var once sync.Once
	done := make(chan streamResult, 1)
	go func() {
		var res streamResult
		res.err = run.Stream(ctx, func(ev composer.Event) error {
			res.events = append(res.events, ev)
			if ev.Name() == "delta" {
				once.Do(func() { close(firstDelta) })
			}
			return nil
		})
		done <- res
	}()
	select {
	case <-firstDelta:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("no delta from stalled_stream within 10s")
	}
	return func() {
		t.Helper()
		cancel()
		select {
		case res := <-done:
			for _, ev := range res.events {
				if ev.Name() != "delta" {
					t.Fatalf("event %q after the client went away, want no terminal event", ev.Name())
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Stream did not return within 10s after the client went away")
		}
	}
}

// REQ-CNT-04, REQ-APR-03 (D47 a, D41, README §5.4 step 1)
// The service canonicalizes ids: while a generation runs on the lower-case id, StartContentGeneration,
// EditContent and Approve on the UPPER-CASE id are ErrGenerationInProgress (one lock per campaign,
// whatever the spelling). Afterwards approving the latest version through UPPER-CASE ids succeeds
// (not ErrStaleContentVersion), is idempotent, and every returned id is lower case; an UPPER-CASE
// expected version is the same base as the lower-case one.
func TestService_UpperCaseIDsAreCanonical(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	upID, upV1 := strings.ToUpper(id), strings.ToUpper(v1.ContentVersionID)
	if upID == id || upV1 == v1.ContentVersionID {
		t.Fatalf("test setup: ids %s / %s have no hex letters to upper-case", id, v1.ContentVersionID)
	}
	rowsBefore := h.versionRows(t, id)

	stop := h.stall(t, id)
	bg := context.Background()
	startCtx, startCancel := context.WithCancel(scen(llm.ScenarioOK))
	locked := map[string]error{}
	_, locked["StartContentGeneration(UPPER)"] = h.svc.StartContentGeneration(startCtx, upID)
	_, locked["EditContent(UPPER)"] = h.svc.EditContent(bg, upID, v1.ContentVersionID, rawOf(t, editContent("büyük harf")))
	_, locked["Approve(UPPER, UPPER)"] = h.svc.Approve(bg, upID, upV1)
	for op, err := range locked {
		if !errors.Is(err, campaign.ErrGenerationInProgress) {
			t.Errorf("%s during a generation on the lower-case id error = %v, want %v", op, err, campaign.ErrGenerationInProgress)
		}
	}
	startCancel()
	stop()
	if t.Failed() {
		t.FailNow()
	}
	if n := len(h.versionRows(t, id)); n != len(rowsBefore) {
		t.Fatalf("refused operations stored rows: %d, want %d", n, len(rowsBefore))
	}

	c, err := h.svc.Approve(bg, upID, upV1)
	if err != nil {
		t.Fatalf("Approve(UPPER id, UPPER latest version) error = %v, want nil (not stale)", err)
	}
	if c.ID != id || c.State != campaign.StateApproved || c.Approval == nil || c.Approval.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("approved campaign id/state/approval = %s/%s/%+v, want %s/approved bound to %s (lower case)", c.ID, c.State, c.Approval, id, v1.ContentVersionID)
	}
	if c.LatestContentVersion == nil || c.LatestContentVersion.ID != v1.ContentVersionID || c.LatestContentVersion.CampaignID != id {
		t.Fatalf("latest_content_version = %+v, want lower-case ids", c.LatestContentVersion)
	}
	again, err := h.svc.Approve(bg, upID, upV1)
	if err != nil || again.Approval == nil || again.Approval.ID != c.Approval.ID {
		t.Fatalf("second Approve(UPPER) = %+v, %v; want the unchanged approval %s", again.Approval, err, c.Approval.ID)
	}
	if rows := h.approvalRows(t, id); len(rows) != 1 {
		t.Fatalf("approval rows = %d, want 1 (idempotent re-approval through the upper-case spelling)", len(rows))
	}

	got := h.get(t, upID)
	if got.ID != id {
		t.Fatalf("Get(UPPER).ID = %s, want %s", got.ID, id)
	}
	v, err := h.svc.GetContentVersion(bg, upID, upV1)
	if err != nil || v.ID != v1.ContentVersionID || v.CampaignID != id {
		t.Fatalf("GetContentVersion(UPPER, UPPER) = id %s campaign %s, %v; want lower-case ids", v.ID, v.CampaignID, err)
	}
	list, err := h.svc.ListContentVersions(bg, upID)
	if err != nil || len(list) != 1 || list[0].ID != v1.ContentVersionID || list[0].CampaignID != id {
		t.Fatalf("ListContentVersions(UPPER) = %+v, %v; want v1 with lower-case ids", list, err)
	}
	p, err := h.svc.Preview(bg, upID, campaign.PreviewQuery{ContentVersionID: upV1})
	if err != nil || p.CampaignID != id || p.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("Preview(UPPER, UPPER version) = campaign %s version %s, %v; want lower-case ids", p.CampaignID, p.ContentVersionID, err)
	}

	// The same base spelled in upper case is not a conflict.
	e, err := h.svc.EditContent(bg, upID, upV1, rawOf(t, editContent("büyük taban")))
	if err != nil {
		t.Fatalf("EditContent(UPPER id, UPPER expected = latest) error = %v, want nil", err)
	}
	if e.CampaignID != id || e.ID != strings.ToLower(e.ID) {
		t.Fatalf("edited version id/campaign = %s/%s, want lower case", e.ID, e.CampaignID)
	}
}

// REQ-APR-03, REQ-CNT-04 (D47 b, D41)
// Approve racing a generation start (best effort, bounded loop): Approve returns only nil,
// ErrGenerationInProgress or ErrStaleContentVersion; the generation always validates (never a final
// invalid such as internal_error); afterwards the campaign is never left approved on a version that
// is not the latest (the generation's version is the latest, so an approval granted before it was
// persisted must have been revoked with content_changed).
func TestService_ApproveRacingAGenerationStart(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	h.mustValidated(t, scen(llm.ScenarioOK), id)

	const rounds = 25
	for i := range rounds {
		base := h.get(t, id).LatestContentVersion.ID
		var wg sync.WaitGroup
		start := make(chan struct{})
		var startErr, approveErr error
		var gen streamResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			run, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
			if err != nil {
				startErr = err
				return
			}
			gen.err = run.Stream(context.Background(), func(ev composer.Event) error {
				gen.events = append(gen.events, ev)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, approveErr = h.svc.Approve(context.Background(), id, base)
		}()
		close(start)
		wg.Wait()

		if startErr != nil {
			t.Fatalf("round %d: StartContentGeneration error = %v, want nil (only generations hold the lock)", i, startErr)
		}
		if gen.err != nil {
			t.Fatalf("round %d: Stream error = %v, want nil", i, gen.err)
		}
		v, ok := asValidated(gen.terminal(t))
		if !ok {
			t.Fatalf("round %d: generation terminal event = %#v, want validated (never internal_error)", i, gen.terminal(t))
		}
		if approveErr != nil && !errors.Is(approveErr, campaign.ErrGenerationInProgress) && !errors.Is(approveErr, campaign.ErrStaleContentVersion) {
			t.Fatalf("round %d: Approve error = %v, want nil, %v or %v", i, approveErr, campaign.ErrGenerationInProgress, campaign.ErrStaleContentVersion)
		}
		c := h.get(t, id)
		if c.LatestContentVersion == nil || c.LatestContentVersion.ID != v.ContentVersionID {
			t.Fatalf("round %d: latest = %+v, want the generated version %s", i, c.LatestContentVersion, v.ContentVersionID)
		}
		if c.Approval != nil || c.State != campaign.StateContentReady {
			t.Fatalf("round %d: state/approval = %s/%+v, want content_ready with no approval (the generation is newer than %s)", i, c.State, c.Approval, base)
		}
		if approveErr == nil {
			found := false
			for _, r := range h.approvalRows(t, id) {
				if r.VersionID == base {
					found = true
					if r.RevokedAt == nil || r.Reason == nil || *r.Reason != "content_changed" {
						t.Fatalf("round %d: approval of %s = %+v, want revoked with content_changed", i, base, r)
					}
				}
			}
			if !found {
				t.Fatalf("round %d: Approve returned nil but no approval row for %s exists", i, base)
			}
		}
	}
}

// REQ-APR-03, REQ-CNT-04 (D47 b, D40)
// EditContent racing a generation start (best effort, bounded loop): the edit returns only nil,
// ErrGenerationInProgress or ErrContentEditConflict; the generation always validates; version
// numbers stay unique and gap-free and the latest version is the highest validated one.
func TestService_EditRacingAGenerationStart(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	h.mustValidated(t, scen(llm.ScenarioOK), id)

	const rounds = 25
	for i := range rounds {
		base := h.get(t, id).LatestContentVersion.ID
		raw := rawOf(t, editContent("yarış "+strconv.Itoa(i)))
		var wg sync.WaitGroup
		start := make(chan struct{})
		var startErr, editErr error
		var edited campaign.ContentVersion
		var gen streamResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			run, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
			if err != nil {
				startErr = err
				return
			}
			gen.err = run.Stream(context.Background(), func(ev composer.Event) error {
				gen.events = append(gen.events, ev)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			edited, editErr = h.svc.EditContent(context.Background(), id, base, raw)
		}()
		close(start)
		wg.Wait()

		if startErr != nil {
			t.Fatalf("round %d: StartContentGeneration error = %v, want nil", i, startErr)
		}
		if gen.err != nil {
			t.Fatalf("round %d: Stream error = %v, want nil", i, gen.err)
		}
		v, ok := asValidated(gen.terminal(t))
		if !ok {
			t.Fatalf("round %d: generation terminal event = %#v, want validated (never internal_error)", i, gen.terminal(t))
		}
		if editErr != nil && !errors.Is(editErr, campaign.ErrGenerationInProgress) && !errors.Is(editErr, campaign.ErrContentEditConflict) {
			t.Fatalf("round %d: EditContent error = %v, want nil, %v or %v", i, editErr, campaign.ErrGenerationInProgress, campaign.ErrContentEditConflict)
		}
		rows := h.versionRows(t, id)
		for j, r := range rows {
			if r.Number != j+1 {
				t.Fatalf("round %d: version numbers %v are not 1..%d", i, numbers(rows), len(rows))
			}
		}
		wantLatest := v.ContentVersionID
		if editErr == nil && edited.VersionNumber > v.VersionNumber {
			wantLatest = edited.ID
		}
		if c := h.get(t, id); c.LatestContentVersion == nil || c.LatestContentVersion.ID != wantLatest {
			t.Fatalf("round %d: latest = %+v, want %s", i, c.LatestContentVersion, wantLatest)
		}
	}
}

// REQ-CNT-04 (sse-events.md §1.3, go-interfaces campaign.MaxGenerationDuration)
// A client disconnect releases the generation lock by itself: with the injected clock never advanced
// (so no 120 s staleness can play a part) an edit is refused while the stream runs and accepted
// right after the client went away; nothing was stored by the cancelled generation.
func TestService_ClientCancelReleasesTheLockWithoutTime(t *testing.T) {
	t.Parallel()
	bc := &blockingClient{mock: llm.NewMock(llm.MockOptions{})}
	h := newHarness(t, bc)
	id := h.audienceReady(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := h.svc.StartContentGeneration(ctx, id)
	if err != nil {
		t.Fatalf("StartContentGeneration() error = %v", err)
	}
	done := make(chan streamResult, 1)
	go func() {
		var res streamResult
		res.err = run.Stream(ctx, func(ev composer.Event) error {
			res.events = append(res.events, ev)
			return nil
		})
		done <- res
	}()
	if _, err := h.svc.EditContent(context.Background(), id, campaign.ExpectedVersionNone, rawOf(t, editContent("kilitli"))); !errors.Is(err, campaign.ErrGenerationInProgress) {
		t.Fatalf("EditContent during the generation error = %v, want %v", err, campaign.ErrGenerationInProgress)
	}

	cancel()
	var res streamResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not return within 10s after the client went away")
	}
	if res.err != nil && !errors.Is(res.err, context.Canceled) {
		t.Fatalf("Stream error after cancel = %v, want nil or context.Canceled", res.err)
	}
	for _, ev := range res.events {
		if ev.Name() != "delta" {
			t.Fatalf("event %q after the client went away, want no terminal event", ev.Name())
		}
	}
	if !h.clock.Now().Equal(svcRef) {
		t.Fatalf("test setup: the clock moved to %s", h.clock.Now())
	}
	if n := len(h.versionRows(t, id)); n != 0 {
		t.Fatalf("the cancelled generation stored %d versions, want 0", n)
	}
	if _, err := h.svc.EditContent(context.Background(), id, campaign.ExpectedVersionNone, rawOf(t, editContent("serbest"))); err != nil {
		t.Fatalf("EditContent right after the disconnect error = %v, want nil (lock released without any clock advance)", err)
	}
}

// REQ-APR-01, REQ-APR-02, REQ-APR-03 (D47 c, go-interfaces EditContent)
// content.Parse runs after the use-case checks: a structurally invalid body ({"sms":{}}) on a stale
// base is an EditConflictError, on an unknown campaign ErrCampaignNotFound, while a generation runs
// ErrGenerationInProgress, and only on the current base a *validation.Error (schema_violation).
func TestService_EditStructuralErrorsComeAfterTheUseCaseChecks(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	v2 := h.edit(t, id, v1.ContentVersionID, editContent("v2"))
	raw := []byte(`{"sms":{}}`)
	bg := context.Background()

	_, err := h.svc.EditContent(bg, id, v1.ContentVersionID, raw)
	assertCurrent(t, conflictOf(t, err), v2.ID, v2.VersionNumber)
	if _, err := h.svc.EditContent(bg, "00000000-0000-4000-8000-000000000000", campaign.ExpectedVersionNone, raw); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("EditContent(unknown, {\"sms\":{}}) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
	stop := h.stall(t, id)
	_, lockErr := h.svc.EditContent(bg, id, v2.ID, raw)
	stop()
	if !errors.Is(lockErr, campaign.ErrGenerationInProgress) {
		t.Fatalf("EditContent({\"sms\":{}}) during a generation error = %v, want %v", lockErr, campaign.ErrGenerationInProgress)
	}
	_, err = h.svc.EditContent(bg, id, v2.ID, raw)
	var ve *validation.Error
	if !errors.As(err, &ve) || !errors.Is(err, validation.ErrSchemaViolation) || !hasFieldCode(ve.Fields, "sms.body", "schema_violation") {
		t.Fatalf("EditContent(current base, {\"sms\":{}}) error = %v, want a *validation.Error with sms.body schema_violation", err)
	}
	if n := len(h.versionRows(t, id)); n != 2 {
		t.Fatalf("rows = %d, want 2 (nothing stored by refused edits)", n)
	}
}

// REQ-APR-01 (README §5.4 previewContent: 404 campaign_not_found › content_version_not_found, then 409)
// A version id that is not in the campaign is ErrContentVersionNotFound even when the campaign has
// no segment (draft) and no version at all: the 404 checks come before the 409 invalid_state.
func TestService_PreviewUnknownVersionWithoutSegment(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	draft, err := h.svc.Create(context.Background(), h.m.ExampleBrief.Brief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := h.svc.Preview(context.Background(), draft.ID, campaign.PreviewQuery{ContentVersionID: "00000000-0000-4000-8000-0000000000aa"}); !errors.Is(err, campaign.ErrContentVersionNotFound) {
		t.Fatalf("Preview(draft, unknown version) error = %v, want %v", err, campaign.ErrContentVersionNotFound)
	}
	if _, err := h.svc.Preview(context.Background(), draft.ID, campaign.PreviewQuery{}); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("Preview(draft, no version given) error = %v, want %v", err, campaign.ErrInvalidState)
	}
}
