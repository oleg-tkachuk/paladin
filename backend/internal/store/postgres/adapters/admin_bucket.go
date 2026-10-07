package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type BucketRepoV2 struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewBucketRepoV2(q *sqlc.Queries, pool *pgxpool.Pool) *BucketRepoV2 {
	return &BucketRepoV2{q: q, pool: pool}
}

var _ admindomain.BucketRepository = (*BucketRepoV2)(nil)

// RunInTx runs fn in one transaction — the ADR-0003 seam the bucket handler
// uses to write a lifecycle mutation and its outbox rows atomically.
func (r *BucketRepoV2) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("bucket: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("bucket: commit tx: %w", err)
	}
	return nil
}

// BackendEnabled reports the enabled state of a storage backend.
// Returns ErrNotFound when the backend id is unknown.
func (r *BucketRepoV2) BackendEnabled(ctx context.Context, backendID string) (bool, error) {
	row, err := r.q.GetStorageBackendV2(ctx, backendID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, admindomain.ErrNotFound
		}
		return false, fmt.Errorf("backend enabled lookup: %w", err)
	}
	return row.Enabled, nil
}

func (r *BucketRepoV2) Create(ctx context.Context, b admindomain.Bucket) error {
	return r.createWith(ctx, r.q, b)
}

// CreateTx runs Create on the caller's tx (ADR-0003).
func (r *BucketRepoV2) CreateTx(ctx context.Context, tx pgx.Tx, b admindomain.Bucket) error {
	return r.createWith(ctx, r.q.WithTx(tx), b)
}

func (r *BucketRepoV2) createWith(ctx context.Context, q *sqlc.Queries, b admindomain.Bucket) error {
	constraints, _ := json.Marshal(b.Constraints)
	if string(constraints) == "null" {
		constraints = []byte("{}")
	}
	state := b.ProvisionState
	if state == "" {
		// Empty input means the caller didn't ask for backend provisioning,
		// so the row is immediately authoritative. The reconciler ignores
		// 'ready' rows.
		state = admindomain.BucketProvisionStateReady
	}
	if err := q.CreateBucketV2(ctx,
		b.BackendID,
		b.BucketName,
		b.DisplayName,
		b.Region,
		encodeMap(b.Labels),
		pgUUIDOptional(b.OwnerTenantID),
		b.CedarPolicy,
		constraints,
		state,
		b.PublicRead,
		b.PublicBaseURL,
	); err != nil {
		// Surface FK + unique violations as typed domain errors so the
		// handler can map them to user-friendly Connect codes instead of
		// leaking raw "buckets_backend_id_fkey (SQLSTATE 23503)" strings.
		switch pgerr.Classify(err) {
		case pgerr.NotNullViolation, pgerr.ForeignKeyViolation:
			// An unknown backend arrives as NOT NULL, not as a foreign key.
			// The INSERT resolves the backend through a subselect —
			// `(SELECT sb.id FROM storage_backends sb WHERE sb.name = $1)` —
			// which yields NULL when the name matches nothing, so the row is
			// rejected by `backend_id NOT NULL` (23502) before any FK is
			// tested. This branch used to match only the FK and carried a
			// comment saying that was "the only realistic source", so the
			// friendly message was unreachable and the caller got
			// `null value in column "backend_id"` as CodeInternal.
			//
			// The FK is kept in the match: it is the shape a direct id write
			// would take, and matching both costs nothing.
			return fmt.Errorf("%w: backend %q is not registered (run BackendService.CreateBackend or declare it in storage.backends)", admindomain.ErrConflict, b.BackendID)
		case pgerr.UniqueViolation:
			// AlreadyExists, not Conflict. Both used to be ErrConflict, which
			// the registry maps to FailedPrecondition — so "this bucket is
			// already there" and "the backend it names is not registered"
			// arrived as the same code, and a client could only tell them
			// apart by reading the prose. They call for opposite responses:
			// one is done, the other needs the backend created first.
			return fmt.Errorf("%w: bucket %q already exists in backend %q", admindomain.ErrAlreadyExists, b.BucketName, b.BackendID)
		}
		return err
	}
	return nil
}

// ─── outbox / reconciler ────────────────────────────────────────────────────

func (r *BucketRepoV2) ListPendingProvisions(ctx context.Context, maxAttempts, limit int32) ([]admindomain.BucketProvisionRow, error) {
	if limit <= 0 {
		limit = 25
	}
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	rows, err := r.q.ListPendingBucketProvisions(ctx, maxAttempts, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admindomain.BucketProvisionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.BucketProvisionRow{
			BackendID:         row.BackendName,
			BucketName:        row.BucketName,
			Region:            row.Region,
			ProvisionState:    row.ProvisionState,
			ProvisionAttempts: row.ProvisionAttempts,
			LastProvisionAt:   timeFrom(row.LastProvisionAt),
			OwnerTenantID:     uuidFrom(row.OwnerTenantID),
			PublicRead:        row.PublicRead,
		})
	}
	return out, nil
}

