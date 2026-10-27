package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Content versions are immutable (db-schema.md §3.5): this file only INSERTs them. Every campaign
// write runs in one transaction under the campaign row lock (db-schema.md §3.9).

const versionColumns = `v.id::text, v.campaign_id::text, v.version_number, v.segment_revision, v.status, v.source,
	v.sms_body, v.email_subject, v.email_body, v.validation_errors, v.llm_attempts,
	v.worst_case_sms_runes, v.worst_case_subject_runes, v.created_at`

func scanVersion(row pgx.Row) (campaign.ContentVersion, error) {
	var (
		v            campaign.ContentVersion
		status, src  string
		errs         []byte
		wcSMS, wcSub *int
	)
	if err := row.Scan(&v.ID, &v.CampaignID, &v.VersionNumber, &v.SegmentRevision, &status, &src,
		&v.Content.SMS.Body, &v.Content.Email.Subject, &v.Content.Email.Body, &errs, &v.LLMAttempts,
		&wcSMS, &wcSub, &v.CreatedAt); err != nil {
		return campaign.ContentVersion{}, err
	}
	v.Status, v.Source = campaign.ContentStatus(status), campaign.ContentSource(src)
	v.CreatedAt = v.CreatedAt.UTC()
	if wcSMS != nil && wcSub != nil {
		v.WorstCase = &content.WorstCase{SMSRunes: *wcSMS, SubjectRunes: *wcSub}
	}
	v.ValidationErrors = []validation.FieldError{}
	if len(errs) > 0 {
		if err := json.Unmarshal(errs, &v.ValidationErrors); err != nil {
			return campaign.ContentVersion{}, fmt.Errorf("postgres: read content version errors: %w", err)
		}
		if v.ValidationErrors == nil {
			v.ValidationErrors = []validation.FieldError{}
		}
	}
	return v, nil
}

func (s *CampaignStore) campaignExists(ctx context.Context, id string) error {
	var one int
	err := s.db.QueryRow(ctx, `SELECT 1 FROM campaigns WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.ErrCampaignNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: read campaign: %w", err)
	}
	return nil
}

func (s *CampaignStore) ListContentVersions(ctx context.Context, id string) ([]campaign.ContentVersion, error) {
	if err := s.campaignExists(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+versionColumns+` FROM content_versions v
		WHERE v.campaign_id = $1 ORDER BY v.version_number DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: list content versions: %w", err)
	}
	defer rows.Close()
	out := []campaign.ContentVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list content versions: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list content versions: %w", err)
	}
	return out, nil
}

func (s *CampaignStore) GetContentVersion(ctx context.Context, id, versionID string) (campaign.ContentVersion, error) {
	if err := s.campaignExists(ctx, id); err != nil {
		return campaign.ContentVersion{}, err
	}
	return getVersion(s.db.QueryRow(ctx, `SELECT `+versionColumns+` FROM content_versions v
		WHERE v.campaign_id = $1 AND v.id = $2`, id, versionID))
}

func getVersion(row pgx.Row) (campaign.ContentVersion, error) {
	v, err := scanVersion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.ContentVersion{}, campaign.ErrContentVersionNotFound
	}
	if err != nil {
		return campaign.ContentVersion{}, fmt.Errorf("postgres: read content version: %w", err)
	}
	return v, nil
}

// lockedCampaign is the campaign row read under SELECT ... FOR UPDATE.
type lockedCampaign struct {
	campaign.LockedCampaign
	rule    []byte
	refTime *time.Time
}

func lockCampaign(ctx context.Context, tx pgx.Tx, id string) (lockedCampaign, error) {
	var (
		lc                            lockedCampaign
		state                         string
		longFirst, longLast, longCity *int
	)
	err := tx.QueryRow(ctx, `
		SELECT state, segment_rule, segment_reference_time, segment_revision,
			longest_first_name, longest_last_name, longest_city, approved_content_version_id::text
		FROM campaigns WHERE id = $1 FOR UPDATE`, id).
		Scan(&state, &lc.rule, &lc.refTime, &lc.SegmentRevision, &longFirst, &longLast, &longCity, &lc.ApprovedVersionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedCampaign{}, campaign.ErrCampaignNotFound
	}
	if err != nil {
		return lockedCampaign{}, fmt.Errorf("postgres: lock campaign: %w", err)
	}
	lc.State = campaign.State(state)
	lc.HasSegment = lc.rule != nil && lc.refTime != nil
	lc.Longest = content.Longest{FirstName: deref(longFirst), LastName: deref(longLast), City: deref(longCity)}

	var ref campaign.VersionRef
	err = tx.QueryRow(ctx, `
		SELECT id::text, version_number FROM content_versions
		WHERE campaign_id = $1 AND status = 'validated' AND segment_revision = $2
		ORDER BY version_number DESC LIMIT 1`, id, lc.SegmentRevision).Scan(&ref.ID, &ref.Number)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return lockedCampaign{}, fmt.Errorf("postgres: read latest content version: %w", err)
	default:
		lc.Latest = &ref
	}
	return lc, nil
}

