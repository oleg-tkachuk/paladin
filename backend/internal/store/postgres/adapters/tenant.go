package adapters

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"
	"uuid"

	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
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

var _ tenanth.Repository = (*TenantRepo)(nil)

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

func (r *TenantRepo) Create(ctx context.Context, args tenanth.CreateTenantArgs) (tenanth.Tenant, error) {
	// The tenant insert + the optional default-binding insert already need
	// one tx; RunInTx provides it. A binding FK violation rolls the tenant
	// back too — better to surface the error than half-commit.
	if err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return r.CreateTx(ctx, tx, args)
	}); err != nil {
		return tenanth.Tenant{}, err
	}
	return r.Get(ctx, args.TenantID)
}

// CreateTx inserts the tenant (+ optional default binding) on the caller's
// tx so an event-producing handler can write the paladin.tenant.created outbox
// rows atomically with the row (ADR-0003). Typed UNIQUE/FK sentinels are
// preserved.
func (r *TenantRepo) CreateTx(ctx context.Context, tx pgx.Tx, args tenanth.CreateTenantArgs) error {
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
		storageLayout = tenanth.StorageLayoutShared
	}
	// While slugs are reserved, a slug a trashed tenant still holds or a
	// purged one held is refused before the insert, in one read so a purge
	// committing meanwhile cannot hide it. A live holder is left to the
	// unique index, which reports it as ErrSlugConflict.
	if !args.AllowSlugReuse {
		holder, err := qtx.TenantSlugHolder(ctx, args.Slug)
		if err != nil {
			return fmt.Errorf("create tenant: slug holder: %w", err)
		}
		switch tenanth.SlugHolder(holder) {
		case tenanth.SlugHolderTrashed:
			return tenanth.ErrSlugHeldInTrash
		case tenanth.SlugHolderRetired:
			return tenanth.ErrSlugRetired
		}
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
		// names match `001_initial_schema.sql` (the PK and the live-tenant
		// unique indexes on slug and display_name).
		if pgerr.Is(err, pgerr.UniqueViolation) {
			switch pgerr.Constraint(err) {
			case schema.TenantsPK:
				return tenanth.ErrTenantIDConflict
			case schema.TenantsSlugUnique:
				return tenanth.ErrSlugConflict
			case schema.TenantsDisplayNameUnique:
				return tenanth.ErrDisplayNameConflict
			}
		}
		return fmt.Errorf("create tenant: %w", err)
	}

	// Dedicated layout: provision the tenant's own bucket in the same tx
	// (ADR-0015). The row lands provision_state='pending' with the tenant as
	// owner; the bucket reconciler (backend-routed) creates it physically.
	// The default binding points at it so the tenant's collections land there.
	// Bucket name is derived from the tenant id (globally unique per
	// deployment; an org prefix for cross-account uniqueness is a follow-up).
	if args.StorageLayout == tenanth.StorageLayoutDedicated {
		bucketName := "paladin-" + args.TenantID.String()
		actor := actorFromContext(ctx)
		if err := qtx.CreateBucketV2(ctx,
			args.DedicatedBackend, bucketName,
			"", "", []byte("{}"), // display_name, region, labels
			pgUUID(args.TenantID), // owner_tenant_id
			"", []byte("{}"),      // cedar_policy, constraints
			"pending", // provision_state
			false, "", // public_read, public_base_url: a tenant's dedicated bucket is private
			true, // created_on_backend: the reconciler creates this bucket, named for the tenant
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
			return tenanth.ErrDefaultBindingBucketMissing
		}
		if err != nil {
			// FK violation = picked bucket doesn't exist on this backend.
			// Surface as a typed sentinel so the handler can return a
			// clean InvalidArgument instead of a Postgres error string.
			if pgerr.Is(err, pgerr.ForeignKeyViolation) &&
				pgerr.ConstraintIs(err, schema.TenantDefaultBindingsBucketFK) {
				return tenanth.ErrDefaultBindingBucketMissing
			}
			return fmt.Errorf("create tenant: bind default: %w", err)
		}
		// A new tenant owns no bucket, so the shared bucket it is bound to is
		// granted to it here, in the same transaction (ADR-0032): creating a
		// tenant on the operator's pool is letting it in.
		if _, err := qtx.GrantBucket(ctx, pgUUID(args.TenantID), actor,
			args.DefaultBackendID, args.DefaultBucketName); err != nil {
			return fmt.Errorf("create tenant: grant default bucket: %w", err)
		}
	}
	return nil
}

