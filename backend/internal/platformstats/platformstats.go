// Package platformstats computes the cross-tenant census behind the
// console's /stats page.
//
// It is split in two because the underlying tables have two different
// visibility rules (migration 023):
//
//   - ControlPlane covers `tenants`, `storage_backends`, `buckets`,
//     `collections` and `users` — deliberately NOT RLS'd, precisely so
//     platform-admin reads span tenants. The admin pod runs these on its
//     own request pool.
//
//   - ObjectCensus covers `objects`, which IS RLS'd per tenant. A query
//     on the admin pod's `paladin_app` pool sees only the caller's tenant (or
//     zero rows with no GUC set), so this half runs on the worker pod's
//     BYPASSRLS pool and reaches the console over the worker's ops
//     listener — the same shape as the dispatcher's delivery stats.
//
// Every query here is a single aggregate scan with no user input, so the
// callers need no per-row authorization: the admin RPC's platform-admin
// gate is the whole access-control story.
package platformstats

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Control-plane census (admin pod, RLS-exempt tables) ────────────────────

// ControlPlane is the un-RLS'd half of the census: resource inventory that
// any platform admin may see in full.
type ControlPlane struct {
	Tenants     TenantCensus     `json:"tenants"`
	Backends    BackendCensus    `json:"backends"`
	Buckets     BucketCensus     `json:"buckets"`
	Collections CollectionCensus `json:"collections"`
	Users       UserCensus       `json:"users"`
}

type TenantCensus struct {
	Total   int64 `json:"total"`
	Active  int64 `json:"active"`
	Trashed int64 `json:"trashed"`
	// Layout split counts ACTIVE tenants only — a trashed tenant's layout
	// is not an operational fact about the fleet.
	SharedLayout    int64 `json:"shared_layout"`
	DedicatedLayout int64 `json:"dedicated_layout"`
	// Active tenants with no tenant_default_bindings row: they cannot
	// accept a bare (unqualified) Collection bind.
	WithoutDefaultBinding int64 `json:"without_default_binding"`
}

type BackendCensus struct {
	Total       int64            `json:"total"`
	Enabled     int64            `json:"enabled"`
	Disabled    int64            `json:"disabled"`
	ReadOnly    int64            `json:"read_only"`
	Maintenance int64            `json:"maintenance"`
	ByKind      map[string]int64 `json:"by_kind"`
}

type BucketCensus struct {
	Total             int64            `json:"total"`
	ByProvisionState  map[string]int64 `json:"by_provision_state"`
	ByBackend         map[string]int64 `json:"by_backend"`
	TenantOwned       int64            `json:"tenant_owned"`
	Shared            int64            `json:"shared"`
	VersioningEnabled int64            `json:"versioning_enabled"`
	ObjectLockEnabled int64            `json:"object_lock_enabled"`
	ReplicationOn     int64            `json:"replication_enabled"`
}

type CollectionCensus struct {
	Total     int64            `json:"total"`
	ByBackend map[string]int64 `json:"by_backend"`
	// Unbound = bucket_id IS NULL: registered but never bound to a
	// physical bucket, so uploads through it have nowhere to land.
	Unbound int64 `json:"unbound"`
}

type UserCensus struct {
	Total    int64 `json:"total"`
	Disabled int64 `json:"disabled"`
}

