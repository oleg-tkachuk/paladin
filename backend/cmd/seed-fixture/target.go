package main

import (
	"context"
	"fmt"
	"uuid"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The planes as `task backend:seed:up` port-forwards them, plain HTTP as the
// chart runs them by default (internalTLS off). The flags default to these;
// they used to name /api/rpc/* on the console's host, routes of its old Node
// server that no longer exist.
const (
	defaultAdminURL = "http://127.0.0.1:18090"
	defaultIAMURL   = "http://127.0.0.1:18085"
	defaultDataURL  = "http://127.0.0.1:18080"
)

// The resource-name segments a collection's parent is built from.
const (
	tenantPrefix       = "tenants/"
	collectionsSegment = "/collections/"
)

// tenantGetter is the slice of the admin TenantService the seeder reads.
type tenantGetter interface {
	GetTenant(context.Context, *adminv1.GetTenantRequest) (*adminv1.Tenant, error)
}

// collectionGetter is the slice of the admin CollectionService it reads.
type collectionGetter interface {
	GetCollection(context.Context, *adminv1.GetCollectionRequest) (*adminv1.Collection, error)
}

// resolveTenantID is the id of the tenant ref names: ref itself when it is an
// id, else the tenant whose slug it is. The API names a tenant by its id only.
func resolveTenantID(ctx context.Context, tenants tenantGetter, ref string) (string, error) {
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	t, err := tenants.GetTenant(ctx, &adminv1.GetTenantRequest{Name: tenantPrefix + ref})
	if err != nil {
		return "", fmt.Errorf("tenant %q: %w", ref, err)
	}
	return t.GetTenantId(), nil
}

// collectionParent is the resource name objects are seeded under: the
// collection in the tenant tenantRef names, which must already exist and be
// bound to a bucket. A missing one is said plainly here, before anything is
// written, rather than as a failure deep in the seed.
func collectionParent(ctx context.Context, tenants tenantGetter, collections collectionGetter, tenantRef, collection string) (string, error) {
	tenantID, err := resolveTenantID(ctx, tenants, tenantRef)
	if err != nil {
		return "", err
	}
	parent := tenantPrefix + tenantID + collectionsSegment + collection
	if _, err := collections.GetCollection(ctx, &adminv1.GetCollectionRequest{Name: parent}); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return "", fmt.Errorf("collection %q does not exist in tenant %q: create one bound to a bucket and pass it as --object-key", collection, tenantRef)
		}
		return "", fmt.Errorf("collection %q in tenant %q: %w", collection, tenantRef, err)
	}
	return parent, nil
}
