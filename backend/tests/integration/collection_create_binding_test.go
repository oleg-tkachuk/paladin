//go:build integration

// ADR-0010 Phase 3 wiring: CreateCollection without a bucket routes the NEW
// collection to the tenant's default binding; with no binding it is a clear
// FAILED_PRECONDITION rather than a raw NOT-NULL / FK error. (Get/Update/Delete
// of an EXISTING collection use the row's own binding — not covered here because
// that's exactly what they already did.)
package integration

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

func TestCreateCollection_UsesDefaultBinding(t *testing.T) {
	f := setupDispatcher(t)
	tid := mustCreateTenant(t, f.h.PoolMigrate, "ok-default-binding")
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test")

	q := sqlc.New(f.h.PoolMigrate)
	okRepo := adapters.NewCollectionRepo(q, f.h.PoolMigrate)
	tenantRepo := adapters.NewTenantRepo(q, f.h.PoolMigrate)
	handler := objectkey.NewHandler(okRepo, allowAll{})
	server := admin.NewCollectionServer(handler, tenantRepo)

	ctx := ctxAdmin(t, tid)
	createNoBucket := func(name string) (*connect.Response[pb.Collection], error) {
		return server.CreateCollection(ctx, connect.NewRequest(&pb.CreateCollectionRequest{
			Parent:             "tenants/" + tid.String(),
			Collection:         name,
			CollectionResource: &pb.Collection{}, // no bucket named
		}))
	}

	// No default binding yet → FAILED_PRECONDITION (not a raw DB error).
	if _, err := createNoBucket("no-binding-ok"); err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("create without bucket or binding: err = %v (code %v), want FailedPrecondition", err, connect.CodeOf(err))
	}

	// Set a default binding, then create without a bucket → lands in it.
	if _, err := tenantRepo.SetDefaultBinding(context.Background(), tid,
		"storageBackends/primary/buckets/paladin-test", "admin@local"); err != nil {
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