// CollectControlPlane runs the five inventory aggregates. Each is a single
// sequential scan over a small operator-managed table (tens to low
// thousands of rows), so this is cheap enough for a dashboard poll.
func CollectControlPlane(ctx context.Context, pool *pgxpool.Pool) (*ControlPlane, error) {
	out := &ControlPlane{
		Backends:    BackendCensus{ByKind: map[string]int64{}},
		Buckets:     BucketCensus{ByProvisionState: map[string]int64{}, ByBackend: map[string]int64{}},
		Collections: CollectionCensus{ByBackend: map[string]int64{}},
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE deleted_at IS NULL),
		       count(*) FILTER (WHERE deleted_at IS NOT NULL),
		       count(*) FILTER (WHERE deleted_at IS NULL AND storage_layout <> 'dedicated'),
		       count(*) FILTER (WHERE deleted_at IS NULL AND storage_layout  = 'dedicated'),
		       count(*) FILTER (WHERE deleted_at IS NULL AND NOT EXISTS (
		           SELECT 1 FROM tenant_default_bindings b WHERE b.tenant_id = t.id))
		FROM tenants t`,
	).Scan(&out.Tenants.Total, &out.Tenants.Active, &out.Tenants.Trashed,
		&out.Tenants.SharedLayout, &out.Tenants.DedicatedLayout,
		&out.Tenants.WithoutDefaultBinding); err != nil {
		return nil, fmt.Errorf("census: tenants: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE enabled),
		       count(*) FILTER (WHERE NOT enabled),
		       count(*) FILTER (WHERE read_only),
		       count(*) FILTER (WHERE maintenance)
		FROM storage_backends`,
	).Scan(&out.Backends.Total, &out.Backends.Enabled, &out.Backends.Disabled,
		&out.Backends.ReadOnly, &out.Backends.Maintenance); err != nil {
		return nil, fmt.Errorf("census: backends: %w", err)
	}
	if err := scanCounts(ctx, pool,
		`SELECT kind, count(*) FROM storage_backends GROUP BY kind`,
		out.Backends.ByKind); err != nil {
		return nil, fmt.Errorf("census: backends by kind: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE owner_tenant_id IS NOT NULL),
		       count(*) FILTER (WHERE owner_tenant_id IS NULL),
		       count(*) FILTER (WHERE versioning_enabled),
		       count(*) FILTER (WHERE object_lock_enabled),
		       count(*) FILTER (WHERE replication_enabled)
		FROM buckets`,
	).Scan(&out.Buckets.Total, &out.Buckets.TenantOwned, &out.Buckets.Shared,
		&out.Buckets.VersioningEnabled, &out.Buckets.ObjectLockEnabled,
		&out.Buckets.ReplicationOn); err != nil {
		return nil, fmt.Errorf("census: buckets: %w", err)
	}
	if err := scanCounts(ctx, pool,
		`SELECT provision_state, count(*) FROM buckets GROUP BY provision_state`,
		out.Buckets.ByProvisionState); err != nil {
		return nil, fmt.Errorf("census: buckets by provision state: %w", err)
	}
	if err := scanCounts(ctx, pool,
		`SELECT sb.name, count(*) FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id GROUP BY sb.name`,
		out.Buckets.ByBackend); err != nil {
		return nil, fmt.Errorf("census: buckets by backend: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE bucket_id IS NULL) FROM collections`,
	).Scan(&out.Collections.Total, &out.Collections.Unbound); err != nil {
		return nil, fmt.Errorf("census: collections: %w", err)
	}
	if err := scanCounts(ctx, pool,
		`SELECT sb.name, count(*) FROM collections c
		   JOIN buckets b ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id GROUP BY sb.name`,
		out.Collections.ByBackend); err != nil {
		return nil, fmt.Errorf("census: collections by backend: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE disabled) FROM users`,
	).Scan(&out.Users.Total, &out.Users.Disabled); err != nil {
		return nil, fmt.Errorf("census: users: %w", err)
	}

	return out, nil
}

// scanCounts fills `into` from a two-column (label, count) aggregate.
func scanCounts(ctx context.Context, pool *pgxpool.Pool, sql string, into map[string]int64) error {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var n int64
		if err := rows.Scan(&label, &n); err != nil {
			return err
		}
		into[label] = n
	}
	return rows.Err()
}

// ─── RLS'd census (worker pod, BYPASSRLS) ───────────────────────────────────

// maxTenantRows caps the per-tenant breakdown so a fleet with thousands of
// tenants can't balloon the ops payload (and the console table). Tenants
// sort by total object count descending, so the cap trims the quiet tail;
// TenantsTruncated reports how many were dropped.
const maxTenantRows = 200

// nearLimitRatio is the fraction of a quota cap at which a row counts as
// "near limit". 0.9 gives an operator roughly one business day of warning
// at typical ingest rates without crying wolf on a half-full tenant.
const nearLimitRatio float64 = 0.9

