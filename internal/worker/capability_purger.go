// CapabilityPurger drops rows from capability_revocations whose
// underlying capability has been expired for at least ExpiredFor.
// Bounds the denylist over time without affecting verifier
// correctness — an expired token can never satisfy the time gate, so
// removing its revocation row is safe.
//
// Lives next to the other housekeeping workers but in its own file
// because it needs the capability.Store interface, which the
// surrounding housekeeping workers don't import. Keeping it isolated
// also lets the cmd/server build_jobs registration treat it as a
// distinct cadence (default 1h, separate from audit log / operations
// retention).
package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/capability"
)

// CapabilityPurger periodically calls Store.PurgeExpired. Disabled
// when Interval <= 0.
type CapabilityPurger struct {
	Store      capability.Store
	Interval   time.Duration
	ExpiredFor time.Duration
	Logger     *zap.Logger
}

// Run blocks until ctx is cancelled or the store returns a fatal error.
// Per-tick errors are logged and the loop continues so a transient DB
// blip doesn't take the whole worker pod offline.
func (p *CapabilityPurger) Run(ctx context.Context) error {
	if p.Interval <= 0 {
		return nil
	}
	if p.ExpiredFor <= 0 {
		p.ExpiredFor = 24 * time.Hour
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
				p.log().Warn("failed to purge capability revocations", zap.Error(err))
				continue
			}
			if n > 0 {
				p.log().Info("purged expired capability revocations", zap.Int64("rows", n))
			}
		}
	}
}

func (p *CapabilityPurger) log() *zap.Logger {
	if p.Logger == nil {
		return zap.NewNop()
	}
	return p.Logger
}