func (r *BucketRepoV2) MarkProvisionReady(ctx context.Context, backendID, bucketName string) error {
	rows, err := r.q.MarkBucketProvisionReady(ctx, backendID, bucketName)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

func (r *BucketRepoV2) MarkProvisionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error {
	rows, err := r.q.MarkBucketProvisionFailed(ctx, backendID, bucketName, terminal, errMsg)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

// ─── outbox / delete path ──────────────────────────────────────────────────

func (r *BucketRepoV2) MarkDeleting(ctx context.Context, backendID, bucketName string, expectedVersion int64) error {
	return r.markDeletingWith(ctx, r.q, backendID, bucketName, expectedVersion)
}

// MarkDeletingTx runs MarkDeleting on the caller's tx (ADR-0003).
func (r *BucketRepoV2) MarkDeletingTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, expectedVersion int64) error {
	return r.markDeletingWith(ctx, r.q.WithTx(tx), backendID, bucketName, expectedVersion)
}

func (r *BucketRepoV2) markDeletingWith(ctx context.Context, q *sqlc.Queries, backendID, bucketName string, expectedVersion int64) error {
	rows, err := q.MarkBucketDeleting(ctx, backendID, bucketName, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		// Either the row doesn't exist or the resource_version check
		// failed. Surface as ErrVersionMismatch to match the rest of the
		// repo — handlers can still decode "not found" via the prior
		// Get.
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) ListPendingDeletions(ctx context.Context, maxAttempts, limit int32) ([]admindomain.BucketProvisionRow, error) {
	if limit <= 0 {
		limit = 25
	}
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	rows, err := r.q.ListPendingBucketDeletions(ctx, maxAttempts, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admindomain.BucketProvisionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.BucketProvisionRow{
			BackendID:         row.BackendName,
			BucketName:        row.BucketName,
			Region:            row.Region,
			ProvisionState:    row.ProvisionState,
			ProvisionAttempts: row.ProvisionAttempts,
			LastProvisionAt:   timeFrom(row.LastProvisionAt),
		})
	}
	return out, nil
}

func (r *BucketRepoV2) MarkDeletionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error {
	rows, err := r.q.MarkBucketDeletionFailed(ctx, backendID, bucketName, terminal, errMsg)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

func (r *BucketRepoV2) Get(ctx context.Context, backendID, bucketName string) (admindomain.Bucket, error) {
	return r.getWith(ctx, r.q, backendID, bucketName)
}

// GetTx reads a bucket on the caller's tx so the handler can resolve the
// owner tenant_id (the fan-out target) inside the mutation tx (ADR-0003).
// LockTx row-locks the bucket for the rest of tx.
func (r *BucketRepoV2) LockTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) error {
	if _, err := r.q.WithTx(tx).LockBucketForDeletion(ctx, backendID, bucketName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.ErrNotFound
		}
		return err
	}
	return nil
}

func (r *BucketRepoV2) GetTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) (admindomain.Bucket, error) {
	return r.getWith(ctx, r.q.WithTx(tx), backendID, bucketName)
}

func (r *BucketRepoV2) getWith(ctx context.Context, q *sqlc.Queries, backendID, bucketName string) (admindomain.Bucket, error) {
	row, err := q.GetBucketV2(ctx, backendID, bucketName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Bucket{}, admindomain.ErrNotFound
		}
		return admindomain.Bucket{}, err
	}
	return bucketFromV2Row(row), nil
}

func (r *BucketRepoV2) List(ctx context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)
	var backendFilter *string
	if args.BackendID != "" {
		v := args.BackendID
		backendFilter = &v
	}
	// OwnerTenantID is forwarded to sqlc as a pgtype.UUID. The
	// query treats Valid=false as "no tenant filter" so the existing
	// cross-tenant listing path is unchanged. `001_initial_schema.sql`
	// creates an index on buckets(owner_tenant_id) for the
	// filtered case.
	var ownerFilter pgtype.UUID
	if args.OwnerTenantID != nil {
		ownerFilter = pgUUID(*args.OwnerTenantID)
	}
	// Pushdown: the SQL-expressible conjuncts of the caller's CEL filter
	// narrow the scan. The handler still evaluates the whole expression over
	// the returned page, so a hint that is absent costs a wider read and
	// never a wrong row.
	pd := hints(cel.PhysicalBucketSchema, args.Filter)
	if backendFilter == nil {
		// `backend_id == "x"` in the filter narrows the same way the typed
		// field does; the typed field wins when both are set. The typed field
		// is one backend, so a set of several is left to the CEL pass.
		if in, _ := pd.StringHint("backend_id"); len(in) == 1 {
			backendFilter = &in[0]
		}
	}
	nameIn, nameLike := pd.StringHint("bucket_id")
	displayIn, displayLike := pd.StringHint("display_name")

	createdGTE, createdLTE := createdBounds(pd)

	// Only the `like` half: the derived `search` field exists for a search
	// box, which emits `contains`. An `==` over a newline-joined concatenation
	// is not something anyone types, so it falls through to the CEL pass —
	// wider read, same answer.
	_, searchLike := pd.StringHint("search")

	rows, err := r.q.ListBucketsV2(ctx, backendFilter, ownerFilter,
		nameIn, nameLike, displayIn, displayLike, searchLike,
		pd.BoolHint("versioning_enabled"), pd.BoolHint("object_lock_enabled"),
		pd.BoolHint("replication_enabled"),
		createdGTE, createdLTE,
		args.AfterBackend, args.AfterName, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromV2RowList(row))
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		last := out[len(out)-1]
		next = last.BackendID + "/" + last.BucketName
	}
	return out, next, nil
}

