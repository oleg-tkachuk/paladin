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

	"github.com/jackc/pgx/v5"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
)

// CapabilityPurger periodically calls Store.PurgeExpired and, when
// Usage is wired, sweeps orphan capability_usage rows whose parent
// capability is gone. Disabled when Interval <= 0.
type CapabilityPurger struct {
	Store      capability.Store
	Usage      capability.UsageStore[pgx.Tx]
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
	return RunTicker(ctx, "capability_purger", p.Interval, func(ctx context.Context) error {
		// The purger runs on a timer, not on a request, so it carries no
		// principal and the RLS pool has no session tenant to install.
		// Both tables it sweeps are tenant-scoped, which without this flag
		// means every statement matches zero rows and the job reports
		// success while the tables grow without bound. The flag widens
		// USING only — a purger can remove rows across tenants, and still
		// cannot write into one.
		ctx = auth.WithCrossTenantRead(ctx)

		n, err := p.Store.PurgeExpired(ctx, p.ExpiredFor)
		if err != nil {
			p.log().Warn("failed to purge capability revocations", zap.Error(err))
			return err
		}
		if n > 0 {
			p.log().Info("purged expired capability revocations", zap.Int64("rows", n))
		}
		var tickErr error
		if p.Usage != nil {
			// Expired reservations first: their holds keep counting against
			// every ceiling until released, and the orphan sweep below may
			// drop the usage rows they would be released from.
			for {
				n, err := p.Usage.ReleaseExpired(ctx)
				if err != nil {
					p.log().Warn("failed to release expired capability reservations", zap.Error(err))
					tickErr = err
					break
				}
				if n == 0 {
					break
				}
				p.log().Info("released expired capability reservations", zap.Int64("reservations", n))
			}
			// Loop until 0 — bounded SQL keeps each statement
			// short, but a backlog (operator just ran a mass
			// revoke) needs more than one batch to drain.
			for {
				n, err := p.Usage.PurgeOrphans(ctx)
				if err != nil {
					p.log().Warn("failed to purge capability usage orphans", zap.Error(err))
					tickErr = err
					break
				}
				if n == 0 {
					break
				}
				p.log().Info("purged capability usage orphans", zap.Int64("rows", n))
			}
		}
		return tickErr
	})
}

func (p *CapabilityPurger) log() *zap.Logger {
	if p.Logger == nil {
		return zap.NewNop()
	}
	return p.Logger
}
