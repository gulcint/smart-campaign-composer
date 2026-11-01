package campaign_test

// A/B variants (P9, REQ-ABT-01, REQ-ABT-03) over the real Postgres of compose.test.yml (TEST_DATABASE_URL,
// scc_app), with the harness of service_integration_test.go; skips under -short (testdb.App). Every test
// creates its own campaigns and never mutates customers. Inserts that must be refused run in a
// transaction that is always rolled back. TestScheduler_StartsAnABPlanWithTheSameSplit follows the
// isolation rules of schedule_integration_test.go (not parallel, schRef clock, cleanup cancels the plan).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// abVersionContent is CNT_OK (variant A) plus a valid variant B and both angles; withEN adds an English
// version of A (en) and of B (b.en) (REQ-ABT-01 C1 shape).
func abVersionContent(withEN bool) content.Content {
	c := okContent()
	c.B = &content.Variant{
		SMS:   content.SMS{Body: "Son gün {{first_name}}! %30 indirim bugün bitiyor."},
		Email: content.Email{Subject: "Son gün: %30 indirim", Body: "Merhaba {{first_name}},\n\nİndirim bugün bitiyor."},
	}
	c.Angles = &content.Angles{A: "İndirim oranı öne çıkar", B: "Aciliyet: indirim bugün bitiyor"}
	if withEN {
		c.EN = &content.English{SMS: content.SMS{Body: "Hi {{first_name}}, your cart is waiting."}, Email: content.Email{Subject: "Your cart is waiting", Body: "Hi {{first_name}},\n\nYour cart is waiting."}}
		c.B.EN = &content.English{SMS: content.SMS{Body: "Last day {{first_name}}! 30% off ends today."}, Email: content.Email{Subject: "Last day: 30% off", Body: "Hi {{first_name}},\n\nThe discount ends today."}}
	}
	return c
}

// bColumns are the 0010_content_variants columns of one content_versions row.
type bColumns struct {
	SMS, Subject, Body, AngleA, AngleB *string
	ENSMS, ENSubject, ENBody           *string
	WSMS, WSubject, WENSMS, WENSubject *int
}

func (h *harness) bColumns(t *testing.T, versionID string) bColumns {
	t.Helper()
	var c bColumns
	err := h.pool.QueryRow(t.Context(), `SELECT b_sms_body, b_email_subject, b_email_body, angle_a, angle_b,
			b_en_sms_body, b_en_email_subject, b_en_email_body,
			worst_case_b_sms_runes, worst_case_b_subject_runes, worst_case_b_en_sms_runes, worst_case_b_en_subject_runes
		FROM content_versions WHERE id = $1`, versionID).Scan(&c.SMS, &c.Subject, &c.Body, &c.AngleA, &c.AngleB,
		&c.ENSMS, &c.ENSubject, &c.ENBody, &c.WSMS, &c.WSubject, &c.WENSMS, &c.WENSubject)
	if err != nil {
		t.Fatalf("read b columns of %s: %v", versionID, err)
	}
	return c
}

func sp(s string) *string { return &s }
func ipt(n int) *int      { return &n }

