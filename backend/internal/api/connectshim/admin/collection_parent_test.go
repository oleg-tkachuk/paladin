package admin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The collection RPCs take `tenants/{tenant_id_or_slug}` as the proto
// documents. ListCollections parsed the parent as a UUID and, when that failed,
// dropped the tenant filter instead of refusing — so a slug listed across every
// tenant, which reads as "this tenant has no collections".
func TestListCollectionsResolvesASlugParent(t *testing.T) {
	t.Parallel()

	h := &recordingCollections{}
	tenants := &slugTenant{slug: "acme", id: uuid.New()}
	srv := &CollectionServer{H: h, bindings: okBindings{}, tenants: tenants}

	_, err := srv.ListCollections(context.Background(), connect.NewRequest(&pb.ListCollectionsRequest{
		Parent: "tenants/acme",
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.listArgs.TenantID != tenants.id {
		t.Errorf("tenant filter = %v, want %v resolved from the slug", h.listArgs.TenantID, tenants.id)
	}
}

func TestCreateCollectionResolvesASlugParent(t *testing.T) {
	t.Parallel()

	h := &recordingCollections{}
	tenants := &slugTenant{slug: "acme", id: uuid.New()}
	srv := &CollectionServer{H: h, bindings: okBindings{}, tenants: tenants}

	_, err := srv.CreateCollection(context.Background(), connect.NewRequest(&pb.CreateCollectionRequest{
		Parent:             "tenants/acme",
		Collection:         "invoices",
		CollectionResource: &pb.Collection{Bucket: "storageBackends/primary/buckets/b1"},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.createArgs.TenantID != tenants.id {
		t.Errorf("tenant = %v, want %v resolved from the slug", h.createArgs.TenantID, tenants.id)
	}
}

func TestCollectionParentErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		parent string
		want   connect.Code
	}{
		// Refused rather than listed unfiltered.
		"unparseable": {parent: "tenants/Not A Slug!", want: connect.CodeInvalidArgument},
		// The lookup's answer, passed through.
		"unknown slug": {parent: "tenants/other", want: connect.CodeNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := &CollectionServer{
				H:        &recordingCollections{},
				bindings: okBindings{},
				tenants:  &slugTenant{slug: "acme", id: uuid.New()},
			}
			_, err := srv.ListCollections(context.Background(), connect.NewRequest(&pb.ListCollectionsRequest{
				Parent: tc.parent,
			}))
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", got, tc.want, err)
			}
		})
	}
}

// A UUID parent and an empty one never reach the slug lookup: tenants is nil
// here, so a lookup would panic.
func TestCollectionParentWithoutLookup(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	for parent, want := range map[string]uuid.UUID{
		"tenants/" + id.String():               id,
		"tenants/" + id.String() + "/anything": id,
		"":                                     uuid.Nil,
	} {
		h := &recordingCollections{}
		srv := &CollectionServer{H: h, bindings: okBindings{}}
		if _, err := srv.ListCollections(context.Background(), connect.NewRequest(&pb.ListCollectionsRequest{
			Parent: parent,
		})); err != nil {
			t.Fatalf("parent %q: %v", parent, err)
		}
		if h.listArgs.TenantID != want {
			t.Errorf("parent %q: tenant filter = %v, want %v", parent, h.listArgs.TenantID, want)
		}
	}
}
