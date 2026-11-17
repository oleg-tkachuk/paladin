package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// RetiredSlugExpirer releases retired tenant slugs whose reservation period
// has run, for worker.SlugReservationExpirer.
type RetiredSlugExpirer struct{ q *sqlc.Queries }

// NewRetiredSlugExpirer wraps q; retired_tenant_slugs carries no RLS policy.
func NewRetiredSlugExpirer(q *sqlc.Queries) *RetiredSlugExpirer {
	return &RetiredSlugExpirer{q: q}
}

// ExpireRetiredSlugs deletes every slug retired at or before retiredBefore
// and returns them.
func (e *RetiredSlugExpirer) ExpireRetiredSlugs(ctx context.Context, retiredBefore time.Time) ([]worker.ExpiredSlug, error) {
	rows, err := e.q.ExpireRetiredTenantSlugs(ctx, pgtype.Timestamptz{Time: retiredBefore, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("expire retired tenant slugs: %w", err)
	}
	out := make([]worker.ExpiredSlug, 0, len(rows))
	for _, r := range rows {
		out = append(out, worker.ExpiredSlug{
			Slug:           r.Slug,
			FormerTenantID: uuidFrom(r.FormerTenantID).String(),
			RetiredAt:      r.RetiredAt.Time,
		})
	}
	return out, nil
}