// REQ-ABT-01 C7, C8, C11
// An edit with b and angles stores variant B in b_sms_body / b_email_subject / b_email_body, the angles
// in angle_a / angle_b and B's measurement in worst_case_b_* (= MeasureWorstCase with the segment's
// longest values); with b.en also b_en_* and worst_case_b_en_*. The next edit WITHOUT b stores a version
// without B: the previous b is not carried over (every b column NULL, Content.B / Angles / WorstCase.B nil).
func TestService_VariantColumns(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	l := h.get(t, id).Segment.LongestValues

	for _, withEN := range []bool{false, true} {
		c := abVersionContent(withEN)
		wantWC, err := content.MeasureWorstCase(c, l)
		if err != nil || wantWC.B == nil {
			t.Fatalf("setup: MeasureWorstCase() = %+v, %v; want a B measurement", wantWC, err)
		}
		expected := campaign.ExpectedVersionNone
		if latest := h.get(t, id).LatestContentVersion; latest != nil {
			expected = latest.ID
		}
		v := h.edit(t, id, expected, c)
		if v.Content.B == nil || v.Content.Angles == nil || *v.Content.Angles != *c.Angles {
			t.Fatalf("version content B/Angles = %+v/%+v, want the edited ones", v.Content.B, v.Content.Angles)
		}
		if v.WorstCase == nil || v.WorstCase.B == nil {
			t.Fatalf("version worst case = %+v, want a B measurement", v.WorstCase)
		}
		if diff := cmp.Diff(wantWC.B, v.WorstCase.B); diff != "" {
			t.Fatalf("version WorstCase.B mismatch (-want +got):\n%s", diff)
		}
		want := bColumns{
			SMS: sp(c.B.SMS.Body), Subject: sp(c.B.Email.Subject), Body: sp(c.B.Email.Body),
			AngleA: sp(c.Angles.A), AngleB: sp(c.Angles.B),
			WSMS: ipt(wantWC.B.SMSRunes), WSubject: ipt(wantWC.B.SubjectRunes),
		}
		if withEN {
			want.ENSMS, want.ENSubject, want.ENBody = sp(c.B.EN.SMS.Body), sp(c.B.EN.Email.Subject), sp(c.B.EN.Email.Body)
			want.WENSMS, want.WENSubject = ipt(wantWC.B.EN.SMSRunes), ipt(wantWC.B.EN.SubjectRunes)
		}
		if diff := cmp.Diff(want, h.bColumns(t, v.ID)); diff != "" {
			t.Fatalf("b columns (en %v) mismatch (-want +got):\n%s", withEN, diff)
		}
	}

	latest := h.get(t, id).LatestContentVersion
	v := h.edit(t, id, latest.ID, okContent())
	if v.Content.B != nil || v.Content.Angles != nil || (v.WorstCase != nil && v.WorstCase.B != nil) {
		t.Fatalf("version without b has B %+v / Angles %+v / WorstCase %+v; the previous b is not kept", v.Content.B, v.Content.Angles, v.WorstCase)
	}
	if got := h.bColumns(t, v.ID); got != (bColumns{}) {
		t.Fatalf("version without b columns = %+v, want all NULL", got)
	}
}

// REQ-ABT-01 C8
// An edit that changes b or angles drops the approval like any content change (revoke reason
// content_changed, state back to content_ready); an edit without b removes A/B the same way.
func TestService_EditChangingVariantRevokesApproval(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	angle := abVersionContent(false)
	angle.Angles.B = "Aciliyet: son saatler"
	bText := abVersionContent(false)
	bText.B.SMS.Body = "Bugün son {{first_name}}! %30 indirim."
	tests := []struct {
		name string
		next content.Content
	}{
		{name: "a changed angle", next: angle},
		{name: "a changed B text", next: bText},
		{name: "removing b and angles", next: okContent()},
	}
	base := h.edit(t, id, campaign.ExpectedVersionNone, abVersionContent(false))
	for _, tc := range tests {
		if _, err := h.svc.Approve(context.Background(), id, base.ID); err != nil {
			t.Fatalf("%s: Approve() error = %v", tc.name, err)
		}
		next := h.edit(t, id, base.ID, tc.next)
		if c := h.get(t, id); c.State != campaign.StateContentReady || c.Approval != nil {
			t.Fatalf("%s: after the edit state %s approval %+v, want content_ready and no approval", tc.name, c.State, c.Approval)
		}
		// The fixed harness clock gives every approval the same approved_at, so the row is picked by
		// version: each round approves a fresh version, hence exactly one row per base.
		var found int
		for _, r := range h.approvalRows(t, id) {
			if r.VersionID != base.ID {
				continue
			}
			found++
			if r.RevokedAt == nil || r.Reason == nil || *r.Reason != "content_changed" {
				t.Fatalf("%s: approval row = %+v (reason %s), want version %s revoked with content_changed", tc.name, r, ptrS(r.Reason), base.ID)
			}
		}
		if found != 1 {
			t.Fatalf("%s: %d approval rows for version %s, want 1", tc.name, found, base.ID)
		}
		// The next round edits on top of a fresh A/B version.
		base = h.edit(t, id, next.ID, abVersionContent(false))
	}
}