// actorFromContext extracts the caller subject for the audit-style
// `set_by` column on tenant_default_bindings. Falls back to the empty
// string when no principal is established — bootstrap and seed paths
// create tenants with no caller, and the column is NOT NULL with an
// empty default at the DB level.
//
// This used to read ctx.Value(struct{}{}) and assert to a local
// `interface{ GetSubject() string }`, on the stated grounds of avoiding a
// circular import. It could not match: auth stores the principal under its
// own unexported principalKey{}, which is a different type, and *Principal
// has a Subject field and no GetSubject method — so both the lookup and the
// assertion failed, and every binding created with the tenant recorded an
// empty actor. There is no cycle to avoid; this package already imports
// internal/auth for the user repository.
func actorFromContext(ctx context.Context) string {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return ""
	}
	return p.Subject
}

func (r *TenantRepo) Get(ctx context.Context, tenantID uuid.UUID) (tenanth.Tenant, error) {
	return r.getWith(ctx, r.q, tenantID)
}

func (r *TenantRepo) getWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID) (tenanth.Tenant, error) {
	row, err := q.GetTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return tenanth.Tenant{}, err
	}
	t := tenantFromSQLC(row.Tenant)
	t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
	return t, nil
}

// GetBySlug — slug → tenant row. Used by handlers accepting the
// `tenants/{tenant_id_or_slug}` resource-name form. pgx's no-row
// error becomes the domain ErrNotFound so the handler can surface
// CodeNotFound (kept consistent with the Get-by-UUID path).
func (r *TenantRepo) GetBySlug(ctx context.Context, slug string) (tenanth.Tenant, error) {
	row, err := r.q.GetTenantBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenanth.Tenant{}, tenanth.ErrNotFound
		}
		return tenanth.Tenant{}, err
	}
	t := tenantFromSQLC(row.Tenant)
	t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
	return t, nil
}

// TenantDefaultBinding returns the tenant's default (backend, bucket) route,
// with found=false (nil error) when none is set. Implements
// resolve.DefaultBindingLookup — completes the bare (B) collection shape to
// canonical (ADR-0014 Phase 3 / the schema baseline (001_initial_schema.sql)).
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
func (r *TenantRepo) GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenanth.DefaultBinding, error) {
	row, err := r.q.GetTenantDefaultBinding(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenanth.DefaultBinding{}, tenanth.ErrNotFound
		}
		return tenanth.DefaultBinding{}, err
	}
	return defaultBindingFromSQLC(row.TenantDefaultBinding, row.BackendName, row.BucketName), nil
}

