package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// CampaignStore is the concrete persistence type for internal/campaign (implements campaign.Store).
// It uses only DML, so the least-privileged scc_app role is enough.
type CampaignStore struct{ db *pgxpool.Pool }

// NewCampaignStore returns the campaign store.
func NewCampaignStore(db *pgxpool.Pool) *CampaignStore { return &CampaignStore{db: db} }

var _ campaign.Store = (*CampaignStore)(nil)

func (s *CampaignStore) CreateCampaign(ctx context.Context, c campaign.Campaign) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO campaigns (id, brief, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		c.ID, c.Brief, string(c.State), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create campaign: %w", err)
	}
	return nil
}

// campaignSelect reads a campaign with the latest VALIDATED content version of the current segment
// revision and the active approval (openapi Campaign).
const campaignSelect = `
	SELECT c.id::text, c.brief, c.state, c.segment_rule, c.segment_reference_time, c.segment_revision,
		c.segment_llm_attempts, c.segment_generated_at, c.audience_matched, c.audience_sms, c.audience_email,
		c.audience_languages, c.longest_first_name, c.longest_last_name, c.longest_city, c.scheduled_at, c.sending_started_at, c.completed_at,
		c.created_at, c.updated_at,
		cv.id::text, cv.version_number, cv.segment_revision, cv.status, cv.source, cv.sms_body, cv.email_subject,
		cv.email_body, cv.validation_errors, cv.llm_attempts, cv.worst_case_sms_runes, cv.worst_case_subject_runes,
		cv.en_sms_body, cv.en_email_subject, cv.en_email_body, cv.worst_case_en_sms_runes, cv.worst_case_en_subject_runes,
		cv.created_at,
		a.id::text, a.content_version_id::text, a.approved_at
	FROM campaigns c
	LEFT JOIN LATERAL (
		SELECT * FROM content_versions v
		WHERE v.campaign_id = c.id AND v.status = 'validated' AND v.segment_revision = c.segment_revision
		ORDER BY v.version_number DESC
		LIMIT 1
	) cv ON true
	LEFT JOIN LATERAL (
		SELECT * FROM approvals p
		WHERE p.campaign_id = c.id AND p.revoked_at IS NULL
		LIMIT 1
	) a ON true`

func (s *CampaignStore) GetCampaign(ctx context.Context, id string) (campaign.Campaign, error) {
	rows, err := s.db.Query(ctx, campaignSelect+` WHERE c.id = $1`, id)
	if err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: get campaign: %w", err)
	}
	cs, err := scanCampaigns(rows)
	if err != nil {
		return campaign.Campaign{}, err
	}
	if len(cs) == 0 {
		return campaign.Campaign{}, campaign.ErrCampaignNotFound
	}
	return cs[0], nil
}

func (s *CampaignStore) ListCampaigns(ctx context.Context, limit, offset int) ([]campaign.Campaign, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM campaigns`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count campaigns: %w", err)
	}
	rows, err := s.db.Query(ctx, campaignSelect+` ORDER BY c.created_at DESC, c.id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list campaigns: %w", err)
	}
	cs, err := scanCampaigns(rows)
	if err != nil {
		return nil, 0, err
	}
	return cs, total, nil
}

func (s *CampaignStore) Longest(ctx context.Context, r segment.Rule, ref time.Time) (content.Longest, error) {
	return longestValues(ctx, s.db, r, ref)
}

func (s *CampaignStore) SaveSegment(ctx context.Context, id string, w campaign.SegmentWrite) (campaign.Campaign, error) {
	rule, err := w.Rule.MarshalJSON()
	if err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
	}
	// P8 (D63): the per-channel language split, NULL when the counter did not provide it.
	var languages []byte
	if w.Audience.Languages != nil {
		if languages, err = json.Marshal(w.Audience.Languages); err != nil {
			return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return campaign.Campaign{}, err
	}
	// Only the generation that still holds the lease may store its segment (D52).
	if err := lc.CheckGenerationOwner(w.LeaseToken); err != nil {
		return campaign.Campaign{}, err
	}
	next, err := campaign.Transition(lc.State, campaign.EventSegmentGenerated)
	if err != nil {
		return campaign.Campaign{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE approvals SET revoked_at = $2, revoke_reason = 'segment_changed'
		WHERE campaign_id = $1 AND revoked_at IS NULL`, id, w.GeneratedAt); err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE campaigns SET
			state = $2, segment_rule = $3, segment_reference_time = $4, segment_revision = segment_revision + 1,
			segment_llm_attempts = $5, segment_generated_at = $6,
			audience_matched = $7, audience_sms = $8, audience_email = $9,
			longest_first_name = $10, longest_last_name = $11, longest_city = $12,
			audience_languages = $13::jsonb,
			approved_content_version_id = NULL, updated_at = $6
		WHERE id = $1`,
		id, string(next), string(rule), w.ReferenceTime, w.LLMAttempts, w.GeneratedAt,
		w.Audience.Matched, w.Audience.SMS, w.Audience.Email,
		w.LongestValues.FirstName, w.LongestValues.LastName, w.LongestValues.City, nullableJSON(languages)); err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: save segment: %w", err)
	}
	return s.GetCampaign(ctx, id)
}

// ---- scanning ----

func scanCampaigns(rows pgx.Rows) ([]campaign.Campaign, error) {
	defer rows.Close()
	var out []campaign.Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read campaigns: %w", err)
	}
	return out, nil
}

