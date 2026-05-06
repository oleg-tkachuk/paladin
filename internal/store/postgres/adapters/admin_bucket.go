package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type BucketRepoV2 struct {
	q *sqlc.Queries
}

func NewBucketRepoV2(q *sqlc.Queries) *BucketRepoV2 { return &BucketRepoV2{q: q} }

var _ admindomain.BucketRepository = (*BucketRepoV2)(nil)

func (r *BucketRepoV2) Create(ctx context.Context, b admindomain.Bucket) error {
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
	return r.q.CreateBucketV2(ctx,
		b.BackendID,
		b.BucketName,
		strPtr(b.DisplayName),
		strPtr(b.Region),
		encodeMap(b.Labels),
		pgUUIDOptional(b.OwnerTenantID),
		b.CedarPolicy,
		constraints,
		state,
	)
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
			BackendID:         row.BackendID,
			BucketName:        row.BucketName,
			Region:            derefStr(row.Region),
			ProvisionState:    row.ProvisionState,
			ProvisionAttempts: row.ProvisionAttempts,
			LastProvisionAt:   timeFrom(row.LastProvisionAt),
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
	rows, err := r.q.MarkBucketDeleting(ctx, backendID, bucketName, expectedVersion)
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
			BackendID:         row.BackendID,
			BucketName:        row.BucketName,
			Region:            derefStr(row.Region),
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
	row, err := r.q.GetBucketV2(ctx, backendID, bucketName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Bucket{}, admindomain.ErrNotFound
		}
		return admindomain.Bucket{}, err
	}
	return bucketFromV2Row(row), nil
}

func (r *BucketRepoV2) List(ctx context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	var backendFilter *string
	if args.BackendID != "" {
		v := args.BackendID
		backendFilter = &v
	}
	rows, err := r.q.ListBucketsV2(ctx, backendFilter, args.AfterBackend, args.AfterName, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromV2RowList(row))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = last.BackendID + "/" + last.BucketName
	}
	return out, next, nil
}

func (r *BucketRepoV2) ListAccessible(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error) {
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	rows, err := r.q.ListAccessibleBuckets(ctx, pgUUID(tenantID), afterBackend, afterName, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromV2RowAccessible(row))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = last.BackendID + "/" + last.BucketName
	}
	return out, next, nil
}

func (r *BucketRepoV2) UpdateBasic(ctx context.Context, b admindomain.Bucket, expectedVersion int64, mask []string) error {
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
	rows, err := r.q.UpdateBucketBasic(ctx, b.BackendID, b.BucketName, displayName, labels, ownerID, expectedVersion)
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
	rows, err := r.q.SetBucketObjectLock(ctx, backendID, bucketName, lock.Enabled, lock.DefaultMode, retentionSeconds, expectedVersion)
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

func (r *BucketRepoV2) Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error {
	rows, err := r.q.DeleteBucketV2(ctx, backendID, bucketName, expectedVersion)
	if err != nil {
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
		row.BackendID, row.BucketName, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, row.ObjectLockDefaultMode, row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func bucketFromV2RowList(row sqlc.ListBucketsV2Row) admindomain.Bucket {
	return decodeBucketRow(
		row.BackendID, row.BucketName, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, row.ObjectLockDefaultMode, row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func bucketFromV2RowAccessible(row sqlc.ListAccessibleBucketsRow) admindomain.Bucket {
	return decodeBucketRow(
		row.BackendID, row.BucketName, row.DisplayName, row.Region, row.Labels,
		row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
		row.ObjectLockEnabled, row.ObjectLockDefaultMode, row.ObjectLockDefaultRetentionSeconds,
		row.VersioningEnabled, row.VersioningKeepDeletesForever,
		row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
		row.ProvisionState,
		row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
	)
}

func decodeBucketRow(
	backendID, bucketName string,
	displayName, region *string,
	labels []byte,
	ownerTenantID pgtype.UUID,
	cedarPolicy string,
	constraints []byte,
	lifecycleRules []byte,
	lockEnabled bool, lockMode string, lockRetentionSeconds int64,
	versioningEnabled, keepDeletesForever bool,
	replicationEnabled bool, replicationDest, replicationFilter string,
	provisionState string,
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
		DisplayName:    derefStr(displayName),
		Region:         derefStr(region),
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