// errRetryVersionNumber marks the one retry of a content_versions_campaign_version_key violation.
var errRetryVersionNumber = errors.New("postgres: version number taken")

func (s *CampaignStore) WriteContentVersion(ctx context.Context, id string, decide func(campaign.LockedCampaign) (campaign.ContentWrite, error)) (campaign.ContentVersion, error) {
	// version_number is computed under the row lock, so the unique key is only a last guarantee;
	// should it fire anyway the whole transaction is retried once and never surfaces as a 500.
	for try := 0; ; try++ {
		v, err := s.writeContentVersion(ctx, id, decide)
		if errors.Is(err, errRetryVersionNumber) && try == 0 {
			continue
		}
		return v, err
	}
}

func (s *CampaignStore) writeContentVersion(ctx context.Context, id string, decide func(campaign.LockedCampaign) (campaign.ContentWrite, error)) (campaign.ContentVersion, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return campaign.ContentVersion{}, fmt.Errorf("postgres: write content version: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return campaign.ContentVersion{}, err
	}
	w, err := decide(lc.LockedCampaign)
	if err != nil {
		return campaign.ContentVersion{}, err
	}
	var number int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version_number), 0) + 1 FROM content_versions WHERE campaign_id = $1`, id).
		Scan(&number); err != nil {
		return campaign.ContentVersion{}, fmt.Errorf("postgres: next version number: %w", err)
	}
	errs := w.ValidationErrors
	if errs == nil {
		errs = []validation.FieldError{}
	}
	errJSON, err := json.Marshal(errs)
	if err != nil {
		return campaign.ContentVersion{}, fmt.Errorf("postgres: encode validation errors: %w", err)
	}
	var wcSMS, wcSub *int
	if w.WorstCase != nil {
		wcSMS, wcSub = &w.WorstCase.SMSRunes, &w.WorstCase.SubjectRunes
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO content_versions (id, campaign_id, version_number, segment_revision, status, source,
			sms_body, email_subject, email_body, validation_errors, llm_attempts,
			worst_case_sms_runes, worst_case_subject_runes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13, $14)`,
		w.ID, id, number, lc.SegmentRevision, string(w.Status), string(w.Source),
		w.Content.SMS.Body, w.Content.Email.Subject, w.Content.Email.Body, string(errJSON), w.LLMAttempts,
		wcSMS, wcSub, w.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "content_versions_campaign_version_key" {
			return campaign.ContentVersion{}, errRetryVersionNumber
		}
		return campaign.ContentVersion{}, fmt.Errorf("postgres: insert content version: %w", err)
	}
	if w.NextState != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE approvals SET revoked_at = $2, revoke_reason = 'content_changed'
			WHERE campaign_id = $1 AND revoked_at IS NULL`, id, w.CreatedAt); err != nil {
			return campaign.ContentVersion{}, fmt.Errorf("postgres: revoke approval: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns SET state = $2, approved_content_version_id = NULL, updated_at = $3 WHERE id = $1`,
			id, string(w.NextState), w.CreatedAt); err != nil {
			return campaign.ContentVersion{}, fmt.Errorf("postgres: update campaign state: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return campaign.ContentVersion{}, fmt.Errorf("postgres: write content version: %w", err)
	}
	return campaign.ContentVersion{
		ID: w.ID, CampaignID: id, VersionNumber: number, SegmentRevision: lc.SegmentRevision,
		Status: w.Status, Source: w.Source, Content: w.Content, WorstCase: w.WorstCase,
		ValidationErrors: errs, LLMAttempts: w.LLMAttempts, CreatedAt: w.CreatedAt.UTC(),
	}, nil
}

