// Package postgres holds the pgx-based implementations of the persistence ports.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/metrics"
)

// Open creates a connection pool. It does not wait for the database: connections are made
// lazily, so a process can start (and report /readyz 503) while the database is still down.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// pgx error text may echo the URL (with its password); keep it out of the error.
		return nil, fmt.Errorf("postgres: invalid database url")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	return pool, nil
}

// NewMetricsSource returns the scrape-time source of the API's DB-derived metrics.
func NewMetricsSource(db *pgxpool.Pool) metrics.DeliveryStatsSource {
	return metricsSource{db: db}
}

type metricsSource struct{ db *pgxpool.Pool }

func (s metricsSource) DeliveryStats(ctx context.Context) ([]metrics.DeliveryStat, error) {
	rows, err := s.db.Query(ctx, `
		SELECT channel, status, coalesce(failure_reason, ''), count(*), coalesce(sum(retry_count), 0)
		FROM deliveries
		GROUP BY channel, status, failure_reason`)
	if err != nil {
		return nil, fmt.Errorf("postgres: delivery stats: %w", err)
	}
	defer rows.Close()
	var out []metrics.DeliveryStat
	for rows.Next() {
		var st metrics.DeliveryStat
		if err := rows.Scan(&st.Channel, &st.Status, &st.FailureReason, &st.Count, &st.Retries); err != nil {
			return nil, fmt.Errorf("postgres: delivery stats: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: delivery stats: %w", err)
	}
	return out, nil
}

func (s metricsSource) CampaignStates(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT state, count(*) FROM campaigns GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("postgres: campaign states: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("postgres: campaign states: %w", err)
		}
		out[state] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: campaign states: %w", err)
	}
	return out, nil
}
