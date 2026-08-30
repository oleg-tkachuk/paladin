package adapters

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/filter/cel"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TenantRepo satisfies tenant.Repository. The raw pool is needed because the
// sqlc DeleteTenant query bakes in an OCC guard (`AND resource_version = $2`)
// and the current DeleteTenantRequest proto has no resource_version field —
// see Delete below for the unconditional path.
type TenantRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewTenantRepo(q *sqlc.Queries, pool *pgxpool.Pool) *TenantRepo {
	return &TenantRepo{q: q, pool: pool}
}

var _ tenant.Repository = (*TenantRepo)(nil)

// RunInTx runs fn in one transaction on the repo's pool — the seam an
// event-producing handler uses to write a tenant mutation and its outbox
// rows atomically (ADR-0003). The *Tx mutation methods run on the same tx.
func (r *TenantRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("tenant: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenant: commit tx: %w", err)
	}
	return nil
}

func (r *TenantRepo) Create(ctx context.Context, args tenant.CreateTenantArgs) (tenant.Tenant, error) {
	// The tenant insert + the optional default-binding insert already need
	// one tx; RunInTx provides it. A binding FK violation rolls the tenant
	// back too — better to surface the error than half-commit.
	if err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return r.CreateTx(ctx, tx, args)
	}); err != nil {
		return tenant.Tenant{}, err
	}
	return r.Get(ctx, args.TenantID)
}

// CreateTx inserts the tenant (+ optional default binding) on the caller's
// tx so an event-producing handler can write the paladin.tenant.created outbox
// rows atomically with the row (ADR-0003). Typed UNIQUE/FK sentinels are
// preserved.
func (r *TenantRepo) CreateTx(ctx context.Context, tx pgx.Tx, args tenant.CreateTenantArgs) error {
	// tenants.labels is JSONB NOT NULL DEFAULT '{}'. The INSERT binds it
	// explicitly, so a nil []byte becomes SQL NULL and violates the
	// constraint. Normalize to an empty JSON object.
	labels := args.Labels
	if len(labels) == 0 {
		labels = []byte("{}")
	}
	qtx := r.q.WithTx(tx)

	storageLayout := args.StorageLayout
	if storageLayout == "" {
		storageLayout = "shared"
	}
	if err := qtx.CreateTenant(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		args.DisplayName,
		labels,
		args.InheritedCedarPolicy,
		sqlc.TenantStorageLayout(storageLayout),
	); err != nil {
		// Map UNIQUE violations to typed sentinels so the handler can
		// surface ALREADY_EXISTS with the offending field. Constraint
		// names match migrations 001 (PK), 009 (slug), 033 (display_name).
		if pgerr.Is(err, pgerr.UniqueViolation) {
			switch pgerr.Constraint(err) {
			case schema.TenantsPK:
				return tenant.ErrTenantIDConflict
			case schema.TenantsSlugUnique:
				return tenant.ErrSlugConflict
			case schema.TenantsDisplayNameUnique:
				return tenant.ErrDisplayNameConflict
			}
		}
		return fmt.Errorf("create tenant: %w", err)
	}

	// Dedicated layout: provision the tenant's own bucket in the same tx
	// (ADR-0011). The row lands provision_state='pending' with the tenant as
	// owner; the bucket reconciler (backend-routed) creates it physically.
	// The default binding points at it so the tenant's collections land there.
	// Bucket name is derived from the tenant id (globally unique per
	// deployment; an org prefix for cross-account uniqueness is a follow-up).
	if args.StorageLayout == "dedicated" {
		bucketName := "paladin-" + args.TenantID.String()
		actor := actorFromContext(ctx)
		if err := qtx.CreateBucketV2(ctx,
			args.DedicatedBackend, bucketName,
			"", "", []byte("{}"), // display_name, region, labels
			pgUUID(args.TenantID), // owner_tenant_id
			"", []byte("{}"),      // cedar_policy, constraints
			"pending", // provision_state
		); err != nil {
			return fmt.Errorf("create tenant: provision dedicated bucket: %w", err)
		}
		n, err := qtx.SetTenantDefaultBinding(ctx,
			pgUUID(args.TenantID), args.DedicatedBackend, bucketName, actor,
		)
		if err != nil {
			return fmt.Errorf("create tenant: bind dedicated bucket: %w", err)
		}
		if n == 0 {
			// The bucket was inserted a few statements up in this same tx,
			// so zero rows here means the insert and this lookup disagree
			// about the name — a bug, not a user error. Fail the tx rather
			// than leaving a dedicated tenant with no binding.
			return fmt.Errorf(
				"create tenant: dedicated bucket %q/%q not found for binding",
				args.DedicatedBackend, bucketName)
		}
	}

	// Optional default binding. The handler validates that backend +
	// bucket are paired (both empty or both set); we just translate
	// "both set" into a row in tenant_default_bindings.
	if args.DefaultBackendID != "" && args.DefaultBucketName != "" {
		actor := actorFromContext(ctx)
		n, err := qtx.SetTenantDefaultBinding(ctx,
			pgUUID(args.TenantID),
			args.DefaultBackendID,
			args.DefaultBucketName,
			actor,
		)
		if n == 0 && err == nil {
			// Resolved to no bucket: same meaning as the FK violation
			// handled below, but it arrives as a zero count instead.
			return tenant.ErrDefaultBindingBucketMissing
		}
		if err != nil {
			// FK violation = picked bucket doesn't exist on this backend.
			// Surface as a typed sentinel so the handler can return a
			// clean InvalidArgument instead of a Postgres error string.
			if pgerr.Is(err, pgerr.ForeignKeyViolation) &&
				pgerr.ConstraintIs(err, schema.TenantDefaultBindingsBucketFK) {
				return tenant.ErrDefaultBindingBucketMissing
			}
			return fmt.Errorf("create tenant: bind default: %w", err)
		}
	}
	return nil
}