// RLSCensus is everything migration 023 puts behind row-level security:
// objects, quotas, capability records, API tokens and event subscriptions.
// All of it is computed on the worker pod's BYPASSRLS pool and shipped as
// one JSON payload over its ops listener — one round-trip, not five.
//
// The worker knows tenant_id only; slug / display name are joined in by
// the admin pod, which can read `tenants`.
type RLSCensus struct {
	Objects       ObjectCensus       `json:"objects"`
	Quotas        QuotaCensus        `json:"quotas"`
	Capabilities  CapabilityCensus   `json:"capabilities"`
	APITokens     APITokenCensus     `json:"api_tokens"`
	Subscriptions SubscriptionCensus `json:"subscriptions"`
	CollectedAt   string             `json:"collected_at"` // RFC3339 UTC
}

// ObjectCensus is object counts and bytes, per tenant and per state.
type ObjectCensus struct {
	States     []StateStat  `json:"states"`
	TotalCount int64        `json:"total_count"`
	TotalBytes int64        `json:"total_bytes"`
	Tenants    []TenantStat `json:"tenants"`
	TenantsCut int64        `json:"tenants_truncated"`
}

// StateStat is one (state, count, bytes) triple. `State` carries the
// object_state enum's wire name ("PENDING", "AVAILABLE", "FAILED",
// "DELETED") verbatim, so a new enum member surfaces without a code change.
type StateStat struct {
	State string `json:"state"`
	Count int64  `json:"count"`
	// Bytes sums size_bytes; the column is NULL until an object commits,
	// so PENDING rows contribute 0.
	Bytes int64 `json:"bytes"`
}

type TenantStat struct {
	TenantID   string      `json:"tenant_id"`
	States     []StateStat `json:"states"`
	TotalCount int64       `json:"total_count"`
	TotalBytes int64       `json:"total_bytes"`
}

// CollectRLS computes every RLS'd aggregate in five sequential queries.
// The caller MUST pass a BYPASSRLS pool — on the RLS-scoped runtime pool
// these return only the session tenant's rows (or nothing at all when no
// paladin.tenant_id GUC is set), which would read as "the fleet is empty".
//
// Sequential rather than concurrent on purpose: this runs on the worker's
// least-privilege pool alongside the background jobs, and a dashboard poll
// has no business claiming five connections at once to save a few
// milliseconds.
func CollectRLS(ctx context.Context, pool *pgxpool.Pool) (*RLSCensus, error) {
	objects, err := collectObjects(ctx, pool)
	if err != nil {
		return nil, err
	}
	out := &RLSCensus{Objects: *objects}
	if err := collectQuotas(ctx, pool, &out.Quotas); err != nil {
		return nil, err
	}
	if err := collectCapabilities(ctx, pool, &out.Capabilities); err != nil {
		return nil, err
	}
	if err := collectAPITokens(ctx, pool, &out.APITokens); err != nil {
		return nil, err
	}
	if err := collectSubscriptions(ctx, pool, &out.Subscriptions); err != nil {
		return nil, err
	}
	return out, nil
}

// collectObjects folds one GROUP BY over (tenant_id, state) into the
// global rollup plus the per-tenant rows.
func collectObjects(ctx context.Context, pool *pgxpool.Pool) (*ObjectCensus, error) {
	rows, err := pool.Query(ctx, `
		SELECT tenant_id::text, state::text,
		       count(*), COALESCE(sum(size_bytes), 0)
		FROM objects
		GROUP BY tenant_id, state`)
	if err != nil {
		return nil, fmt.Errorf("object census: %w", err)
	}
	defer rows.Close()

	byTenant := map[string]*TenantStat{}
	global := map[string]*StateStat{}
	out := &ObjectCensus{States: []StateStat{}, Tenants: []TenantStat{}}

	for rows.Next() {
		var tenantID, state string
		var count, bytes int64
		if err := rows.Scan(&tenantID, &state, &count, &bytes); err != nil {
			return nil, fmt.Errorf("object census: scan: %w", err)
		}
		t, ok := byTenant[tenantID]
		if !ok {
			t = &TenantStat{TenantID: tenantID}
			byTenant[tenantID] = t
		}
		t.States = append(t.States, StateStat{State: state, Count: count, Bytes: bytes})
		t.TotalCount += count
		t.TotalBytes += bytes

		g, ok := global[state]
		if !ok {
			g = &StateStat{State: state}
			global[state] = g
		}
		g.Count += count
		g.Bytes += bytes

		out.TotalCount += count
		out.TotalBytes += bytes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("object census: rows: %w", err)
	}

	for _, s := range global {
		out.States = append(out.States, *s)
	}
	sortStates(out.States)

	tenants := make([]TenantStat, 0, len(byTenant))
	for _, t := range byTenant {
		sortStates(t.States)
		tenants = append(tenants, *t)
	}
	sortTenants(tenants)
	if len(tenants) > maxTenantRows {
		out.TenantsCut = int64(len(tenants) - maxTenantRows)
		tenants = tenants[:maxTenantRows]
	}
	out.Tenants = tenants
	return out, nil
}

