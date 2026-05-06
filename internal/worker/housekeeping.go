// Housekeeping workers — periodic background tasks that keep the IAM and
// audit tables tidy.
//
//   - RefreshTokenPurger — drops expired refresh-token rows so the table
//     stays bounded. Revoked-but-not-expired rows are kept for forensic
//     audit; expired ones can never be presented again.
//
//   - ApiKeyExpirer — flips `revoked = true` on api_keys whose `expires_at`
//     has passed. Keeps the row for audit (no DELETE).
//
//   - AuditLogPurger — deletes audit_log rows older than the configured
//     retention window. Disabled when AuditLogTTL == 0.
//
// All three share the same scheduler shape: tick on Interval, log errors,
// continue. They are NOT redundant with DB-side TTLs — running on Postgres
// 17 you'd use pg_cron or partitioning instead. We keep the worker form so
// PALADIN runs identically on managed Postgres tiers without extensions.
package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
)

// RefreshTokenPurger removes expired refresh tokens.
type RefreshTokenPurger struct {
	Repo     authstore.RefreshTokenRepository
	Interval time.Duration
	Logger   *zap.Logger
}

func (r *RefreshTokenPurger) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = 1 * time.Hour
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			n, err := r.Repo.PurgeExpired(ctx, time.Now().UTC())
			if err != nil {
				r.log().Warn("failed to purge refresh tokens", zap.Error(err))
				continue
			}
			if n > 0 {
				r.log().Info("purged expired refresh tokens", zap.Int64("rows", n))
			}
		}
	}
}

func (r *RefreshTokenPurger) log() *zap.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}

// ApiKeyExpirer flips revoked=true on api_keys whose expires_at has passed.
// Implementations rely on a query that filters by expires_at < now() AND
// revoked = false; we expose the repo seam narrowly via this interface.
type ApiKeyExpirer struct {
	Repo     ApiKeyExpirerRepo
	Interval time.Duration
	Logger   *zap.Logger
}

// ApiKeyExpirerRepo is the narrow read+revoke seam this worker needs.
// Postgres adapter satisfies it with a tiny query.
type ApiKeyExpirerRepo interface {
	ListExpired(ctx context.Context, at time.Time, limit int32) ([]authstore.ApiKey, error)
	Revoke(ctx context.Context, apiKeyID uuid.UUID) error
}

func (a *ApiKeyExpirer) Run(ctx context.Context) error {
	if a.Interval <= 0 {
		a.Interval = 1 * time.Hour
	}
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			a.tick(ctx)
		}
	}
}

func (a *ApiKeyExpirer) tick(ctx context.Context) {
	keys, err := a.Repo.ListExpired(ctx, time.Now().UTC(), 200)
	if err != nil {
		a.log().Warn("failed to list expired api keys", zap.Error(err))
		return
	}
	for _, k := range keys {
		if err := a.Repo.Revoke(ctx, k.ApiKeyID); err != nil {
			a.log().Warn("failed to revoke expired api key",
				zap.String("api_key_id", k.ApiKeyID.String()),
				zap.Error(err))
			continue
		}
		a.log().Info("auto-revoked expired api key",
			zap.String("api_key_id", k.ApiKeyID.String()))
	}
}

func (a *ApiKeyExpirer) log() *zap.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return zap.NewNop()
}

// AuditLogPurger drops audit_log rows older than TTL. Disabled when TTL == 0.
type AuditLogPurger struct {
	Purger   AuditPurgerRepo
	TTL      time.Duration
	Interval time.Duration
	Logger   *zap.Logger
}

// AuditPurgerRepo is the narrow seam — one method on the audit store.
type AuditPurgerRepo interface {
	PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

func (p *AuditLogPurger) Run(ctx context.Context) error {
	if p.TTL <= 0 {
		// Disabled — return without ticking. Caller treats nil error as
		// "worker exited cleanly" and proceeds.
		return nil
	}
	if p.Interval <= 0 {
		p.Interval = 24 * time.Hour
	}
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			cutoff := time.Now().UTC().Add(-p.TTL)
			n, err := p.Purger.PurgeOlderThan(ctx, cutoff)
			if err != nil {
				p.log().Warn("failed to purge audit log", zap.Error(err))
				continue
			}
			if n > 0 {
				p.log().Info("purged audit log entries", zap.Int64("rows", n), zap.Time("older_than", cutoff))
			}
		}
	}
}

func (p *AuditLogPurger) log() *zap.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return zap.NewNop()
}