// actorFromContext extracts the caller subject for the audit-style
// `set_by` column on tenant_default_bindings. Falls back to empty
// string when auth isn't established (test paths) — the column is
// NOT NULL with a ” default at the DB level.
func actorFromContext(ctx context.Context) string {
	// Lazy import path — keep this adapter free of circular deps.
	type principal interface{ GetSubject() string }
	if p, ok := ctx.Value(struct{}{}).(principal); ok {
		return p.GetSubject()
	}
	return ""
}

func (r *TenantRepo) Get(ctx context.Context, tenantID uuid.UUID) (tenant.Tenant, error) {
	return r.getWith(ctx, r.q, tenantID)
}

func (r *TenantRepo) getWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID) (tenant.Tenant, error) {
	row, err := q.GetTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return tenant.Tenant{}, err
	}
	t := tenantFromSQLC(row.Tenant)
	t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
	return t, nil
}

// GetBySlug — slug → tenant row. Used by handlers accepting the
// `tenants/{tenant_id_or_slug}` resource-name form. pgx's no-row
// error becomes the domain ErrNotFound so the handler can surface
// CodeNotFound (kept consistent with the Get-by-UUID path).
func (r *TenantRepo) GetBySlug(ctx context.Context, slug string) (tenant.Tenant, error) {
	row, err := r.q.GetTenantBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.Tenant{}, tenant.ErrNotFound
		}
		return tenant.Tenant{}, err
	}
	t := tenantFromSQLC(row.Tenant)
	t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
	return t, nil
}

// TenantDefaultBinding returns the tenant's default (backend, bucket) route,
// with found=false (nil error) when none is set. Implements
// resolve.DefaultBindingLookup — completes the bare (B) collection shape to
// canonical (ADR-0010 Phase 3 / the schema baseline (001_initial_schema.sql)).
func (r *TenantRepo) TenantDefaultBinding(ctx context.Context, tenantID uuid.UUID) (string, string, bool, error) {
	row, err := r.q.GetTenantDefaultBinding(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return row.BackendName, row.BucketName, true, nil
}

// GetDefaultBinding — the richer domain read used by GetTenantDefaultBinding.
func (r *TenantRepo) GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenant.DefaultBinding, error) {
	row, err := r.q.GetTenantDefaultBinding(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.DefaultBinding{}, tenant.ErrNotFound
		}
		return tenant.DefaultBinding{}, err
	}
	return defaultBindingFromSQLC(row.TenantDefaultBinding, row.BackendName, row.BucketName), nil
}

