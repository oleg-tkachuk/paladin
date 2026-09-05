package admin

import (
	"errors"
	"testing"

	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/audith"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/backendh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/systemh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	policyh "github.com/oleg-tkachuk/paladin/internal/api/v1/policy"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// The property, for every handler-backed server in this package: a handler
// failure must reach the caller AS a failure, wrapping the original.
//
// A shim that swallowed one and returned a zero value answers the console
// with 200 and an empty body — "this tenant has no buckets", "the backend
// has no credentials", "there is no audit trail". Each reads exactly like
// the truth, and the operator acts on it.
//
// None of these branches were held before the seams landed: standing up a
// real handler needs a database, so only the slow gate reached them, and a
// mutation run over this package scored 4%.

var errBoom = errors.New("backend unavailable")

type failingAudit struct{}

func (failingAudit) ExportAuditLog(context.Context, string, string) (*audith.ExportAuditLogResult, error) {
	return nil, errBoom
}
func (failingAudit) GetAuditLogEntry(context.Context, uuid.UUID) (*admindomain.AuditEntry, error) {
	return nil, errBoom
}
func (failingAudit) ListAuditLog(context.Context, admindomain.ListAuditArgs, string) ([]admindomain.AuditEntry, string, error) {
	return nil, "", errBoom
}

type failingBackend struct{}

func (failingBackend) CreateBackend(context.Context, admindomain.StorageBackend) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) DeleteBackend(context.Context, string, int64) error {
	return errBoom
}
func (failingBackend) GetBackend(context.Context, string) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) ListBackends(context.Context, int32, string, string) ([]admindomain.StorageBackend, string, error) {
	return nil, "", errBoom
}
func (failingBackend) RotateCredentials(context.Context, string, string, time.Duration) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) SetBackendEnabled(context.Context, string, bool, int64) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) SetBackendMaintenance(context.Context, string, bool, int64) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) SetBackendReadOnly(context.Context, string, bool, int64) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}
func (failingBackend) TestBackend(context.Context, string) (*backendh.TestBackendOutput, error) {
	return nil, errBoom
}
func (failingBackend) UpdateBackend(context.Context, admindomain.StorageBackend, int64, []string) (*admindomain.StorageBackend, error) {
	return nil, errBoom
}

type failingBilling struct{}

func (failingBilling) GetTenantSummary(context.Context, uuid.UUID, time.Time, time.Time) (*billingh.Summary, error) {
	return nil, errBoom
}
func (failingBilling) GetTenantTimeSeries(context.Context, uuid.UUID, time.Time, time.Time, string) (*billingh.TimeSeries, error) {
	return nil, errBoom
}

type failingBucket struct{}

func (failingBucket) CreateBucket(context.Context, bucketh.CreateBucketInput) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) DeleteBucket(context.Context, bucketh.DeleteBucketInput) error {
	return errBoom
}
func (failingBucket) GetBucket(context.Context, string, string) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) ListAccessibleBuckets(context.Context, uuid.UUID, int32, string, string) ([]admindomain.Bucket, string, error) {
	return nil, "", errBoom
}
func (failingBucket) ListBuckets(context.Context, admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	return nil, "", errBoom
}
func (failingBucket) SetLifecycleRules(context.Context, string, string, []admindomain.LifecycleRule, int64) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) SetObjectLock(context.Context, string, string, admindomain.ObjectLockConfig, int64) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) SetPolicy(context.Context, string, string, string, int64) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) SetReplication(context.Context, string, string, admindomain.BucketReplication, int64) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) SetVersioning(context.Context, string, string, admindomain.BucketVersioning, int64) (*admindomain.Bucket, error) {
	return nil, errBoom
}
func (failingBucket) UpdateBucket(context.Context, bucketh.UpdateBucketInput) (*admindomain.Bucket, error) {
	return nil, errBoom
}

type failingCollection struct{}

