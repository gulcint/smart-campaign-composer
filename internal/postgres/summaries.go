package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
)

// ListSummaries reads the campaign summary page, its delivery counts, the filtered total and the
// overview in one read-only REPEATABLE READ transaction (db-schema.md §3.10), so one response is
// one snapshot. The ORDER BY text is one of two constants; the state filter is a parameter.
func (s *CampaignStore) ListSummaries(ctx context.Context, f campaign.SummaryFilter) (campaign.SummaryPage, error) {
	// Never NULL: nil and empty States both mean "no filter" (cardinality = 0).
	states := make([]string, 0, len(f.States))
	for _, st := range f.States {
		states = append(states, string(st))
	}
	order := `created_at DESC, id ASC`
	if f.Order == campaign.SummaryOrderScheduledAt {
		order = `scheduled_at ASC NULLS LAST, id ASC`
	}

	tx, err := s.db.BeginTx(ctx, readSnapshot)
	if err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: list summaries: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	const where = `WHERE (cardinality($1::text[]) = 0 OR state = ANY($1::text[]))`
	page := campaign.SummaryPage{Items: []campaign.Summary{}}
	rows, err := tx.Query(ctx, `
		SELECT id::text, brief, state, audience_matched, audience_sms, audience_email,
			scheduled_at, sending_started_at, completed_at, created_at, updated_at
		FROM campaigns `+where+` ORDER BY `+order+` LIMIT $2 OFFSET $3`, states, f.Limit, f.Offset)
	if err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: list summaries: %w", err)
	}
	for rows.Next() {
		var (
			c                       campaign.Summary
			state                   string
			matched, sms, email     *int
			scheduled, sending, end *time.Time
		)
		if err := rows.Scan(&c.ID, &c.Brief, &state, &matched, &sms, &email,
			&scheduled, &sending, &end, &c.CreatedAt, &c.UpdatedAt); err != nil {
			rows.Close()
			return campaign.SummaryPage{}, fmt.Errorf("postgres: list summaries: %w", err)
		}
		c.State = campaign.State(state)
		if matched != nil || sms != nil || email != nil {
			c.Audience = &campaign.SummaryAudience{Matched: deref(matched), SMS: deref(sms), Email: deref(email)}
		}
		c.ScheduledAt, c.SendingStartedAt, c.CompletedAt = utcPtr(scheduled), utcPtr(sending), utcPtr(end)
		c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
		page.Items = append(page.Items, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: list summaries: %w", err)
	}

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM campaigns `+where, states).Scan(&page.Total); err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: count summaries: %w", err)
	}

	// Delivery counts only for started campaigns (sending/completed); the others stay null.
	started := map[string]*campaign.StatusCounts{}
	var ids []string
	for i := range page.Items {
		if st := page.Items[i].State; st == campaign.StateSending || st == campaign.StateCompleted {
			page.Items[i].Deliveries = &campaign.StatusCounts{}
			started[page.Items[i].ID] = page.Items[i].Deliveries
			ids = append(ids, page.Items[i].ID)
		}
	}
	if len(ids) > 0 {
		err := scanStatusCounts(ctx, tx, func(id string) *campaign.StatusCounts { return started[id] }, `
			SELECT campaign_id::text, status, count(*) FROM deliveries
			WHERE campaign_id = ANY($1::uuid[]) GROUP BY campaign_id, status`, ids)
		if err != nil {
			return campaign.SummaryPage{}, fmt.Errorf("postgres: summary deliveries: %w", err)
		}
	}

	stateRows, err := tx.Query(ctx, `SELECT state, count(*) FROM campaigns GROUP BY state`)
	if err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: campaign overview: %w", err)
	}
	cc := &page.Overview.Campaigns
	for stateRows.Next() {
		var state string
		var n int
		if err := stateRows.Scan(&state, &n); err != nil {
			stateRows.Close()
			return campaign.SummaryPage{}, fmt.Errorf("postgres: campaign overview: %w", err)
		}
		switch campaign.State(state) {
		case campaign.StateDraft:
			cc.Draft = n
		case campaign.StateAudienceReady:
			cc.AudienceReady = n
		case campaign.StateContentReady:
			cc.ContentReady = n
		case campaign.StateApproved:
			cc.Approved = n
		case campaign.StateScheduled:
			cc.Scheduled = n
		case campaign.StateSending:
			cc.Sending = n
		case campaign.StateCompleted:
			cc.Completed = n
		}
	}
	stateRows.Close()
	if err := stateRows.Err(); err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: campaign overview: %w", err)
	}
	cc.Total = cc.Draft + cc.AudienceReady + cc.ContentReady + cc.Approved + cc.Scheduled + cc.Sending + cc.Completed

	dc := &page.Overview.Deliveries
	if err := scanStatusCounts(ctx, tx, func(string) *campaign.StatusCounts { return dc }, `
		SELECT '', status, count(*) FROM deliveries GROUP BY status`); err != nil {
		return campaign.SummaryPage{}, fmt.Errorf("postgres: delivery overview: %w", err)
	}
	return page, nil
}

// scanStatusCounts adds the (key, status, count) rows of query to the counters pick returns for
// key and recomputes their totals (total = queued + sending + sent + failed, as GET …/status).
func scanStatusCounts(ctx context.Context, tx pgx.Tx, pick func(key string) *campaign.StatusCounts, query string, args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, status string
		var n int
		if err := rows.Scan(&key, &status, &n); err != nil {
			return err
		}
		c := pick(key)
		if c == nil {
			continue
		}
		switch status {
		case "queued":
			c.Queued += n
		case "sending":
			c.Sending += n
		case "sent":
			c.Sent += n
		case "failed":
			c.Failed += n
		}
		c.Total = c.Queued + c.Sending + c.Sent + c.Failed
	}
	return rows.Err()
}