// SetDefaultBinding upserts + reads back (the query is :exec). A bad bucket
// trips the composite FK → ErrDefaultBindingBucketMissing (InvalidArgument).
func (r *TenantRepo) SetDefaultBinding(ctx context.Context, tenantID uuid.UUID, bucket, setBy string) (tenant.DefaultBinding, error) {
	// bucket arrives as "storageBackends/{backend}/buckets/{bucket}" — one
	// reference, per AIP-122. Split here rather than making every caller pass
	// the halves separately.
	backendName, bucketName, err := splitBucketResourceName(bucket)
	if err != nil {
		return tenant.DefaultBinding{}, err
	}
	n, err := r.q.SetTenantDefaultBinding(ctx, pgUUID(tenantID), backendName, bucketName, setBy)
	if err != nil {
		if pgerr.ConstraintIs(err, schema.TenantDefaultBindingsBucketFK) {
			return tenant.DefaultBinding{}, tenant.ErrDefaultBindingBucketMissing
		}
		return tenant.DefaultBinding{}, err
	}
	// Zero rows means the (backend, bucket) pair resolved to nothing — see
	// the note on the query. The FK cannot fire for a row that was never
	// built, so this is the only place the missing bucket is detectable.
	if n == 0 {
		return tenant.DefaultBinding{}, tenant.ErrDefaultBindingBucketMissing
	}
	row, err := r.q.GetTenantDefaultBinding(ctx, pgUUID(tenantID))
	if err != nil {
		return tenant.DefaultBinding{}, err
	}
	return defaultBindingFromSQLC(row.TenantDefaultBinding, row.BackendName, row.BucketName), nil
}

// ClearDefaultBinding is idempotent — 0 rows affected is a no-op success.
func (r *TenantRepo) ClearDefaultBinding(ctx context.Context, tenantID uuid.UUID) error {
	_, err := r.q.ClearTenantDefaultBinding(ctx, pgUUID(tenantID))
	return err
}

func defaultBindingFromSQLC(row sqlc.TenantDefaultBinding,
	backendName, bucketName string,
) tenant.DefaultBinding {
	return tenant.DefaultBinding{
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		BucketID:    uuid.UUID(row.BucketID.Bytes),
		BackendName: backendName,
		BucketName:  bucketName,
		SetAt:       row.SetAt.Time,
		SetBy:       row.SetBy,
	}
}

func (r *TenantRepo) Update(ctx context.Context, args tenant.UpdateTenantArgs) (tenant.Tenant, error) {
	return r.updateWith(ctx, r.q, args)
}

// UpdateTx runs Update on the caller's tx (ADR-0003) so the handler can
// enqueue paladin.tenant.updated atomically with the row update.
func (r *TenantRepo) UpdateTx(ctx context.Context, tx pgx.Tx, args tenant.UpdateTenantArgs) (tenant.Tenant, error) {
	return r.updateWith(ctx, r.q.WithTx(tx), args)
}

func (r *TenantRepo) updateWith(ctx context.Context, q *sqlc.Queries, args tenant.UpdateTenantArgs) (tenant.Tenant, error) {
	var policyHash []byte
	if args.InheritedCedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.InheritedCedarPolicy))
		policyHash = sum[:]
	}
	rows, err := q.UpdateTenant(ctx,
		pgUUID(args.TenantID),
		args.DisplayName,
		args.Labels,
		args.InheritedCedarPolicy,
		policyHash,
		args.ExpectedVersion,
	)
	if err != nil {
		// display_name UNIQUE collision lands here. Map to a typed
		// sentinel so the handler surfaces ALREADY_EXISTS with the
		// offending field.
		if pgerr.Is(err, pgerr.UniqueViolation) &&
			pgerr.ConstraintIs(err, schema.TenantsDisplayNameUnique) {
			return tenant.Tenant{}, tenant.ErrDisplayNameConflict
		}
		return tenant.Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	if rows == 0 {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}
	return r.getWith(ctx, q, args.TenantID)
}

