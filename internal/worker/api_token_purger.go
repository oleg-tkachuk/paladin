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

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
)

// APITokenPurger periodically calls api_token.Store.PurgeExpired.
// Disabled when Interval <= 0.
type APITokenPurger struct {
	Store      api_token.Store
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
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			n, err := p.Store.PurgeExpired(ctx, p.ExpiredFor)
			if err != nil {
				p.log().Warn("failed to purge api tokens", zap.Error(err))
				continue
			}
			if n > 0 {
				p.log().Info("purged expired api tokens", zap.Int64("rows", n))
			}
		}
	}
}

func (p *APITokenPurger) log() *zap.Logger {
	if p.Logger == nil {
		return zap.NewNop()
	}
	return p.Logger
}