// SetDefaultBinding upserts + reads back (the query is :exec). A bad bucket
// trips the composite FK → ErrDefaultBindingBucketMissing (InvalidArgument).
func (r *TenantRepo) SetDefaultBinding(ctx context.Context, tenantID uuid.UUID, bucket, setBy string) (tenanth.DefaultBinding, error) {
	// bucket arrives as "storageBackends/{backend}/buckets/{bucket}" — one
	// reference, per AIP-122. Split here rather than making every caller pass
	// the halves separately.
	backendName, bucketName, err := splitBucketResourceName(bucket)
	if err != nil {
		return tenanth.DefaultBinding{}, err
	}
	// A default binding is where the tenant's collections go, so it names
	// only a bucket the tenant owns or holds a grant on (ADR-0032); the
	// binding trigger would refuse the first collection otherwise.
	switch may, err := r.q.TenantMayBindBucket(ctx, pgUUID(tenantID), backendName, bucketName); {
	case errors.Is(err, pgx.ErrNoRows):
		return tenanth.DefaultBinding{}, tenanth.ErrDefaultBindingBucketMissing
	case err != nil:
		return tenanth.DefaultBinding{}, fmt.Errorf("set default binding: may bind: %w", err)
	case !may:
		return tenanth.DefaultBinding{}, admindomain.ErrBucketNotGranted
	}
	n, err := r.q.SetTenantDefaultBinding(ctx, pgUUID(tenantID), backendName, bucketName, setBy)
	if err != nil {
		if pgerr.ConstraintIs(err, schema.TenantDefaultBindingsBucketFK) {
			return tenanth.DefaultBinding{}, tenanth.ErrDefaultBindingBucketMissing
		}
		return tenanth.DefaultBinding{}, err
	}
	// Zero rows means the (backend, bucket) pair resolved to nothing — see
	// the note on the query. The FK cannot fire for a row that was never
	// built, so this is the only place the missing bucket is detectable.
	if n == 0 {
		return tenanth.DefaultBinding{}, tenanth.ErrDefaultBindingBucketMissing
	}
	row, err := r.q.GetTenantDefaultBinding(ctx, pgUUID(tenantID))
	if err != nil {
		return tenanth.DefaultBinding{}, err
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
) tenanth.DefaultBinding {
	return tenanth.DefaultBinding{
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		BucketID:    uuid.UUID(row.BucketID.Bytes),
		BackendName: backendName,
		BucketName:  bucketName,
		SetAt:       row.SetAt.Time,
		SetBy:       row.SetBy,
	}
}

func (r *TenantRepo) Update(ctx context.Context, args tenanth.UpdateTenantArgs) (tenanth.Tenant, error) {
	return r.updateWith(ctx, r.q, args)
}

// UpdateTx runs Update on the caller's tx (ADR-0003) so the handler can
// enqueue paladin.tenant.updated atomically with the row update.
func (r *TenantRepo) UpdateTx(ctx context.Context, tx pgx.Tx, args tenanth.UpdateTenantArgs) (tenanth.Tenant, error) {
	return r.updateWith(ctx, r.q.WithTx(tx), args)
}

func (r *TenantRepo) updateWith(ctx context.Context, q *sqlc.Queries, args tenanth.UpdateTenantArgs) (tenanth.Tenant, error) {
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
			return tenanth.Tenant{}, tenanth.ErrDisplayNameConflict
		}
		return tenanth.Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	if rows == 0 {
		// Nothing matched: a stale version, or a tenant in the trash. Read
		// the row to say which.
		if t, gerr := r.getWith(ctx, q, args.TenantID); gerr == nil && !t.DeletedAt.IsZero() {
			return tenanth.Tenant{}, tenanth.ErrAlreadyDeleted
		}
		return tenanth.Tenant{}, tenanth.ErrVersionMismatch
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
			return tenanth.ErrNotFound
		}
		if gerr != nil {
			return fmt.Errorf("soft-delete tenant: probe: %w", gerr)
		}
		if !t.DeletedAt.IsZero() {
			return tenanth.ErrAlreadyDeleted
		}
		return tenanth.ErrVersionMismatch
	}
	return nil
}

// HardDeleteTx runs HardDelete on the caller's tx (ADR-0003): the tenant row
// and what cascades from it. The limes schema keeps no foreign key to
// tenants, so its rows stay; Purge erases them with the tenant.
func (r *TenantRepo) HardDeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, expectedVersion int64) error {
	return r.hardDeleteWith(ctx, r.q.WithTx(tx), tenantID, expectedVersion)
}

// purgeNote is the note each capability of a purged tenant is revoked with.
const purgeNote = "tenant purged"

// Purge implements tenanth.Repository: pgstore erases the tenant's
// capabilities and meter, and the tenant row goes in the same transaction
// (onErased), so both go or neither does. A capability not yet past
// limes.MinPurgeRetention stays on record, revoked, until the purger takes
// it: an id no longer on record is not revoked, and its token would verify
// again.
//
// The erase is scoped to the tenant it removes, whoever asks: the platform
// admin who purges it acts from another tenant. PurgeTenant authorises the
// call before it gets here.
func (r *TenantRepo) Purge(ctx context.Context, args tenanth.PurgeTenantArgs) error {
	records, err := capabilitypg.New(r.pool)
	if err != nil {
		return fmt.Errorf("purge tenant: %w", err)
	}
	ctx = auth.WithActingTenant(ctx, args.TenantID)
	_, err = records.EraseTenant(ctx, limes.EraseTenantRequest{TenantID: args.TenantID, Note: purgeNote, Actor: args.Actor},
		func(ctx context.Context, e pgstore.Erased) error {
			return r.hardDeleteWith(ctx, r.q.WithTx(e.Tx), args.TenantID, 0)
		})
	return err
}