// SoftDelete moves an active row to the trash. Returns ErrAlreadyDeleted
// when the row is already trashed (rows=0 on the conditional UPDATE),
// or ErrVersionMismatch when expected_version > 0 and doesn't match.
// Distinguishing the two by re-reading the row is acceptable here —
// soft-delete is a control-plane op, not on the hot path.
func (r *TenantRepo) SoftDelete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	return r.softDeleteWith(ctx, r.q, tenantID, expectedVersion)
}

// SoftDeleteTx runs SoftDelete on the caller's tx (ADR-0003).
func (r *TenantRepo) SoftDeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, expectedVersion int64) error {
	return r.softDeleteWith(ctx, r.q.WithTx(tx), tenantID, expectedVersion)
}

func (r *TenantRepo) softDeleteWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, expectedVersion int64) error {
	rows, err := q.SoftDeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		return fmt.Errorf("soft-delete tenant: %w", err)
	}
	if rows == 0 {
		// Re-read to disambiguate.
		t, gerr := r.getWith(ctx, q, tenantID)
		if errors.Is(gerr, pgx.ErrNoRows) {
			return tenant.ErrNotFound
		}
		if gerr != nil {
			return fmt.Errorf("soft-delete tenant: probe: %w", gerr)
		}
		if !t.DeletedAt.IsZero() {
			return tenant.ErrAlreadyDeleted
		}
		return tenant.ErrVersionMismatch
	}
	return nil
}

// HardDeleteTx runs HardDelete on the caller's tx (ADR-0003).
func (r *TenantRepo) HardDeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, expectedVersion int64) error {
	return r.hardDeleteWith(ctx, r.q.WithTx(tx), tenantID, expectedVersion)
}

func (r *TenantRepo) hardDeleteWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, expectedVersion int64) error {
	rows, err := q.HardDeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		// FK RESTRICT from collections/users/objects → the tenant still owns
		// data. Map to a typed sentinel so the handler returns a clear
		// FailedPrecondition instead of a generic Internal error.
		//
		// pgerr folds the RESTRICT pair into one Kind: Postgres splits 23503
		// foreign_key_violation from 23001 restrict_violation purely on how
		// the constraint was declared, and checking only the first meant
		// DeleteTenant(force=true) on a tenant with users — which every
		// tenant has — leaked the raw SQL text as CodeInternal instead of
		// saying what was wrong.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) {
			// Name the relation that is actually refusing. The static text
			// said "object keys or objects", which is the internal name for
			// collections plus one guess — and the blocker is just as often
			// `users`, which every tenant has. An operator following that
			// message went looking in the wrong place; a purge blocked by
			// users reported collections.
			return fmt.Errorf("%w: %s", tenant.ErrTenantHasChildren, blockingRelation(err))
		}
		return fmt.Errorf("hard-delete tenant: %w", err)
	}
	if rows == 0 {
		if expectedVersion == 0 {
			return tenant.ErrNotFound
		}
		return tenant.ErrVersionMismatch
	}
	return nil
}

// Restore clears deleted_at on a trashed row. Returns ErrNotTrashed
// when the row is currently active; ErrNotFound when missing; maps
// slug/display_name UNIQUE collisions (a fresh tenant claimed the
// handle while this one was trashed) to typed sentinels.
func (r *TenantRepo) Restore(ctx context.Context, tenantID uuid.UUID) (tenant.Tenant, error) {
	return r.restoreWith(ctx, r.q, tenantID)
}

// RestoreTx runs Restore on the caller's tx (ADR-0003).
func (r *TenantRepo) RestoreTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (tenant.Tenant, error) {
	return r.restoreWith(ctx, r.q.WithTx(tx), tenantID)
}

