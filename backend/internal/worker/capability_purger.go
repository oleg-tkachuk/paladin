// CapabilityPurger drops the capability records — with their revocation
// entries — that have been expired for at least ExpiredFor (limes
// Store.Purge), then releases expired holds and drops the usage rows of the
// capabilities purged, and, when asked, the ledger's charges older than its
// retention (limes LedgerHousekeeping). Bounds the records and the denylist over time without
// affecting verifier correctness: the retention is never shorter than
// limes.MinPurgeRetention, past which no verifier accepts the token.
//
// Lives next to the other housekeeping workers but in its own file
// because it needs the limes.Store interface, which the
// surrounding housekeeping workers don't import. Keeping it isolated
// also lets the cmd/server build_jobs registration treat it as a
// distinct cadence (default 1h, separate from audit log / operations
// retention).
package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
)

// ReplayPurger drops DPoP proof ids that can no longer be replayed, at most
// a bounded batch per call, and reports how many it dropped.
type ReplayPurger interface {
	PurgeExpired(ctx context.Context) (int64, error)
}

// ChargePurger removes a batch of the ledger's old charges: limes's
// meter.LedgerHousekeeping, which pgstore's Ledger is.
type ChargePurger = meter.LedgerHousekeeping

// DefaultCapabilityExpiredFor is how long a capability stays on record past
// its expiry before the purger drops it, when ExpiredFor is unset. A cost
// reported later than that is charged to nothing: the meter refuses a
// capability not on record.
const DefaultCapabilityExpiredFor = 24 * time.Hour

// CapabilityPurger periodically calls Store.Purge and, when Usage is wired,
// releases expired holds and sweeps the usage rows of capabilities no longer
// on record; when Replay is wired, it drops expired DPoP proof ids. Disabled
// when Interval <= 0.
type CapabilityPurger struct {
	Store      limes.Store
	Usage      meter.UsageStore
	Replay     ReplayPurger
	Interval   time.Duration
	ExpiredFor time.Duration
	// Ledger, when wired, has the charges taken more than ChargesOlderThan
	// ago removed from the ledger (limes LedgerHousekeeping). Unwired, the
	// ledger keeps every charge.
	Ledger           ChargePurger
	ChargesOlderThan time.Duration
	Logger           *zap.Logger
}

// Run blocks until ctx is cancelled or the store returns a fatal error.
// Per-tick errors are logged and the loop continues so a transient DB
// blip doesn't take the whole worker pod offline.
func (p *CapabilityPurger) Run(ctx context.Context) error {
	if p.Interval <= 0 {
		return nil
	}
	if p.ExpiredFor <= 0 {
		p.ExpiredFor = DefaultCapabilityExpiredFor
	}
	return RunTicker(ctx, "capability_purger", p.Interval, p.tick)
}

// tick runs one purge pass and returns its failure, if any, for the tick
// metric.
func (p *CapabilityPurger) tick(ctx context.Context) error {
	// The purger runs on a timer, not on a request, so it carries no
	// principal: scoped to no tenant, every statement would match zero rows
	// and the job would report success while the tables grow without bound.
	// Housekeeping reads and writes every tenant's rows (pgstore's
	// WithAllTenants); a platform-wide read would remove nothing.
	ctx = pgstore.WithAllTenants(ctx)

	// Each housekeeping call handles at most one batch; a batch short of
	// the limit is the last, and a backlog drains over several.
	for {
		res, err := p.Store.Purge(ctx, limes.PurgeRequest{ExpiredFor: p.ExpiredFor})
		if err != nil {
			p.log().Warn("failed to purge expired capabilities", zap.Error(err))
			return err
		}
		if res.Purged > 0 {
			p.log().Info("purged expired capabilities", zap.Int64("records", res.Purged))
		}
		if res.Purged < limes.DefaultHousekeepingLimit {
			break
		}
	}
	var tickErr error
	if p.Usage != nil {
		// Expired reservations first: their holds keep counting against
		// every ceiling until released.
		for {
			res, err := p.Usage.ReleaseExpired(ctx, meter.ReleaseExpiredRequest{})
			if err != nil {
				p.log().Warn("failed to release expired capability reservations", zap.Error(err))
				tickErr = err
				break
			}
			if res.Released > 0 {
				p.log().Info("released expired capability reservations", zap.Int64("reservations", res.Released))
			}
			if res.Released < limes.DefaultHousekeepingLimit {
				break
			}
		}
		// Then what the meter keeps for the capabilities just purged: their
		// holds go back to their ancestors and tenants, their rows go.
		for {
			res, err := p.Usage.PurgeOrphans(ctx, meter.PurgeOrphansRequest{})
			if err != nil {
				p.log().Warn("failed to purge capability usage orphans", zap.Error(err))
				tickErr = err
				break
			}
			if res.Purged > 0 {
				p.log().Info("purged capability usage orphans", zap.Int64("capabilities", res.Purged))
			}
			if res.Purged < limes.DefaultHousekeepingLimit {
				break
			}
		}
	}
	if p.Ledger != nil {
		for {
			res, err := p.Ledger.PurgeCharges(ctx, meter.PurgeChargesRequest{OlderThan: p.ChargesOlderThan})
			if err != nil {
				p.log().Warn("failed to purge old capability charges", zap.Error(err))
				tickErr = err
				break
			}
			if res.Purged > 0 {
				p.log().Info("purged old capability charges", zap.Int64("charges", res.Purged))
			}
			if res.Purged < limes.DefaultHousekeepingLimit {
				break
			}
		}
	}
	if p.Replay != nil {
		// The table is not tenant-scoped, so the scope above neither helps
		// nor hurts here.
		for {
			n, err := p.Replay.PurgeExpired(ctx)
			if err != nil {
				p.log().Warn("failed to purge expired DPoP proof ids", zap.Error(err))
				tickErr = err
				break
			}
			if n == 0 {
				break
			}
			p.log().Info("purged expired DPoP proof ids", zap.Int64("ids", n))
		}
	}
	return tickErr
}

func (p *CapabilityPurger) log() *zap.Logger {
	if p.Logger == nil {
		return zap.NewNop()
	}
	return p.Logger
}
