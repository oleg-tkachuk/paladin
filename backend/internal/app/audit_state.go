package app

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	adminconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// named is a request that names the resource it acts on.
type named interface{ GetName() string }

// byName is a reader that asks get for the resource req names, through the
// request build makes of the name; nothing for a request that names none.
func byName[G any, R proto.Message](get func(context.Context, *G) (R, error), build func(name string) *G) middleware.StateReader {
	return func(ctx context.Context, req proto.Message) (proto.Message, error) {
		n, ok := req.(named)
		if !ok || n.GetName() == "" {
			return nil, nil
		}
		return get(ctx, build(n.GetName()))
	}
}

// adminStateServers are the admin plane's servers the audit log reads a
// resource through, as its caller would.
type adminStateServers struct {
	backend      *admin.BackendServer
	bucket       *admin.BucketServer
	tenant       *admin.TenantServer
	collection   *admin.CollectionServer
	quota        *admin.QuotaServer
	budget       *admin.TenantBudgetServer
	subscription *admin.EventSubscriptionServer
}

// fill puts into readers, by procedure, the reader of each admin action that
// changes one resource: the resource's own Get, with the caller's context.
// The audit interceptor is built before the servers, so it holds readers and
// sees them filled before the listener serves.
func (s adminStateServers) fill(readers middleware.StateReaders) {
	backend := byName(s.backend.GetBackend, func(n string) *adminv1.GetBackendRequest { return &adminv1.GetBackendRequest{Name: n} })
	for _, p := range []string{
		adminconnect.BackendServiceUpdateBackendProcedure,
		adminconnect.BackendServiceDeleteBackendProcedure,
		adminconnect.BackendServiceRotateCredentialsProcedure,
		adminconnect.BackendServiceSetBackendEnabledProcedure,
		adminconnect.BackendServiceSetBackendReadOnlyProcedure,
		adminconnect.BackendServiceSetBackendMaintenanceProcedure,
	} {
		readers[p] = backend
	}
	bucket := byName(s.bucket.GetBucket, func(n string) *adminv1.GetBucketRequest { return &adminv1.GetBucketRequest{Name: n} })
	for _, p := range []string{
		adminconnect.BucketServiceUpdateBucketProcedure,
		adminconnect.BucketServiceDeleteBucketProcedure,
		adminconnect.BucketServiceSetBucketPolicyProcedure,
		adminconnect.BucketServiceSetLifecycleRulesProcedure,
		adminconnect.BucketServiceSetObjectLockProcedure,
		adminconnect.BucketServiceSetVersioningProcedure,
		adminconnect.BucketServiceSetReplicationProcedure,
	} {
		readers[p] = bucket
	}
	tenant := byName(s.tenant.GetTenant, func(n string) *adminv1.GetTenantRequest { return &adminv1.GetTenantRequest{Name: n} })
	for _, p := range []string{
		adminconnect.TenantServiceUpdateTenantProcedure,
		adminconnect.TenantServiceDeleteTenantProcedure,
		adminconnect.TenantServiceSetInheritedPolicyProcedure,
		adminconnect.TenantServiceRestoreTenantProcedure,
		adminconnect.TenantServicePurgeTenantProcedure,
		adminconnect.TenantServiceRenameTenantSlugProcedure,
	} {
		readers[p] = tenant
	}
	binding := byName(s.tenant.GetTenantDefaultBinding, func(n string) *adminv1.GetTenantDefaultBindingRequest {
		return &adminv1.GetTenantDefaultBindingRequest{Name: n}
	})
	readers[adminconnect.TenantServiceSetTenantDefaultBindingProcedure] = binding
	readers[adminconnect.TenantServiceClearTenantDefaultBindingProcedure] = binding
	readers[adminconnect.TenantServiceMigrateTenantStorageLayoutProcedure] = byName(s.tenant.GetTenantStorageMigration,
		func(n string) *adminv1.GetTenantStorageMigrationRequest {
			return &adminv1.GetTenantStorageMigrationRequest{Name: n}
		})
	collection := byName(s.collection.GetCollection, func(n string) *adminv1.GetCollectionRequest { return &adminv1.GetCollectionRequest{Name: n} })
	for _, p := range []string{
		adminconnect.CollectionServiceUpdateCollectionProcedure,
		adminconnect.CollectionServiceDeleteCollectionProcedure,
		adminconnect.CollectionServiceSetCollectionPolicyProcedure,
		adminconnect.CollectionServiceBindCollectionToBucketProcedure,
	} {
		readers[p] = collection
	}
	quota := byName(s.quota.GetQuota, func(n string) *adminv1.GetQuotaRequest { return &adminv1.GetQuotaRequest{Name: n} })
	readers[adminconnect.QuotaServiceSetQuotaProcedure] = quota
	readers[adminconnect.QuotaServiceResetUsageProcedure] = quota
	subscription := byName(s.subscription.GetSubscription, func(n string) *adminv1.GetSubscriptionRequest {
		return &adminv1.GetSubscriptionRequest{Name: n}
	})
	readers[adminconnect.EventSubscriptionServiceUpdateSubscriptionProcedure] = subscription
	readers[adminconnect.EventSubscriptionServiceDeleteSubscriptionProcedure] = subscription
	// Creates: the answer is the resource made, and there was none before.
	for _, p := range []string{
		adminconnect.BackendServiceCreateBackendProcedure,
		adminconnect.BucketServiceCreateBucketProcedure,
		adminconnect.BucketServiceGrantBucketProcedure,
		adminconnect.TenantServiceCreateTenantProcedure,
		adminconnect.CollectionServiceCreateCollectionProcedure,
		adminconnect.EventSubscriptionServiceCreateSubscriptionProcedure,
		adminconnect.APITokenServiceCreateProcedure,
		adminconnect.CapabilityServiceIssueProcedure,
		adminconnect.CapabilityServiceDelegateProcedure,
	} {
		readers[p] = nil
	}
	if s.budget != nil {
		readers[adminconnect.TenantBudgetServiceSetProcedure] = func(ctx context.Context, req proto.Message) (proto.Message, error) {
			set, ok := req.(*adminv1.TenantBudgetServiceSetRequest)
			if !ok || set.GetTenantId() == "" {
				return nil, nil
			}
			return s.budget.Get(ctx, &adminv1.TenantBudgetServiceGetRequest{TenantId: set.GetTenantId()})
		}
	}
}