// insertVersionCopy copies the content_versions row vid under a new id and a version_number far above
// any real one, with the given column expressions overridden, inside a transaction that is rolled
// back; it returns the INSERT error (nil = accepted). Runs as scc_app (no TEMP privilege, REQ-SEC-02).
func (h *harness) insertVersionCopy(t *testing.T, vid string, override map[string]string) error {
	t.Helper()
	rows, err := h.pool.Query(t.Context(), `SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'content_versions'
		  AND is_generated = 'NEVER' AND identity_generation IS DISTINCT FROM 'ALWAYS'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	cols, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect columns: %v", err)
	}
	all := map[string]string{"id": "gen_random_uuid()", "version_number": "version_number + 1000000"}
	for k, v := range override {
		all[k] = v
	}
	var names, exprs []string
	for _, c := range cols {
		names = append(names, pgx.Identifier{c}.Sanitize())
		if e, ok := all[c]; ok {
			exprs = append(exprs, e)
		} else {
			exprs = append(exprs, pgx.Identifier{c}.Sanitize())
		}
	}
	for k := range override {
		found := false
		for _, c := range cols {
			found = found || c == k
		}
		if !found {
			t.Fatalf("content_versions has no column %q (REQ-ABT-01 C11)", k)
		}
	}
	tx, err := h.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), `INSERT INTO content_versions (`+strings.Join(names, ", ")+`) SELECT `+strings.Join(exprs, ", ")+` FROM content_versions WHERE id = $1`, vid)
	return err
}

// REQ-ABT-01 C11
// The 0010_content_variants constraints refuse inconsistent rows with SQLSTATE 23514:
// content_versions_b_all_or_none_check (b_sms_body, b_email_subject, b_email_body, angle_a, angle_b all
// NULL or all set), content_versions_b_en_check (b_en_* set exactly when b_sms_body and en_sms_body are
// both set) and content_versions_worst_case_b_check (worst_case_b_* NULL when b_sms_body is NULL,
// worst_case_b_en_* NULL when b_en_sms_body is NULL). An unchanged copy of a stored row is accepted.
func TestContentVersions_VariantConstraints(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	plain := h.edit(t, id, campaign.ExpectedVersionNone, okContent())
	ab := h.edit(t, id, plain.ID, abVersionContent(false))
	abEN := h.edit(t, id, ab.ID, abVersionContent(true))
	nullBEN := map[string]string{"b_en_sms_body": "NULL", "b_en_email_subject": "NULL", "b_en_email_body": "NULL", "worst_case_b_en_sms_runes": "NULL", "worst_case_b_en_subject_runes": "NULL"}
	tests := []struct {
		name       string
		from       string
		override   map[string]string
		constraint string // "" => accepted
	}{
		{name: "an unchanged A/B copy is accepted", from: ab.ID},
		{name: "an unchanged bilingual A/B copy is accepted", from: abEN.ID},
		{name: "only b_sms_body set", from: plain.ID, override: map[string]string{"b_sms_body": "'x'"}, constraint: "content_versions_b_all_or_none_check"},
		{name: "b without angle_b", from: ab.ID, override: map[string]string{"angle_b": "NULL"}, constraint: "content_versions_b_all_or_none_check"},
		{name: "b_en set without en", from: ab.ID, override: map[string]string{"b_en_sms_body": "'x'", "b_en_email_subject": "'y'", "b_en_email_body": "'z'"}, constraint: "content_versions_b_en_check"},
		{name: "b and en without b_en", from: abEN.ID, override: nullBEN, constraint: "content_versions_b_en_check"},
		{name: "worst_case_b without b", from: plain.ID, override: map[string]string{"worst_case_b_sms_runes": "1", "worst_case_b_subject_runes": "1"}, constraint: "content_versions_worst_case_b_check"},
		{name: "worst_case_b_en without b_en", from: ab.ID, override: map[string]string{"worst_case_b_en_sms_runes": "1", "worst_case_b_en_subject_runes": "1"}, constraint: "content_versions_worst_case_b_check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.insertVersionCopy(t, tc.from, tc.override)
			if tc.constraint == "" {
				if err != nil {
					t.Fatalf("insert error = %v, want nil", err)
				}
				return
			}
			pgErr := testdb.PgError(err)
			if pgErr == nil || pgErr.Code != "23514" || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("insert error = %v, want SQLSTATE 23514 on %s", err, tc.constraint)
			}
		})
	}
}

// abVectors are the ten test vectors of REQ-ABT-03 C3 (campaign UUID, customer UUID, variant).
var abVectors = [][3]string{
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001", "a"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "a"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000003", "a"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000004", "b"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000005", "a"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000006", "b"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000007", "b"},
	{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000008", "b"},
	{"3f1c2a4e-0b8d-4c55-9e1a-7d2b6c8f9a01", "00000000-0000-4000-8000-000000000004", "a"},
	{"3f1c2a4e-0b8d-4c55-9e1a-7d2b6c8f9a01", "00000000-0000-4000-8000-000000000005", "b"},
}

// REQ-ABT-03 C2, C3
// The SQL form of the split rule gives the ten test vectors in Postgres (as scc_app).
func TestVariantSplit_SQLVectors(t *testing.T) {
	t.Parallel()
	pool := testdb.App(t)
	for _, v := range abVectors {
		t.Run(v[0]+" "+v[1], func(t *testing.T) {
			t.Parallel()
			var got string
			err := pool.QueryRow(t.Context(), `SELECT CASE WHEN get_byte(sha256(uuid_send($1::uuid) || uuid_send($2::uuid)), 0) < 128 THEN 'a' ELSE 'b' END`, v[0], v[1]).Scan(&got)
			if err != nil || got != v[2] {
				t.Fatalf("SQL split(%s, %s) = %q, %v; want %q", v[0], v[1], got, err, v[2])
			}
		})
	}
}

// deliveryVariants reads (customer_id, channel, variant) of every delivery of the campaign.
func (h *harness) deliveryVariants(t *testing.T, id string) [][3]string {
	t.Helper()
	rows, err := h.pool.Query(t.Context(), `SELECT customer_id::text, channel::text, variant FROM deliveries WHERE campaign_id = $1`, id)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// assertSplit checks every delivery of an A/B campaign against the split rule: variant =
// content.VariantFor(campaign, customer), the SMS and e-mail deliveries of one customer have the same
// variant, and the share of distinct customers with b is within 40 %–60 % (seed example audience).
func assertSplit(t *testing.T, id string, rows [][3]string) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("the A/B campaign has no deliveries")
	}
	perCustomer := map[string]string{}
	for _, r := range rows {
		want, err := content.VariantFor(id, r[0])
		if err != nil {
			t.Fatalf("VariantFor(%s, %s) error = %v", id, r[0], err)
		}
		if r[2] != want {
			t.Fatalf("delivery (%s, %s) variant = %q, want %q (split rule)", r[0], r[1], r[2], want)
		}
		if prev, ok := perCustomer[r[0]]; ok && prev != r[2] {
			t.Fatalf("customer %s has deliveries of variants %s and %s; nobody gets two variants", r[0], prev, r[2])
		}
		perCustomer[r[0]] = r[2]
	}
	b := 0
	for _, v := range perCustomer {
		if v == "b" {
			b++
		}
	}
	if share := float64(b) / float64(len(perCustomer)); share < 0.4 || share > 0.6 {
		t.Fatalf("share of customers with b = %d/%d = %.3f, want within 0.40–0.60", b, len(perCustomer), share)
	}
}

// REQ-ABT-03 C1, C5, C6
// Sending an approved A/B version writes each delivery's variant with the split rule (=
// content.VariantFor); a customer's SMS and e-mail deliveries carry the same variant; about half of the
// example audience gets b. A version without b writes only a.
func TestSend_VariantSplit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    content.Content
		ab   bool
	}{
		{name: "A/B version", c: abVersionContent(false), ab: true},
		{name: "version without b", c: okContent()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newMockHarness(t)
			id := h.audienceReady(t)
			v := h.edit(t, id, campaign.ExpectedVersionNone, tc.c)
			if _, err := h.svc.Approve(context.Background(), id, v.ID); err != nil {
				t.Fatalf("Approve() error = %v", err)
			}
			if _, err := h.svc.Send(context.Background(), id, v.ID); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			rows := h.deliveryVariants(t, id)
			if want := h.m.ExampleBrief.Audience.SMS + h.m.ExampleBrief.Audience.Email; len(rows) != want {
				t.Fatalf("deliveries = %d, want %d", len(rows), want)
			}
			if tc.ab {
				assertSplit(t, id, rows)
				return
			}
			for _, r := range rows {
				if r[2] != "a" {
					t.Fatalf("delivery (%s, %s) of a version without b has variant %q, want a", r[0], r[1], r[2])
				}
			}
		})
	}
}

// REQ-ABT-03 C4
// deliveries.variant is text NOT NULL DEFAULT 'a', readable by scc_app; a value outside a / b is
// refused with SQLSTATE 23514 on deliveries_variant_check.
func TestDeliveries_VariantColumn(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	var dataType, nullable string
	var def *string
	if err := h.pool.QueryRow(t.Context(), `SELECT data_type, is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'deliveries' AND column_name = 'variant'`).Scan(&dataType, &nullable, &def); err != nil {
		t.Fatalf("read deliveries.variant column: %v", err)
	}
	if dataType != "text" || nullable != "NO" || def == nil || !strings.HasPrefix(*def, "'a'") {
		t.Fatalf("deliveries.variant = %s, nullable %s, default %s; want text, NO, 'a'", dataType, nullable, ptrS(def))
	}

	id := h.audienceReady(t)
	v := h.edit(t, id, campaign.ExpectedVersionNone, okContent())
	if _, err := h.svc.Approve(context.Background(), id, v.ID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := h.svc.Send(context.Background(), id, v.ID); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	var did string
	if err := h.pool.QueryRow(t.Context(), `SELECT id::text FROM deliveries WHERE campaign_id = $1 ORDER BY customer_id LIMIT 1`, id).Scan(&did); err != nil {
		t.Fatalf("select one delivery: %v", err)
	}
	for _, bad := range []string{"c", "A", ""} {
		t.Run("variant "+bad+" is refused", func(t *testing.T) {
			tx, err := h.pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			_, err = tx.Exec(t.Context(), `INSERT INTO deliveries
				SELECT (jsonb_populate_record(d, jsonb_build_object('id', gen_random_uuid(), 'variant', $2::text))).*
				FROM deliveries AS d WHERE d.id::text = $1`, did, bad)
			pgErr := testdb.PgError(err)
			if pgErr == nil || pgErr.Code != "23514" || pgErr.ConstraintName != "deliveries_variant_check" {
				t.Fatalf("insert with variant %q error = %v, want SQLSTATE 23514 on deliveries_variant_check", bad, err)
			}
		})
	}
}

// REQ-ABT-03 C1, C11
// A planned A/B send started by the Scheduler writes the deliveries with the same split rule and the
// same result as an instant send (variant = content.VariantFor, one variant per customer).
func TestScheduler_StartsAnABPlanWithTheSameSplit(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, time.Minute)
	id := h.audienceReady(t)
	vid := h.edit(t, id, campaign.ExpectedVersionNone, abVersionContent(false)).ID
	if _, err := h.svc.Approve(context.Background(), id, vid); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	at := schRef.Add(time.Hour)
	h.schSchedule(t, id, vid, at)
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})
	h.clock.Set(at)
	if n := runOnce(t, s); n != 1 {
		t.Fatalf("RunOnce() at scheduled_at = %d, want 1", n)
	}
	rows := h.deliveryVariants(t, id)
	if want := h.audienceTotal(t, id); len(rows) != want {
		t.Fatalf("deliveries = %d, want %d", len(rows), want)
	}
	assertSplit(t, id, rows)
}
