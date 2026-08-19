// Package lease provides Postgres-backed leader election for Paladin
// background workers.
//
// Why not pg_try_advisory_lock: a session-scoped advisory lock releases
// only when the connection drops; a stuck-but-alive process holds the
// lock indefinitely. This package forces the leader to prove liveness by
// renewing `expires_at`, and surfaces a fence token (`generation`) that
// callers thread through every write they perform — see migrations/
// 015_worker_leases.sql for the table shape and rationale.
//
// Why not coordination.k8s.io/Lease: workers operate on Postgres state,
// not k8s state. Using a Lease in kube-apiserver couples worker mode to
// k8s availability, RBAC sprawls, and local-dev / docker-compose paths
// need a fallback anyway. The DB we already require is the right home.
//
// Typical use:
//
//	l := lease.New(pool, lease.Config{
//	    Name:          "reaper",
//	    HolderID:      podUUID,
//	    TTL:           30 * time.Second,
//	    RenewInterval: 10 * time.Second,
//	    Logger:        log.Named("lease.reaper"),
//	})
//	err := l.Run(ctx, func(workCtx context.Context, gen int64) error {
//	    // Run the worker. workCtx is cancelled if renewal stalls;
//	    // pass `gen` into UPDATEs as a fence-token check.
//	    return reaper.Run(workCtx, gen)
//	})
package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Config wires a Lease. Defaults are set by New for unspecified fields.
type Config struct {
	// Name is the logical worker class — primary key in worker_leases.
	// Stable across releases; namespacing convention is dotted lowercase
	// (e.g. "reaper", "lifecycle", "reconciler.bucket").
	Name string

	// HolderID identifies this process / pod. Use a UUID generated at
	// boot. Surfaced in worker_leases.holder_id for ops triage.
	HolderID uuid.UUID

	// HolderMeta is shipped to worker_leases.holder_meta on every claim
	// so `kubectl exec ... psql` reveals which pod / version holds. Keep
	// small (<2KB).
	HolderMeta map[string]string

	// TTL is how long a claim survives without renewal. Smaller means
	// faster failover after a crash; bigger means more tolerance to
	// transient renewer stalls. 30s is the Paladin default.
	TTL time.Duration

	// RenewInterval is the cadence at which the leader updates expires_at.
	// Must be < TTL/2 (recommended: TTL/3) so one missed tick doesn't
	// surrender the lease.
	RenewInterval time.Duration

	// PollInterval is how often a non-leader retries claiming. Defaults
	// to TTL so wakeup happens once per natural expiry window.
	PollInterval time.Duration

	// Logger is mandatory; use a no-op zap if logging is undesired.
	Logger *zap.Logger
}

// Lease holds a logical worker leadership claim.
type Lease struct {
	cfg  Config
	pool *pgxpool.Pool
}