func (r *TenantRepo) restoreWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID) (tenant.Tenant, error) {
	rows, err := q.RestoreTenant(ctx, pgUUID(tenantID))
	if err != nil {
		// UNIQUE violations can fire even on UPDATE-to-non-NULL paths
		// if a concurrent restore raced; map them.
		if pgerr.Is(err, pgerr.UniqueViolation) {
			switch pgerr.Constraint(err) {
			case schema.TenantsSlugUnique:
				return tenant.Tenant{}, tenant.ErrSlugConflict
			case schema.TenantsDisplayNameUnique:
				return tenant.Tenant{}, tenant.ErrDisplayNameConflict
			}
		}
		return tenant.Tenant{}, fmt.Errorf("restore tenant: %w", err)
	}
	if rows == 0 {
		// Re-read to distinguish missing vs active.
		t, gerr := r.getWith(ctx, q, tenantID)
		if errors.Is(gerr, pgx.ErrNoRows) {
			return tenant.Tenant{}, tenant.ErrNotFound
		}
		if gerr != nil {
			return tenant.Tenant{}, fmt.Errorf("restore tenant: probe: %w", gerr)
		}
		if t.DeletedAt.IsZero() {
			return tenant.Tenant{}, tenant.ErrNotTrashed
		}
		return tenant.Tenant{}, tenant.ErrNotFound
	}
	return r.getWith(ctx, q, tenantID)
}

func (r *TenantRepo) List(ctx context.Context, args tenant.ListTenantsArgs) ([]tenant.Tenant, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	// Pushdown: see admin_bucket.go — the handler's CEL pass over the page
	// stays authoritative, these only narrow the scan.
	pd := hints(cel.TenantSchema, args.Filter)
	slugEq, slugLike := pd.StringHint("slug")
	displayEq, displayLike := pd.StringHint("display_name")
	layoutEq, _ := pd.StringHint("storage_layout")
	createdGTE, createdLTE := createdBounds(pd)

	rows, err := r.q.ListTenants(ctx,
		pgUUID(args.AfterID),
		args.OnlyTrashed,
		args.IncludeTrashed,
		slugEq, slugLike, displayEq, displayLike, layoutEq,
		createdGTE, createdLTE,
		pageSize,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	out := make([]tenant.Tenant, 0, len(rows))
	for _, row := range rows {
		t := tenantFromSQLC(row.Tenant)
		t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
		out = append(out, t)
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].TenantID.String()
	}
	return out, next, nil
}

