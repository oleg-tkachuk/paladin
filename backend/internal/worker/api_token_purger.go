// APITokenPurger drops rows from api_tokens whose `expires_at` is past
// the configured grace window. Mirrors CapabilityPurger in shape; lives
// alongside it because the api_token store is structurally similar.
//
// Verifier correctness is unaffected — an expired token can never pass
// the time gate, so removing the row only bounds the table size.
package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin-private/internal/auth/api_token/ratelimit"
)

// APITokenPurger periodically calls api_token.Store.PurgeExpired plus
// (when Limiter is set) ratelimit.Limiter.Sweep. Disabled when
// Interval <= 0.
//
// Two-pronged sweep: dropping expired token rows (no verifier impact —
// expired tokens never match the time gate) AND dropping rate-bucket
// rows older than the sliding window (no impact — verifier never reads
// past 2 buckets). Both use ExpiredFor as the grace; bucket sweep has
// its own minimum of 5 minutes inside the limiter.
type APITokenPurger struct {
	Store      api_token.Store
	Limiter    ratelimit.Limiter
	Interval   time.Duration
	ExpiredFor time.Duration
	Logger     *zap.Logger
}

func (p *APITokenPurger) Run(ctx context.Context) error {
	if p.Interval <= 0 {
		return nil
	}
	if p.ExpiredFor <= 0 {
		// 7 days matches the chart default. Keeps a recently-revoked /
		// expired token's row visible in admin tooling for forensics
		// before the reaper drops it.
		p.ExpiredFor = 7 * 24 * time.Hour
	}
	return RunTicker(ctx, "api_token_purger", p.Interval, func(ctx context.Context) error {
		var tickErr error
		n, err := p.Store.PurgeExpired(ctx, p.ExpiredFor)
		if err != nil {
			p.log().Warn("failed to purge api tokens", zap.Error(err))
			tickErr = err
		} else if n > 0 {
			p.log().Info("purged expired api tokens", zap.Int64("rows", n))
		}
		// Rate-bucket sweep — independent of the row purge so a
		// failure on one doesn't skip the other. Bucket grace is
		// fixed at 5 minutes inside the limiter (the sliding
		// window only ever reads back 2 buckets).
		if p.Limiter != nil {
			if m, err := p.Limiter.Sweep(ctx, 5*time.Minute); err != nil {
				p.log().Warn("failed to sweep api token rate buckets", zap.Error(err))
				if tickErr == nil {
					tickErr = err
				}
			} else if m > 0 {
				p.log().Debug("swept stale rate buckets", zap.Int64("rows", m))
			}
		}
		return tickErr
	})
}

func (p *APITokenPurger) log() *zap.Logger {
	if p.Logger == nil {
		return zap.NewNop()
	}
	return p.Logger
}
