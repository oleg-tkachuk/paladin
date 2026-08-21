//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// uploadRequest stands in for UploadObjectRequest: the interceptor reads the
// two fields it needs through interface assertions, so a local struct with
// the same getters exercises the real code path without importing the
// generated protobuf types.
type uploadRequest struct {
	parent   string
	sizeHint int64
}

func (r *uploadRequest) GetParent() string       { return r.parent }
func (r *uploadRequest) GetSizeHintBytes() int64 { return r.sizeHint }

const uploadProc = "/paladin.data.v1.ObjectService/UploadObject"

// TestQuotaEnforcementEndToEnd wires the REAL adapters into the REAL
// interceptor and drives the whole loop over Postgres:
//
//	objects → QuotaReconciler → quotas.usage_* → QuotaSoftCheck → reject
//
// The unit tests in internal/middleware use fakes, so they prove the
// arithmetic but not that repos.Quota actually satisfies the widened
// QuotaReader, that repos.Object resolves a binding the way the interceptor
// expects, or that the reconciled numbers are the ones enforcement reads.
// This closes that gap.
func TestQuotaEnforcementEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	q := sqlc.New(pool)
	quotaRepo := adapters.NewQuotaRepoV2(q, pool)
	objectRepo := adapters.NewObjectRepo(q, pool)
	reconciler := adapters.NewQuotaReconcileRepo(pool)

	// The interceptor as the data plane wires it: tenant scope from the
	// quota repo, bucket scope resolved through the object repo. Headroom
	// off so the assertions are exact arithmetic.
	check := &middleware.QuotaSoftCheck{
		Reader:           quotaRepo,
		Bindings:         objectRepo,
		UploadProcedures: map[string]struct{}{uploadProc: {}},
	}
	callerCtx := auth.WithPrincipal(ctx, &auth.Principal{TenantID: f.tenantID})
	parent := "tenants/" + f.tenantID.String() + "/collections/" + f.collection

	// A tenant cap of 1000 bytes, and 900 bytes actually stored. The quota
	// row starts at zero usage — exactly the state a freshly-created quota
	// is in — so only the reconciler can make enforcement see reality.
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_total_bytes) VALUES ($1, $2, 1000)`,
		uuid.New(), f.tenantID)
	insertObj(t, ctx, pool, f, "AVAILABLE", 900)

	// Before reconciling, usage reads 0, so a 200-byte upload looks fine.
	if err := check.CheckUpload(callerCtx, uploadProc, &uploadRequest{parent: parent, sizeHint: 200}); err != nil {
		t.Fatalf("pre-reconcile upload should pass (usage still 0), got %v", err)
	}

	if _, err := reconciler.ReconcileUsage(ctx); err != nil {
		t.Fatalf("ReconcileUsage: %v", err)
	}

	// After reconciling, usage is 900 and the same request is over the cap.
	err := check.CheckUpload(callerCtx, uploadProc, &uploadRequest{parent: parent, sizeHint: 200})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("post-reconcile code = %v, want ResourceExhausted", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "tenant quota exceeded") {
		t.Errorf("message %q should attribute the rejection to the tenant scope", err)
	}

	// And a request that fits still passes — the cap rejects overage, not
	// the tenant.
	if err := check.CheckUpload(callerCtx, uploadProc, &uploadRequest{parent: parent, sizeHint: 50}); err != nil {
		t.Errorf("900+50 is under the 1000 cap, want admit; got %v", err)
	}
}

// TestBucketQuotaEnforcementEndToEnd proves the scope that previously
// rejected nothing: a bucket-scoped quota row, reached from an upload that
// names only its Collection, resolved through the real object repository.
func TestBucketQuotaEnforcementEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	q := sqlc.New(pool)
	check := &middleware.QuotaSoftCheck{
		Reader:           adapters.NewQuotaRepoV2(q, pool),
		Bindings:         adapters.NewObjectRepo(q, pool),
		UploadProcedures: map[string]struct{}{uploadProc: {}},
	}
	callerCtx := auth.WithPrincipal(ctx, &auth.Principal{TenantID: f.tenantID})
	parent := "tenants/" + f.tenantID.String() + "/collections/" + f.collection

	var backendID, bucketName string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name, b.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection).Scan(&backendID, &bucketName); err != nil {
		t.Fatalf("lookup binding: %v", err)
	}

	// No tenant quota at all — this must be the BUCKET row doing the work,
	// not a tenant row happening to reject.
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, bucket_id, max_object_count)
		 SELECT $1, (SELECT b.id FROM buckets b
			  JOIN storage_backends sb ON sb.id = b.backend_id
			 WHERE sb.name = $2 AND b.name = $3), 2`,
		uuid.New(), backendID, bucketName)
	insertObj(t, ctx, pool, f, "AVAILABLE", 10)
	insertObj(t, ctx, pool, f, "AVAILABLE", 10)

	if _, err := adapters.NewQuotaReconcileRepo(pool).ReconcileUsage(ctx); err != nil {
		t.Fatalf("ReconcileUsage: %v", err)
	}

	err := check.CheckUpload(callerCtx, uploadProc, &uploadRequest{parent: parent, sizeHint: 1})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted from the bucket cap", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "bucket quota exceeded") {
		t.Errorf("message %q should attribute the rejection to the bucket scope", err)
	}

	// Raising the bucket cap unblocks it — proving the rejection tracked
	// that row rather than some unrelated condition.
	mustExec(t, ctx, pool,
		`UPDATE quotas SET max_object_count = 10 WHERE backend_id = $1 AND bucket_name = $2`,
		backendID, bucketName)
	if err := check.CheckUpload(callerCtx, uploadProc, &uploadRequest{parent: parent, sizeHint: 1}); err != nil {
		t.Errorf("want admit after raising the bucket cap, got %v", err)
	}
}
