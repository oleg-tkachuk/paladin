package adapters

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
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

func (r *TenantRepo) Create(ctx context.Context, args tenant.CreateTenantArgs) (tenant.Tenant, error) {
	// tenants.labels is JSONB NOT NULL DEFAULT '{}'. The INSERT binds it
	// explicitly, so a nil []byte becomes SQL NULL and violates the
	// constraint. Normalize to an empty JSON object.
	labels := args.Labels
	if len(labels) == 0 {
		labels = []byte("{}")
	}

	// One tx covers the tenant insert + the optional default-binding
	// insert so a tenant never lands without its operator-chosen
	// (backend, bucket) when one was supplied. If the binding INSERT
	// trips the bucket FK (operator typo, race with bucket delete),
	// the tenant is rolled back too — better to surface the error to
	// the operator than to half-commit.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("create tenant: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)

	if err := qtx.CreateTenant(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		args.DisplayName,
		labels,
		args.InheritedCedarPolicy,
	); err != nil {
		// Map UNIQUE violations to typed sentinels so the handler can
		// surface ALREADY_EXISTS with the offending field. Constraint
		// names match migrations 001 (PK), 009 (slug), 033 (display_name).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case schema.TenantsPK:
				return tenant.Tenant{}, tenant.ErrTenantIDConflict
			case schema.TenantsSlugUnique:
				return tenant.Tenant{}, tenant.ErrSlugConflict
			case schema.TenantsDisplayNameUnique:
				return tenant.Tenant{}, tenant.ErrDisplayNameConflict
			}
		}
		return tenant.Tenant{}, fmt.Errorf("create tenant: %w", err)
	}

	// Optional default binding. The handler validates that backend +
	// bucket are paired (both empty or both set); we just translate
	// "both set" into a row in tenant_default_bindings.
	if args.DefaultBackendID != "" && args.DefaultBucketName != "" {
		actor := actorFromContext(ctx)
		if err := qtx.SetTenantDefaultBinding(ctx,
			pgUUID(args.TenantID),
			args.DefaultBackendID,
			args.DefaultBucketName,
			actor,
		); err != nil {
			// FK violation = picked bucket doesn't exist on this backend.
			// Surface as a typed sentinel so the handler can return a
			// clean InvalidArgument instead of a Postgres error string.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" &&
				pgErr.ConstraintName == schema.TenantDefaultBindingsBucketFK {
				return tenant.Tenant{}, tenant.ErrDefaultBindingBucketMissing
			}
			return tenant.Tenant{}, fmt.Errorf("create tenant: bind default: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return tenant.Tenant{}, fmt.Errorf("create tenant: commit: %w", err)
	}
	return r.Get(ctx, args.TenantID)
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
	row, err := r.q.GetTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return tenant.Tenant{}, err
	}
	return tenantFromSQLC(row.Tenant), nil
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
	return tenantFromSQLC(row.Tenant), nil
}

func (r *TenantRepo) Update(ctx context.Context, args tenant.UpdateTenantArgs) (tenant.Tenant, error) {
	var policyHash []byte
	if args.InheritedCedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.InheritedCedarPolicy))
		policyHash = sum[:]
	}
	rows, err := r.q.UpdateTenant(ctx,
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == schema.TenantsDisplayNameUnique {
			return tenant.Tenant{}, tenant.ErrDisplayNameConflict
		}
		return tenant.Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	if rows == 0 {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID)
}