// Rename atomically rotates the tenant slug AND rewrites every
// `Tenant::"<old_slug>"` reference in the tenant's
// inherited_cedar_policy plus every collections.cedar_policy for the
// tenant. Bumps resource_version on the tenant row (but NOT on the
// collections rows — the rewrite is a derived consequence of the
// tenant slug rotation, not an independent edit; bumping each
// collection's RV would invalidate every in-flight client that holds
// a collection resource_version mid-transaction). Operators that
// need a per-collection audit row can list the affected rows from the
// audit_log entry's after_json.
//
// Uniqueness on the new slug is enforced by the tenants_slug_unique
// constraint (the schema baseline (001_initial_schema.sql)) — caught here as ErrSlugConflict.
//
// Implementation uses a single tx so a partial rewrite (tenant
// updated, collections not) cannot leak.
func (r *TenantRepo) Rename(ctx context.Context, args tenant.RenameTenantSlugArgs) (tenant.Tenant, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Migration 033 added a BEFORE UPDATE trigger that blocks
	// slug changes unless the tx opts in via this session GUC.
	// Set it once at the top of the rename tx so the UPDATE below
	// passes the trigger; the LOCAL scope means it's gone the moment
	// this tx commits or rolls back.
	if _, err := tx.Exec(ctx, "SET LOCAL paladin.allow_slug_rename = on"); err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: enable slug rename: %w", err)
	}

	// Read + lock the tenant row. SELECT FOR UPDATE so a concurrent
	// rename or update can't race past us between the read and the
	// rewrite.
	var oldSlug string
	var oldPolicy string
	var rv int64
	err = tx.QueryRow(ctx,
		`SELECT slug, inherited_cedar_policy, resource_version
		   FROM tenants
		  WHERE id = $1
		    FOR UPDATE`,
		pgUUID(args.TenantID),
	).Scan(&oldSlug, &oldPolicy, &rv)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.Tenant{}, tenant.ErrNotFound
		}
		return tenant.Tenant{}, fmt.Errorf("rename tenant: select: %w", err)
	}
	if rv != args.ExpectedVersion {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}
	if oldSlug == args.NewSlug {
		// Idempotent no-op — nothing to rewrite, no version bump.
		row, err := r.q.GetTenant(ctx, pgUUID(args.TenantID))
		if err != nil {
			return tenant.Tenant{}, err
		}
		t := tenantFromSQLC(row.Tenant)
		t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
		return t, nil
	}

	newPolicy := rewriteTenantSlugRefs(oldPolicy, oldSlug, args.NewSlug)
	newPolicySum := sha256.Sum256([]byte(newPolicy))

	// Bump tenant: slug, inherited_cedar_policy, hash, RV.
	tag, err := tx.Exec(ctx,
		`UPDATE tenants
		    SET slug                   = $2,
		        inherited_cedar_policy = $3,
		        inherited_policy_hash  = $4,
		        resource_version       = resource_version + 1,
		        updated_at             = NOW()
		  WHERE id               = $1
		    AND resource_version = $5`,
		pgUUID(args.TenantID),
		args.NewSlug,
		newPolicy,
		newPolicySum[:],
		args.ExpectedVersion,
	)
	if err != nil {
		// Slug uniqueness violation surfaces as a unique-constraint
		// PG error — translate to ErrSlugConflict so the handler can
		// return AlreadyExists.
		if pgerr.ConstraintIs(err, schema.TenantsSlugUnique) {
			return tenant.Tenant{}, tenant.ErrSlugConflict
		}
		return tenant.Tenant{}, fmt.Errorf("rename tenant: update tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}

	// Rewrite per-collection policies. The cedar_policy column is
	// nullable in some collections rows; use COALESCE so empty
	// policies don't produce phantom hashes.
	rows, err := tx.Query(ctx,
		`SELECT name, cedar_policy
		   FROM collections
		  WHERE tenant_id = $1
		    AND cedar_policy IS NOT NULL
		    AND cedar_policy <> ''
		    FOR UPDATE`,
		pgUUID(args.TenantID),
	)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: list collections: %w", err)
	}
	type okRewrite struct {
		key       string
		newPolicy string
		newHash   []byte
	}
	var rewrites []okRewrite
	for rows.Next() {
		var key, pol string
		if err := rows.Scan(&key, &pol); err != nil {
			rows.Close()
			return tenant.Tenant{}, fmt.Errorf("rename tenant: sca collection: %w", err)
		}
		rewritten := rewriteTenantSlugRefs(pol, oldSlug, args.NewSlug)
		if rewritten == pol {
			continue
		}
		sum := sha256.Sum256([]byte(rewritten))
		rewrites = append(rewrites, okRewrite{key: key, newPolicy: rewritten, newHash: sum[:]})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: iterate collections: %w", err)
	}
	for _, w := range rewrites {
		if _, err := tx.Exec(ctx,
			`UPDATE collections
			    SET cedar_policy      = $3,
			        cedar_policy_hash = $4,
			        resource_version  = resource_version + 1,
			        updated_at        = NOW()
			  WHERE tenant_id = $1
			    AND name      = $2`,
			pgUUID(args.TenantID), w.key, w.newPolicy, w.newHash,
		); err != nil {
			return tenant.Tenant{}, fmt.Errorf("rename tenant: update collection %q: %w", w.key, err)
		}
	}

	// Record the rotation so a later 404 on the old slug can resolve to the
	// new one (see the schema baseline (001_initial_schema.sql)). Same tx as the slug bump: the trail can
	// never disagree with the live slug. The same-slug no-op returned above
	// before reaching here, so old_slug != new_slug always holds.
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenant_slug_history (tenant_id, old_slug, new_slug)
		 VALUES ($1, $2, $3)`,
		pgUUID(args.TenantID), oldSlug, args.NewSlug,
	); err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: record slug history: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: commit: %w", err)
	}
	return r.Get(ctx, args.TenantID)
}

// LookupRenamedSlug returns the most recent rotation away FROM oldSlug within
// `window` (0 = unbounded), from the tenant_slug_history table Rename writes.
// found=false (nil error) when no rotation matches — the resolver maps that to
// NotFound. tenant_id is read as text to avoid pgx uuid-codec registration.
func (r *TenantRepo) LookupRenamedSlug(ctx context.Context, oldSlug string, window time.Duration) (tenant.RenamedSlug, bool, error) {
	q := `SELECT tenant_id::text, new_slug, renamed_at
	        FROM tenant_slug_history
	       WHERE old_slug = $1`
	args := []any{oldSlug}
	if window > 0 {
		q += ` AND renamed_at >= now() - make_interval(secs => $2)`
		args = append(args, window.Seconds())
	}
	q += ` ORDER BY renamed_at DESC LIMIT 1`

	var tidStr string
	var res tenant.RenamedSlug
	err := r.pool.QueryRow(ctx, q, args...).Scan(&tidStr, &res.NewSlug, &res.RenamedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenant.RenamedSlug{}, false, nil
	}
	if err != nil {
		return tenant.RenamedSlug{}, false, fmt.Errorf("lookup renamed slug: %w", err)
	}
	tid, perr := uuid.Parse(tidStr)
	if perr != nil {
		return tenant.RenamedSlug{}, false, fmt.Errorf("lookup renamed slug: parse tenant_id: %w", perr)
	}
	res.TenantID = tid
	return res, true, nil
}

// rewriteTenantSlugRefs replaces every `Tenant::"<oldSlug>"` literal
// with `Tenant::"<newSlug>"`. Match is exact (case-sensitive, full-
// quoted UID) so policies that mention <oldSlug> as a label substring
// elsewhere — e.g. inside a string value — are NOT rewritten. This is
// the safe default: false-positives on an over-eager rewrite would
// leak into unrelated rules and break Cedar parsing.
func rewriteTenantSlugRefs(policy, oldSlug, newSlug string) string {
	if oldSlug == "" || policy == "" {
		return policy
	}
	oldRef := `Tenant::"` + oldSlug + `"`
	newRef := `Tenant::"` + newSlug + `"`
	return strings.ReplaceAll(policy, oldRef, newRef)
}

// defaultBucketName composes the tenant's default-binding resource name from
// the LEFT-JOINed tenant_default_bindings columns. Both are NULL (→ nil) when
// the tenant has no binding, yielding "" (no default).
// The LEFT JOIN COALESCEs both names to ”, so an unbound tenant yields the
// empty resource name rather than "storageBackends//buckets/".
func defaultBucketName(backendName, bucketName string) string {
	if backendName == "" || bucketName == "" {
		return ""
	}
	return fmt.Sprintf("storageBackends/%s/buckets/%s", backendName, bucketName)
}

func tenantFromSQLC(t sqlc.Tenant) tenant.Tenant {
	return tenant.Tenant{
		TenantID:             uuidFrom(t.ID),
		Slug:                 t.Slug,
		DisplayName:          t.DisplayName,
		Labels:               t.Labels,
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		InheritedPolicyHash:  t.InheritedPolicyHash,
		ResourceVersion:      t.ResourceVersion,
		CreatedAt:            timeFrom(t.CreatedAt),
		UpdatedAt:            timeFrom(t.UpdatedAt),
		DeletedAt:            timeFrom(t.DeletedAt),
		StorageLayout:        string(t.StorageLayout),
	}
}

// splitBucketResourceName parses "storageBackends/{backend}/buckets/{bucket}".
// Anything else is a caller error, not a lookup miss, so it returns
// ErrDefaultBindingBucketMissing rather than silently binding nothing.
func splitBucketResourceName(name string) (backend, bucket string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "storageBackends" || parts[2] != "buckets" ||
		parts[1] == "" || parts[3] == "" {
		return "", "", tenant.ErrDefaultBindingBucketMissing
	}
	return parts[1], parts[3], nil
}