func scanCampaign(row pgx.Row) (campaign.Campaign, error) {
	var (
		c                                           campaign.Campaign
		state                                       string
		rule                                        []byte
		refTime, generatedAt, sendingAt, completeAt *time.Time
		scheduledAt                                 *time.Time
		revision                                    int
		llmAttempts                                 *int
		matched, sms, email                         *int
		audienceLanguages                           []byte
		longFirst, longLast, longCity               *int

		cvID, cvStatus, cvSource, cvSMS, cvSubject, cvBody *string
		cvNumber, cvRevision, cvAttempts, cvWCSMS, cvWCSub *int
		cvErrors                                           []byte
		cvENSMS, cvENSubject, cvENBody                     *string
		cvWCENSMS, cvWCENSub                               *int
		cvCreated                                          *time.Time

		apID, apVersion *string
		apAt            *time.Time
	)
	err := row.Scan(&c.ID, &c.Brief, &state, &rule, &refTime, &revision,
		&llmAttempts, &generatedAt, &matched, &sms, &email,
		&audienceLanguages, &longFirst, &longLast, &longCity, &scheduledAt, &sendingAt, &completeAt,
		&c.CreatedAt, &c.UpdatedAt,
		&cvID, &cvNumber, &cvRevision, &cvStatus, &cvSource, &cvSMS, &cvSubject,
		&cvBody, &cvErrors, &cvAttempts, &cvWCSMS, &cvWCSub,
		&cvENSMS, &cvENSubject, &cvENBody, &cvWCENSMS, &cvWCENSub,
		&cvCreated,
		&apID, &apVersion, &apAt)
	if err != nil {
		return campaign.Campaign{}, fmt.Errorf("postgres: read campaign: %w", err)
	}
	c.State = campaign.State(state)
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	c.ScheduledAt = utcPtr(scheduledAt)
	c.SendingStartedAt = utcPtr(sendingAt)
	c.CompletedAt = utcPtr(completeAt)

	if rule != nil && refTime != nil {
		c.Segment = &campaign.Segment{
			Rule:          canonicalRule(rule),
			Audience:      segment.Audience{Matched: deref(matched), SMS: deref(sms), Email: deref(email)},
			LongestValues: content.Longest{FirstName: deref(longFirst), LastName: deref(longLast), City: deref(longCity)},
			ReferenceTime: refTime.UTC(),
			Revision:      revision,
			LLMAttempts:   deref(llmAttempts),
		}
		if len(audienceLanguages) > 0 {
			var langs segment.AudienceLanguages
			if err := json.Unmarshal(audienceLanguages, &langs); err != nil {
				return campaign.Campaign{}, fmt.Errorf("postgres: read audience languages: %w", err)
			}
			c.Segment.Audience.Languages = &langs
		}
		if generatedAt != nil {
			c.Segment.GeneratedAt = generatedAt.UTC()
		}
	}

	if cvID != nil {
		v := &campaign.ContentVersion{
			ID: *cvID, CampaignID: c.ID, VersionNumber: deref(cvNumber), SegmentRevision: deref(cvRevision),
			Status: campaign.ContentStatus(deref(cvStatus)), Source: campaign.ContentSource(deref(cvSource)),
			Content: content.Content{
				SMS:   content.SMS{Body: deref(cvSMS)},
				Email: content.Email{Subject: deref(cvSubject), Body: deref(cvBody)},
				EN:    englishOf(cvENSMS, cvENSubject, cvENBody),
			},
			ValidationErrors: []validation.FieldError{},
			LLMAttempts:      cvAttempts,
		}
		if cvCreated != nil {
			v.CreatedAt = cvCreated.UTC()
		}
		if cvWCSMS != nil && cvWCSub != nil {
			v.WorstCase = &content.WorstCase{SMSRunes: *cvWCSMS, SubjectRunes: *cvWCSub}
			if cvWCENSMS != nil && cvWCENSub != nil {
				v.WorstCase.EN = &content.WorstCaseLang{SMSRunes: *cvWCENSMS, SubjectRunes: *cvWCENSub}
			}
		}
		if len(cvErrors) > 0 {
			if err := json.Unmarshal(cvErrors, &v.ValidationErrors); err != nil {
				return campaign.Campaign{}, fmt.Errorf("postgres: read content version errors: %w", err)
			}
			if v.ValidationErrors == nil {
				v.ValidationErrors = []validation.FieldError{}
			}
		}
		c.LatestContentVersion = v
	}

	if apID != nil && apVersion != nil && apAt != nil {
		c.Approval = &campaign.Approval{ID: *apID, ContentVersionID: *apVersion, ApprovedAt: apAt.UTC()}
	}
	return c, nil
}

// canonicalRule re-emits a stored rule (jsonb reorders keys and adds spaces) in the canonical
// form of segment.Rule.MarshalJSON: {"field","op","value"} order, compact.
func canonicalRule(stored []byte) json.RawMessage {
	doc := append(append([]byte(`{"rule":`), stored...), '}')
	if r, err := segment.Parse(doc); err == nil {
		if b, err := r.MarshalJSON(); err == nil {
			return b
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, stored); err != nil {
		return json.RawMessage(stored)
	}
	return buf.Bytes()
}

// englishOf rebuilds Content.EN from the en_* columns (all NULL or all set: 0009 check).
func englishOf(sms, subject, body *string) *content.English {
	if sms == nil || subject == nil || body == nil {
		return nil
	}
	return &content.English{SMS: content.SMS{Body: *sms}, Email: content.Email{Subject: *subject, Body: *body}}
}

// nullableJSON maps an empty document to SQL NULL.
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