func (failingCollection) BindCollectionToBucket(context.Context, uuid.UUID, string, string, int64) (*objectkey.Collection, error) {
	return nil, errBoom
}
func (failingCollection) CreateCollection(context.Context, objectkey.CreateCollectionArgs) (*objectkey.Collection, error) {
	return nil, errBoom
}
func (failingCollection) DeleteCollection(context.Context, uuid.UUID, string, int64) error {
	return errBoom
}
func (failingCollection) GetCollection(context.Context, uuid.UUID, string) (*objectkey.Collection, error) {
	return nil, errBoom
}
func (failingCollection) ListCollections(context.Context, objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error) {
	return nil, "", errBoom
}
func (failingCollection) UpdateCollection(context.Context, objectkey.UpdateCollectionArgs) (*objectkey.Collection, error) {
	return nil, errBoom
}

type failingEventSubscription struct{}

func (failingEventSubscription) Create(context.Context, admindomain.EventSubscription) (*admindomain.EventSubscription, error) {
	return nil, errBoom
}
func (failingEventSubscription) Delete(context.Context, uuid.UUID, uuid.UUID, int64) error {
	return errBoom
}
func (failingEventSubscription) Get(context.Context, uuid.UUID, uuid.UUID) (*admindomain.EventSubscription, error) {
	return nil, errBoom
}
func (failingEventSubscription) List(context.Context, admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return nil, "", errBoom
}
func (failingEventSubscription) TestSubscription(context.Context, uuid.UUID, uuid.UUID) error {
	return errBoom
}
func (failingEventSubscription) Update(context.Context, uuid.UUID, admindomain.EventSubscription, int64, []string) (*admindomain.EventSubscription, error) {
	return nil, errBoom
}

type failingOperation struct{}

func (failingOperation) CancelOperation(context.Context, uuid.UUID) error {
	return errBoom
}
func (failingOperation) GetOperation(context.Context, uuid.UUID) (*operation.Operation, error) {
	return nil, errBoom
}
func (failingOperation) ListOperations(context.Context, *operation.State, int32, string, string, bool) ([]operation.Operation, string, error) {
	return nil, "", errBoom
}

type failingPolicy struct{}

func (failingPolicy) GetEffectivePolicy(context.Context, string, uuid.UUID) (*policyh.EffectivePolicyOutput, error) {
	return nil, errBoom
}
func (failingPolicy) SimulateAuthz(context.Context, policyh.SimulateAuthzInput) (*policyh.SimulateAuthzOutput, error) {
	return nil, errBoom
}
func (failingPolicy) ValidatePolicy(context.Context, string) (bool, string, error) {
	return false, "", errBoom
}

type failingQuota struct{}

func (failingQuota) GetBucketQuota(context.Context, string, string) (*admindomain.Quota, error) {
	return nil, errBoom
}
func (failingQuota) GetTenantQuota(context.Context, uuid.UUID) (*admindomain.Quota, error) {
	return nil, errBoom
}
func (failingQuota) ResetUsage(context.Context, uuid.UUID) error {
	return errBoom
}
func (failingQuota) SetQuota(context.Context, admindomain.Quota, []string) (*admindomain.Quota, error) {
	return nil, errBoom
}

type failingSystem struct{}

func (failingSystem) DispatcherStats(context.Context) (*worker.DeliveryStats, bool, error) {
	return nil, false, errBoom
}
func (failingSystem) MarshalRedacted(context.Context) (string, string, error) {
	return "", "", errBoom
}
func (failingSystem) PlatformStats(context.Context) (*systemh.PlatformStatsResult, error) {
	return nil, errBoom
}

type failingTenant struct{}

