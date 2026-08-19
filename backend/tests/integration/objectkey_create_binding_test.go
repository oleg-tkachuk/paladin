//go:build integration

// ADR-0010 Phase 3 wiring: CreateObjectKey without a bucket routes the NEW
// objectKey to the tenant's default binding; with no binding it is a clear
// FAILED_PRECONDITION rather than a raw NOT-NULL / FK error. (Get/Update/Delete
// of an EXISTING objectKey use the row's own binding — not covered here because
// that's exactly what they already did.)
package integration

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/admin"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

func TestCreateObjectKey_UsesDefaultBinding(t *testing.T) {
	f := setupDispatcher(t)
	tid := mustCreateTenant(t, f.h.PoolMigrate, "ok-default-binding")
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test")

	q := sqlc.New(f.h.PoolMigrate)
	okRepo := adapters.NewObjectKeyRepo(q, f.h.PoolMigrate)
	tenantRepo := adapters.NewTenantRepo(q, f.h.PoolMigrate)
	handler := objectkey.NewHandler(okRepo, allowAll{})
	server := admin.NewObjectKeyServer(handler, tenantRepo)

	ctx := ctxAdmin(t, tid)
	createNoBucket := func(name string) (*connect.Response[pb.ObjectKey], error) {
		return server.CreateObjectKey(ctx, connect.NewRequest(&pb.CreateObjectKeyRequest{
			Parent:            "tenants/" + tid.String(),
			ObjectKey:         name,
			ObjectKeyResource: &pb.ObjectKey{}, // no bucket named
		}))
	}

	// No default binding yet → FAILED_PRECONDITION (not a raw DB error).
	if _, err := createNoBucket("no-binding-ok"); err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("create without bucket or binding: err = %v (code %v), want FailedPrecondition", err, connect.CodeOf(err))
	}

	// Set a default binding, then create without a bucket → lands in it.
	if _, err := tenantRepo.SetDefaultBinding(context.Background(), tid, "primary", "paladin-test", "admin@local"); err != nil {
		t.Fatalf("set binding: %v", err)
	}
	resp, err := createNoBucket("routed-ok")
	if err != nil {
		t.Fatalf("create with default binding: %v", err)
	}
	if b := resp.Msg.GetBucket(); !strings.Contains(b, "paladin-test") {
		t.Errorf("created OK bucket = %q, want it to name paladin-test", b)
	}
}
