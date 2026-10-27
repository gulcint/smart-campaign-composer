package postgres_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/postgres"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// The integration database (TEST_DATABASE_URL) is migrated and seeded with
// --seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z by scripts/test-container.sh, i.e. the
// dataset described by testdata/seed/manifest.json. These tests only read it.

const audManifestPath = "../../testdata/seed/manifest.json"

type audAudience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

type audGroup struct {
	Count               int      `json:"count"`
	CustomerIDs         []string `json:"customer_ids"`
	MatchesExampleBrief bool     `json:"matches_example_brief"`
	InSMSAudience       bool     `json:"in_sms_audience"`
	InEmailAudience     bool     `json:"in_email_audience"`
}

type audManifest struct {
	Params struct {
		RefTime string `json:"ref_time"`
	} `json:"params"`
	Totals struct {
		Customers int `json:"customers"`
		Events    int `json:"events"`
	} `json:"totals"`
	Reachable    audAudience    `json:"reachable"`
	Cities       map[string]int `json:"cities"`
	ExampleBrief struct {
		Rule     json.RawMessage `json:"rule"`
		Audience audAudience     `json:"audience"`
	} `json:"example_brief"`
	Groups map[string]audGroup `json:"groups"`
}

// REQ-AUD-00, REQ-AUD-03, REQ-AUD-04
// The example brief rule counted by the SQL path (postgres.NewAudience) equals the manifest
// numbers computed independently by segment.Match: matched, and per channel after consent.
func TestAudience_ExampleBriefMatchesManifest(t *testing.T) {
	pool, m := audSetup(t)
	got := audCount(t, pool, audRule(t, string(m.ExampleBrief.Rule)), audRef(t, m))
	want := segment.Audience{Matched: m.ExampleBrief.Audience.Matched, SMS: m.ExampleBrief.Audience.SMS, Email: m.ExampleBrief.Audience.Email}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Count(example rule) mismatch with manifest example_brief.audience (-want +got):\n%s", diff)
	}
}

// REQ-AUD-03, REQ-AUD-04
// A rule matching everybody yields the manifest's reachable numbers: SMS needs sms_opt_in and a
// phone, email needs email_opt_in and an email (segment-dsl.md §6), independently per channel.
func TestAudience_EveryoneMatchesReachable(t *testing.T) {
	pool, m := audSetup(t)
	got := audCount(t, pool, audRule(t, `{"all":[{"field":"total_orders","op":"gte","value":0}]}`), audRef(t, m))
	want := segment.Audience{Matched: m.Totals.Customers, SMS: m.Reachable.SMS, Email: m.Reachable.Email}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Count(everyone) mismatch with manifest reachable (-want +got):\n%s", diff)
	}
	if m.Reachable.Matched != m.Totals.Customers {
		t.Fatalf("manifest reachable.matched = %d, totals.customers = %d", m.Reachable.Matched, m.Totals.Customers)
	}
}

// REQ-AUD-03
// city eq X matches exactly the manifest's customer count per city (exact, case-sensitive match).
func TestAudience_CityCountsMatchManifest(t *testing.T) {
	pool, m := audSetup(t)
	ref := audRef(t, m)
	for city, want := range m.Cities {
		t.Run(city, func(t *testing.T) {
			got := audCount(t, pool, audRule(t, `{"all":[{"field":"city","op":"eq","value":`+audJSONString(city)+`}]}`), ref)
			if got.Matched != want {
				t.Fatalf("Count(city eq %s).Matched = %d, want %d", city, got.Matched, want)
			}
			if got.SMS > got.Matched || got.Email > got.Matched {
				t.Fatalf("channel counts exceed matched: %+v", got)
			}
		})
	}
	t.Run("lowercase spelling matches nobody", func(t *testing.T) {
		got := audCount(t, pool, audRule(t, `{"all":[{"field":"city","op":"eq","value":"istanbul"}]}`), ref)
		if diff := cmp.Diff(segment.Audience{}, got); diff != "" {
			t.Fatalf("Count(city eq istanbul) mismatch (-want +got):\n%s", diff)
		}
	})
}

