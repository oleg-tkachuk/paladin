package admin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/tenant"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
)

// Where a shim makes MORE THAN ONE call, "an error came back" is not the same
// as "the right error came back".
//
// CancelOperation cancels and then re-reads; PurgeTenant resolves a slug and
// then purges; CreateCollection may consult the tenant's default binding
// before creating. Against a double that fails everything, deleting the first
// check still produces an error from the second call — so the table in
// seams_test.go cannot see the difference, and a mutation run confirmed it:
// all three error checks survived being inverted.
//
// These doubles fail exactly one step. What they pin is not "an error" but
// "the operation did not happen, and the caller was told so".

type cancelFailsGetSucceeds struct{ failingOperation }

func (cancelFailsGetSucceeds) GetOperation(context.Context, uuid.UUID) (*operation.Operation, error) {
	return &operation.Operation{}, nil
}

func TestCancelOperation_ReportsTheCancelNotTheReread(t *testing.T) {
	srv := &OperationServer{H: cancelFailsGetSucceeds{}}
	_, err := srv.CancelOperation(context.Background(), connect.NewRequest(&pb.CancelOperationRequest{
		Name: "operations/" + uuid.NewString(),
	}))
	if err == nil {
		t.Fatal("the cancel failed and the shim reported success because the " +
			"following read worked — the caller believes the operation stopped")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("error %v is not the cancel's", err)
	}
}

// slugFailsPurgeSucceeds resolves no slug but purges happily. Dropping the
// error check on the lookup leaves tid at its zero value, so the purge runs
// against uuid.Nil and answers success: a destructive RPC reporting that it
// removed a tenant it never identified.
type slugFailsPurgeSucceeds struct{ failingTenant }

func (slugFailsPurgeSucceeds) PurgeTenant(context.Context, uuid.UUID) error { return nil }

func TestPurgeTenant_ReportsTheSlugLookupNotThePurge(t *testing.T) {
	srv := &TenantServer{H: slugFailsPurgeSucceeds{}}
	_, err := srv.PurgeTenant(context.Background(), connect.NewRequest(&pb.PurgeTenantRequest{
		Name: "tenants/acme-corp", // a slug, so the lookup runs
	}))
	if err == nil {
		t.Fatal("the tenant could not be resolved and the shim answered success — " +
			"a purge was reported against a tenant that was never identified")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("error %v is not the slug lookup's", err)
	}
}

// The default-binding check needs the opposite shape: with a lookup that
// FAILS, inverting `if err != nil` merely skips the branch and the create
// still fails downstream. With a lookup that SUCCEEDS, the inverted check
// enters the branch and returns (nil, nil) — no response, no error. So the
// case that holds it is a successful creation.
type okBindings struct{}

func (okBindings) GetDefaultBinding(context.Context, uuid.UUID) (tenant.DefaultBinding, error) {
	return tenant.DefaultBinding{BackendName: "primary", BucketName: "b1"}, nil
}

type okCollection struct{ failingCollection }

func (okCollection) CreateCollection(context.Context, objectkey.CreateCollectionArgs) (*objectkey.Collection, error) {
	return &objectkey.Collection{TenantID: uuid.New(), Collection: "c1"}, nil
}

func TestCreateCollection_RoutesThroughTheDefaultBinding(t *testing.T) {
	srv := &CollectionServer{H: okCollection{}, bindings: okBindings{}}
	resp, err := srv.CreateCollection(context.Background(), connect.NewRequest(&pb.CreateCollectionRequest{
		Parent: "tenants/" + uuid.NewString(), Collection: "c1",
		CollectionResource: &pb.Collection{}, // no bucket: the binding supplies it
	}))
	if err != nil {
		t.Fatalf("the binding resolved and the create succeeded, yet the shim failed: %v", err)
	}
	if resp == nil || resp.Msg == nil {
		t.Fatal("the shim answered with no error and no response — a client reads " +
			"that as a created collection it can never address")
	}
}

// The mirror of the cancel case: the cancel lands and the re-read fails. A
// dropped check there answers the caller with a zero-valued Operation — id
// 00000000-…, state unset — for an operation that was in fact cancelled. The
// caller cannot tell that from an operation it is not allowed to see.
type cancelSucceedsGetFails struct{ failingOperation }

func (cancelSucceedsGetFails) CancelOperation(context.Context, uuid.UUID) error { return nil }

func TestCancelOperation_ReportsAFailedReread(t *testing.T) {
	srv := &OperationServer{H: cancelSucceedsGetFails{}}
	_, err := srv.CancelOperation(context.Background(), connect.NewRequest(&pb.CancelOperationRequest{
		Name: "operations/" + uuid.NewString(),
	}))
	if err == nil {
		t.Fatal("the re-read failed and the shim answered with an empty operation")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("error %v is not the re-read's", err)
	}
}
