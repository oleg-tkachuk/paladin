package app

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// BuildBackgroundJobs turns config into a slice of goroutines, and the whole
// surface fails the same way: a job that is not in the slice does not run, and
// nothing says so. There is no error, no log line, and no request to fail —
// the first symptom is a table that stopped being reaped, weeks later. The
// file's own comments record one such incident already (ReconcilerV2 bound to
// the RLS pool: "lease healthy, tick every 30s, ScanPendingExpired returning
// nothing while pending-expired objects piled up for weeks").
//
// These tests read the built slice rather than run it, so they need no
// database: the pools are handed in as never-dialed pointers.

func jobsFor(t *testing.T, mutate func(*config.Config)) []string {
	t.Helper()
	cfg := config.Config{}
	if mutate != nil {
		mutate(&cfg)
	}
	deps := &SharedDeps{
		Cfg:        cfg,
		Logger:     zap.NewNop(),
		Pool:       &pgxpool.Pool{},
		ReaperPool: &pgxpool.Pool{},
	}
	names := make([]string, 0, 16)
	for _, j := range BuildBackgroundJobs(deps) {
		names = append(names, fmt.Sprintf("%T", j))
	}
	sort.Strings(names)
	return names
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// Four jobs carry comments saying they run whatever else is toggled, because
// the tables they reclaim grow on paths no toggle controls — the rate limiter
// writes a row per active tenant per minute, idempotency keys accrue on every
// idempotent Create. Nothing held that claim: wrapping one in a config flag
// would compile, pass review, and stop reclaiming.
func TestBuildBackgroundJobs_AlwaysOn(t *testing.T) {
	want := []string{
		"*worker.IdempotencyKeyPurger",
		"*worker.PartitionMaintainer",
		"*worker.RefreshTokenPurger",
		"*worker.StorageMigrationWorker",
		"*worker.TenantRateBucketSweeper",
	}
	got := jobsFor(t, nil)
	if len(got) != len(want) {
		t.Fatalf("empty config built %d jobs %v, want exactly the %d always-on ones %v",
			len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("job[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// One knob per job, each asserted in both directions. The zero value is the
// disabling one throughout, which is the trap: a config that omits the key
// entirely reads as "default" to an operator and as "off" to this function.
func TestBuildBackgroundJobs_ConfigGates(t *testing.T) {
	cases := []struct {
		name string
		set  func(*config.Config)
		want []string
	}{
		{
			name: "lifecycle",
			set:  func(c *config.Config) { c.Worker.Jobs.Lifecycle.Enabled = true },
			want: []string{"*worker.LifecycleWorker"},
		},
		{
			name: "replication",
			set:  func(c *config.Config) { c.Worker.Jobs.Replication.Enabled = true },
			want: []string{"*worker.ReplicationWorker"},
		},
		{
			// One interval gates two jobs, and the second is the back half of
			// CreateBucket(provision_on_backend=true): with the reconciler
			// off, a bucket stays provision_state='pending' forever and every
			// write to it is refused by the resolver gate.
			name: "reconciler interval brings the bucket reconciler with it",
			set:  func(c *config.Config) { c.Worker.Jobs.Reconciler.Interval = time.Second },
			want: []string{"*worker.ReconcilerV2", "*worker.BucketReconciler"},
		},
		{
			name: "audit log ttl",
			set:  func(c *config.Config) { c.Worker.Jobs.Housekeeping.AuditLogTTL = time.Hour },
			want: []string{"*worker.AuditLogPurger"},
		},
		{
			name: "event deliveries ttl",
			set:  func(c *config.Config) { c.Worker.Jobs.Housekeeping.EventDeliveriesTTL = time.Hour },
			want: []string{"*worker.EventDeliveryPurger"},
		},
		{
			name: "operations ttl",
			set:  func(c *config.Config) { c.Worker.Jobs.Housekeeping.OperationsTTL = time.Hour },
			want: []string{"*worker.OperationsReaper"},
		},
		{
			name: "multipart ttl brings the abort drainer with it",
			set:  func(c *config.Config) { c.Worker.Jobs.Housekeeping.MultipartTTL = time.Hour },
			want: []string{"*worker.MultipartReaper", "*worker.MultipartAbortDrainer"},
		},
		{
			name: "hard delete after",
			set:  func(c *config.Config) { c.Worker.Jobs.Housekeeping.HardDeleteAfter = time.Hour },
			want: []string{"*worker.LifecycleHardDeleter"},
		},
		{
			name: "purge drain interval",
			set:  func(c *config.Config) { c.Worker.Jobs.PurgeDrain.Interval = time.Second },
			want: []string{"*worker.PurgeDrainer"},
		},
		{
			name: "quota reconcile interval",
			set:  func(c *config.Config) { c.Worker.Jobs.QuotaReconcile.Interval = time.Second },
			want: []string{"*worker.QuotaReconciler"},
		},
		{
			name: "operations interval brings the stale reclaimer with it",
			set:  func(c *config.Config) { c.Worker.Jobs.Operations.Interval = time.Second },
			want: []string{"*operations.Runner", "*worker.StaleOperationReclaimer"},
		},
	}

	base := jobsFor(t, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if has(base, w) {
					t.Fatalf("%s is already built with an empty config — this case cannot fail", w)
				}
			}
			got := jobsFor(t, tc.set)
			for _, w := range tc.want {
				if !has(got, w) {
					t.Errorf("%s missing after enabling %s; built %v", w, tc.name, got)
				}
			}
		})
	}
}

// Two subsystems need both a non-nil bundle and a non-zero interval. The
// bundle is nil whenever the subsystem is off, so the interval alone must not
// be enough — dereferencing it would panic the worker at startup.
func TestBuildBackgroundJobs_SubsystemPurgersNeedTheirBundle(t *testing.T) {
	withInterval := func(c *config.Config) {
		c.Worker.Jobs.Capability.Interval = time.Second
		c.Worker.Jobs.APIToken.Interval = time.Second
	}
	got := jobsFor(t, withInterval)
	for _, w := range []string{"*worker.CapabilityPurger", "*worker.APITokenPurger"} {
		if has(got, w) {
			t.Errorf("%s built with a nil bundle; built %v", w, got)
		}
	}

	cfg := config.Config{}
	withInterval(&cfg)
	deps := &SharedDeps{
		Cfg:        cfg,
		Logger:     zap.NewNop(),
		Pool:       &pgxpool.Pool{},
		ReaperPool: &pgxpool.Pool{},
		Capability: &CapabilityBundle{},
		APIToken:   &APITokenBundle{},
	}
	names := make([]string, 0, 16)
	for _, j := range BuildBackgroundJobs(deps) {
		names = append(names, fmt.Sprintf("%T", j))
	}
	for _, w := range []string{"*worker.CapabilityPurger", "*worker.APITokenPurger"} {
		if !has(names, w) {
			t.Errorf("%s missing with both a bundle and an interval; built %v", w, names)
		}
	}
}

// The purger sweeps DPoP proof ids only when the bundle carries a replay
// cache. A nil *ReplayCache stored in the interface field would read as wired
// and panic on the first tick.
func TestBuildBackgroundJobs_CapabilityPurgerReplayWiring(t *testing.T) {
	purgerFor := func(t *testing.T, bundle *CapabilityBundle) *worker.CapabilityPurger {
		t.Helper()
		cfg := config.Config{}
		cfg.Worker.Jobs.Capability.Interval = time.Second
		deps := &SharedDeps{
			Cfg:        cfg,
			Logger:     zap.NewNop(),
			Pool:       &pgxpool.Pool{},
			ReaperPool: &pgxpool.Pool{},
			Capability: bundle,
		}
		for _, j := range BuildBackgroundJobs(deps) {
			if p, ok := j.(*worker.CapabilityPurger); ok {
				return p
			}
		}
		t.Fatal("CapabilityPurger not built")
		return nil
	}

	if p := purgerFor(t, &CapabilityBundle{}); p.Replay != nil {
		t.Errorf("Replay = %#v with no replay cache in the bundle, want a nil interface", p.Replay)
	}

	replay, err := capabilitypg.NewReplayCache(&pgxpool.Pool{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := purgerFor(t, &CapabilityBundle{Replay: replay}); p.Replay != replay {
		t.Errorf("Replay = %#v, want the bundle's replay cache", p.Replay)
	}
}

// The audit retention mapping is the seam between two things that are each
// tested and whose join is not: worker holds that Retention < 0 never drops
// and Retention == 0 drops a fully-elapsed period immediately, and config
// documents AuditLogTTL == 0 as "keep forever". This function is what turns
// one into the other, and it does it with `if auditRetention <= 0`.
//
// Relaxed to `< 0`, keep-forever becomes drop-immediately: the partition
// maintainer would DROP each audit_log month as it elapsed, on the config
// that asks for the audit log to be kept. Nothing else in the system would
// object — dropping a partition is what the job is for.
func TestBuildBackgroundJobs_AuditRetentionKeepsForever(t *testing.T) {
	specFor := func(t *testing.T, ttl time.Duration) worker.PartitionSpec {
		t.Helper()
		cfg := config.Config{}
		cfg.Worker.Jobs.Housekeeping.AuditLogTTL = ttl
		deps := &SharedDeps{
			Cfg:        cfg,
			Logger:     zap.NewNop(),
			Pool:       &pgxpool.Pool{},
			ReaperPool: &pgxpool.Pool{},
		}
		for _, j := range BuildBackgroundJobs(deps) {
			pm, ok := j.(*worker.PartitionMaintainer)
			if !ok {
				continue
			}
			for _, s := range pm.Specs {
				if s.Table == "audit_log" {
					return s
				}
			}
			t.Fatal("PartitionMaintainer carries no audit_log spec")
		}
		t.Fatal("PartitionMaintainer was not built")
		return worker.PartitionSpec{}
	}

	if got := specFor(t, 0).Retention; got >= 0 {
		t.Errorf("AuditLogTTL=0 (keep forever) produced Retention=%v; a non-negative retention drops elapsed audit partitions", got)
	}
	if got := specFor(t, -time.Hour).Retention; got >= 0 {
		t.Errorf("a negative AuditLogTTL produced Retention=%v, want keep-forever", got)
	}
	if got := specFor(t, 90*24*time.Hour).Retention; got != 90*24*time.Hour {
		t.Errorf("AuditLogTTL=90d produced Retention=%v, want it carried through", got)
	}

	// The other spec is unconditional and deliberately zero: a fully elapsed
	// day of idempotency keys is entirely expired, so its partition drops at
	// once. Pinned so the audit mapping above cannot be "fixed" by applying
	// it to both.
	cfg := config.Config{}
	deps := &SharedDeps{Cfg: cfg, Logger: zap.NewNop(), Pool: &pgxpool.Pool{}, ReaperPool: &pgxpool.Pool{}}
	for _, j := range BuildBackgroundJobs(deps) {
		pm, ok := j.(*worker.PartitionMaintainer)
		if !ok {
			continue
		}
		for _, s := range pm.Specs {
			if s.Table == "idempotency_keys" && s.Retention != 0 {
				t.Errorf("idempotency_keys Retention = %v, want 0", s.Retention)
			}
		}
	}
}

// The reapers are cross-tenant and run with no request principal, so binding
// them to the RLS-scoped runtime pool makes every one of them a no-op: with
// no paladin.tenant_id set, RLS matches zero rows and the job reports success
// having done nothing. This is not hypothetical — the comment on the pool
// selection records ReconcilerV2 sitting in exactly that state for weeks.
//
// The selection is two lines of `if deps.X != nil`, and inverting either one
// reinstates the incident. Handing in three distinct pointers makes which
// pool each job received directly observable.
func TestBuildBackgroundJobs_ReapersBindToTheBypassRLSPool(t *testing.T) {
	runtimePool := &pgxpool.Pool{}
	reaperPool := &pgxpool.Pool{}
	partitionPool := &pgxpool.Pool{}

	cfg := config.Config{}
	cfg.Worker.Jobs.PurgeDrain.Interval = time.Second
	cfg.Worker.Jobs.Housekeeping.HardDeleteAfter = time.Hour

	deps := &SharedDeps{
		Cfg:           cfg,
		Logger:        zap.NewNop(),
		Pool:          runtimePool,
		ReaperPool:    reaperPool,
		PartitionPool: partitionPool,
	}

	var sawDrainer, sawDeleter, sawPartitions bool
	for _, j := range BuildBackgroundJobs(deps) {
		switch job := j.(type) {
		case *worker.PurgeDrainer:
			sawDrainer = true
			if job.Pool != reaperPool {
				t.Errorf("PurgeDrainer.Pool is not the reaper pool (runtime pool: %v)", job.Pool == runtimePool)
			}
		case *worker.LifecycleHardDeleter:
			sawDeleter = true
			if job.Pool != reaperPool {
				t.Errorf("LifecycleHardDeleter.Pool is not the reaper pool (runtime pool: %v)", job.Pool == runtimePool)
			}
		case *worker.PartitionMaintainer:
			sawPartitions = true
			// The maintainer is the one job that runs DDL, which the
			// least-privilege reaper role cannot do.
			if job.DB != partitionPool {
				t.Errorf("PartitionMaintainer.DB is not the partition pool (reaper: %v, runtime: %v)",
					job.DB == reaperPool, job.DB == runtimePool)
			}
		}
	}
	if !sawDrainer || !sawDeleter || !sawPartitions {
		t.Fatalf("expected all three jobs to be built (drainer=%v deleter=%v partitions=%v)",
			sawDrainer, sawDeleter, sawPartitions)
	}

	// The documented degradation: no reaper pool means fall back to the
	// runtime pool rather than crash, and serve_worker warns. Asserted so the
	// fallback stays a fallback and does not become the normal path.
	deps.ReaperPool = nil
	deps.PartitionPool = nil
	for _, j := range BuildBackgroundJobs(deps) {
		if job, ok := j.(*worker.PurgeDrainer); ok && job.Pool != runtimePool {
			t.Errorf("with no reaper pool, PurgeDrainer.Pool = %p, want the runtime pool", job.Pool)
		}
		if job, ok := j.(*worker.PartitionMaintainer); ok && job.DB != runtimePool {
			t.Error("with neither reaper nor partition pool, PartitionMaintainer.DB should degrade to the runtime pool")
		}
	}
}

// Both subsystem purgers need a bundle AND a non-zero interval. The bundle
// half is asserted above; this is the interval half, which is the same
// zero-means-off shape as the gates and is not implied by it.
func TestBuildBackgroundJobs_SubsystemPurgersNeedANonZeroInterval(t *testing.T) {
	deps := &SharedDeps{
		Cfg:        config.Config{}, // both intervals zero
		Logger:     zap.NewNop(),
		Pool:       &pgxpool.Pool{},
		ReaperPool: &pgxpool.Pool{},
		Capability: &CapabilityBundle{},
		APIToken:   &APITokenBundle{},
	}
	for _, j := range BuildBackgroundJobs(deps) {
		switch j.(type) {
		case *worker.CapabilityPurger:
			t.Error("CapabilityPurger built with a zero interval")
		case *worker.APITokenPurger:
			t.Error("APITokenPurger built with a zero interval")
		}
	}
}
