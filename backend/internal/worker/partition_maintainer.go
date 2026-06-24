package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// PartitionMaintainer keeps RANGE-partitioned tables (migrations 041
// audit_log, 042 idempotency_keys) healthy: it pre-creates upcoming
// partitions so writes never fall to the DEFAULT catch-all, and DROPs whole
// partitions once their entire range is past the retention cutoff — turning
// retention into DROP PARTITION (zero dead tuples, zero VACUUM) instead of
// the ctid-batched DELETE the *Purger workers do.
//
// The DELETE purgers stay wired as a backstop: they sweep the DEFAULT
// partition (rows whose key landed outside every concrete partition) and any
// stragglers in the still-live boundary partition. DROP handles the bulk.
//
// Correctness without this worker: the DEFAULT partition means inserts never
// fail and the DELETE purgers still bound the tables, so the maintainer is a
// pure optimisation — safe to disable (Interval <= 0 or empty Specs).
type PartitionMaintainer struct {
	DB       PartitionDB
	Specs    []PartitionSpec
	Interval time.Duration
	Logger   *zap.Logger
}

// PartitionDB is the narrow pgx seam the maintainer needs. *pgxpool.Pool
// satisfies it. Kept small so the tick logic unit-tests against a fake.
type PartitionDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PartitionPeriod is the bucket width of a partitioned table.
type PartitionPeriod int

const (
	// PeriodDaily buckets one partition per UTC day (idempotency_keys).
	PeriodDaily PartitionPeriod = iota
	// PeriodMonthly buckets one partition per UTC month (audit_log).
	PeriodMonthly
)

// PartitionSpec describes one partitioned table's maintenance policy.
type PartitionSpec struct {
	// Table is the partitioned parent (e.g. "audit_log").
	Table string
	// Period is the partition bucket width.
	Period PartitionPeriod
	// Retention drops a partition once its whole range is older than
	// now-Retention. 0 means "drop as soon as the bucket is fully in the
	// past" (idempotency_keys: an elapsed day's keys are all expired). A
	// NEGATIVE Retention disables dropping entirely (create-ahead only) —
	// used when the table is kept forever (audit TTL = 0).
	Retention time.Duration
	// Ahead is how many future buckets to keep pre-created (including the
	// current one). Staying ahead keeps writes off the DEFAULT partition.
	Ahead int
}

// truncate returns the start of the bucket containing t (UTC).
func (p PartitionPeriod) truncate(t time.Time) time.Time {
	t = t.UTC()
	if p == PeriodMonthly {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// advance returns the bucket start n buckets after start.
func (p PartitionPeriod) advance(start time.Time, n int) time.Time {
	if p == PeriodMonthly {
		return start.AddDate(0, n, 0)
	}
	return start.AddDate(0, 0, n)
}

// layout is the suffix format: YYYYMM monthly, YYYYMMDD daily.
func (p PartitionPeriod) layout() string {
	if p == PeriodMonthly {
		return "200601"
	}
	return "20060102"
}

// partitionDef is a concrete partition's name and half-open [From, To) range.
type partitionDef struct {
	name     string
	from, to time.Time
}

// desiredAhead returns the partitions that should exist now: the current
// bucket and Ahead-1 future ones. Pure — no DB.
func (s PartitionSpec) desiredAhead(now time.Time) []partitionDef {
	start := s.Period.truncate(now)
	defs := make([]partitionDef, 0, s.Ahead)
	for i := 0; i < s.Ahead; i++ {
		from := s.Period.advance(start, i)
		to := s.Period.advance(start, i+1)
		defs = append(defs, partitionDef{
			name: fmt.Sprintf("%s_%s", s.Table, from.Format(s.Period.layout())),
			from: from,
			to:   to,
		})
	}
	return defs
}

// shouldDrop reports whether a child partition named `name` is fully past
// retention and safe to DROP. Returns false for the DEFAULT partition and
// any name that does not parse to this spec's bucket layout (defensive: we
// only ever drop partitions we recognise). Pure — no DB.
func (s PartitionSpec) shouldDrop(name string, now time.Time) bool {
	if s.Retention < 0 { // dropping disabled — keep every partition
		return false
	}
	suffix := strings.TrimPrefix(name, s.Table+"_")
	if suffix == name || suffix == "default" {
		return false
	}
	start, err := time.ParseInLocation(s.Period.layout(), suffix, time.UTC)
	if err != nil {
		return false
	}
	upper := s.Period.advance(start, 1)
	cutoff := now.Add(-s.Retention)
	return !upper.After(cutoff) // upper <= cutoff
}

func (m *PartitionMaintainer) Run(ctx context.Context) error {
	if len(m.Specs) == 0 {
		// Nothing to maintain — the DEFAULT partition + DELETE purgers keep
		// the tables correct without us. Exit clean.
		return nil
	}
	if m.Interval <= 0 {
		// Default like the other housekeeping workers rather than disable:
		// partitions must stay provisioned even if the shared interval is
		// unset. Daily is ample — partitions are created buckets ahead.
		m.Interval = 24 * time.Hour
	}
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	// Run once on startup so a fresh deploy provisions ahead immediately
	// rather than waiting a full Interval.
	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			m.tick(ctx)
		}
	}
}

