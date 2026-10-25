package app

import (
	"context"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	connectdata "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	adminconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	dataconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	iamconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
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

// versionSegment separates an object's name from one of its versions'.
const versionSegment = "/versions/"

// objectOfVersion is the name of the object a version's name names: the
// name itself when it names no version.
func objectOfVersion(name string) string {
	object, _, _ := strings.Cut(name, versionSegment)
	return object
}

// dataStateServers are the data plane's servers the audit log reads an
// object through, as its caller would.
type dataStateServers struct {
	object *connectdata.ObjectServer
	tags   *connectdata.ObjectTagServer
}

// fill puts into readers the reader of each data-plane action that changes
// one object: the object, its lock or its tags, by the object's name.
func (s dataStateServers) fill(readers middleware.StateReaders) {
	object := byName(s.object.GetObject, func(n string) *datav1.GetObjectRequest { return &datav1.GetObjectRequest{Name: n} })
	for _, p := range []string{
		dataconnect.ObjectServiceUpdateObjectProcedure,
		dataconnect.ObjectServiceDeleteObjectProcedure,
		dataconnect.ObjectServiceRestoreObjectProcedure,
		dataconnect.ObjectServiceSetObjectTaintProcedure,
	} {
		readers[p] = object
	}
	// Restoring a version changes the object it is a version of.
	readers[dataconnect.ObjectServiceRestoreObjectVersionProcedure] = byName(s.object.GetObject, func(n string) *datav1.GetObjectRequest {
		return &datav1.GetObjectRequest{Name: objectOfVersion(n)}
	})
	lock := byName(s.object.GetObjectLock, func(n string) *datav1.GetObjectLockRequest { return &datav1.GetObjectLockRequest{Name: n} })
	readers[dataconnect.ObjectServiceSetObjectRetentionProcedure] = lock
	readers[dataconnect.ObjectServiceSetObjectLegalHoldProcedure] = lock
	tags := byName(s.tags.GetObjectTags, func(n string) *datav1.GetObjectTagsRequest { return &datav1.GetObjectTagsRequest{Name: n} })
	readers[dataconnect.ObjectTagServicePutObjectTagsProcedure] = tags
	readers[dataconnect.ObjectTagServiceDeleteObjectTagsProcedure] = tags
	// Creates: the answer is the object made, and there was none before.
	for _, p := range []string{
		dataconnect.ObjectServiceUploadObjectProcedure,
		dataconnect.ObjectServiceCompleteObjectProcedure,
		dataconnect.ObjectServiceCopyObjectProcedure,
		dataconnect.MultipartUploadServiceInitiateMultipartUploadProcedure,
		dataconnect.MultipartUploadServiceCompleteMultipartUploadProcedure,
	} {
		readers[p] = nil
	}
}

// iamStateServers are the IAM plane's servers the audit log reads a user
// through, as its caller would.
type iamStateServers struct {
	user     *connectiam.UserServer
	settings *connectiam.UserSettingsServer
}

// fill puts into readers the reader of each IAM action that changes one user
// or a user's settings.
func (s iamStateServers) fill(readers middleware.StateReaders) {
	user := byName(s.user.GetUser, func(n string) *iamv1.GetUserRequest { return &iamv1.GetUserRequest{Name: n} })
	for _, p := range []string{
		iamconnect.UserServiceUpdateUserProcedure,
		iamconnect.UserServiceDeleteUserProcedure,
		iamconnect.UserServiceGrantScopesProcedure,
		iamconnect.UserServiceRevokeScopesProcedure,
	} {
		readers[p] = user
	}
	readers[iamconnect.UserServiceCreateUserProcedure] = nil
	// The caller's own settings, which the request does not name.
	readers[iamconnect.UserSettingsServiceUpdateMineProcedure] = func(ctx context.Context, _ proto.Message) (proto.Message, error) {
		return s.settings.GetMine(ctx, &iamv1.GetMineRequest{})
	}
	readers[iamconnect.UserSettingsServiceDeleteForUserProcedure] = byName(s.settings.GetForUser, func(n string) *iamv1.GetForUserRequest {
		return &iamv1.GetForUserRequest{Name: n}
	})
}
