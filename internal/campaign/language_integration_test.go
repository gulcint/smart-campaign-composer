package campaign_test

// Language (P8) integration tests over the real Postgres of compose.test.yml, with the harness of
// service_integration_test.go. Every test creates its own campaigns and never mutates customers.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

type enColumns struct {
	SMS, Subject, Body           *string
	WorstSMSRunes, WorstSubjRune *int
}

func (h *harness) enColumns(t *testing.T, versionID string) enColumns {
	t.Helper()
	var c enColumns
	err := h.pool.QueryRow(t.Context(), `SELECT en_sms_body, en_email_subject, en_email_body, worst_case_en_sms_runes, worst_case_en_subject_runes
		FROM content_versions WHERE id = $1`, versionID).Scan(&c.SMS, &c.Subject, &c.Body, &c.WorstSMSRunes, &c.WorstSubjRune)
	if err != nil {
		t.Fatalf("read en columns of %s: %v", versionID, err)
	}
	return c
}

// REQ-LNG-01 C4, C5, C9
// An edit with `en` stores the English version in en_sms_body / en_email_subject / en_email_body and
// its measurement in worst_case_en_sms_runes / worst_case_en_subject_runes (= MeasureWorstCase with
// the segment's longest values); the next edit WITHOUT `en` stores a version without English: the
// previous version's `en` is not carried over (all five columns NULL, WorstCase.EN nil).
func TestService_EnglishVersionColumns(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	l := h.get(t, id).Segment.LongestValues

	bilingual := okContent()
	bilingual.EN = &content.English{SMS: content.SMS{Body: "Hi {{first_name}}, your cart is waiting."}, Email: content.Email{Subject: "Your cart in {{city}}", Body: "Hi {{first_name}},\n\nYour cart is waiting."}}
	wantWC, err := content.MeasureWorstCase(bilingual, l)
	if err != nil || wantWC.EN == nil {
		t.Fatalf("setup: MeasureWorstCase() = %+v, %v; want an EN measurement", wantWC, err)
	}

	v1 := h.edit(t, id, campaign.ExpectedVersionNone, bilingual)
	if v1.WorstCase == nil || v1.WorstCase.EN == nil || *v1.WorstCase.EN != *wantWC.EN {
		t.Fatalf("version worst case = %+v, want EN %+v", v1.WorstCase, *wantWC.EN)
	}
	got := h.enColumns(t, v1.ID)
	if got.SMS == nil || got.Subject == nil || got.Body == nil ||
		*got.SMS != bilingual.EN.SMS.Body || *got.Subject != bilingual.EN.Email.Subject || *got.Body != bilingual.EN.Email.Body {
		t.Fatalf("en columns = %v/%v/%v, want the English version", ptrS(got.SMS), ptrS(got.Subject), ptrS(got.Body))
	}
	if got.WorstSMSRunes == nil || got.WorstSubjRune == nil || *got.WorstSMSRunes != wantWC.EN.SMSRunes || *got.WorstSubjRune != wantWC.EN.SubjectRunes {
		t.Fatalf("worst_case_en columns = %v/%v, want %d/%d", ptrI(got.WorstSMSRunes), ptrI(got.WorstSubjRune), wantWC.EN.SMSRunes, wantWC.EN.SubjectRunes)
	}

	v2 := h.edit(t, id, v1.ID, okContent())
	if v2.WorstCase == nil || v2.WorstCase.EN != nil {
		t.Fatalf("Turkish-only version worst case = %+v, want EN nil", v2.WorstCase)
	}
	if got := h.enColumns(t, v2.ID); got != (enColumns{}) {
		t.Fatalf("Turkish-only version en columns = %+v, want all NULL (the previous en is not kept)", got)
	}
}

// REQ-LNG-01 C9
// content_versions_en_all_or_none_check: the three English text columns are NULL together or set
// together; a row with only en_sms_body set is refused with SQLSTATE 23514 naming the constraint.
// Runs as scc_app (no TEMP privilege, REQ-SEC-02): the row is a copy of a stored Turkish-only
// version inserted with INSERT … SELECT (new id, version_number far above any real one, only
// en_sms_body set) inside a transaction that is rolled back.
func TestContentVersions_EnglishColumnsAllOrNone(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v := h.edit(t, id, campaign.ExpectedVersionNone, okContent())

	// Insertable columns of content_versions, in table order.
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
	override := map[string]string{
		"id":             "gen_random_uuid()",
		"version_number": "version_number + 1000000",
		"en_sms_body":    "'Hi'",
	}
	var names, exprs []string
	for _, c := range cols {
		names = append(names, pgx.Identifier{c}.Sanitize())
		if e, ok := override[c]; ok {
			exprs = append(exprs, e)
		} else {
			exprs = append(exprs, pgx.Identifier{c}.Sanitize())
		}
	}
	insert := `INSERT INTO content_versions (` + strings.Join(names, ", ") + `) SELECT ` + strings.Join(exprs, ", ") + ` FROM content_versions WHERE id = $1`

	tx, err := h.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), insert, v.ID)
	pgErr := testdb.PgError(err)
	if pgErr == nil || pgErr.Code != "23514" || pgErr.ConstraintName != "content_versions_en_all_or_none_check" {
		t.Fatalf("insert with only en_sms_body error = %v, want SQLSTATE 23514 content_versions_en_all_or_none_check", err)
	}
}

// REQ-LNG-01 C9, REQ-LNG-02 C2, REQ-SEED-01
// After a segment is generated, campaigns.audience_languages holds the per-language split of each
// channel audience: sms.tr + sms.en = audience.sms and email.tr + email.en = audience.email (the
// manifest's example audience).
func TestService_AudienceLanguagesStored(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)

	var raw []byte
	if err := h.pool.QueryRow(t.Context(), `SELECT audience_languages FROM campaigns WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read audience_languages: %v", err)
	}
	if raw == nil {
		t.Fatalf("audience_languages is NULL after a P8 segment generation")
	}
	var langs map[string]map[string]int
	if err := json.Unmarshal(raw, &langs); err != nil {
		t.Fatalf("audience_languages %s: %v", raw, err)
	}
	a := h.m.ExampleBrief.Audience
	for ch, want := range map[string]int{"sms": a.SMS, "email": a.Email} {
		l := langs[ch]
		if len(l) != 2 || l["tr"] < 0 || l["en"] < 0 || l["tr"]+l["en"] != want {
			t.Fatalf("audience_languages.%s = %v, want {tr, en} summing to %d", ch, l, want)
		}
	}
	// REQ-SEED-01: the manifest states the same split.
	b, err := os.ReadFile(filepath.FromSlash(svcManifestPath))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		ExampleBrief struct {
			Audience struct {
				Languages map[string]map[string]int `json:"languages"`
			} `json:"audience"`
		} `json:"example_brief"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if diff := cmp.Diff(m.ExampleBrief.Audience.Languages, langs); diff != "" {
		t.Fatalf("audience_languages mismatch with manifest example_brief.audience.languages (-manifest +db):\n%s", diff)
	}
}