// RunOnce performs a single maintenance sweep (create-ahead + drop-old) and
// returns. Run calls it on a schedule and once on startup; exposed for tests
// and any on-demand trigger.
func (m *PartitionMaintainer) RunOnce(ctx context.Context) { m.tick(ctx) }

func (m *PartitionMaintainer) tick(ctx context.Context) {
	now := time.Now().UTC()
	for _, spec := range m.Specs {
		m.ensureAhead(ctx, spec, now)
		m.dropOld(ctx, spec, now)
	}
}

// ensureAhead creates the current + upcoming partitions. CREATE ... IF NOT
// EXISTS makes it idempotent; a failure (e.g. the DEFAULT partition holds a
// row in the new range) is logged, not fatal — the row stays in DEFAULT and
// the DELETE backstop sweeps it.
func (m *PartitionMaintainer) ensureAhead(ctx context.Context, spec PartitionSpec, now time.Time) {
	for _, d := range spec.desiredAhead(now) {
		// Identifiers are derived from trusted Spec.Table + a digit suffix;
		// quote them anyway. Bounds are passed as date literals.
		sql := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
			pgQuoteIdent(d.name), pgQuoteIdent(spec.Table),
			d.from.Format("2006-01-02"), d.to.Format("2006-01-02"),
		)
		if _, err := m.DB.Exec(ctx, sql); err != nil {
			m.log().Warn("ensure partition failed",
				zap.String("table", spec.Table),
				zap.String("partition", d.name),
				zap.Error(err))
		}
	}
}

// dropOld lists the parent's child partitions and DROPs those fully past
// retention.
func (m *PartitionMaintainer) dropOld(ctx context.Context, spec PartitionSpec, now time.Time) {
	rows, err := m.DB.Query(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = $1`, spec.Table)
	if err != nil {
		m.log().Warn("list partitions failed", zap.String("table", spec.Table), zap.Error(err))
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			m.log().Warn("scan partition failed", zap.String("table", spec.Table), zap.Error(err))
			return
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		m.log().Warn("iterate partitions failed", zap.String("table", spec.Table), zap.Error(err))
		return
	}

	for _, n := range names {
		if !spec.shouldDrop(n, now) {
			continue
		}
		if _, err := m.DB.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, pgQuoteIdent(n))); err != nil {
			m.log().Warn("drop partition failed",
				zap.String("table", spec.Table), zap.String("partition", n), zap.Error(err))
			continue
		}
		m.log().Info("dropped expired partition",
			zap.String("table", spec.Table), zap.String("partition", n))
	}
}

func (m *PartitionMaintainer) log() *zap.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return zap.NewNop()
}

// pgQuoteIdent double-quotes a Postgres identifier, escaping embedded
// quotes. Our identifiers are internal (table const + digit suffix) so this
// is belt-and-suspenders, not the primary defence.
func pgQuoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}
