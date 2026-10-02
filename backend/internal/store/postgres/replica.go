package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// ReadRouter decides where a lag-tolerant read runs: on the read replica when
// one is configured and keeping up, on the primary otherwise.
//
// Only reads that can tolerate a replica's lag go through it — listing,
// counting and searching objects. Authorization never does: a revoked
// capability, a tightened Cedar policy or a disabled user has to be seen on
// the very next request, and a replica a second behind would still say yes.
// Nor does anything that reads back what the same request just wrote. Those
// paths keep using DB.Queries, which is always the primary.
//
// A router built without a replica routes everything to the primary, so
// callers never branch on whether a replica exists.
type ReadRouter struct {
	primary sqlc.DBTX
	replica sqlc.DBTX

	// lag reports how far the replica is behind; nil with a nil error means
	// "unknown", which is treated as too far. A field, not a method on the
	// pool, so tests can drive the state machine without a standby.
	lag    func(ctx context.Context) (*time.Duration, error)
	maxLag time.Duration
	period time.Duration

	// inSync is false until the first probe says otherwise: a replica that
	// has not been checked yet is not trusted with a read.
	inSync atomic.Bool
	log    *zap.Logger

	// last is the most recent probe's outcome, for the health snapshot.
	mu   sync.Mutex
	last ReplicaStatus
}

// ReplicaStatus is the outcome of the most recent lag probe.
type ReplicaStatus struct {
	InSync bool
	// Lag is nil when it could not be measured (probe failed, or replay
	// with no commit to date).
	Lag *time.Duration
	// Err is the probe's error, if it failed.
	Err error
	// CheckedAt is zero until the first probe has run.
	CheckedAt time.Time
}

