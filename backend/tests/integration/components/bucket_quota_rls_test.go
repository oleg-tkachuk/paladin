//go:build integration

package components

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/quotah"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A bucket quota used to be a `quotas` row with tenant_id NULL, under a tenant
// isolation policy no NULL could satisfy. Through the runtime pool it could be
// neither written nor read, and the upload check never saw it — a bucket cap
// was not enforced at all, while every component test, run as a superuser,
// passed. This drives the whole life of one bucket quota through a
// NOBYPASSRLS pool: set, read, charged on promote, enforced, reset.
func TestBucketQuota_SetReadChargeEnforceResetUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	f := seedFixture(t, ctx, admin)

	var backendID, bucketName string
	if err := admin.QueryRow(ctx,
		`SELECT sb.name, b.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.id = $1`, f.collectionID).Scan(&backendID, &bucketName); err != nil {
		t.Fatalf("lookup binding: %v", err)
	}

	pool := rlsPool(t, ctx, admin)
	q := sqlc.New(pool)
	quotas := adapters.NewQuotaRepoV2(q, pool)
	h := quotah.NewHandler(quotas, quotaResetAllow{})

	operator := auth.WithPrincipal(ctx, &auth.Principal{
		Subject:  "operator",
		TenantID: uuid.New(), // a platform admin from a tenant of its own
		Roles:    []string{apiutil.RolePlatformAdmin},
	})
	uploader := auth.WithPrincipal(ctx, &auth.Principal{TenantID: f.tenantID})

	// One admission a day, so a single charged upload reaches the cap.
	const objectsPerDay = 1
	set, err := h.SetQuota(operator, admindomain.Quota{
		BackendID:        backendID,
		BucketName:       bucketName,
		MaxObjectsPerDay: objectsPerDay,
	}, nil)
	if err != nil {
		t.Fatalf("SetQuota (bucket): %v", err)
	}
	if set.BackendID != backendID || set.BucketName != bucketName || set.TenantID != uuid.Nil {
		t.Errorf("SetQuota returned %+v, want the bucket scope", set)
	}

	got, err := h.GetBucketQuota(operator, backendID, bucketName)
	if err != nil {
		t.Fatalf("GetBucketQuota: %v", err)
	}
	if got.MaxObjectsPerDay != objectsPerDay {
		t.Errorf("max_objects_per_day = %d, want %d", got.MaxObjectsPerDay, objectsPerDay)
	}

	check := &middleware.QuotaSoftCheck{
		Reader:           quotas,
		Bindings:         adapters.NewObjectRepo(q, pool),
		UploadProcedures: map[string]struct{}{uploadProc: {}},
	}
	parent := "tenants/" + f.tenantID.String() + "/collections/" + f.collection
	upload := &uploadRequest{parent: parent, sizeHint: 1}

	if err := check.CheckUpload(uploader, uploadProc, upload); err != nil {
		t.Fatalf("first upload refused before any charge: %v", err)
	}

	// The promote path charges the bucket through the uploader's own session.
	objectID := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, admin,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state,
		                      content_type, checksum_algorithm, size_bytes)
		 VALUES ($1, $2, $3, $4, 'AVAILABLE', 'application/octet-stream', 0, 10)`,
		objectID, f.tenantID, f.collectionID, "k-"+uuid.NewString()[:8])
	if err := quotas.OnObjectPromoted(uploader, f.tenantID, objectID, 10); err != nil {
		t.Fatalf("OnObjectPromoted: %v", err)
	}
	charged, err := h.GetBucketQuota(operator, backendID, bucketName)
	if err != nil {
		t.Fatalf("GetBucketQuota after charge: %v", err)
	}
	if charged.UsageObjectsToday != 1 || charged.UsageObjectCount != 1 {
		t.Fatalf("bucket usage after one promote = %d today / %d total, want 1 / 1",
			charged.UsageObjectsToday, charged.UsageObjectCount)
	}

	err = check.CheckUpload(uploader, uploadProc, upload)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted from the bucket's daily cap", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "bucket daily quota exceeded") {
		t.Errorf("message %q should name the bucket's daily cap", err)
	}

	if err := h.ResetUsage(operator, set.QuotaID); err != nil {
		t.Fatalf("ResetUsage (bucket): %v", err)
	}
	if err := check.CheckUpload(uploader, uploadProc, upload); err != nil {
		t.Errorf("upload refused after the daily counters were reset: %v", err)
	}
}

// What the migration took away: a tenant quota is the only shape `quotas`
// holds now, and it still upserts and reads back through the runtime pool.
func TestTenantQuota_StillUpsertsUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	f := seedFixture(t, ctx, admin)
	pool := rlsPool(t, ctx, admin)
	h := quotah.NewHandler(adapters.NewQuotaRepoV2(sqlc.New(pool), pool), quotaResetAllow{})

	owner := auth.WithPrincipal(ctx, &auth.Principal{
		Subject:  "operator",
		TenantID: f.tenantID,
		Roles:    []string{apiutil.RolePlatformAdmin},
	})
	const maxObjects = 7
	if _, err := h.SetQuota(owner, admindomain.Quota{TenantID: f.tenantID, MaxObjectCount: maxObjects}, nil); err != nil {
		t.Fatalf("SetQuota (tenant): %v", err)
	}
	got, err := h.GetTenantQuota(owner, f.tenantID)
	if err != nil {
		t.Fatalf("GetTenantQuota: %v", err)
	}
	if got.MaxObjectCount != maxObjects {
		t.Errorf("max_object_count = %d, want %d", got.MaxObjectCount, maxObjects)
	}
}