func (r *TenantRepo) hardDeleteWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, expectedVersion int64) error {
	// The slug is retired on the delete's own transaction, whatever
	// admin.tenants.reserve_slugs says: it decides whether a retired slug is
	// refused, not whether it is recorded. Both callers run on a tx, so a
	// delete refused below takes this insert back with it.
	if err := q.RetireTenantSlug(ctx, pgUUID(tenantID)); err != nil {
		return fmt.Errorf("retire tenant slug: %w", err)
	}
	rows, err := q.HardDeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		// A legal hold or an unexpired retention window refuses the purge
		// before any foreign key is consulted (061). The retention trigger on
		// object_locks raises the same refusal if a cascade reaches a lock row
		// first. Either way it is a precondition the operator can act on, not
		// an Internal error carrying raw SQL.
		if msg, ok := lockRefusal(err); ok {
			return fmt.Errorf("%w: %s", tenanth.ErrTenantHoldsLockedObjects, msg)
		}
		// FK RESTRICT from collections/users/objects → the tenant still owns
		// data. Map to a typed sentinel so the handler returns a clear
		// FailedPrecondition instead of a generic Internal error.
		//
		// pgerr folds the RESTRICT pair into one Kind: Postgres splits 23503
		// foreign_key_violation from 23001 restrict_violation purely on how
		// the constraint was declared, and checking only the first meant
		// purging a tenant with users — which every tenant has — leaked the raw SQL text as CodeInternal instead of
		// saying what was wrong.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) {
			// Name the relation that is actually refusing. The static text
			// said "object keys or objects", which is the internal name for
			// collections plus one guess — and the blocker is just as often
			// `users`, which every tenant has. An operator following that
			// message went looking in the wrong place; a purge blocked by
			// users reported collections.
			return fmt.Errorf("%w: %s", tenanth.ErrTenantHasChildren, blockingRelation(err))
		}
		return fmt.Errorf("hard-delete tenant: %w", err)
	}
	if rows == 0 {
		if expectedVersion == 0 {
			return tenanth.ErrNotFound
		}
		return tenanth.ErrVersionMismatch
	}
	return nil
}

// Restore clears deleted_at on a trashed row. Returns ErrNotTrashed
// when the row is currently active; ErrNotFound when missing; maps
// slug/display_name UNIQUE collisions (a fresh tenant claimed the
// handle while this one was trashed) to typed sentinels.
func (r *TenantRepo) Restore(ctx context.Context, tenantID uuid.UUID) (tenanth.Tenant, error) {
	return r.restoreWith(ctx, r.q, tenantID)
}

// RestoreTx runs Restore on the caller's tx (ADR-0003).
func (r *TenantRepo) RestoreTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (tenanth.Tenant, error) {
	return r.restoreWith(ctx, r.q.WithTx(tx), tenantID)
}

func (r *TenantRepo) restoreWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID) (tenanth.Tenant, error) {
	rows, err := q.RestoreTenant(ctx, pgUUID(tenantID))
	if err != nil {
		// UNIQUE violations can fire even on UPDATE-to-non-NULL paths
		// if a concurrent restore raced; map them.
		if pgerr.Is(err, pgerr.UniqueViolation) {
			switch pgerr.Constraint(err) {
			case schema.TenantsSlugUnique:
				return tenanth.Tenant{}, tenanth.ErrSlugConflict
			case schema.TenantsDisplayNameUnique:
				return tenanth.Tenant{}, tenanth.ErrDisplayNameConflict
			}
		}
		return tenanth.Tenant{}, fmt.Errorf("restore tenant: %w", err)
	}
	if rows == 0 {
		// Re-read to distinguish missing vs active.
		t, gerr := r.getWith(ctx, q, tenantID)
		if errors.Is(gerr, pgx.ErrNoRows) {
			return tenanth.Tenant{}, tenanth.ErrNotFound
		}
		if gerr != nil {
			return tenanth.Tenant{}, fmt.Errorf("restore tenant: probe: %w", gerr)
		}
		if t.DeletedAt.IsZero() {
			return tenanth.Tenant{}, tenanth.ErrNotTrashed
		}
		return tenanth.Tenant{}, tenanth.ErrNotFound
	}
	return r.getWith(ctx, q, tenantID)
}