// REQ-AUD-02
// SQL text inside a value is only a value: the count is 0 and no table is touched
// (segment-dsl.md §7.3 #1).
func TestAudience_SQLInjectionValueIsOnlyAValue(t *testing.T) {
	pool, m := audSetup(t)
	ref := audRef(t, m)
	for _, raw := range []string{
		`{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}`,
		`{"all":[{"field":"city","op":"in","value":["x'); DELETE FROM events; --","İstanbul' OR '1'='1"]}]}`,
		`{"any":[{"field":"city","op":"eq","value":"\\'; TRUNCATE customers; --"},{"not":{"field":"city","op":"neq","value":"'; UPDATE customers SET sms_opt_in = true; --"}}]}`,
	} {
		got := audCount(t, pool, audRule(t, raw), ref)
		if diff := cmp.Diff(segment.Audience{}, got); diff != "" {
			t.Fatalf("Count(%s) mismatch (-want +got):\n%s", raw, diff)
		}
	}
	audAssertDatasetIntact(t, pool, m)
	again := audCount(t, pool, audRule(t, string(m.ExampleBrief.Rule)), ref)
	if again.Matched != m.ExampleBrief.Audience.Matched || again.SMS != m.ExampleBrief.Audience.SMS || again.Email != m.ExampleBrief.Audience.Email {
		t.Fatalf("example audience after injection attempts = %+v, want %+v", again, m.ExampleBrief.Audience)
	}
}

// REQ-AUD-00, REQ-AUD-02, REQ-AUD-04
// The compiled predicate, run in the fixed skeleton of segment-dsl.md §5, selects exactly the
// manifest groups flagged matches_example_brief (29/30-day edges in, 31 out, purchase_before_cart_add
// out); the channel audiences of §6 contain exactly the groups flagged in_sms/in_email_audience and
// have the sizes the Counter reports.
func TestAudience_ExampleBriefMembershipAndConsent(t *testing.T) {
	pool, m := audSetup(t)
	ref := audRef(t, m)
	rule := audRule(t, string(m.ExampleBrief.Rule))
	matched := audMatchedIDs(t, pool, rule, ref)
	if len(matched) != m.ExampleBrief.Audience.Matched {
		t.Fatalf("compiled predicate selects %d customers, want %d", len(matched), m.ExampleBrief.Audience.Matched)
	}

	sms, email := audChannelSets(t, pool, matched)
	counted := audCount(t, pool, rule, ref)
	if len(sms) != counted.SMS || len(email) != counted.Email {
		t.Fatalf("channel audiences from §6 = (%d, %d), Counter = (%d, %d)", len(sms), len(email), counted.SMS, counted.Email)
	}

	inMatched := audSet(matched)
	for name, g := range m.Groups {
		t.Run(name, func(t *testing.T) {
			if len(g.CustomerIDs) != g.Count {
				t.Fatalf("manifest group has %d ids, count %d", len(g.CustomerIDs), g.Count)
			}
			for _, id := range g.CustomerIDs {
				if inMatched[id] != g.MatchesExampleBrief {
					t.Errorf("customer %s matched = %v, want %v", id, inMatched[id], g.MatchesExampleBrief)
				}
				if sms[id] != g.InSMSAudience {
					t.Errorf("customer %s in SMS audience = %v, want %v", id, sms[id], g.InSMSAudience)
				}
				if email[id] != g.InEmailAudience {
					t.Errorf("customer %s in email audience = %v, want %v", id, email[id], g.InEmailAudience)
				}
			}
		})
	}
}

