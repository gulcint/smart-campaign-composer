package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// audienceFrom is the fixed skeleton of segment-dsl.md §5; the compiled predicate is appended
// after WHERE. The predicate holds only constant SQL fragments produced by segment.Compile and
// $n placeholders: no rule value is ever part of the SQL text.
const audienceFrom = `
	FROM customers c
	JOIN customer_stats_at($1) s ON s.customer_id = c.id
	WHERE `

// NewAudience returns the segment.Counter backed by PostgreSQL. Channel consent is applied here,
// by the system (segment-dsl.md §6): a customer counts for a channel only with opt-in AND the
// contact address present.
func NewAudience(db *pgxpool.Pool) segment.Counter {
	return audience{db: db}
}

type audience struct{ db *pgxpool.Pool }

func (a audience) Count(ctx context.Context, r segment.Rule, ref time.Time) (segment.Audience, error) {
	comp, err := segment.Compile(r, ref)
	if err != nil {
		return segment.Audience{}, err
	}
	// One query: the channel counts and their split by customers.language (P8, D63). language is
	// 'tr' or 'en' (CHECK), so tr + en == the channel count.
	q := `SELECT count(*),
		count(*) FILTER (WHERE c.sms_opt_in AND c.phone IS NOT NULL),
		count(*) FILTER (WHERE c.email_opt_in AND c.email IS NOT NULL),
		count(*) FILTER (WHERE c.sms_opt_in AND c.phone IS NOT NULL AND c.language = 'tr'),
		count(*) FILTER (WHERE c.sms_opt_in AND c.phone IS NOT NULL AND c.language = 'en'),
		count(*) FILTER (WHERE c.email_opt_in AND c.email IS NOT NULL AND c.language = 'tr'),
		count(*) FILTER (WHERE c.email_opt_in AND c.email IS NOT NULL AND c.language = 'en')` + audienceFrom + comp.Where
	var out segment.Audience
	var langs segment.AudienceLanguages
	if err := a.db.QueryRow(ctx, q, comp.Args...).Scan(&out.Matched, &out.SMS, &out.Email,
		&langs.SMS.TR, &langs.SMS.EN, &langs.Email.TR, &langs.Email.EN); err != nil {
		return segment.Audience{}, fmt.Errorf("postgres: count audience: %w", err)
	}
	out.Languages = &langs
	return out, nil
}

// queryer is the read side shared by *pgxpool.Pool and pgx.Tx.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// longestValues computes content.Longest over the customers matched by r at ref (before consent),
// with the same rune counting as the content validator.
func longestValues(ctx context.Context, db queryer, r segment.Rule, ref time.Time) (content.Longest, error) {
	comp, err := segment.Compile(r, ref)
	if err != nil {
		return content.Longest{}, err
	}
	rows, err := db.Query(ctx, `SELECT c.first_name, c.last_name, c.city`+audienceFrom+comp.Where, comp.Args...)
	if err != nil {
		return content.Longest{}, fmt.Errorf("postgres: longest values: %w", err)
	}
	defer rows.Close()
	var l content.Longest
	matched := false
	for rows.Next() {
		var first, last, city string
		if err := rows.Scan(&first, &last, &city); err != nil {
			return content.Longest{}, fmt.Errorf("postgres: longest values: %w", err)
		}
		matched = true
		l.FirstName = max(l.FirstName, content.RuneCount(content.EffectiveFirstName(first)))
		l.LastName = max(l.LastName, content.RuneCount(last))
		l.City = max(l.City, content.RuneCount(city))
	}
	if err := rows.Err(); err != nil {
		return content.Longest{}, fmt.Errorf("postgres: longest values: %w", err)
	}
	if !matched {
		return content.Longest{FirstName: content.RuneCount(content.FirstNameFallback)}, nil
	}
	return l, nil
}