// purgeEligibleField is the derived TenantSchema field ListTenants pushes down.
const purgeEligibleField = "purge_eligible"

// tenantLabelsField is the TenantSchema map field the labels column answers.
const tenantLabelsField = "labels"

func (r *TenantRepo) List(ctx context.Context, args tenanth.ListTenantsArgs) ([]tenanth.Tenant, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)
	// Pushdown: see admin_bucket.go — the handler's CEL pass over the page
	// stays authoritative, these only narrow the scan. One query per
	// disjunct of the filter, merged in id order (fanOut).
	rows, err := fanOut(branchHints(cel.TenantSchema, args.Filter), pageSize,
		func(pd cel.Pushdown) ([]sqlc.ListTenantsRow, error) {
			return r.listBranch(ctx, args, pd, pageSize)
		},
		func(a, b sqlc.ListTenantsRow) bool { return uuidBefore(a.Tenant.ID, b.Tenant.ID) },
		func(row sqlc.ListTenantsRow) [16]byte { return row.Tenant.ID.Bytes },
	)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	out := make([]tenanth.Tenant, 0, len(rows))
	for _, row := range rows {
		t := tenantFromSQLC(row.Tenant)
		t.DefaultBucket = defaultBucketName(row.BackendName, row.BucketName)
		out = append(out, t)
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		next = out[len(out)-1].TenantID.String()
	}
	return out, next, nil
}

// ListRetiredSlugs is a page of retired_tenant_slugs by slug.
func (r *TenantRepo) ListRetiredSlugs(ctx context.Context, afterSlug string, pageSize int32) ([]tenanth.RetiredSlug, string, error) {
	limit := pageSizeOrDefault(pageSize)
	rows, err := r.q.ListRetiredTenantSlugs(ctx, afterSlug, limit)
	if err != nil {
		return nil, "", fmt.Errorf("list retired tenant slugs: %w", err)
	}
	out := make([]tenanth.RetiredSlug, 0, len(rows))
	for _, row := range rows {
		out = append(out, tenanth.RetiredSlug{
			Slug:           row.Slug,
			FormerTenantID: uuidFrom(row.FormerTenantID),
			RetiredAt:      row.RetiredAt.Time,
		})
	}
	var next string
	if len(out) == int(limit) {
		next = out[len(out)-1].Slug
	}
	return out, next, nil
}

// ReleaseSlug deletes a retired slug's row and returns whose it was.
func (r *TenantRepo) ReleaseSlug(ctx context.Context, slug string) (uuid.UUID, error) {
	former, err := r.q.ReleaseTenantSlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil(), tenanth.ErrSlugNotRetired
	}
	if err != nil {
		return uuid.Nil(), fmt.Errorf("release tenant slug: %w", err)
	}
	return uuidFrom(former), nil
}

