package postgres_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// REQ-AUD-01, REQ-AUD-02, REQ-AUD-03
// A day in a relative window is exactly 24 hours whatever the session time zone (segment-dsl.md §4,
// §5 rule 4). The manifest group cart_add_30d has one cart_add at exactly ref − 30×24h
// (2025-12-16T12:00:00Z, seed.md §3.3). At ref2 = that instant + 90×24h (2026-03-16T12:00:00Z) the
// 90-day window edge falls exactly on the event, and America/New_York switched to DST on 2026-03-08
// in between: a calendar-day interval evaluated in that zone would move the edge by one hour and flip
// the result. The compiled predicate, run on a connection whose session TimeZone is UTC,
// Europe/Istanbul or America/New_York, must select the same customers, agree with segment.Match
// (Go) for the edge group, and agree with the Counter.
func TestAudience_DayWindowsIgnoreSessionTimeZone(t *testing.T) {
	pool, m := audSetup(t)
	ref := audRef(t, m)
	event := ref.Add(-30 * 24 * time.Hour)
	ref2 := event.Add(90 * 24 * time.Hour)
	edge := m.Groups["cart_add_30d"].CustomerIDs
	if len(edge) == 0 {
		t.Fatal("manifest group cart_add_30d has no customer ids")
	}

	tests := []struct {
		name      string
		rule      string
		edgeMatch bool // the event lies exactly on the window edge: within_last includes it, older_than excludes it
	}{
		{name: "within_last 90 includes the edge", rule: `{"all":[{"field":"last_cart_add_at","op":"within_last","value":{"days":90}}]}`, edgeMatch: true},
		{name: "older_than 90 excludes the edge", rule: `{"all":[{"field":"last_cart_add_at","op":"older_than","value":{"days":90}}]}`, edgeMatch: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := audRule(t, tc.rule)

			got, err := segment.Match(rule, segment.Facts{LastCartAddAt: &event}, ref2)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != tc.edgeMatch {
				t.Fatalf("segment.Match at the edge = %v, want %v", got, tc.edgeMatch)
			}

			var baseline []string
			first := true
			for _, zone := range []string{"UTC", "Europe/Istanbul", "America/New_York"} {
				ids := matchedIDsInZone(t, pool, rule, ref2, zone)
				set := audSet(ids)
				for _, id := range edge {
					if set[id] != tc.edgeMatch {
						t.Errorf("TimeZone %s: cart_add_30d customer %s selected = %v, want %v (as segment.Match)", zone, id, set[id], tc.edgeMatch)
					}
				}
				if first {
					baseline, first = ids, false
				} else if !slices.Equal(baseline, ids) {
					t.Errorf("TimeZone %s selects %d customers, UTC selects %d; the window must not depend on the session time zone", zone, len(ids), len(baseline))
				}
			}
			if counted := audCount(t, pool, rule, ref2); counted.Matched != len(baseline) {
				t.Fatalf("Counter matched = %d, compiled predicate selects %d", counted.Matched, len(baseline))
			}
		})
	}
}

// matchedIDsInZone runs the compiled predicate in the fixed skeleton of segment-dsl.md §5 on a
// dedicated connection with the given session TimeZone (zone is a constant of the test, not input). The connection is taken out of the pool
// and closed afterwards, so the setting never leaks to other tests.
func matchedIDsInZone(t *testing.T, pool *pgxpool.Pool, r segment.Rule, ref time.Time, zone string) []string {
	t.Helper()
	c, err := segment.Compile(r, ref)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	pc, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	conn := pc.Hijack()
	defer func() { _ = conn.Close(t.Context()) }()
	if _, err := conn.Exec(t.Context(), `SELECT set_config('TimeZone', $1, false)`, zone); err != nil {
		t.Fatalf("SET TIME ZONE %s: %v", zone, err)
	}
	var shown string
	if err := conn.QueryRow(t.Context(), `SHOW TIME ZONE`).Scan(&shown); err != nil || shown != zone {
		t.Fatalf("session TimeZone = %q (%v), want %q", shown, err, zone)
	}
	q := `SELECT c.id::text FROM customers c JOIN customer_stats_at($1) s ON s.customer_id = c.id WHERE ` + c.Where
	rows, err := conn.Query(t.Context(), q, c.Args...)
	if err != nil {
		t.Fatalf("skeleton query (TimeZone %s): %v", zone, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect ids: %v", err)
	}
	slices.Sort(ids)
	return ids
}