// Status returns the most recent probe's outcome. Zero when there is no
// replica or it has not been probed yet.
func (r *ReadRouter) Status() ReplicaStatus {
	if !r.HasReplica() {
		return ReplicaStatus{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// HealthErr describes why reads are not on the replica, nil when they are.
// For the health snapshot, which shows it as a non-critical database
// component: a replica that is down slows nothing — reads are on the
// primary — so it must never fail readiness.
func (r *ReadRouter) HealthErr() error {
	st := r.Status()
	switch {
	case st.InSync:
		return nil
	case st.CheckedAt.IsZero():
		return errors.New("not probed yet; reads on the primary")
	case st.Err != nil:
		return fmt.Errorf("unreachable, reads on the primary: %w", st.Err)
	case st.Lag == nil:
		return errors.New("lag unknown; reads on the primary")
	default:
		return fmt.Errorf("%s behind (max_lag %s); reads on the primary", st.Lag.Round(time.Millisecond), r.maxLag)
	}
}

// NewPrimaryOnlyRouter returns a router with no replica: every read runs on
// primary.
func NewPrimaryOnlyRouter(primary sqlc.DBTX) *ReadRouter {
	return &ReadRouter{primary: primary}
}

func newReplicaRouter(
	primary sqlc.DBTX, replica PgxPool, maxLag, period time.Duration, log *zap.Logger,
) *ReadRouter {
	return &ReadRouter{
		primary: primary,
		replica: replica,
		lag:     func(ctx context.Context) (*time.Duration, error) { return replicaLag(ctx, replica) },
		maxLag:  maxLag,
		period:  period,
		log:     log,
	}
}

// HasReplica reports whether a replica is configured, in sync or not.
func (r *ReadRouter) HasReplica() bool { return r != nil && r.replica != nil }

// InSync reports whether reads are currently going to the replica.
func (r *ReadRouter) InSync() bool { return r.HasReplica() && r.inSync.Load() }

// Read runs fn against the replica when it is in sync, and against the
// primary otherwise. fn must be a pure read: when the replica fails it is run
// a second time, on the primary — a standby cancels queries that conflict with
// WAL replay, and an outage between two probes is not the caller's problem.
// A cancelled context is returned as is rather than retried.
//
// fn gets a connection source, not a transaction: a read that needs a
// consistent snapshot across statements does not belong here.
func (r *ReadRouter) Read(ctx context.Context, fn func(db sqlc.DBTX) error) error {
	if !r.InSync() {
		if r.HasReplica() {
			countRead(ctx, "primary", "out_of_sync")
		}
		return fn(r.primary)
	}
	err := fn(r.replica)
	if err == nil || ctx.Err() != nil || errors.Is(err, pgx.ErrNoRows) {
		countRead(ctx, "replica", "in_sync")
		return err
	}
	r.log.Warn("read replica query failed; retrying on the primary", zap.Error(err))
	countRead(ctx, "primary", "replica_error")
	return fn(r.primary)
}

// Run probes the replica's lag every period until ctx is cancelled, switching
// reads between it and the primary. It returns at once when there is no
// replica. The first probe runs immediately, so a healthy replica takes reads
// as soon as the process is up.
func (r *ReadRouter) Run(ctx context.Context) {
	if !r.HasReplica() {
		return
	}
	r.probe(ctx)
	t := time.NewTicker(r.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.probe(ctx)
		}
	}
}

func (r *ReadRouter) probe(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, r.period)
	defer cancel()
	lag, err := r.lag(pctx)
	if ctx.Err() != nil {
		return // shutting down; the verdict no longer matters
	}
	ok := err == nil && lag != nil && (r.maxLag <= 0 || *lag <= r.maxLag)
	was := r.inSync.Swap(ok)
	r.mu.Lock()
	r.last = ReplicaStatus{InSync: ok, Lag: lag, Err: err, CheckedAt: time.Now()}
	r.mu.Unlock()
	recordProbe(ctx, ok, lag)
	switch {
	case ok && !was:
		r.log.Info("read replica in sync; routing reads to it", zap.Durationp("lag", lag))
	case !ok && was:
		fields := []zap.Field{zap.Duration("max_lag", r.maxLag)}
		if err != nil {
			fields = append(fields, zap.Error(err))
		} else if lag != nil {
			fields = append(fields, zap.Duration("lag", *lag))
		} else {
			fields = append(fields, zap.String("lag", "unknown"))
		}
		r.log.Warn("read replica out of sync; routing reads to the primary", fields...)
	}
}

// replicaLagSQL measures how far the replica's replay is behind.
//
// now() - pg_last_xact_replay_timestamp() alone is wrong on a quiet primary:
// with nothing to replay the last replayed commit ages, and an idle but fully
// caught-up replica would read as minutes behind. So a replica that has
// replayed everything it received is at zero.
//
// A connection that is not in recovery is at zero too: a managed reader
// endpoint can land on the primary after a failover, and reading from the
// primary is never stale.
//
// NULL — in recovery, with WAL received but not replayed and no replayed
// commit to date — means unknown, and the router treats unknown as behind.
const replicaLagSQL = `
SELECT CASE
  WHEN NOT pg_is_in_recovery() THEN 0::float8
  WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0::float8
  ELSE EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp())::float8
END`

func replicaLag(ctx context.Context, pool PgxPool) (*time.Duration, error) {
	var secs *float64
	if err := pool.QueryRow(ctx, replicaLagSQL).Scan(&secs); err != nil {
		return nil, err
	}
	if secs == nil {
		return nil, nil
	}
	d := time.Duration(*secs * float64(time.Second))
	if d < 0 {
		// Clock skew between the primary that stamped the commit and the
		// replica reading now(): the replica cannot be ahead.
		d = 0
	}
	return &d, nil
}

// Replica metrics. Same lazy-init shape as the outbox gauges
// (internal/worker/metrics.go): no instrument exists before the OTel
// MeterProvider is wired, and recording is a no-op until then.
//
// Exported to Prometheus as paladin_db_replica_in_sync (0/1),
// paladin_db_replica_lag_seconds and paladin_db_replica_reads_total{served_by,
// reason}. Bounded labels only; one series set per pod.
var (
	replicaMetricsOnce sync.Once

	replicaInSync   metric.Int64Gauge
	replicaLagGauge metric.Float64Gauge
	replicaReads    metric.Int64Counter
)

func initReplicaMetrics() {
	replicaMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/store/postgres")
		replicaInSync, _ = meter.Int64Gauge(
			"paladin.db.replica.in_sync",
			metric.WithDescription("1 while lag-tolerant reads go to the read replica, 0 while they go to the primary."),
		)
		replicaLagGauge, _ = meter.Float64Gauge(
			"paladin.db.replica.lag",
			metric.WithDescription("Read replica replay lag at the last probe. Not recorded when it could not be measured."),
			metric.WithUnit("s"),
		)
		replicaReads, _ = meter.Int64Counter(
			"paladin.db.replica.reads",
			metric.WithDescription("Lag-tolerant reads by where they ran (served_by) and why (reason: in_sync, out_of_sync, replica_error)."),
		)
	})
}

func recordProbe(ctx context.Context, inSync bool, lag *time.Duration) {
	initReplicaMetrics()
	if replicaInSync != nil {
		v := int64(0)
		if inSync {
			v = 1
		}
		replicaInSync.Record(ctx, v)
	}
	if replicaLagGauge != nil && lag != nil {
		replicaLagGauge.Record(ctx, lag.Seconds())
	}
}

func countRead(ctx context.Context, servedBy, reason string) {
	initReplicaMetrics()
	if replicaReads == nil {
		return
	}
	replicaReads.Add(ctx, 1, metric.WithAttributes(
		attribute.String("served_by", servedBy),
		attribute.String("reason", reason),
	))
}
