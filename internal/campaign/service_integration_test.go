package campaign_test

// Service integration tests over the real Postgres of compose.test.yml (TEST_DATABASE_URL, scc_app),
// migrated and seeded with the canonical dataset of testdata/seed/manifest.json. They skip under
// -short (testdb.App). Every test creates its own campaigns and never mutates customers, so they
// can run in parallel with each other and with the other integration packages.
//
// The service is built exactly as the contract allows: campaign.NewService(campaign.Deps{...}) with
// postgres.NewCampaignStore, postgres.NewAudience, a clock.Fake and an llm.Client (llm.Mock, or a
// scripted fake for outcomes no mock scenario produces — llm-tools.md §3.5).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

const svcManifestPath = "../../testdata/seed/manifest.json"

// CNT_OK of llm-tools.md §3.3.
const (
	okSMS     = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
	okSubject = "Sepetin seni bekliyor: %30 indirim fırsatı"
	okBody    = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer"
)

var svcRef = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func okContent() content.Content {
	return content.Content{SMS: content.SMS{Body: okSMS}, Email: content.Email{Subject: okSubject, Body: okBody}}
}

func editContent(tag string) content.Content {
	return content.Content{
		SMS:   content.SMS{Body: "Merhaba {{first_name}}, " + tag + " sepetin seni bekliyor."},
		Email: content.Email{Subject: "Sepetin seni bekliyor " + tag, Body: "Merhaba {{first_name}},\n\n" + tag + " indirim seni bekliyor."},
	}
}

type svcManifest struct {
	ExampleBrief struct {
		Brief         string                            `json:"brief"`
		Audience      struct{ Matched, SMS, Email int } `json:"audience"`
		LongestValues content.Longest                   `json:"longest_values"`
	} `json:"example_brief"`
}