func (failingTenant) ClearDefaultBinding(context.Context, uuid.UUID) error {
	return errBoom
}
func (failingTenant) CreateTenant(context.Context, tenant.CreateTenantArgs) (*tenant.Tenant, error) {
	return nil, errBoom
}
func (failingTenant) DeleteTenant(context.Context, uuid.UUID, int64) error {
	return errBoom
}
func (failingTenant) GetDefaultBinding(context.Context, uuid.UUID) (*tenant.DefaultBinding, error) {
	return nil, errBoom
}
func (failingTenant) GetTenant(context.Context, uuid.UUID) (*tenant.Tenant, error) {
	return nil, errBoom
}
func (failingTenant) GetTenantBySlug(context.Context, string) (*tenant.Tenant, error) {
	return nil, errBoom
}
func (failingTenant) GetTenantStorageMigration(context.Context, uuid.UUID) (*tenant.StorageMigration, error) {
	return nil, errBoom
}
func (failingTenant) ListTenants(context.Context, tenant.ListTenantsArgs, string) ([]tenant.Tenant, string, error) {
	return nil, "", errBoom
}
func (failingTenant) MigrateTenantStorageLayout(context.Context, uuid.UUID, string, int64) (*tenant.StorageMigration, error) {
	return nil, errBoom
}
func (failingTenant) PurgeTenant(context.Context, uuid.UUID) error {
	return errBoom
}
func (failingTenant) RenameTenantSlug(context.Context, tenant.RenameTenantSlugArgs) (*tenant.Tenant, error) {
	return nil, errBoom
}
func (failingTenant) ResolveRenamedSlug(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, errBoom
}
func (failingTenant) RestoreTenant(context.Context, uuid.UUID) (*tenant.Tenant, error) {
	return nil, errBoom
}
func (failingTenant) SetDefaultBinding(context.Context, uuid.UUID, string) (*tenant.DefaultBinding, error) {
	return nil, errBoom
}
func (failingTenant) UpdateTenant(context.Context, tenant.UpdateTenantArgs) (*tenant.Tenant, error) {
	return nil, errBoom
}

// The doubles have to satisfy the same interfaces the servers hold, or the
// table below would be testing something the production wiring never uses.
var (
	_ auditHandler             = failingAudit{}
	_ backendHandler           = failingBackend{}
	_ billingHandler           = failingBilling{}
	_ bucketHandler            = failingBucket{}
	_ collectionHandler        = failingCollection{}
	_ eventSubscriptionHandler = failingEventSubscription{}
	_ operationHandler         = failingOperation{}
	_ policyHandler            = failingPolicy{}
	_ quotaHandler             = failingQuota{}
	_ systemHandler            = failingSystem{}
	_ tenantHandler            = failingTenant{}
)

// CreateCollection consults the tenant's default binding when the request
// names no bucket. Production always wires one (build_listeners_admin.go
// passes repos.Tenant), so a nil field is a test artefact, not a reachable
// state — but the lookup can fail, and that branch is worth holding too.
type failingBindings struct{}

func (failingBindings) GetDefaultBinding(context.Context, uuid.UUID) (tenant.DefaultBinding, error) {
	return tenant.DefaultBinding{}, errBoom
}

var _ defaultBindingSource = failingBindings{}