// New constructs a Lease. Validates required fields; populates sensible
// defaults for the rest.
func New(pool *pgxpool.Pool, cfg Config) (*Lease, error) {
	// Config validation runs first so unit tests can exercise it without
	// standing up a Postgres pool; the infra check (pool != nil) is a
	// wiring concern and lives at the bottom.
	if cfg.Name == "" {
		return nil, errors.New("lease: Name required")
	}
	if cfg.HolderID == uuid.Nil {
		return nil, errors.New("lease: HolderID required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("lease: Logger required")
	}
	if cfg.TTL == 0 {
		cfg.TTL = 30 * time.Second
	}
	if cfg.RenewInterval == 0 {
		cfg.RenewInterval = cfg.TTL / 3
	}
	if cfg.RenewInterval >= cfg.TTL/2 {
		return nil, fmt.Errorf("lease: RenewInterval (%s) must be < TTL/2 (%s)", cfg.RenewInterval, cfg.TTL/2)
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = cfg.TTL
	}
	if pool == nil {
		return nil, errors.New("lease: pool required")
	}
	return &Lease{cfg: cfg, pool: pool}, nil
}

// claim attempts to acquire or renew the lease. Returns (gen, expires, ok).
// ok=false means another holder is alive and we should not run yet.
func (l *Lease) claim(ctx context.Context) (int64, time.Time, bool, error) {
	meta, err := json.Marshal(l.cfg.HolderMeta)
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("lease: marshal meta: %w", err)
	}

	// One round-trip: insert if absent, otherwise update iff (we already
	// hold it) or (it has expired). RETURNING gives us the post-write
	// state directly. Using interval arithmetic on the server avoids
	// clock-skew between app and DB.
	const stmt = "\nINSERT INTO worker_leases AS w\n    (name, holder_id, holder_meta, acquired_at, renewed_at, expires_at, generation)\nVALUES\n    ($1, $2, $3::jsonb, NOW(), NOW(), NOW() + ($4::bigint || ' microseconds')::interval, 1)\nON CONFLICT (name) DO UPDATE\nSET holder_id   = EXCLUDED.holder_id,\n    holder_meta = EXCLUDED.holder_meta,\n    acquired_at = CASE\n        WHEN w.holder_id = EXCLUDED.holder_id THEN w.acquired_at\n        ELSE NOW()\n    END,\n    renewed_at  = NOW(),\n    expires_at  = NOW() + ($4::bigint || ' microseconds')::interval,\n    generation  = CASE\n        WHEN w.holder_id = EXCLUDED.holder_id THEN w.generation\n        ELSE w.generation + 1\n    END\nWHERE  w.holder_id = EXCLUDED.holder_id\n   OR  w.expires_at < NOW()\nRETURNING generation, expires_at" //nolint:dupword // adjacent NOW() are distinct columns (acquired_at, renewed_at)

	row := l.pool.QueryRow(ctx, stmt, l.cfg.Name, l.cfg.HolderID, meta, l.cfg.TTL.Microseconds())
	var gen int64
	var exp time.Time
	if err := row.Scan(&gen, &exp); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Another live holder; INSERT was no-op. Not an error.
			return 0, time.Time{}, false, nil
		}
		return 0, time.Time{}, false, fmt.Errorf("lease: claim: %w", err)
	}
	return gen, exp, true, nil
}

// release marks the lease expired so a new claimant doesn't have to wait
// for the natural TTL. Best-effort: errors are logged, not returned, since
// release is shutdown-path code.
func (l *Lease) release(ctx context.Context) {
	const stmt = `
UPDATE worker_leases
SET    expires_at = NOW() - interval '1 second'
WHERE  name = $1 AND holder_id = $2;
`
	if _, err := l.pool.Exec(ctx, stmt, l.cfg.Name, l.cfg.HolderID); err != nil {
		l.cfg.Logger.Warn("release lease", zap.String("name", l.cfg.Name), zap.Error(err))
	}
}