func readSvcManifest(t *testing.T) svcManifest {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(svcManifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", svcManifestPath, err)
	}
	var m svcManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// ---- harness ----

type harness struct {
	svc   campaign.Service
	pool  *pgxpool.Pool
	clock *clock.Fake
	m     svcManifest
}

func newHarness(t *testing.T, c llm.Client) *harness {
	t.Helper()
	pool := testdb.App(t)
	fc := clock.NewFake(svcRef)
	ref := svcRef
	svc := campaign.NewService(campaign.Deps{
		Store:                postgres.NewCampaignStore(pool),
		Audience:             postgres.NewAudience(pool),
		LLM:                  c,
		Clock:                fc,
		SegmentReferenceTime: &ref,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &harness{svc: svc, pool: pool, clock: fc, m: readSvcManifest(t)}
}

func newMockHarness(t *testing.T) *harness { return newHarness(t, llm.NewMock(llm.MockOptions{})) }

func scen(s llm.Scenario) context.Context { return llm.WithScenario(context.Background(), s) }

// audienceReady creates a campaign with the example brief and stores the SEG_OK segment.
func (h *harness) audienceReady(t *testing.T) string {
	t.Helper()
	c, err := h.svc.Create(context.Background(), h.m.ExampleBrief.Brief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	res, err := h.svc.GenerateSegment(scen(llm.ScenarioOK), c.ID)
	if err != nil {
		t.Fatalf("GenerateSegment() error = %v", err)
	}
	if res.State != campaign.StateAudienceReady {
		t.Fatalf("state after segment = %s, want audience_ready", res.State)
	}
	return c.ID
}

type streamResult struct {
	events []composer.Event
	err    error
}

func (r streamResult) terminal(t *testing.T) composer.Event {
	t.Helper()
	if len(r.events) == 0 {
		t.Fatalf("stream emitted no events (err %v)", r.err)
	}
	return r.events[len(r.events)-1]
}

func asValidated(ev composer.Event) (composer.ValidatedEvent, bool) {
	switch e := ev.(type) {
	case composer.ValidatedEvent:
		return e, true
	case *composer.ValidatedEvent:
		return *e, true
	}
	return composer.ValidatedEvent{}, false
}

func asInvalid(ev composer.Event) (composer.InvalidEvent, bool) {
	switch e := ev.(type) {
	case composer.InvalidEvent:
		return e, true
	case *composer.InvalidEvent:
		return *e, true
	}
	return composer.InvalidEvent{}, false
}

// generate starts a content generation and consumes the whole stream.
func (h *harness) generate(t *testing.T, ctx context.Context, id string) streamResult {
	t.Helper()
	run, err := h.svc.StartContentGeneration(ctx, id)
	if err != nil {
		t.Fatalf("StartContentGeneration() error = %v", err)
	}
	var res streamResult
	res.err = run.Stream(ctx, func(ev composer.Event) error {
		res.events = append(res.events, ev)
		return nil
	})
	return res
}

func (h *harness) mustValidated(t *testing.T, ctx context.Context, id string) composer.ValidatedEvent {
	t.Helper()
	res := h.generate(t, ctx, id)
	if res.err != nil {
		t.Fatalf("Stream() error = %v", res.err)
	}
	v, ok := asValidated(res.terminal(t))
	if !ok {
		t.Fatalf("terminal event = %#v, want validated", res.terminal(t))
	}
	return v
}

func (h *harness) finalInvalid(t *testing.T, ctx context.Context, id string) composer.InvalidEvent {
	t.Helper()
	res := h.generate(t, ctx, id)
	if res.err != nil {
		t.Fatalf("Stream() error = %v", res.err)
	}
	inv, ok := asInvalid(res.terminal(t))
	if !ok || !inv.Final {
		t.Fatalf("terminal event = %#v, want invalid with final=true", res.terminal(t))
	}
	return inv
}

func rawOf(t *testing.T, c content.Content) []byte {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func (h *harness) edit(t *testing.T, id, expected string, c content.Content) campaign.ContentVersion {
	t.Helper()
	v, err := h.svc.EditContent(context.Background(), id, expected, rawOf(t, c))
	if err != nil {
		t.Fatalf("EditContent(expected %s) error = %v", expected, err)
	}
	return v
}

func (h *harness) get(t *testing.T, id string) campaign.Campaign {
	t.Helper()
	c, err := h.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return c
}

type versionRow struct {
	ID, Status, Source, SMS, Subject, Body string
	Number, SegmentRevision                int
	CreatedAt                              time.Time
}

func (h *harness) versionRows(t *testing.T, id string) []versionRow {
	t.Helper()
	rows, err := h.pool.Query(t.Context(), `SELECT id::text, version_number, segment_revision, status, source, sms_body, email_subject, email_body, created_at
		FROM content_versions WHERE campaign_id = $1 ORDER BY version_number`, id)
	if err != nil {
		t.Fatalf("query content_versions: %v", err)
	}
	defer rows.Close()
	var out []versionRow
	for rows.Next() {
		var r versionRow
		if err := rows.Scan(&r.ID, &r.Number, &r.SegmentRevision, &r.Status, &r.Source, &r.SMS, &r.Subject, &r.Body, &r.CreatedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

type approvalRow struct {
	ID, VersionID string
	ApprovedAt    time.Time
	RevokedAt     *time.Time
	Reason        *string
}

func (h *harness) approvalRows(t *testing.T, id string) []approvalRow {
	t.Helper()
	rows, err := h.pool.Query(t.Context(), `SELECT id::text, content_version_id::text, approved_at, revoked_at, revoke_reason
		FROM approvals WHERE campaign_id = $1 ORDER BY approved_at, id`, id)
	if err != nil {
		t.Fatalf("query approvals: %v", err)
	}
	defer rows.Close()
	var out []approvalRow
	for rows.Next() {
		var r approvalRow
		if err := rows.Scan(&r.ID, &r.VersionID, &r.ApprovedAt, &r.RevokedAt, &r.Reason); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func (h *harness) deliveryCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM deliveries WHERE campaign_id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

func numbers(rows []versionRow) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.Number
	}
	return out
}

func hasFieldCode(fs []validation.FieldError, path, code string) bool {
	for _, f := range fs {
		if f.Path == path && f.Code == code {
			return true
		}
	}
	return false
}

func conflictOf(t *testing.T, err error) *campaign.EditConflictError {
	t.Helper()
	if !errors.Is(err, campaign.ErrContentEditConflict) {
		t.Fatalf("error = %v, want %v", err, campaign.ErrContentEditConflict)
	}
	var ce *campaign.EditConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *campaign.EditConflictError", err)
	}
	return ce
}

func assertCurrent(t *testing.T, ce *campaign.EditConflictError, id string, number int) {
	t.Helper()
	if id == "" {
		if ce.CurrentID != nil || ce.CurrentVersionNumber != nil {
			t.Fatalf("conflict current = %v/%v, want both nil", ce.CurrentID, ce.CurrentVersionNumber)
		}
		return
	}
	if ce.CurrentID == nil || *ce.CurrentID != id || ce.CurrentVersionNumber == nil || *ce.CurrentVersionNumber != number {
		t.Fatalf("conflict current = %v/%v, want %s/%d", ptrS(ce.CurrentID), ptrI(ce.CurrentVersionNumber), id, number)
	}
}

func ptrS(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func ptrI(p *int) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// ---- tests ----

// REQ-CNT-04, REQ-AI-01, REQ-CNT-05
// A successful generation persists the version BEFORE the validated event is emitted: inside the
// emit callback of the terminal event the campaign already reports it as latest_content_version
// and state content_ready. The version is validated, source llm, and its worst case is measured
// with the segment's longest values.
func TestService_ValidatedVersionIsStoredBeforeTheEvent(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)

	run, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
	if err != nil {
		t.Fatalf("StartContentGeneration() error = %v", err)
	}
	var seenInEmit *campaign.Campaign
	var events []composer.Event
	err = run.Stream(scen(llm.ScenarioOK), func(ev composer.Event) error {
		events = append(events, ev)
		if _, ok := asValidated(ev); ok {
			c, gerr := h.svc.Get(context.Background(), id)
			if gerr != nil {
				return gerr
			}
			seenInEmit = &c
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	v, ok := asValidated(events[len(events)-1])
	if !ok {
		t.Fatalf("terminal event = %#v, want validated", events[len(events)-1])
	}
	for _, ev := range events[:len(events)-1] {
		if ev.Name() != "delta" {
			t.Fatalf("event %q before the terminal event, want only deltas for scenario ok", ev.Name())
		}
	}
	if v.Attempt != 1 || v.VersionNumber != 1 {
		t.Fatalf("validated attempt/version_number = %d/%d, want 1/1", v.Attempt, v.VersionNumber)
	}
	if diff := cmp.Diff(okContent(), v.Content); diff != "" {
		t.Fatalf("validated content mismatch (-want +got):\n%s", diff)
	}
	if seenInEmit == nil || seenInEmit.LatestContentVersion == nil || seenInEmit.LatestContentVersion.ID != v.ContentVersionID {
		t.Fatalf("while emitting validated, latest_content_version = %+v, want the new version %s", seenInEmit, v.ContentVersionID)
	}
	if seenInEmit.State != campaign.StateContentReady {
		t.Fatalf("while emitting validated, state = %s, want content_ready", seenInEmit.State)
	}

	c := h.get(t, id)
	lv := c.LatestContentVersion
	if lv.Status != campaign.ContentValidated || lv.Source != campaign.SourceLLM || lv.LLMAttempts == nil || *lv.LLMAttempts != 1 {
		t.Fatalf("latest version status/source/llm_attempts = %s/%s/%v, want validated/llm/1", lv.Status, lv.Source, ptrI(lv.LLMAttempts))
	}
	if len(lv.ValidationErrors) != 0 {
		t.Fatalf("validated version has validation errors %+v", lv.ValidationErrors)
	}
	wantWC, err := content.MeasureWorstCase(okContent(), c.Segment.LongestValues)
	if err != nil {
		t.Fatalf("MeasureWorstCase() error = %v", err)
	}
	if lv.WorstCase == nil || *lv.WorstCase != wantWC || v.WorstCase != wantWC {
		t.Fatalf("worst case (version %v, event %+v), want %+v", lv.WorstCase, v.WorstCase, wantWC)
	}
	if c.Segment.LongestValues != h.m.ExampleBrief.LongestValues {
		t.Fatalf("segment longest_values = %+v, want the manifest's %+v", c.Segment.LongestValues, h.m.ExampleBrief.LongestValues)
	}
	if c.Segment.Audience.Matched != h.m.ExampleBrief.Audience.Matched || c.Segment.Audience.SMS != h.m.ExampleBrief.Audience.SMS || c.Segment.Audience.Email != h.m.ExampleBrief.Audience.Email {
		t.Fatalf("audience after content = %+v, want the manifest's (consent is not influenced by content)", c.Segment.Audience)
	}
}

// REQ-APR-03, REQ-CNT-04 (D38)
// version_number counts ALL stored versions of the campaign (invalid drafts included): numbers are
// never reused, validated versions may skip numbers, the list is newest first, the latest version
// is the highest VALIDATED number, and a non-storable failure stores nothing.
func TestService_VersionNumbersCountInvalidDrafts(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)

	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	draft := h.finalInvalid(t, scen(llm.ScenarioOverLimitSMS), id)
	if draft.ContentVersionID == nil || draft.Attempt != 2 {
		t.Fatalf("over_limit_sms final invalid = %+v, want attempt 2 with a stored draft id", draft)
	}
	if !hasFieldCode(draft.Errors, "sms.body", "sms_too_long") {
		t.Fatalf("final invalid errors = %+v, want sms.body sms_too_long", draft.Errors)
	}
	nothing := h.finalInvalid(t, scen(llm.ScenarioInvalidJSON), id)
	if nothing.ContentVersionID != nil {
		t.Fatalf("invalid_json final invalid stored a version %s, want null", *nothing.ContentVersionID)
	}
	v3 := h.edit(t, id, v1.ContentVersionID, editContent("v3"))
	v4 := h.mustValidated(t, scen(llm.ScenarioOK), id)

	if v1.VersionNumber != 1 || v3.VersionNumber != 3 || v4.VersionNumber != 4 {
		t.Fatalf("version numbers = %d/%d/%d, want 1/3/4 (draft consumed 2)", v1.VersionNumber, v3.VersionNumber, v4.VersionNumber)
	}
	if diff := cmp.Diff([]int{1, 2, 3, 4}, numbers(h.versionRows(t, id))); diff != "" {
		t.Fatalf("stored version numbers mismatch (-want +got):\n%s", diff)
	}

	list, err := h.svc.ListContentVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("ListContentVersions() error = %v", err)
	}
	var got []int
	for _, v := range list {
		got = append(got, v.VersionNumber)
	}
	if diff := cmp.Diff([]int{4, 3, 2, 1}, got); diff != "" {
		t.Fatalf("listed version numbers mismatch (-want +got, newest first):\n%s", diff)
	}

	d, err := h.svc.GetContentVersion(context.Background(), id, *draft.ContentVersionID)
	if err != nil {
		t.Fatalf("GetContentVersion(draft) error = %v", err)
	}
	if d.Status != campaign.ContentInvalid || d.Source != campaign.SourceLLM || d.VersionNumber != 2 || d.LLMAttempts == nil || *d.LLMAttempts != 2 {
		t.Fatalf("draft status/source/number/attempts = %s/%s/%d/%v, want invalid/llm/2/2", d.Status, d.Source, d.VersionNumber, ptrI(d.LLMAttempts))
	}
	if !hasFieldCode(d.ValidationErrors, "sms.body", "sms_too_long") || d.WorstCase == nil {
		t.Fatalf("draft validation_errors/worst_case = %+v/%v, want sms_too_long and a worst case", d.ValidationErrors, d.WorstCase)
	}
	if e := h.get(t, id).LatestContentVersion; e == nil || e.ID != v4.ContentVersionID {
		t.Fatalf("latest_content_version = %+v, want v4", e)
	}
	if v3.Source != campaign.SourceEdit || v3.LLMAttempts != nil || v3.Status != campaign.ContentValidated {
		t.Fatalf("edit version source/llm_attempts/status = %s/%v/%s, want edit/nil/validated", v3.Source, ptrI(v3.LLMAttempts), v3.Status)
	}
}

// REQ-APR-03
// Versions are immutable: later edits, generations, approvals and a segment regeneration add rows
// and never change an existing one.
func TestService_VersionsAreImmutable(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	h.finalInvalid(t, scen(llm.ScenarioUnknownVariable), id)
	before := h.versionRows(t, id)
	firstBefore, err := h.svc.GetContentVersion(context.Background(), id, v1.ContentVersionID)
	if err != nil {
		t.Fatalf("GetContentVersion() error = %v", err)
	}

	v3 := h.edit(t, id, v1.ContentVersionID, editContent("değişti"))
	if _, err := h.svc.Approve(context.Background(), id, v3.ID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	h.edit(t, id, v3.ID, okContent()) // identical text still creates a new version
	if _, err := h.svc.GenerateSegment(scen(llm.ScenarioOK), id); err != nil {
		t.Fatalf("GenerateSegment() error = %v", err)
	}

	after := h.versionRows(t, id)
	if len(after) != len(before)+2 {
		t.Fatalf("rows after = %d, want %d", len(after), len(before)+2)
	}
	if diff := cmp.Diff(before, after[:len(before)]); diff != "" {
		t.Fatalf("existing version rows changed (-before +after):\n%s", diff)
	}
	firstAfter, err := h.svc.GetContentVersion(context.Background(), id, v1.ContentVersionID)
	if err != nil {
		t.Fatalf("GetContentVersion() error = %v", err)
	}
	if diff := cmp.Diff(firstBefore, firstAfter); diff != "" {
		t.Fatalf("GetContentVersion(v1) changed (-before +after):\n%s", diff)
	}
}

// REQ-APR-01, REQ-APR-03 (D40)
// X-Expected-Version semantics in the service: the id of the latest VALIDATED version of the
// current revision, or "none" when there is none. A mismatch is EditConflictError carrying the
// current version and stores nothing; an invalid draft never counts as a version; a regeneration
// that validates makes an open editor stale, a failed one does not; repeating a successful edit is
// a conflict too (no de-duplication).
func TestService_EditExpectedVersion(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)

	// No version yet: "none" is the only accepted base.
	_, err := h.svc.EditContent(context.Background(), id, "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11", rawOf(t, editContent("x")))
	assertCurrent(t, conflictOf(t, err), "", 0)
	v1 := h.edit(t, id, campaign.ExpectedVersionNone, editContent("v1"))
	if v1.VersionNumber != 1 || v1.Status != campaign.ContentValidated || v1.Source != campaign.SourceEdit {
		t.Fatalf("first edit = number %d status %s source %s, want 1 validated edit", v1.VersionNumber, v1.Status, v1.Source)
	}
	if st := h.get(t, id).State; st != campaign.StateContentReady {
		t.Fatalf("state after first edit = %s, want content_ready", st)
	}

	// Repeating the same request (base "none") is a conflict: the server never de-duplicates.
	_, err = h.svc.EditContent(context.Background(), id, campaign.ExpectedVersionNone, rawOf(t, editContent("v1")))
	assertCurrent(t, conflictOf(t, err), v1.ID, 1)

	v2 := h.edit(t, id, v1.ID, editContent("v2"))
	rowsBefore := len(h.versionRows(t, id))
	_, err = h.svc.EditContent(context.Background(), id, v1.ID, rawOf(t, editContent("stale")))
	assertCurrent(t, conflictOf(t, err), v2.ID, 2)
	if n := len(h.versionRows(t, id)); n != rowsBefore {
		t.Fatalf("a conflicting edit stored a row: %d rows, want %d", n, rowsBefore)
	}

	// A validated regeneration makes the editor based on v2 stale.
	v3 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	_, err = h.svc.EditContent(context.Background(), id, v2.ID, rawOf(t, editContent("after regen")))
	assertCurrent(t, conflictOf(t, err), v3.ContentVersionID, 3)

	// A failed regeneration stores an invalid draft (v4) that is NOT a base.
	draft := h.finalInvalid(t, scen(llm.ScenarioOverLimitSubject), id)
	if draft.ContentVersionID == nil {
		t.Fatalf("over_limit_subject stored no draft")
	}
	_, err = h.svc.EditContent(context.Background(), id, *draft.ContentVersionID, rawOf(t, editContent("from draft id")))
	assertCurrent(t, conflictOf(t, err), v3.ContentVersionID, 3)
	v5 := h.edit(t, id, v3.ContentVersionID, editContent("v5"))
	if v5.VersionNumber != 5 {
		t.Fatalf("edit after the draft = number %d, want 5", v5.VersionNumber)
	}
}

// REQ-APR-03
// Two (here: eight) edits racing from the same base: exactly one succeeds, every other gets
// EditConflictError; no other error (never a unique violation / 500) and no duplicate number.
func TestService_ConcurrentEditsFromSameBase(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	base := h.mustValidated(t, scen(llm.ScenarioOK), id)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	raws := make([][]byte, n)
	for i := range n {
		raws[i] = rawOf(t, editContent("yarış "+strconv.Itoa(i)))
	}
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = h.svc.EditContent(context.Background(), id, base.ContentVersionID, raws[i])
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, campaign.ErrContentEditConflict):
		default:
			t.Errorf("edit %d error = %v, want nil or ErrContentEditConflict", i, err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d edits succeeded, want exactly 1", ok)
	}
	if diff := cmp.Diff([]int{1, 2}, numbers(h.versionRows(t, id))); diff != "" {
		t.Fatalf("version numbers mismatch (-want +got):\n%s", diff)
	}
}

// REQ-APR-03, REQ-CNT-04
// A generation finishing while edits race never produces a duplicate version number or an error
// other than the contracted ones.
func TestService_GenerationAndEditNeverCollide(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	base := h.mustValidated(t, scen(llm.ScenarioOK), id)

	var wg sync.WaitGroup
	var genErr, editErr error
	raceRaw := rawOf(t, editContent("yarış"))
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		run, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
		if err != nil {
			if !errors.Is(err, campaign.ErrGenerationInProgress) {
				genErr = err
			}
			return
		}
		genErr = run.Stream(context.Background(), func(composer.Event) error { return nil })
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := h.svc.EditContent(context.Background(), id, base.ContentVersionID, raceRaw)
		if err != nil && !errors.Is(err, campaign.ErrGenerationInProgress) && !errors.Is(err, campaign.ErrContentEditConflict) {
			editErr = err
		}
	}()
	close(start)
	wg.Wait()
	if genErr != nil || editErr != nil {
		t.Fatalf("generation error = %v, edit error = %v; want only contracted outcomes", genErr, editErr)
	}
	rows := h.versionRows(t, id)
	seen := map[int]bool{}
	for _, r := range rows {
		if seen[r.Number] {
			t.Fatalf("duplicate version_number %d in %v", r.Number, numbers(rows))
		}
		seen[r.Number] = true
	}
}

// REQ-APR-01, REQ-APR-03 (D39)
// content_ready -> approved; approving the same version again is idempotent (same approval,
// approved_at unchanged, no new row); an edit after approval drops the campaign to content_ready,
// revokes the approval (content_changed) and a send is refused with no delivery row.
func TestService_ApproveIdempotentAndEditRevokes(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)

	if _, err := h.svc.Send(context.Background(), id, v1.ContentVersionID); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("Send() before approval error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if n := h.deliveryCount(t, id); n != 0 {
		t.Fatalf("deliveries after a refused send = %d, want 0", n)
	}

	c1, err := h.svc.Approve(context.Background(), id, v1.ContentVersionID)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if c1.State != campaign.StateApproved || c1.Approval == nil || c1.Approval.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("after approve state/approval = %s/%+v, want approved bound to v1", c1.State, c1.Approval)
	}

	h.clock.Advance(time.Minute)
	c2, err := h.svc.Approve(context.Background(), id, v1.ContentVersionID)
	if err != nil {
		t.Fatalf("second Approve() error = %v", err)
	}
	if c2.Approval == nil || c2.Approval.ID != c1.Approval.ID || !c2.Approval.ApprovedAt.Equal(c1.Approval.ApprovedAt) {
		t.Fatalf("second approval = %+v, want the unchanged %+v", c2.Approval, c1.Approval)
	}
	if rows := h.approvalRows(t, id); len(rows) != 1 {
		t.Fatalf("approvals rows = %d, want 1 (idempotent re-approval)", len(rows))
	}

	v2 := h.edit(t, id, v1.ContentVersionID, editContent("onaydan sonra"))
	c3 := h.get(t, id)
	if c3.State != campaign.StateContentReady || c3.Approval != nil {
		t.Fatalf("after edit state/approval = %s/%+v, want content_ready/nil", c3.State, c3.Approval)
	}
	rows := h.approvalRows(t, id)
	if len(rows) != 1 || rows[0].RevokedAt == nil || rows[0].Reason == nil || *rows[0].Reason != "content_changed" {
		t.Fatalf("approval rows after edit = %+v, want the one row revoked with content_changed", rows)
	}
	if _, err := h.svc.Send(context.Background(), id, v2.ID); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("Send() after the approval was revoked error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.Approve(context.Background(), id, v1.ContentVersionID); !errors.Is(err, campaign.ErrStaleContentVersion) {
		t.Fatalf("Approve(old version) error = %v, want %v", err, campaign.ErrStaleContentVersion)
	}
	c4, err := h.svc.Approve(context.Background(), id, v2.ID)
	if err != nil || c4.State != campaign.StateApproved {
		t.Fatalf("re-approval of the new version = %s, %v; want approved", c4.State, err)
	}
	if rows := h.approvalRows(t, id); len(rows) != 2 {
		t.Fatalf("approvals rows after re-approval = %d, want 2", len(rows))
	}
	// Send pre-checks only (the successful enqueue is P5): a version other than the approved one is
	// stale, an unknown version / campaign is not found.
	if _, err := h.svc.Send(context.Background(), id, v1.ContentVersionID); !errors.Is(err, campaign.ErrStaleContentVersion) {
		t.Fatalf("Send(not the approved version) error = %v, want %v", err, campaign.ErrStaleContentVersion)
	}
	if _, err := h.svc.Send(context.Background(), id, "00000000-0000-4000-8000-000000000001"); !errors.Is(err, campaign.ErrContentVersionNotFound) {
		t.Fatalf("Send(unknown version) error = %v, want %v", err, campaign.ErrContentVersionNotFound)
	}
	if _, err := h.svc.Send(context.Background(), "00000000-0000-4000-8000-000000000000", v2.ID); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("Send(unknown campaign) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
	if n := h.deliveryCount(t, id); n != 0 {
		t.Fatalf("deliveries before any accepted send = %d, want 0", n)
	}
}

// REQ-APR-03, REQ-CNT-04
// approveContent check order (openapi): unknown campaign, version not in the campaign, status not
// validated, state, then stale.
func TestService_ApproveChecks(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	other := h.audienceReady(t)
	otherV := h.mustValidated(t, scen(llm.ScenarioOK), other)

	draft := h.finalInvalid(t, scen(llm.ScenarioOverLimitSMS), id)
	if draft.ContentVersionID == nil {
		t.Fatal("no stored draft")
	}
	if _, err := h.svc.Approve(context.Background(), id, *draft.ContentVersionID); !errors.Is(err, campaign.ErrContentNotValidated) {
		t.Fatalf("Approve(invalid draft) error = %v, want %v", err, campaign.ErrContentNotValidated)
	}
	if _, err := h.svc.Approve(context.Background(), id, otherV.ContentVersionID); !errors.Is(err, campaign.ErrContentVersionNotFound) {
		t.Fatalf("Approve(other campaign's version) error = %v, want %v", err, campaign.ErrContentVersionNotFound)
	}
	if _, err := h.svc.Approve(context.Background(), id, "00000000-0000-4000-8000-000000000001"); !errors.Is(err, campaign.ErrContentVersionNotFound) {
		t.Fatalf("Approve(unknown version) error = %v, want %v", err, campaign.ErrContentVersionNotFound)
	}
	if _, err := h.svc.Approve(context.Background(), "00000000-0000-4000-8000-000000000000", otherV.ContentVersionID); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("Approve(unknown campaign) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
	if st := h.get(t, id).State; st != campaign.StateAudienceReady {
		t.Fatalf("state after refused approvals = %s, want audience_ready", st)
	}

	v := h.mustValidated(t, scen(llm.ScenarioOK), id)
	if _, err := h.svc.GenerateSegment(scen(llm.ScenarioOK), id); err != nil {
		t.Fatalf("GenerateSegment() error = %v", err)
	}
	// The version is validated but the campaign is audience_ready again: invalid_state comes first.
	if _, err := h.svc.Approve(context.Background(), id, v.ContentVersionID); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("Approve(after segment regeneration) error = %v, want %v", err, campaign.ErrInvalidState)
	}
}

// REQ-APR-03
// A segment regenerated after content and approval: state audience_ready, latest_content_version
// and approval null, the approval revoked with segment_changed, old versions still listed, and the
// next version number continues (not reset); an editor then bases on "none".
func TestService_SegmentRegenerationInvalidatesContent(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	if _, err := h.svc.Approve(context.Background(), id, v1.ContentVersionID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	res, err := h.svc.GenerateSegment(scen(llm.ScenarioOK), id)
	if err != nil {
		t.Fatalf("GenerateSegment() error = %v", err)
	}
	if res.Segment.Revision != 2 {
		t.Fatalf("segment revision = %d, want 2", res.Segment.Revision)
	}
	c := h.get(t, id)
	if c.State != campaign.StateAudienceReady || c.LatestContentVersion != nil || c.Approval != nil {
		t.Fatalf("after regeneration state/latest/approval = %s/%v/%v, want audience_ready/nil/nil", c.State, c.LatestContentVersion, c.Approval)
	}
	rows := h.approvalRows(t, id)
	if len(rows) != 1 || rows[0].Reason == nil || *rows[0].Reason != "segment_changed" {
		t.Fatalf("approval rows = %+v, want one revoked with segment_changed", rows)
	}
	list, err := h.svc.ListContentVersions(context.Background(), id)
	if err != nil || len(list) != 1 || list[0].ID != v1.ContentVersionID || list[0].SegmentRevision != 1 {
		t.Fatalf("ListContentVersions() = %+v, %v; want the old v1 of revision 1", list, err)
	}

	_, err = h.svc.EditContent(context.Background(), id, v1.ContentVersionID, rawOf(t, editContent("eski taban")))
	assertCurrent(t, conflictOf(t, err), "", 0)
	v2 := h.edit(t, id, campaign.ExpectedVersionNone, editContent("yeni segment"))
	if v2.VersionNumber != 2 || v2.SegmentRevision != 2 {
		t.Fatalf("edit after regeneration = number %d revision %d, want 2/2", v2.VersionNumber, v2.SegmentRevision)
	}
}

// REQ-APR-03, REQ-CNT-04 (sse-events.md §1.2 #6)
// A failed regeneration changes nothing on an approved campaign; a successful one revokes the
// approval (content_changed) and returns to content_ready.
func TestService_RegenerationAndApproval(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	approved, err := h.svc.Approve(context.Background(), id, v1.ContentVersionID)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	for _, s := range []llm.Scenario{llm.ScenarioOverLimitSMS, llm.ScenarioSchemaViolation} {
		h.finalInvalid(t, scen(s), id)
		c := h.get(t, id)
		if c.State != campaign.StateApproved || c.Approval == nil || c.Approval.ID != approved.Approval.ID {
			t.Fatalf("after failed regeneration (%s) state/approval = %s/%+v, want approved/unchanged", s, c.State, c.Approval)
		}
		if c.LatestContentVersion == nil || c.LatestContentVersion.ID != v1.ContentVersionID {
			t.Fatalf("after failed regeneration (%s) latest = %+v, want v1", s, c.LatestContentVersion)
		}
	}

	v := h.mustValidated(t, scen(llm.ScenarioThenOKAfterRepair), id)
	if v.Attempt != 2 {
		t.Fatalf("then_ok_after_repair validated attempt = %d, want 2", v.Attempt)
	}
	c := h.get(t, id)
	if c.State != campaign.StateContentReady || c.Approval != nil || c.LatestContentVersion.ID != v.ContentVersionID {
		t.Fatalf("after successful regeneration state/approval/latest = %s/%+v/%v, want content_ready/nil/new", c.State, c.Approval, c.LatestContentVersion)
	}
	rows := h.approvalRows(t, id)
	if len(rows) != 1 || rows[0].Reason == nil || *rows[0].Reason != "content_changed" {
		t.Fatalf("approval rows = %+v, want one revoked with content_changed", rows)
	}
}

// REQ-APR-02, REQ-CNT-01, REQ-CNT-03, REQ-CNT-05
// An edit goes through the same validator as LLM output, against the CURRENT segment's longest
// values: invalid content is a *validation.Error and nothing is stored; the use-case order is
// not found, generation lock, invalid state, edit conflict, then validation.
func TestService_EditValidationAndOrder(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	l := h.get(t, id).Segment.LongestValues
	base := okContent()

	fit := strings.Repeat("a", 160-l.FirstName)
	tests := []struct {
		name string
		c    content.Content
		raw  string
		path string
		code string
	}{
		{name: "161-char SMS", c: content.Content{SMS: content.SMS{Body: strings.Repeat("a", 161)}, Email: base.Email}, path: "sms.body", code: "sms_too_long"},
		{name: "first name placeholder over the limit after substitution", c: content.Content{SMS: content.SMS{Body: "{{first_name}}" + fit + "a"}, Email: base.Email}, path: "sms.body", code: "sms_too_long"},
		{name: "81-char subject", c: content.Content{SMS: base.SMS, Email: content.Email{Subject: strings.Repeat("b", 81), Body: base.Email.Body}}, path: "email.subject", code: "subject_too_long"},
		{name: "unknown variable", c: content.Content{SMS: content.SMS{Body: "şifren {{password}}"}, Email: base.Email}, path: "sms.body", code: "unknown_variable"},
		{name: "malformed variable", c: content.Content{SMS: base.SMS, Email: content.Email{Subject: base.Email.Subject, Body: "Merhaba {{first_name"}}, path: "email.body", code: "malformed_variable"},
		{name: "empty subject", c: content.Content{SMS: base.SMS, Email: content.Email{Subject: " ", Body: base.Email.Body}}, path: "email.subject", code: "required"},
		{name: "missing key", raw: `{"sms":{"body":"x"},"email":{"subject":"y"}}`, path: "email.body", code: "schema_violation"},
		{name: "extra key", raw: `{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"html":"<b>x</b>"}`, path: "html", code: "schema_violation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			if tc.raw == "" {
				raw = rawOf(t, tc.c)
			}
			_, err := h.svc.EditContent(context.Background(), id, v1.ContentVersionID, raw)
			var ve *validation.Error
			if !errors.As(err, &ve) {
				t.Fatalf("EditContent() error = %v, want *validation.Error", err)
			}
			if !hasFieldCode(ve.Fields, tc.path, tc.code) {
				t.Fatalf("field errors = %+v, want %s %s", ve.Fields, tc.path, tc.code)
			}
		})
	}
	if diff := cmp.Diff([]int{1}, numbers(h.versionRows(t, id))); diff != "" {
		t.Fatalf("rejected edits stored rows (-want +got):\n%s", diff)
	}
	if st := h.get(t, id).State; st != campaign.StateContentReady {
		t.Fatalf("state after rejected edits = %s, want content_ready", st)
	}

	// Exactly at the limit after substitution is accepted.
	ok := h.edit(t, id, v1.ContentVersionID, content.Content{SMS: content.SMS{Body: "{{first_name}}" + fit}, Email: base.Email})
	if ok.WorstCase == nil || ok.WorstCase.SMSRunes != 160 {
		t.Fatalf("worst case of the boundary edit = %v, want sms_runes 160", ok.WorstCase)
	}

	// Order: a stale base with invalid content is a conflict, not a validation error.
	_, err := h.svc.EditContent(context.Background(), id, v1.ContentVersionID, rawOf(t, content.Content{SMS: content.SMS{Body: strings.Repeat("a", 161)}, Email: base.Email}))
	assertCurrent(t, conflictOf(t, err), ok.ID, ok.VersionNumber)

	// Draft (no segment) is invalid_state, before the conflict check; unknown campaign is not found.
	draft, err := h.svc.Create(context.Background(), h.m.ExampleBrief.Brief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := h.svc.EditContent(context.Background(), draft.ID, v1.ContentVersionID, rawOf(t, base)); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("EditContent(draft) error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.EditContent(context.Background(), "00000000-0000-4000-8000-000000000000", campaign.ExpectedVersionNone, rawOf(t, base)); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("EditContent(unknown) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
}

// REQ-CNT-04, REQ-APR-03 (D41)
// While a generation streams (stalled_stream), segment/content generation, edit, approve and send
// on the same campaign are ErrGenerationInProgress and reads work; when the client goes away no
// version is stored, the state is unchanged and the lock is released.
func TestService_GenerationLock(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v1 := h.mustValidated(t, scen(llm.ScenarioOK), id)
	rowsBefore := h.versionRows(t, id)

	ctx, cancel := context.WithCancel(scen(llm.ScenarioStalledStream))
	defer cancel()
	run, err := h.svc.StartContentGeneration(ctx, id)
	if err != nil {
		t.Fatalf("StartContentGeneration() error = %v", err)
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
		t.Fatal("no delta from stalled_stream within 10s")
	}

	bg := context.Background()
	locked := map[string]error{}
	_, locked["StartContentGeneration"] = h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
	_, locked["GenerateSegment"] = h.svc.GenerateSegment(scen(llm.ScenarioOK), id)
	_, locked["EditContent"] = h.svc.EditContent(bg, id, v1.ContentVersionID, rawOf(t, editContent("kilit")))
	_, locked["Approve"] = h.svc.Approve(bg, id, v1.ContentVersionID)
	_, locked["Send"] = h.svc.Send(bg, id, v1.ContentVersionID)
	for op, err := range locked {
		if !errors.Is(err, campaign.ErrGenerationInProgress) {
			t.Errorf("%s during a generation error = %v, want %v", op, err, campaign.ErrGenerationInProgress)
		}
	}
	if _, err := h.svc.Get(bg, id); err != nil {
		t.Errorf("Get during a generation error = %v, want nil", err)
	}
	if _, err := h.svc.ListContentVersions(bg, id); err != nil {
		t.Errorf("ListContentVersions during a generation error = %v, want nil", err)
	}
	if _, err := h.svc.Preview(bg, id, campaign.PreviewQuery{}); err != nil {
		t.Errorf("Preview during a generation error = %v, want nil", err)
	}

	cancel()
	var res streamResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not return within 10s after the client went away")
	}
	for _, ev := range res.events {
		if ev.Name() != "delta" {
			t.Fatalf("event %q after a client disconnect, want no terminal event", ev.Name())
		}
	}
	if diff := cmp.Diff(rowsBefore, h.versionRows(t, id)); diff != "" {
		t.Fatalf("a cancelled generation stored a version (-before +after):\n%s", diff)
	}
	c := h.get(t, id)
	if c.State != campaign.StateContentReady || c.LatestContentVersion == nil || c.LatestContentVersion.ID != v1.ContentVersionID {
		t.Fatalf("after a cancelled generation state/latest = %s/%v, want content_ready/v1", c.State, c.LatestContentVersion)
	}
	if _, err := h.svc.Approve(bg, id, v1.ContentVersionID); err != nil {
		t.Fatalf("Approve after the lock was released error = %v, want nil", err)
	}
}

// blockingClient: segments from llm.Mock; content streams that open, optionally yield chunks, then
// fail with endErr or (endErr nil) block until ctx is done.
type blockingClient struct {
	mock   *llm.Mock
	chunks []llm.ContentChunk
	endErr error
	raw    func(attempt int) (string, bool) // scripted raw outputs per attempt; ok=false => use chunks/endErr
	mu     sync.Mutex
	calls  int
}

func (b *blockingClient) GenerateSegment(ctx context.Context, req llm.SegmentRequest) (json.RawMessage, error) {
	return b.mock.GenerateSegment(ctx, req)
}

func (b *blockingClient) StreamContent(ctx context.Context, req llm.ContentRequest) (llm.ContentStream, error) {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()
	if b.raw != nil {
		if raw, ok := b.raw(n); ok {
			return &blockingStream{ctx: ctx, raw: raw, done: true}, nil
		}
	}
	return &blockingStream{ctx: ctx, chunks: b.chunks, endErr: b.endErr}, nil
}

func (b *blockingClient) streamCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type blockingStream struct {
	ctx    context.Context
	chunks []llm.ContentChunk
	i      int
	endErr error
	raw    string
	done   bool // raw mode: io.EOF after zero chunks
}

func (s *blockingStream) Recv() (llm.ContentChunk, error) {
	if s.done {
		return llm.ContentChunk{}, io.EOF
	}
	if s.i < len(s.chunks) {
		c := s.chunks[s.i]
		s.i++
		return c, nil
	}
	if s.endErr != nil {
		return llm.ContentChunk{}, s.endErr
	}
	<-s.ctx.Done()
	return llm.ContentChunk{}, s.ctx.Err()
}

func (s *blockingStream) Raw() json.RawMessage { return json.RawMessage(s.raw) }
func (s *blockingStream) Close() error         { return nil }

// REQ-CNT-04 (llm-tools.md §3.5)
// A generation that does not finish within campaign.MaxGenerationDuration (fake clock) ends with
// the final invalid event llm_unavailable, stores nothing, changes nothing and releases the lock.
func TestService_GenerationTimeout(t *testing.T) {
	t.Parallel()
	bc := &blockingClient{mock: llm.NewMock(llm.MockOptions{})}
	h := newHarness(t, bc)
	id := h.audienceReady(t)

	run, err := h.svc.StartContentGeneration(context.Background(), id)
	if err != nil {
		t.Fatalf("StartContentGeneration() error = %v", err)
	}
	done := make(chan streamResult, 1)
	go func() {
		var res streamResult
		res.err = run.Stream(context.Background(), func(ev composer.Event) error {
			res.events = append(res.events, ev)
			return nil
		})
		done <- res
	}()
	wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wcancel()
	if err := h.clock.BlockUntilWaiters(wctx, 1); err != nil {
		t.Fatalf("the service registered no timer on the injected clock: %v", err)
	}
	h.clock.Advance(campaign.MaxGenerationDuration)

	var res streamResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not end within 10s after the clock passed MaxGenerationDuration")
	}
	inv, ok := asInvalid(res.terminal(t))
	if !ok || !inv.Final || inv.ContentVersionID != nil {
		t.Fatalf("terminal = %#v, want final invalid with null version id", res.terminal(t))
	}
	if !hasFieldCode(inv.Errors, "", "llm_unavailable") {
		t.Fatalf("terminal errors = %+v, want {\"\" llm_unavailable}", inv.Errors)
	}
	if n := len(h.versionRows(t, id)); n != 0 {
		t.Fatalf("stored versions = %d, want 0", n)
	}
	if st := h.get(t, id).State; st != campaign.StateAudienceReady {
		t.Fatalf("state = %s, want audience_ready", st)
	}
	if _, err := h.svc.EditContent(context.Background(), id, campaign.ExpectedVersionNone, rawOf(t, editContent("kilit bitti"))); err != nil {
		t.Fatalf("EditContent after the timeout error = %v, want nil (lock released)", err)
	}
}

// REQ-CNT-04, REQ-AI-01 (llm-tools.md §3.5)
// LLM access lost after HTTP 200: one chunk, then ErrUnavailable => final invalid llm_unavailable
// without a repair attempt; nothing stored; state unchanged.
func TestService_UnavailableAfterStreamOpened(t *testing.T) {
	t.Parallel()
	bc := &blockingClient{mock: llm.NewMock(llm.MockOptions{}), chunks: []llm.ContentChunk{{Path: "sms.body", Text: "Merhaba {{first_"}}, endErr: llm.ErrUnavailable}
	h := newHarness(t, bc)
	id := h.audienceReady(t)
	inv := h.finalInvalid(t, context.Background(), id)
	if inv.ContentVersionID != nil || !hasFieldCode(inv.Errors, "", "llm_unavailable") {
		t.Fatalf("terminal = %+v, want llm_unavailable with null version id", inv)
	}
	if bc.streamCalls() != 1 {
		t.Fatalf("StreamContent calls = %d, want 1 (no repair)", bc.streamCalls())
	}
	if n := len(h.versionRows(t, id)); n != 0 {
		t.Fatalf("stored versions = %d, want 0", n)
	}
	if st := h.get(t, id).State; st != campaign.StateAudienceReady {
		t.Fatalf("state = %s, want audience_ready", st)
	}
}

// REQ-CNT-01, REQ-CNT-03, REQ-CNT-04 (llm-tools.md §3.5, sse-events.md §1.1)
// LLM drafts that are structurally valid but break content rules (empty value => required,
// malformed variable, email body over 5000) are stored with status invalid and their id is in the
// final event; a missing key is a schema violation and is not stored. None becomes latest.
func TestService_StorableInvalidDraftsFromLLM(t *testing.T) {
	t.Parallel()
	ok := okContent()
	tests := []struct {
		name    string
		c       *content.Content
		raw     string
		stored  bool
		path    string
		code    string
		noWorst bool
	}{
		{name: "empty sms body", c: &content.Content{SMS: content.SMS{Body: ""}, Email: ok.Email}, stored: true, path: "sms.body", code: "required"},
		{name: "malformed variable", c: &content.Content{SMS: content.SMS{Body: "Merhaba {{first_name"}, Email: ok.Email}, stored: true, path: "sms.body", code: "malformed_variable", noWorst: true},
		{name: "email body over 5000", c: &content.Content{SMS: ok.SMS, Email: content.Email{Subject: ok.Email.Subject, Body: strings.Repeat("ü", 5001)}}, stored: true, path: "email.body", code: "email_body_too_long"},
		{name: "missing key", raw: `{"sms":{"body":"Merhaba"},"email":{"subject":"Konu"}}`, path: "email.body", code: "schema_violation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := tc.raw
			if tc.c != nil {
				raw = string(rawOf(t, *tc.c))
			}
			bc := &blockingClient{mock: llm.NewMock(llm.MockOptions{}), raw: func(int) (string, bool) { return raw, true }}
			h := newHarness(t, bc)
			id := h.audienceReady(t)
			inv := h.finalInvalid(t, context.Background(), id)
			if !hasFieldCode(inv.Errors, tc.path, tc.code) {
				t.Fatalf("final errors = %+v, want %s %s", inv.Errors, tc.path, tc.code)
			}
			if inv.Attempt != 2 {
				t.Fatalf("final attempt = %d, want 2", inv.Attempt)
			}
			if !tc.stored {
				if inv.ContentVersionID != nil {
					t.Fatalf("content_version_id = %s, want null", *inv.ContentVersionID)
				}
				if n := len(h.versionRows(t, id)); n != 0 {
					t.Fatalf("stored rows = %d, want 0", n)
				}
				return
			}
			if inv.ContentVersionID == nil {
				t.Fatal("content_version_id = null, want the stored draft")
			}
			v, err := h.svc.GetContentVersion(context.Background(), id, *inv.ContentVersionID)
			if err != nil {
				t.Fatalf("GetContentVersion() error = %v", err)
			}
			if v.Status != campaign.ContentInvalid || v.Source != campaign.SourceLLM || !hasFieldCode(v.ValidationErrors, tc.path, tc.code) {
				t.Fatalf("draft = %+v, want status invalid, source llm, error %s %s", v, tc.path, tc.code)
			}
			if tc.noWorst && v.WorstCase != nil {
				t.Fatalf("draft worst_case = %+v, want null (template syntax invalid)", *v.WorstCase)
			}
			c := h.get(t, id)
			if c.State != campaign.StateAudienceReady || c.LatestContentVersion != nil {
				t.Fatalf("state/latest = %s/%v, want audience_ready/nil", c.State, c.LatestContentVersion)
			}
		})
	}
}

// REQ-CNT-04
// StartContentGeneration refuses a draft campaign (no segment) and an unknown campaign before
// opening a stream; an unreachable LLM is llm.ErrUnavailable and changes nothing.
func TestService_StartContentGenerationChecks(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	draft, err := h.svc.Create(context.Background(), h.m.ExampleBrief.Brief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), draft.ID); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("StartContentGeneration(draft) error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.StartContentGeneration(scen(llm.ScenarioOK), "00000000-0000-4000-8000-000000000000"); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("StartContentGeneration(unknown) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
	id := h.audienceReady(t)
	if _, err := h.svc.StartContentGeneration(scen(llm.ScenarioLLMUnavailable), id); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("StartContentGeneration(llm_unavailable) error = %v, want %v", err, llm.ErrUnavailable)
	}
	if st := h.get(t, id).State; st != campaign.StateAudienceReady {
		t.Fatalf("state = %s, want audience_ready", st)
	}
	// The lock of the refused start is not held: a new generation works.
	h.mustValidated(t, scen(llm.ScenarioOK), id)
}