// ─── Quotas ─────────────────────────────────────────────────────────────────

// QuotaCensus counts quota rows and how close they are to their caps.
//
// A caveat the console repeats to the operator: `usage_*` here is a
// periodically-reconciled snapshot, not a live measurement. The upload
// path increments the columns best-effort (object/multipart handlers'
// touchQuota swallows write errors so a blip cannot undo a state
// transition) and worker.QuotaReconciler recomputes them from live objects
// every worker.jobs.quota_reconcile.interval. So they converge on the
// truth but can trail it by up to one interval, and they only cover
// tenants that actually have a quota row — quotas are opt-in. Where they
// disagree with ObjectCensus, ObjectCensus is the ground truth: it counts
// rows at read time. Both are reported so the lag stays visible instead of
// being silently averaged away.
type QuotaCensus struct {
	Total int64 `json:"total"`
	// Scope split — the CHECK constraint on `quotas` makes these exclusive
	// and exhaustive: tenant-scoped rows carry tenant_id, bucket-scoped
	// rows carry bucket_id.
	TenantScoped int64 `json:"tenant_scoped"`
	BucketScoped int64 `json:"bucket_scoped"`
	// Rows with at least one non-zero cap. A row with every cap at 0 is
	// usage tracking only — nothing is enforced against it.
	WithLimits int64 `json:"with_limits"`
	// AtLimit / NearLimit consider all four caps and count a row once:
	// at/over any set cap wins over near any set cap.
	AtLimit   int64 `json:"at_limit"`
	NearLimit int64 `json:"near_limit"`
	// Accounting counters summed over TENANT-scoped rows only — summing
	// bucket-scoped rows too would double-count the same bytes.
	UsageObjectCount int64 `json:"usage_object_count"`
	UsageTotalBytes  int64 `json:"usage_total_bytes"`
}