func (s *CampaignStore) ApproveVersion(ctx context.Context, id, versionID string, a campaign.ApprovalWrite, decide func(campaign.ApprovalCheck) (bool, error)) (campaign.Campaign, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: approve: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return campaign.Campaign{}, err
	}
	v, err := getVersion(tx.QueryRow(ctx, `SELECT `+versionColumns+` FROM content_versions v
		WHERE v.campaign_id = $1 AND v.id = $2`, id, versionID))
	if err != nil {
		return campaign.Campaign{}, err
	}
	write, err := decide(campaign.ApprovalCheck{
		Campaign: lc.LockedCampaign,
		Version:  v,
		Longest: func() (content.Longest, error) {
			if !lc.HasSegment {
				return content.Longest{}, campaign.ErrInvalidState
			}
			r, err := segment.Parse(append(append([]byte(`{"rule":`), lc.rule...), '}'))
			if err != nil {
				return content.Longest{}, fmt.Errorf("postgres: stored segment rule: %w", err)
			}
			return longestValues(ctx, tx, r, lc.refTime.UTC())
		},
	})
	if err != nil {
		return campaign.Campaign{}, err
	}
	if write {
		if _, err := tx.Exec(ctx, `
			INSERT INTO approvals (id, campaign_id, content_version_id, approved_at) VALUES ($1, $2, $3, $4)`,
			a.ID, id, versionID, a.ApprovedAt); err != nil {
			return campaign.Campaign{}, fmt.Errorf("postgres: insert approval: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns SET state = 'approved', approved_content_version_id = $2, updated_at = $3 WHERE id = $1`,
			id, versionID, a.ApprovedAt); err != nil {
			return campaign.Campaign{}, fmt.Errorf("postgres: approve campaign: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: approve: %w", err)
	}
	return s.GetCampaign(ctx, id)
}

func (s *CampaignStore) PreviewSample(ctx context.Context, r segment.Rule, ref time.Time, customerID string, index int) (campaign.SampleCustomer, error) {
	comp, err := segment.Compile(r, ref)
	if err != nil {
		return campaign.SampleCustomer{}, err
	}
	// The compiled predicate holds only constant SQL and $n placeholders; the extra parameter of
	// this query is appended after the rule's.
	next := "$" + strconv.Itoa(len(comp.Args)+1)
	var out campaign.SampleCustomer
	if err := s.db.QueryRow(ctx, `SELECT count(*)`+audienceFrom+comp.Where, comp.Args...).Scan(&out.Count); err != nil {
		return campaign.SampleCustomer{}, fmt.Errorf("postgres: preview sample: %w", err)
	}
	cu := &out.Customer
	if customerID != "" {
		err := s.db.QueryRow(ctx, `SELECT c.id::text, c.first_name, c.last_name, c.city, (
				SELECT count(*)`+audienceFrom+`(`+comp.Where+`) AND c.id < `+next+`)
			FROM customers c JOIN customer_stats_at($1) s ON s.customer_id = c.id
			WHERE (`+comp.Where+`) AND c.id = `+next, append(comp.Args, customerID)...).
			Scan(&cu.ID, &cu.FirstName, &cu.LastName, &cu.City, &out.Index)
		if errors.Is(err, pgx.ErrNoRows) {
			return campaign.SampleCustomer{}, campaign.ErrCustomerNotFound
		}
		if err != nil {
			return campaign.SampleCustomer{}, fmt.Errorf("postgres: preview sample: %w", err)
		}
		return out, nil
	}
	if index >= out.Count {
		return campaign.SampleCustomer{}, campaign.ErrCustomerNotFound
	}
	err = s.db.QueryRow(ctx, `SELECT c.id::text, c.first_name, c.last_name, c.city`+audienceFrom+comp.Where+`
		ORDER BY c.id LIMIT 1 OFFSET `+next, append(comp.Args, index)...).
		Scan(&cu.ID, &cu.FirstName, &cu.LastName, &cu.City)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.SampleCustomer{}, campaign.ErrCustomerNotFound
	}
	if err != nil {
		return campaign.SampleCustomer{}, fmt.Errorf("postgres: preview sample: %w", err)
	}
	out.Index = index
	return out, nil
}