// listBranch is one page of ListTenants narrowed by one pushdown branch.
func (r *TenantRepo) listBranch(ctx context.Context, args tenanth.ListTenantsArgs, pd cel.Pushdown, pageSize int32) ([]sqlc.ListTenantsRow, error) {
	slugIn, slugLike := pd.StringHint("slug")
	displayIn, displayLike := pd.StringHint("display_name")
	layoutIn, _ := pd.StringHint("storage_layout")
	labelsContain := pd.MapContainsHint(tenantLabelsField)
	// Only the `like` half — the derived field exists for a search box, which
	// emits `contains`. See admin_bucket.go.
	_, searchLike := pd.StringHint("search")
	createdGTE, createdLTE := createdBounds(pd)
	// purge_eligible is judged by the handler's retention and instant, the
	// same pair its CEL pass projects every row with.
	var purgeEligible *bool
	var retentionSeconds *float64
	var asOf pgtype.Timestamptz
	if want, ok := pd.BoolEq[purgeEligibleField]; ok {
		secs := args.TrashRetention.Seconds()
		purgeEligible, retentionSeconds = &want, &secs
		asOf = pgtype.Timestamptz{Time: args.AsOf, Valid: true}
	}
	return r.q.ListTenants(ctx,
		pgUUID(args.AfterID),
		args.OnlyTrashed,
		args.IncludeTrashed,
		slugIn, slugLike, displayIn, displayLike, searchLike, layoutIn, labelsContain,
		createdGTE, createdLTE,
		purgeEligible, retentionSeconds, asOf,
		pageSize,
	)
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

func tenantFromSQLC(t sqlc.Tenant) tenanth.Tenant {
	return tenanth.Tenant{
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
		return "", "", tenanth.ErrDefaultBindingBucketMissing
	}
	return parts[1], parts[3], nil
}

var _ tenanth.Names = (*TenantRepo)(nil)

// TenantNames implements tenanth.Names, live tenants and those in the trash.
func (r *TenantRepo) TenantNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]tenanth.Name, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]tenanth.Name{}, nil
	}
	keys := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, pgtype.UUID{Bytes: id, Valid: true})
	}
	rows, err := r.q.GetTenantNames(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("tenant names: %w", err)
	}
	out := make(map[uuid.UUID]tenanth.Name, len(rows))
	for _, row := range rows {
		out[uuid.UUID(row.ID.Bytes)] = tenanth.Name{
			Slug: row.Slug, DisplayName: row.DisplayName, Deleted: row.DeletedAt.Valid,
		}
	}
	return out, nil
}

// PreviewPurge reads what Purge would remove with the tenant and what would
// refuse it, in one repeatable-read snapshot, acting on the tenant so its
// row-level security shows its rows. The capability count is pgstore's
// census of the tenant's records, which Purge erases.
func (r *TenantRepo) PreviewPurge(ctx context.Context, tenantID uuid.UUID, names int) (tenanth.PurgePreview, error) {
	ctx = auth.WithActingTenant(ctx, tenantID)
	var out tenanth.PurgePreview
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("preview purge: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q, id, limit := r.q.WithTx(tx), pgUUID(tenantID), int32(min(names, math.MaxInt32)) // #nosec G115 -- bounded above
	tokens, err := q.ListPurgeAPITokens(ctx, id, limit)
	if err != nil {
		return out, fmt.Errorf("preview purge: api tokens: %w", err)
	}
	for _, t := range tokens {
		out.APITokens = append(out.APITokens, tenanth.PurgedAPIToken{ID: uuidFrom(t.ID), Name: t.Name})
	}
	if out.APITokenCount, err = q.CountPurgeAPITokens(ctx, id); err != nil {
		return out, fmt.Errorf("preview purge: count api tokens: %w", err)
	}
	subs, err := q.ListPurgeEventSubscriptions(ctx, id, limit)
	if err != nil {
		return out, fmt.Errorf("preview purge: event subscriptions: %w", err)
	}
	for _, s := range subs {
		out.Subscriptions = append(out.Subscriptions, tenanth.PurgedSubscription{ID: uuidFrom(s.ID), SinkKind: string(s.SinkKind)})
	}
	if out.SubscriptionCount, err = q.CountPurgeEventSubscriptions(ctx, id); err != nil {
		return out, fmt.Errorf("preview purge: count event subscriptions: %w", err)
	}
	blockers, err := q.CountPurgeBlockers(ctx, id)
	if err != nil {
		return out, fmt.Errorf("preview purge: blockers: %w", err)
	}
	out.Blockers = tenanth.PurgeBlockers{
		Collections:     blockers.Collections,
		Buckets:         blockers.Buckets,
		BucketsDeleting: blockers.BucketsDeleting,
		Users:           blockers.Users,
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("preview purge: commit: %w", err)
	}
	if out.CapabilityCount, err = r.capabilityCount(ctx, tenantID); err != nil {
		return out, fmt.Errorf("preview purge: capabilities: %w", err)
	}
	return out, nil
}

// capabilityCount is how many capability records of tenantID pgstore holds,
// every disposition counted.
func (r *TenantRepo) capabilityCount(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	census, err := capabilitypg.NewCensus(r.pool)
	if err != nil {
		return 0, err
	}
	page, err := census.CountRecords(ctx, limes.CountRecordsRequest{TenantID: tenantID})
	if err != nil {
		return 0, err
	}
	var total int64
	for _, c := range page.Items {
		total += c.Total
	}
	return total, nil
}