func collectQuotas(ctx context.Context, pool *pgxpool.Pool, out *QuotaCensus) error {
	// `atLimit` / `nearLimit` are expressed once as SQL fragments so the
	// two FILTERs can't drift apart. A cap of 0 means "no cap" throughout
	// the schema, hence the `> 0` guard on every term.
	const atLimit = `(
		 (max_total_bytes     > 0 AND usage_total_bytes    >= max_total_bytes)
		OR (max_object_count    > 0 AND usage_object_count   >= max_object_count)
		OR (max_bytes_per_day   > 0 AND usage_bytes_today    >= max_bytes_per_day)
		OR (max_objects_per_day > 0 AND usage_objects_today  >= max_objects_per_day))`
	// The ::float8 casts are load-bearing: without them Postgres infers $1
	// from the bigint operand, rounds 0.9 to 1, and "near limit" silently
	// becomes "at limit".
	const nearLimit = `(
		 (max_total_bytes     > 0 AND usage_total_bytes    >= max_total_bytes     * $1::float8)
		OR (max_object_count    > 0 AND usage_object_count   >= max_object_count    * $1::float8)
		OR (max_bytes_per_day   > 0 AND usage_bytes_today    >= max_bytes_per_day   * $1::float8)
		OR (max_objects_per_day > 0 AND usage_objects_today  >= max_objects_per_day * $1::float8))`

	err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE tenant_id IS NOT NULL),
		       count(*) FILTER (WHERE tenant_id IS NULL),
		       count(*) FILTER (WHERE max_total_bytes > 0 OR max_object_count > 0
		                           OR max_bytes_per_day > 0 OR max_objects_per_day > 0),
		       count(*) FILTER (WHERE `+atLimit+`),
		       count(*) FILTER (WHERE `+nearLimit+` AND NOT `+atLimit+`),
		       COALESCE(sum(usage_object_count) FILTER (WHERE tenant_id IS NOT NULL), 0),
		       COALESCE(sum(usage_total_bytes)  FILTER (WHERE tenant_id IS NOT NULL), 0)
		FROM quotas`, nearLimitRatio,
	).Scan(&out.Total, &out.TenantScoped, &out.BucketScoped, &out.WithLimits,
		&out.AtLimit, &out.NearLimit, &out.UsageObjectCount, &out.UsageTotalBytes)
	if err != nil {
		return fmt.Errorf("census: quotas: %w", err)
	}
	return nil
}

// ─── Capabilities ───────────────────────────────────────────────────────────

// CapabilityCensus counts issued capability tokens by disposition.
//
// Active / Expired / Revoked are mutually exclusive and sum to Total:
// a revocation row wins over expiry (an operator who revoked a token wants
// to see it counted as revoked, not quietly reclassified when it lapses),
// and expiry wins over active. `capability_revocations` is deliberately
// NOT RLS'd — the verifier on every plane consults it — so the join costs
// nothing extra on this pool.
type CapabilityCensus struct {
	Total   int64 `json:"total"`
	Active  int64 `json:"active"`
	Expired int64 `json:"expired"`
	Revoked int64 `json:"revoked"`
	// Delegated rows have a parent capability (attenuated re-issue).
	Delegated int64 `json:"delegated"`
	// Active rows expiring within ExpiringSoonWindow — the set an agent
	// runtime is about to lose unless something re-issues.
	ExpiringSoon int64 `json:"expiring_soon"`
	// principal_kind → count, over ACTIVE rows only. A census of expired
	// tokens by kind is archaeology, not an operational signal.
	ByPrincipalKind map[string]int64 `json:"by_principal_kind"`
}

// capabilityExpiringWindow is how far ahead "expiring soon" looks for a
// capability. Capabilities are short-lived by design (minutes to hours),
// so a day of warning covers any realistic re-issue loop.
const capabilityExpiringWindow = "24 hours"

func collectCapabilities(ctx context.Context, pool *pgxpool.Pool, out *CapabilityCensus) error {
	out.ByPrincipalKind = map[string]int64{}
	const revoked = `EXISTS (SELECT 1 FROM capability_revocations r WHERE r.id = c.id)`
	err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE NOT `+revoked+` AND c.expires_at >  now()),
		       count(*) FILTER (WHERE NOT `+revoked+` AND c.expires_at <= now()),
		       count(*) FILTER (WHERE     `+revoked+`),
		       count(*) FILTER (WHERE c.parent_id IS NOT NULL),
		       count(*) FILTER (WHERE NOT `+revoked+` AND c.expires_at > now()
		                          AND c.expires_at <= now() + interval '`+capabilityExpiringWindow+`')
		FROM capability_records c`,
	).Scan(&out.Total, &out.Active, &out.Expired, &out.Revoked,
		&out.Delegated, &out.ExpiringSoon)
	if err != nil {
		return fmt.Errorf("census: capabilities: %w", err)
	}
	if err := scanCounts(ctx, pool, `
		SELECT c.principal_kind, count(*)
		FROM capability_records c
		WHERE c.expires_at > now() AND NOT `+revoked+`
		GROUP BY c.principal_kind`, out.ByPrincipalKind); err != nil {
		return fmt.Errorf("census: capabilities by principal kind: %w", err)
	}
	return nil
}

// ─── API tokens (M2M) ───────────────────────────────────────────────────────

// APITokenCensus counts hashed-bearer M2M tokens by disposition. Same
// exclusive Active / Expired / Revoked contract as CapabilityCensus, here
// off the row's own revoked_at column.
type APITokenCensus struct {
	Total   int64 `json:"total"`
	Active  int64 `json:"active"`
	Expired int64 `json:"expired"`
	Revoked int64 `json:"revoked"`
	// Active tokens expiring within apiTokenExpiringWindow. These are
	// long-lived credentials wired into external systems, so the warning
	// window is a week rather than a day — someone has to go rotate them.
	ExpiringSoon int64 `json:"expiring_soon"`
	// Active tokens that have never authenticated a request. Either
	// provisioning is incomplete or the credential is abandoned; both are
	// worth an operator's attention, and an unused token is pure risk.
	NeverUsed int64 `json:"never_used"`
}