// SoftDelete moves an active row to the trash. Returns ErrAlreadyDeleted
// when the row is already trashed (rows=0 on the conditional UPDATE),
// or ErrVersionMismatch when expected_version > 0 and doesn't match.
// Distinguishing the two by re-reading the row is acceptable here —
// soft-delete is a control-plane op, not on the hot path.
func (r *TenantRepo) SoftDelete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	rows, err := r.q.SoftDeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		return fmt.Errorf("soft-delete tenant: %w", err)
	}
	if rows == 0 {
		// Re-read to disambiguate.
		t, gerr := r.Get(ctx, tenantID)
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

// HardDelete physically removes the row. expected_version=0 means
// "no OCC guard" (purge path); a non-zero value enforces match.
func (r *TenantRepo) HardDelete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	rows, err := r.q.HardDeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		// FK RESTRICT from object_keys/objects → the tenant still owns
		// data. Map to a typed sentinel so the handler returns a clear
		// FailedPrecondition instead of a generic Internal error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return tenant.ErrTenantHasChildren
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
	rows, err := r.q.RestoreTenant(ctx, pgUUID(tenantID))
	if err != nil {
		// UNIQUE violations can fire even on UPDATE-to-non-NULL paths
		// if a concurrent restore raced; map them.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
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
		t, gerr := r.Get(ctx, tenantID)
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
	return r.Get(ctx, tenantID)
}

func (r *TenantRepo) List(ctx context.Context, args tenant.ListTenantsArgs) ([]tenant.Tenant, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	rows, err := r.q.ListTenants(ctx,
		pgUUID(args.AfterID),
		args.OnlyTrashed,
		args.IncludeTrashed,
		pageSize,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	out := make([]tenant.Tenant, 0, len(rows))
	for _, row := range rows {
		out = append(out, tenantFromSQLC(row.Tenant))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].TenantID.String()
	}
	return out, next, nil
}

// Rename atomically rotates the tenant slug AND rewrites every
// `Tenant::"<old_slug>"` reference in the tenant's
// inherited_cedar_policy plus every object_keys.cedar_policy for the
// tenant. Bumps resource_version on the tenant row (but NOT on the
// object_keys rows — the rewrite is a derived consequence of the
// tenant slug rotation, not an independent edit; bumping each
// object_key's RV would invalidate every in-flight client that holds
// an object_key resource_version mid-transaction). Operators that
// need a per-objectKey audit row can list the affected rows from the
// audit_log entry's after_json.
//
// Uniqueness on the new slug is enforced by the tenants_slug_unique
// constraint (migration 009) — caught here as ErrSlugConflict.
//
// Implementation uses a single tx so a partial rewrite (tenant
// updated, object_keys not) cannot leak.
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
		  WHERE tenant_id = $1
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
		return tenantFromSQLC(row.Tenant), nil
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
		  WHERE tenant_id        = $1
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == schema.TenantsSlugUnique {
			return tenant.Tenant{}, tenant.ErrSlugConflict
		}
		return tenant.Tenant{}, fmt.Errorf("rename tenant: update tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}

	// Rewrite per-objectKey policies. The cedar_policy column is
	// nullable in some object_keys rows; use COALESCE so empty
	// policies don't produce phantom hashes.
	rows, err := tx.Query(ctx,
		`SELECT object_key, cedar_policy
		   FROM object_keys
		  WHERE tenant_id = $1
		    AND cedar_policy IS NOT NULL
		    AND cedar_policy <> ''
		    FOR UPDATE`,
		pgUUID(args.TenantID),
	)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: list object_keys: %w", err)
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
			return tenant.Tenant{}, fmt.Errorf("rename tenant: scan object_key: %w", err)
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
		return tenant.Tenant{}, fmt.Errorf("rename tenant: iterate object_keys: %w", err)
	}
	for _, w := range rewrites {
		if _, err := tx.Exec(ctx,
			`UPDATE object_keys
			    SET cedar_policy      = $3,
			        cedar_policy_hash = $4,
			        resource_version  = resource_version + 1,
			        updated_at        = NOW()
			  WHERE tenant_id  = $1
			    AND object_key = $2`,
			pgUUID(args.TenantID), w.key, w.newPolicy, w.newHash,
		); err != nil {
			return tenant.Tenant{}, fmt.Errorf("rename tenant: update object_key %q: %w", w.key, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return tenant.Tenant{}, fmt.Errorf("rename tenant: commit: %w", err)
	}
	return r.Get(ctx, args.TenantID)
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

func tenantFromSQLC(t sqlc.Tenant) tenant.Tenant {
	return tenant.Tenant{
		TenantID:             uuidFrom(t.TenantID),
		Slug:                 t.Slug,
		DisplayName:          t.DisplayName,
		Labels:               t.Labels,
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		InheritedPolicyHash:  t.InheritedPolicyHash,
		ResourceVersion:      t.ResourceVersion,
		CreatedAt:            timeFrom(t.CreatedAt),
		UpdatedAt:            timeFrom(t.UpdatedAt),
		DeletedAt:            timeFrom(t.DeletedAt),
	}
}