// Run is the typical entry point. It loops:
//   - try to claim
//   - if leader: spawn a renewer + invoke `do` with a context that
//     dies if renewal falls behind expires_at; on `do` returning the
//     loop exits (success) or restarts (failure)
//   - if not leader: sleep PollInterval and retry
//
// Returns when ctx is cancelled or `do` returns a non-retryable error.
// `do` should return ctx.Err() on cancellation so the loop exits cleanly.
func (l *Lease) Run(ctx context.Context, do func(workCtx context.Context, generation int64) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		gen, exp, ok, err := l.claim(ctx)
		if err != nil {
			// Transient DB errors should not crash the worker; back off
			// and retry once the cluster recovers.
			l.cfg.Logger.Warn("claim failed; backing off", zap.Error(err))
			if !sleepCtx(ctx, l.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		if !ok {
			// Someone else is leader. Sleep and try again at PollInterval.
			if !sleepCtx(ctx, l.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}

		l.cfg.Logger.Info("acquired lease",
			zap.String("name", l.cfg.Name),
			zap.Int64("generation", gen),
			zap.Time("expires_at", exp),
		)

		// We are leader. Run the work in a child context that the
		// renewer cancels the moment it can no longer prove we hold the
		// lease: either two consecutive renewals failed, or the last
		// confirmed expires_at passed without a successful renewal. On
		// healthy renewals the in-process deadline moves forward with
		// each claim, so the worker no longer self-terminates every TTL.
		workCtx, cancelWork := context.WithCancel(ctx)
		renewerDone := make(chan struct{})
		go l.renewer(workCtx, cancelWork, exp, renewerDone, l.claim)

		err = do(workCtx, gen)

		cancelWork()
		<-renewerDone

		// Best-effort release so the next pod sees expiry instantly.
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
		l.release(releaseCtx)
		cancelRelease()

		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}

		// `do` exited because either ctx (parent) was cancelled or the
		// lease deadline lapsed (renewer failure). The next loop iteration
		// either bails out (ctx.Err) or re-attempts the claim.
	}
}

// renewer keeps expires_at fresh and enforces the lease deadline
// in-process: work is cancelled when renewals persistently fail or when
// the last DB-confirmed expires_at passes. Each successful renewal
// pushes the in-process deadline forward to the new expires_at.
// claimFunc is the renewer's only dependency on the DB — extracted as a
// parameter so the renewer's deadline/failure logic is unit-testable
// with a fake (the pgx-backed l.claim is passed in production).
type claimFunc func(ctx context.Context) (int64, time.Time, bool, error)

func (l *Lease) renewer(workCtx context.Context, cancelWork context.CancelFunc, initialExp time.Time, done chan<- struct{}, claim claimFunc) {
	defer close(done)
	ticker := time.NewTicker(l.cfg.RenewInterval)
	defer ticker.Stop()
	// expiry mirrors the DB lease deadline. It only ever fires if
	// renewals stopped landing — a healthy renewer resets it before
	// each confirmed expires_at arrives.
	expiry := time.NewTimer(time.Until(initialExp))
	defer expiry.Stop()

	consecutive := 0
	for {
		select {
		case <-workCtx.Done():
			return
		case <-expiry.C:
			// The last confirmed expires_at passed without a successful
			// renewal. We can no longer prove leadership; stop the work
			// so another pod can take over.
			l.cfg.Logger.Warn("lease deadline passed without renewal; stopping work",
				zap.String("name", l.cfg.Name),
			)
			cancelWork()
			return
		case <-ticker.C:
			// Renewal uses the parent (still-live on shutdown only briefly)
			// pool; bound it with a short timeout so a stuck DB doesn't
			// pin the goroutine.
			renewCtx, cancel := context.WithTimeout(workCtx, l.cfg.RenewInterval/2)
			_, exp, ok, err := claim(renewCtx)
			cancel()
			if err != nil || !ok {
				consecutive++
				l.cfg.Logger.Warn("renew failed",
					zap.String("name", l.cfg.Name),
					zap.Int("consecutive", consecutive),
					zap.Bool("still_leader", ok),
					zap.Error(err),
				)
				// Two failures in a row means the natural deadline is
				// closer than another renewer cycle could move it; let
				// workCtx hard-expire to free the leader slot and stop
				// pretending we still hold it.
				if consecutive >= 2 {
					cancelWork()
					return
				}
				continue
			}
			consecutive = 0
			// Successful renew: push the in-process deadline forward to
			// the freshly confirmed expires_at.
			if !expiry.Stop() {
				// Timer already fired between selects; drain so Reset
				// arms cleanly. (The <-expiry.C arm would have won the
				// next select and cancelled work — this renewal beat it.)
				select {
				case <-expiry.C:
				default:
				}
			}
			expiry.Reset(time.Until(exp))
		}
	}
}

// sleepCtx waits d or until ctx is done, whichever first, with a
// stoppable timer (time.After can't be stopped and would strand the
// timer until it fires). Returns false if ctx ended the wait.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Generation reads the current generation for the lease without claiming
// it. Useful for callers that want to assert their fence token before a
// long batch — though the more common pattern is to receive `generation`
// via the Run callback.
func (l *Lease) Generation(ctx context.Context) (int64, bool, error) {
	const stmt = `SELECT generation, expires_at > NOW() FROM worker_leases WHERE name = $1`
	var gen int64
	var live bool
	err := l.pool.QueryRow(ctx, stmt, l.cfg.Name).Scan(&gen, &live)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return gen, live, nil
}