func (r *BucketRepoV2) ListAccessible(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error) {
	pageSize = pageSizeOrDefault(pageSize)
	rows, err := r.q.ListAccessibleBuckets(ctx, pgUUID(tenantID), afterBackend, afterName, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromV2RowAccessible(row))
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		last := out[len(out)-1]
		next = last.BackendID + "/" + last.BucketName
	}
	return out, next, nil
}

func (r *BucketRepoV2) UpdateBasic(ctx context.Context, b admindomain.Bucket, expectedVersion int64, mask []string) error {
	return r.updateBasicWith(ctx, r.q, b, expectedVersion, mask)
}

// UpdateBasicTx runs UpdateBasic on the caller's tx (ADR-0003).
func (r *BucketRepoV2) UpdateBasicTx(ctx context.Context, tx pgx.Tx, b admindomain.Bucket, expectedVersion int64, mask []string) error {
	return r.updateBasicWith(ctx, r.q.WithTx(tx), b, expectedVersion, mask)
}

func (r *BucketRepoV2) updateBasicWith(ctx context.Context, q *sqlc.Queries, b admindomain.Bucket, expectedVersion int64, mask []string) error {
	has := func(f string) bool { return slices.Contains(mask, f) }
	var displayName *string
	var labels []byte
	var ownerID pgtype.UUID
	if has("display_name") {
		v := b.DisplayName
		displayName = &v
	}
	if has("labels") {
		labels = encodeMap(b.Labels)
	}
	if has("owner_tenant_id") {
		ownerID = pgUUIDOptional(b.OwnerTenantID)
	}
	rows, err := q.UpdateBucketBasic(ctx, b.BackendID, b.BucketName, displayName, labels, ownerID, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) error {
	rows, err := r.q.SetBucketPolicy(ctx, backendID, bucketName, policy, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetLifecycle(ctx context.Context, backendID, bucketName string, rules []admindomain.LifecycleRule, expectedVersion int64) error {
	b, _ := json.Marshal(rules)
	rows, err := r.q.SetBucketLifecycle(ctx, backendID, bucketName, b, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetObjectLock(ctx context.Context, backendID, bucketName string, lock admindomain.ObjectLockConfig, expectedVersion int64) error {
	retentionSeconds := int64(lock.DefaultRetention.Seconds())
	rows, err := r.q.SetBucketObjectLock(ctx, backendID, bucketName, lock.Enabled, lockModeToSQL(lock.DefaultMode), retentionSeconds, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetVersioning(ctx context.Context, backendID, bucketName string, v admindomain.BucketVersioning, expectedVersion int64) error {
	rows, err := r.q.SetBucketVersioning(ctx, backendID, bucketName, v.Enabled, v.KeepDeletesForever, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetReplication(ctx context.Context, backendID, bucketName string, rep admindomain.BucketReplication, expectedVersion int64) error {
	rows, err := r.q.SetBucketReplication(ctx, backendID, bucketName, rep.Enabled, rep.DestinationBucket, rep.Filter, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) SetConstraints(ctx context.Context, backendID, bucketName string, c admindomain.BucketConstraints, expectedVersion int64) error {
	b, _ := json.Marshal(c)
	rows, err := r.q.SetBucketConstraints(ctx, backendID, bucketName, b, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepoV2) CountBucketReferences(ctx context.Context, backendID, bucketName string) ([]admindomain.BucketReference, error) {
	rows, err := r.q.CountBucketReferences(ctx, backendID, bucketName)
	if err != nil {
		return nil, err
	}
	out := make([]admindomain.BucketReference, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.BucketReference{Relation: row.Relation, Count: row.Count})
	}
	return out, nil
}

func (r *BucketRepoV2) Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error {
	return r.deleteWith(ctx, r.q, backendID, bucketName, expectedVersion)
}

// DeleteTx runs Delete on the caller's tx (ADR-0003).
func (r *BucketRepoV2) DeleteTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, expectedVersion int64) error {
	return r.deleteWith(ctx, r.q.WithTx(tx), backendID, bucketName, expectedVersion)
}

func (r *BucketRepoV2) deleteWith(ctx context.Context, q *sqlc.Queries, backendID, bucketName string, expectedVersion int64) error {
	rows, err := q.DeleteBucketV2(ctx, backendID, bucketName, expectedVersion)
	if err != nil {
		// The handler counts the RESTRICT relations before it gets here, so
		// reaching this is either a race or a relation the count does not
		// know about. Either way the caller must not be handed
		// `violates foreign key constraint … (SQLSTATE 23503)` as an
		// Internal error, which is exactly what used to happen — the create
		// path above has mapped these since it was written, the delete path
		// never did.
		//
		// pgerr folds 23503 and 23001 into one Kind: Postgres picks between
		// them on how the constraint was declared, and matching only the
		// first is how the tenant path once leaked raw SQL text.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) {
			return fmt.Errorf("%w: bucket %q is still referenced by %s",
				admindomain.ErrConflict, bucketName, blockingRelation(err))
		}
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

// ─── Row → domain helpers ──────────────────────────────────────────────────

func bucketFromV2Row(row sqlc.GetBucketV2Row) admindomain.Bucket {
	return decodeBucketRow(
		row.BackendName, row.Name, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, lockModeFromSQL(row.ObjectLockDefaultMode), row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState, row.PublicRead, row.PublicBaseUrl,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func bucketFromV2RowList(row sqlc.ListBucketsV2Row) admindomain.Bucket {
	return decodeBucketRow(
		row.BackendName, row.Name, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, lockModeFromSQL(row.ObjectLockDefaultMode), row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState, row.PublicRead, row.PublicBaseUrl,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func bucketFromV2RowAccessible(row sqlc.ListAccessibleBucketsRow) admindomain.Bucket {
	return decodeBucketRow(
		row.BackendName, row.Name, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, lockModeFromSQL(row.ObjectLockDefaultMode), row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState, row.PublicRead, row.PublicBaseUrl,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func decodeBucketRow(
	backendID, bucketName string,
	displayName, region string,
	labels []byte,
	ownerTenantID pgtype.UUID,
	cedarPolicy string,
	constraints []byte,
	lifecycleRules []byte,
	lockEnabled bool, lockMode string, lockRetentionSeconds int64,
	versioningEnabled, keepDeletesForever bool,
	replicationEnabled bool, replicationDest, replicationFilter string,
	provisionState string,
	publicRead bool, publicBaseURL string,
	resourceVersion int64,
	createdAt, updatedAt pgtype.Timestamptz,
) admindomain.Bucket {
	var c admindomain.BucketConstraints
	_ = json.Unmarshal(constraints, &c)
	var rules []admindomain.LifecycleRule
	_ = json.Unmarshal(lifecycleRules, &rules)
	return admindomain.Bucket{
		BackendID:      backendID,
		BucketName:     bucketName,
		DisplayName:    displayName,
		Region:         region,
		Labels:         decodeMap(labels),
		OwnerTenantID:  uuidFrom(ownerTenantID),
		CedarPolicy:    cedarPolicy,
		Constraints:    c,
		LifecycleRules: rules,
		ObjectLock: admindomain.ObjectLockConfig{
			Enabled:          lockEnabled,
			DefaultMode:      lockMode,
			DefaultRetention: time.Duration(lockRetentionSeconds) * time.Second,
		},
		Versioning: admindomain.BucketVersioning{
			Enabled:            versioningEnabled,
			KeepDeletesForever: keepDeletesForever,
		},
		Replication: admindomain.BucketReplication{
			Enabled:           replicationEnabled,
			DestinationBucket: replicationDest,
			Filter:            replicationFilter,
		},
		ProvisionState:  provisionState,
		PublicRead:      publicRead,
		PublicBaseURL:   publicBaseURL,
		ResourceVersion: resourceVersion,
		CreatedAt:       timeFrom(createdAt),
		UpdatedAt:       timeFrom(updatedAt),
	}
}

// pgUUIDOptional wraps an optional uuid.UUID into pgtype.UUID, with NULL for Nil.
func pgUUIDOptional(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

// silence unused imports across go versions
var _ = fmt.Errorf

// object_lock_mode is a Postgres enum now (ADR-0017), and "no default mode" is
// NULL rather than the empty string the domain uses.
func lockModeToSQL(m string) *sqlc.ObjectLockMode {
	if m == "" {
		return nil
	}
	v := sqlc.ObjectLockMode(m)
	return &v
}

func lockModeFromSQL(m *sqlc.ObjectLockMode) string {
	if m == nil {
		return ""
	}
	return string(*m)
}