// REQ-AUD-00, REQ-AUD-02
// The reference time is injected ($1), not now(): one day later the 30-day group drops out and the
// 29-day group stays, and the Counter agrees with the compiled predicate at that time.
func TestAudience_ReferenceTimeIsInjected(t *testing.T) {
	pool, m := audSetup(t)
	later := audRef(t, m).Add(24 * time.Hour)
	rule := audRule(t, string(m.ExampleBrief.Rule))
	ids := audSet(audMatchedIDs(t, pool, rule, later))
	for _, id := range m.Groups["cart_add_30d"].CustomerIDs {
		if ids[id] {
			t.Errorf("cart_add_30d customer %s still matches one day after the reference time", id)
		}
	}
	for _, id := range m.Groups["cart_add_29d"].CustomerIDs {
		if !ids[id] {
			t.Errorf("cart_add_29d customer %s does not match one day after the reference time (30 days old)", id)
		}
	}
	got := audCount(t, pool, rule, later)
	if got.Matched != len(ids) {
		t.Fatalf("Count at ref+24h matched = %d, compiled predicate selects %d", got.Matched, len(ids))
	}
}

// ---- helpers ----

func audSetup(t *testing.T) (*pgxpool.Pool, audManifest) {
	t.Helper()
	pool := testdb.App(t)
	b, err := os.ReadFile(filepath.FromSlash(audManifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", audManifestPath, err)
	}
	var m audManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	audAssertDatasetIntact(t, pool, m)
	return pool, m
}

// audAssertDatasetIntact checks the database still holds the canonical dataset (tables exist,
// counts equal the manifest totals).
func audAssertDatasetIntact(t *testing.T, pool *pgxpool.Pool, m audManifest) {
	t.Helper()
	var customers, events int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM customers`).Scan(&customers); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if customers != m.Totals.Customers || events != m.Totals.Events {
		t.Fatalf("database holds %d customers / %d events, manifest says %d / %d (canonical seed expected)",
			customers, events, m.Totals.Customers, m.Totals.Events)
	}
}

func audRef(t *testing.T, m audManifest) time.Time {
	t.Helper()
	ref, err := time.Parse(time.RFC3339, m.Params.RefTime)
	if err != nil {
		t.Fatalf("manifest params.ref_time %q: %v", m.Params.RefTime, err)
	}
	return ref.UTC()
}

func audRule(t *testing.T, rootGroup string) segment.Rule {
	t.Helper()
	r, err := segment.Parse([]byte(`{"rule":` + rootGroup + `}`))
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", rootGroup, err)
	}
	if err := segment.Validate(r); err != nil {
		t.Fatalf("Validate(%s) error = %v", rootGroup, err)
	}
	return r
}

func audCount(t *testing.T, pool *pgxpool.Pool, r segment.Rule, ref time.Time) segment.Audience {
	t.Helper()
	got, err := postgres.NewAudience(pool).Count(t.Context(), r, ref)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	return got
}

// audMatchedIDs runs the compiled predicate inside the fixed skeleton of segment-dsl.md §5.
func audMatchedIDs(t *testing.T, pool *pgxpool.Pool, r segment.Rule, ref time.Time) []string {
	t.Helper()
	c, err := segment.Compile(r, ref)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	q := `SELECT c.id::text FROM customers c JOIN customer_stats_at($1) s ON s.customer_id = c.id WHERE ` + c.Where
	rows, err := pool.Query(t.Context(), q, c.Args...)
	if err != nil {
		t.Fatalf("skeleton query error = %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect ids: %v", err)
	}
	slices.Sort(ids)
	return ids
}

// audChannelSets applies the channel filter table of segment-dsl.md §6 to the matched ids.
func audChannelSets(t *testing.T, pool *pgxpool.Pool, matched []string) (sms, email map[string]bool) {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT id::text, (sms_opt_in AND phone IS NOT NULL), (email_opt_in AND email IS NOT NULL)
		   FROM customers WHERE id::text = ANY($1)`, matched)
	if err != nil {
		t.Fatalf("channel query: %v", err)
	}
	defer rows.Close()
	sms, email = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id string
		var s, e bool
		if err := rows.Scan(&id, &s, &e); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if s {
			sms[id] = true
		}
		if e {
			email[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return sms, email
}

func audSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

func audJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