const apiTokenExpiringWindow = "7 days"

func collectAPITokens(ctx context.Context, pool *pgxpool.Pool, out *APITokenCensus) error {
	err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE revoked_at IS NULL AND expires_at >  now()),
		       count(*) FILTER (WHERE revoked_at IS NULL AND expires_at <= now()),
		       count(*) FILTER (WHERE revoked_at IS NOT NULL),
		       count(*) FILTER (WHERE revoked_at IS NULL AND expires_at > now()
		                          AND expires_at <= now() + interval '`+apiTokenExpiringWindow+`'),
		       count(*) FILTER (WHERE revoked_at IS NULL AND expires_at > now()
		                          AND last_used_at IS NULL)
		FROM api_tokens`,
	).Scan(&out.Total, &out.Active, &out.Expired, &out.Revoked,
		&out.ExpiringSoon, &out.NeverUsed)
	if err != nil {
		return fmt.Errorf("census: api tokens: %w", err)
	}
	return nil
}

// ─── Event subscriptions ────────────────────────────────────────────────────

// SubscriptionCensus counts event subscriptions and their sink mix. The
// dispatcher's own delivery backlog is a separate view — see
// admin SystemService.GetDispatcherStats — because it lives on a different
// pod's pool; this is the subscription inventory, not the queue depth.
type SubscriptionCensus struct {
	Total    int64 `json:"total"`
	Enabled  int64 `json:"enabled"`
	Disabled int64 `json:"disabled"`
	// Subscriptions carrying a CEL filter. An unfiltered subscription
	// receives every event its tenant produces — usually intended, but
	// the ratio is a useful sanity check against a runaway fan-out.
	WithFilter int64 `json:"with_filter"`
	// sink_kind → count, over ENABLED rows.
	BySinkKind map[string]int64 `json:"by_sink_kind"`
}

func collectSubscriptions(ctx context.Context, pool *pgxpool.Pool, out *SubscriptionCensus) error {
	out.BySinkKind = map[string]int64{}
	err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE NOT disabled),
		       count(*) FILTER (WHERE disabled),
		       count(*) FILTER (WHERE cel_filter <> '')
		FROM event_subscriptions`,
	).Scan(&out.Total, &out.Enabled, &out.Disabled, &out.WithFilter)
	if err != nil {
		return fmt.Errorf("census: subscriptions: %w", err)
	}
	if err := scanCounts(ctx, pool,
		`SELECT sink_kind, count(*) FROM event_subscriptions
		  WHERE NOT disabled GROUP BY sink_kind`, out.BySinkKind); err != nil {
		return fmt.Errorf("census: subscriptions by sink kind: %w", err)
	}
	return nil
}

// stateOrder pins the object lifecycle order so the console renders the
// same column sequence every poll. States outside the list (a future
// enum member) sort after these, alphabetically.
var stateOrder = map[string]int{
	"PENDING":   0,
	"AVAILABLE": 1,
	"FAILED":    2,
	"DELETED":   3,
}

func sortStates(s []StateStat) {
	sort.Slice(s, func(i, j int) bool {
		oi, oki := stateOrder[s[i].State]
		oj, okj := stateOrder[s[j].State]
		if oki != okj {
			return oki
		}
		if oki && oi != oj {
			return oi < oj
		}
		return s[i].State < s[j].State
	})
}

// sortTenants ranks by object count descending — the cap at maxTenantRows
// then drops the smallest tenants, which is what an operator scanning for
// hot spots wants kept. Ties break on tenant_id so the order is stable
// across polls.
func sortTenants(t []TenantStat) {
	sort.Slice(t, func(i, j int) bool {
		if t[i].TotalCount != t[j].TotalCount {
			return t[i].TotalCount > t[j].TotalCount
		}
		return t[i].TenantID < t[j].TenantID
	})
}