func TestEveryAdminShimPropagatesHandlerErrors(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	tenantName := "tenants/" + tenantID.String()
	bucketName := "storageBackends/primary/buckets/b1"
	collName := tenantName + "/collections/c1"
	subName := tenantName + "/eventSubscriptions/" + uuid.NewString()
	opName := "operations/" + uuid.NewString()
	okPolicy := "permit(principal, action, resource);"

	auditSrv := &AuditServer{H: failingAudit{}}
	backendSrv := &BackendServer{H: failingBackend{}}
	billingSrv := &BillingServer{H: failingBilling{}}
	bucketSrv := &BucketServer{H: failingBucket{}}
	collSrv := &CollectionServer{H: failingCollection{}, bindings: failingBindings{}}
	subSrv := &EventSubscriptionServer{H: failingEventSubscription{}, Tenants: failingTenant{}}
	opSrv := &OperationServer{H: failingOperation{}}
	policySrv := &PolicyServer{H: failingPolicy{}}
	quotaSrv := &QuotaServer{H: failingQuota{}}
	sysSrv := &SystemServer{H: failingSystem{}}
	tenantSrv := &TenantServer{H: failingTenant{}}

	cases := []struct {
		name string
		call func() error
	}{
		{"Audit.ListAuditLog", func() error {
			_, err := auditSrv.ListAuditLog(ctx, connect.NewRequest(&pb.ListAuditLogRequest{}))
			return err
		}},
		{"Audit.GetAuditLogEntry", func() error {
			_, err := auditSrv.GetAuditLogEntry(ctx, connect.NewRequest(&pb.GetAuditLogEntryRequest{
				EntryId: uuid.NewString(),
			}))
			return err
		}},
		{"Audit.ExportAuditLog", func() error {
			_, err := auditSrv.ExportAuditLog(ctx, connect.NewRequest(&pb.ExportAuditLogRequest{
				Destination: "s3://bucket/prefix",
			}))
			return err
		}},

		{"Backend.ListBackends", func() error {
			_, err := backendSrv.ListBackends(ctx, connect.NewRequest(&pb.ListBackendsRequest{}))
			return err
		}},
		{"Backend.GetBackend", func() error {
			_, err := backendSrv.GetBackend(ctx, connect.NewRequest(&pb.GetBackendRequest{
				Name: "storageBackends/primary",
			}))
			return err
		}},
		{"Backend.CreateBackend", func() error {
			_, err := backendSrv.CreateBackend(ctx, connect.NewRequest(&pb.CreateBackendRequest{
				BackendId: "primary",
				Backend:   &pb.StorageBackend{Provider: "s3", Endpoint: "https://s3.example"},
			}))
			return err
		}},
		{"Backend.UpdateBackend", func() error {
			_, err := backendSrv.UpdateBackend(ctx, connect.NewRequest(&pb.UpdateBackendRequest{
				Name: "storageBackends/primary", ResourceVersion: "1",
				Backend: &pb.StorageBackend{},
			}))
			return err
		}},
		{"Backend.DeleteBackend", func() error {
			_, err := backendSrv.DeleteBackend(ctx, connect.NewRequest(&pb.DeleteBackendRequest{
				Name: "storageBackends/primary", ResourceVersion: "1",
			}))
			return err
		}},
		{"Backend.TestBackend", func() error {
			_, err := backendSrv.TestBackend(ctx, connect.NewRequest(&pb.TestBackendRequest{
				Name: "storageBackends/primary",
			}))
			return err
		}},
		{"Backend.RotateCredentials", func() error {
			_, err := backendSrv.RotateCredentials(ctx, connect.NewRequest(&pb.RotateCredentialsRequest{
				Name: "storageBackends/primary",
			}))
			return err
		}},
		{"Backend.SetBackendEnabled", func() error {
			_, err := backendSrv.SetBackendEnabled(ctx, connect.NewRequest(&pb.SetBackendEnabledRequest{
				Name: "storageBackends/primary", Enabled: true, ResourceVersion: "1",
			}))
			return err
		}},
		{"Backend.SetBackendReadOnly", func() error {
			_, err := backendSrv.SetBackendReadOnly(ctx, connect.NewRequest(&pb.SetBackendReadOnlyRequest{
				Name: "storageBackends/primary", ReadOnly: true, ResourceVersion: "1",
			}))
			return err
		}},
		{"Backend.SetBackendMaintenance", func() error {
			_, err := backendSrv.SetBackendMaintenance(ctx, connect.NewRequest(&pb.SetBackendMaintenanceRequest{
				Name: "storageBackends/primary", Maintenance: true, ResourceVersion: "1",
			}))
			return err
		}},

		{"Billing.GetTenantSummary", func() error {
			_, err := billingSrv.GetTenantSummary(ctx, connect.NewRequest(&pb.GetTenantSummaryRequest{
				TenantId: tenantID.String(),
			}))
			return err
		}},
		{"Billing.GetTenantTimeSeries", func() error {
			_, err := billingSrv.GetTenantTimeSeries(ctx, connect.NewRequest(&pb.GetTenantTimeSeriesRequest{
				TenantId: tenantID.String(),
			}))
			return err
		}},

		{"Bucket.ListBuckets", func() error {
			_, err := bucketSrv.ListBuckets(ctx, connect.NewRequest(&pb.ListBucketsRequest{
				Parent: "storageBackends/primary",
			}))
			return err
		}},
		{"Bucket.ListAccessibleBuckets", func() error {
			_, err := bucketSrv.ListAccessibleBuckets(ctx, connect.NewRequest(&pb.ListAccessibleBucketsRequest{
				Tenant: tenantName,
			}))
			return err
		}},
		{"Bucket.GetBucket", func() error {
			_, err := bucketSrv.GetBucket(ctx, connect.NewRequest(&pb.GetBucketRequest{Name: bucketName}))
			return err
		}},
		{"Bucket.CreateBucket", func() error {
			_, err := bucketSrv.CreateBucket(ctx, connect.NewRequest(&pb.CreateBucketRequest{
				Parent: "storageBackends/primary", BucketId: "b1", Bucket: &pb.Bucket{},
			}))
			return err
		}},
		{"Bucket.UpdateBucket", func() error {
			_, err := bucketSrv.UpdateBucket(ctx, connect.NewRequest(&pb.UpdateBucketRequest{
				Name: bucketName, ResourceVersion: "1", Bucket: &pb.Bucket{},
			}))
			return err
		}},
		{"Bucket.DeleteBucket", func() error {
			_, err := bucketSrv.DeleteBucket(ctx, connect.NewRequest(&pb.DeleteBucketRequest{Name: bucketName, ResourceVersion: "1"}))
			return err
		}},
		{"Bucket.SetVersioning", func() error {
			_, err := bucketSrv.SetVersioning(ctx, connect.NewRequest(&pb.SetVersioningRequest{
				Name: bucketName, Versioning: &pb.BucketVersioning{Enabled: true},
			}))
			return err
		}},
		{"Bucket.SetObjectLock", func() error {
			_, err := bucketSrv.SetObjectLock(ctx, connect.NewRequest(&pb.SetObjectLockRequest{Name: bucketName}))
			return err
		}},
		{"Bucket.SetLifecycleRules", func() error {
			_, err := bucketSrv.SetLifecycleRules(ctx, connect.NewRequest(&pb.SetLifecycleRulesRequest{Name: bucketName}))
			return err
		}},
		{"Bucket.SetReplication", func() error {
			_, err := bucketSrv.SetReplication(ctx, connect.NewRequest(&pb.SetReplicationRequest{Name: bucketName}))
			return err
		}},
		{"Bucket.SetBucketPolicy", func() error {
			_, err := bucketSrv.SetBucketPolicy(ctx, connect.NewRequest(&pb.SetBucketPolicyRequest{
				Name: bucketName, CedarPolicy: okPolicy,
			}))
			return err
		}},

		{"Collection.ListCollections", func() error {
			_, err := collSrv.ListCollections(ctx, connect.NewRequest(&pb.ListCollectionsRequest{Parent: tenantName}))
			return err
		}},
		{"Collection.GetCollection", func() error {
			_, err := collSrv.GetCollection(ctx, connect.NewRequest(&pb.GetCollectionRequest{Name: collName}))
			return err
		}},
		{"Collection.CreateCollection", func() error {
			_, err := collSrv.CreateCollection(ctx, connect.NewRequest(&pb.CreateCollectionRequest{
				Parent: tenantName, Collection: "c1",
				CollectionResource: &pb.Collection{CedarPolicy: okPolicy, Bucket: bucketName},
			}))
			return err
		}},
		{"Collection.UpdateCollection", func() error {
			_, err := collSrv.UpdateCollection(ctx, connect.NewRequest(&pb.UpdateCollectionRequest{
				Name: collName, ResourceVersion: "1",
				CollectionResource: &pb.Collection{CedarPolicy: okPolicy},
			}))
			return err
		}},
		{"Collection.DeleteCollection", func() error {
			_, err := collSrv.DeleteCollection(ctx, connect.NewRequest(&pb.DeleteCollectionRequest{Name: collName, ResourceVersion: "1"}))
			return err
		}},
		{"Collection.SetCollectionPolicy", func() error {
			_, err := collSrv.SetCollectionPolicy(ctx, connect.NewRequest(&pb.SetCollectionPolicyRequest{
				Name: collName, CedarPolicy: okPolicy,
			}))
			return err
		}},
		{"Collection.BindCollectionToBucket", func() error {
			_, err := collSrv.BindCollectionToBucket(ctx, connect.NewRequest(&pb.BindCollectionToBucketRequest{
				Name: collName, Bucket: bucketName,
			}))
			return err
		}},

		{"EventSubscription.ListSubscriptions", func() error {
			_, err := subSrv.ListSubscriptions(ctx, connect.NewRequest(&pb.ListSubscriptionsRequest{Parent: tenantName}))
			return err
		}},
		{"EventSubscription.GetSubscription", func() error {
			_, err := subSrv.GetSubscription(ctx, connect.NewRequest(&pb.GetSubscriptionRequest{Name: subName}))
			return err
		}},
		{"EventSubscription.CreateSubscription", func() error {
			_, err := subSrv.CreateSubscription(ctx, connect.NewRequest(&pb.CreateSubscriptionRequest{
				Parent: tenantName,
				Subscription: &pb.EventSubscription{
					Sink: &pb.EventSink{},
				},
			}))
			return err
		}},
		{"EventSubscription.UpdateSubscription", func() error {
			_, err := subSrv.UpdateSubscription(ctx, connect.NewRequest(&pb.UpdateSubscriptionRequest{
				Name: subName, ResourceVersion: "1",
				Subscription: &pb.EventSubscription{Sink: &pb.EventSink{}},
			}))
			return err
		}},
		{"EventSubscription.DeleteSubscription", func() error {
			_, err := subSrv.DeleteSubscription(ctx, connect.NewRequest(&pb.DeleteSubscriptionRequest{Name: subName}))
			return err
		}},
		{"EventSubscription.TestSubscription", func() error {
			_, err := subSrv.TestSubscription(ctx, connect.NewRequest(&pb.TestSubscriptionRequest{Name: subName}))
			return err
		}},

		{"Operation.ListOperations", func() error {
			_, err := opSrv.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{}))
			return err
		}},
		{"Operation.GetOperation", func() error {
			_, err := opSrv.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{Name: opName}))
			return err
		}},
		{"Operation.CancelOperation", func() error {
			_, err := opSrv.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{Name: opName}))
			return err
		}},

		{"Policy.Validate", func() error {
			_, err := policySrv.Validate(ctx, connect.NewRequest(&pb.ValidateRequest{CedarPolicy: okPolicy}))
			return err
		}},
		{"Policy.GetEffectivePolicy", func() error {
			_, err := policySrv.GetEffectivePolicy(ctx, connect.NewRequest(&pb.GetEffectivePolicyRequest{
				ResourceName: collName,
			}))
			return err
		}},
		{"Policy.SimulateAuthz", func() error {
			_, err := policySrv.SimulateAuthz(ctx, connect.NewRequest(&pb.SimulateAuthzRequest{
				ResourceName: collName, Action: "read",
				PrincipalSubject: "tester", PrincipalTenantId: tenantID.String(),
			}))
			return err
		}},

		{"Quota.GetQuota", func() error {
			_, err := quotaSrv.GetQuota(ctx, connect.NewRequest(&pb.GetQuotaRequest{Name: tenantName + "/quota"}))
			return err
		}},
		{"Quota.SetQuota", func() error {
			_, err := quotaSrv.SetQuota(ctx, connect.NewRequest(&pb.SetQuotaRequest{
				Name: tenantName + "/quota", Quota: &pb.Quota{},
			}))
			return err
		}},
		{"Quota.ResetUsage", func() error {
			_, err := quotaSrv.ResetUsage(ctx, connect.NewRequest(&pb.ResetUsageRequest{Name: tenantName + "/quota"}))
			return err
		}},

		{"System.GetConfig", func() error {
			_, err := sysSrv.GetConfig(ctx, connect.NewRequest(&pb.GetConfigRequest{}))
			return err
		}},
		{"System.GetDispatcherStats", func() error {
			_, err := sysSrv.GetDispatcherStats(ctx, connect.NewRequest(&pb.GetDispatcherStatsRequest{}))
			return err
		}},
		{"System.GetPlatformStats", func() error {
			_, err := sysSrv.GetPlatformStats(ctx, connect.NewRequest(&pb.GetPlatformStatsRequest{}))
			return err
		}},

		{"Tenant.ListTenants", func() error {
			_, err := tenantSrv.ListTenants(ctx, connect.NewRequest(&pb.ListTenantsRequest{}))
			return err
		}},
		{"Tenant.GetTenant", func() error {
			_, err := tenantSrv.GetTenant(ctx, connect.NewRequest(&pb.GetTenantRequest{Name: tenantName}))
			return err
		}},
		{"Tenant.CreateTenant", func() error {
			_, err := tenantSrv.CreateTenant(ctx, connect.NewRequest(&pb.CreateTenantRequest{
				TenantId: tenantID.String(), Tenant: &pb.Tenant{DisplayName: "Acme"},
			}))
			return err
		}},
		{"Tenant.UpdateTenant", func() error {
			_, err := tenantSrv.UpdateTenant(ctx, connect.NewRequest(&pb.UpdateTenantRequest{
				Name: tenantName, ResourceVersion: "1",
				Tenant: &pb.Tenant{DisplayName: "Acme"},
			}))
			return err
		}},
		{"Tenant.DeleteTenant", func() error {
			_, err := tenantSrv.DeleteTenant(ctx, connect.NewRequest(&pb.DeleteTenantRequest{Name: tenantName, ResourceVersion: "1"}))
			return err
		}},
		{"Tenant.RestoreTenant", func() error {
			_, err := tenantSrv.RestoreTenant(ctx, connect.NewRequest(&pb.RestoreTenantRequest{Name: tenantName}))
			return err
		}},
		{"Tenant.PurgeTenant", func() error {
			_, err := tenantSrv.PurgeTenant(ctx, connect.NewRequest(&pb.PurgeTenantRequest{Name: tenantName}))
			return err
		}},
		{"Tenant.RenameTenantSlug", func() error {
			_, err := tenantSrv.RenameTenantSlug(ctx, connect.NewRequest(&pb.RenameTenantSlugRequest{
				Name: tenantName, NewSlug: "acme2",
			}))
			return err
		}},
		{"Tenant.ResolveRenamedSlug", func() error {
			_, err := tenantSrv.ResolveRenamedSlug(ctx, connect.NewRequest(&pb.ResolveRenamedSlugRequest{
				OldSlug: "acme",
			}))
			return err
		}},
		{"Tenant.SetInheritedPolicy", func() error {
			_, err := tenantSrv.SetInheritedPolicy(ctx, connect.NewRequest(&pb.SetInheritedPolicyRequest{
				Name: tenantName, CedarPolicy: okPolicy,
			}))
			return err
		}},
		{"Tenant.GetTenantDefaultBinding", func() error {
			_, err := tenantSrv.GetTenantDefaultBinding(ctx, connect.NewRequest(&pb.GetTenantDefaultBindingRequest{Name: tenantName}))
			return err
		}},
		{"Tenant.SetTenantDefaultBinding", func() error {
			_, err := tenantSrv.SetTenantDefaultBinding(ctx, connect.NewRequest(&pb.SetTenantDefaultBindingRequest{Name: tenantName, Bucket: bucketName}))
			return err
		}},
		{"Tenant.ClearTenantDefaultBinding", func() error {
			_, err := tenantSrv.ClearTenantDefaultBinding(ctx, connect.NewRequest(&pb.ClearTenantDefaultBindingRequest{Name: tenantName}))
			return err
		}},
		{"Tenant.MigrateTenantStorageLayout", func() error {
			_, err := tenantSrv.MigrateTenantStorageLayout(ctx, connect.NewRequest(&pb.MigrateTenantStorageLayoutRequest{
				Name: tenantName, TargetBackendId: "primary",
			}))
			return err
		}},
		{"Tenant.GetTenantStorageMigration", func() error {
			_, err := tenantSrv.GetTenantStorageMigration(ctx, connect.NewRequest(&pb.GetTenantStorageMigrationRequest{Name: tenantName}))
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("the handler failed and the shim answered success — the " +
					"console reads that as an empty result rather than an outage")
			}
			if !errors.Is(err, errBoom) {
				t.Errorf("error %v does not wrap the handler's — either the shim "+
					"replaced the cause, or this request never reached the handler "+
					"and the case is testing argument parsing instead", err)
			}
		})
	}
}
