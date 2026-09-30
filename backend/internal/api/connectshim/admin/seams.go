package admin

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/audith"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/backendh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/eventsubh"
	policyh "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/policyh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/quotah"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// auditHandler is what AuditServer needs from *audith.Handler.
type auditHandler interface {
	ExportAuditLog(ctx context.Context, filter, destination string) (*audith.ExportAuditLogResult, error)
	GetAuditLogEntry(ctx context.Context, id uuid.UUID) (*admindomain.AuditEntry, error)
	ListAuditLog(ctx context.Context, args admindomain.ListAuditArgs, filter string) ([]admindomain.AuditEntry, string, error)
}

var _ auditHandler = (*audith.Handler)(nil)

// backendHandler is what BackendServer needs from *backendh.Handler.
type backendHandler interface {
	CreateBackend(ctx context.Context, b admindomain.StorageBackend) (*admindomain.StorageBackend, error)
	DeleteBackend(ctx context.Context, backendID string, expectedVersion int64) error
	GetBackend(ctx context.Context, backendID string) (*admindomain.StorageBackend, error)
	ListBackends(ctx context.Context, pageSize int32, afterID, filter string) ([]admindomain.StorageBackend, string, error)
	RotateCredentials(ctx context.Context, backendID, secretRef string, grace time.Duration) (*admindomain.StorageBackend, error)
	SetBackendEnabled(ctx context.Context, backendID string, enabled bool, expectedVersion int64) (*admindomain.StorageBackend, error)
	SetBackendMaintenance(ctx context.Context, backendID string, maintenance bool, expectedVersion int64) (*admindomain.StorageBackend, error)
	SetBackendReadOnly(ctx context.Context, backendID string, readOnly bool, expectedVersion int64) (*admindomain.StorageBackend, error)
	TestBackend(ctx context.Context, backendID string) (*backendh.TestBackendOutput, error)
	UpdateBackend(ctx context.Context, b admindomain.StorageBackend, expectedVersion int64, mask []string) (*admindomain.StorageBackend, error)
}

var _ backendHandler = (*backendh.Handler)(nil)

// billingHandler is what BillingServer needs from *billingh.Handler.
type billingHandler interface {
	GetTenantSummary(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time) (*billingh.Summary, error)
	GetTenantTimeSeries(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time, granularity string) (*billingh.TimeSeries, error)
}

var _ billingHandler = (*billingh.Handler)(nil)

// bucketHandler is what BucketServer needs from *bucketh.Handler.
type bucketHandler interface {
	CreateBucket(ctx context.Context, in bucketh.CreateBucketInput) (*admindomain.Bucket, error)
	DeleteBucket(ctx context.Context, in bucketh.DeleteBucketInput) error
	GetBucket(ctx context.Context, backendID, bucketName string) (*admindomain.Bucket, error)
	ListAccessibleBuckets(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error)
	ListBuckets(ctx context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error)
	SetLifecycleRules(ctx context.Context, backendID, bucketName string, rules []admindomain.LifecycleRule, expectedVersion int64) (*admindomain.Bucket, error)
	SetObjectLock(ctx context.Context, backendID, bucketName string, lock admindomain.ObjectLockConfig, expectedVersion int64) (*admindomain.Bucket, error)
	SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) (*admindomain.Bucket, error)
	SetReplication(ctx context.Context, backendID, bucketName string, r admindomain.BucketReplication, expectedVersion int64) (*admindomain.Bucket, error)
	SetVersioning(ctx context.Context, backendID, bucketName string, v admindomain.BucketVersioning, expectedVersion int64) (*admindomain.Bucket, error)
	UpdateBucket(ctx context.Context, in bucketh.UpdateBucketInput) (*admindomain.Bucket, error)
}

var _ bucketHandler = (*bucketh.Handler)(nil)

// collectionHandler is what CollectionServer needs from *objectkey.Handler.
type collectionHandler interface {
	BindCollectionToBucket(ctx context.Context, targetTenantID uuid.UUID, collection, bucket string, expectedVersion int64) (*objectkey.Collection, error)
	CreateCollection(ctx context.Context, args objectkey.CreateCollectionArgs) (*objectkey.Collection, error)
	DeleteCollection(ctx context.Context, targetTenantID uuid.UUID, collection string, expectedVersion int64) error
	GetCollection(ctx context.Context, tenantID uuid.UUID, collection string) (*objectkey.Collection, error)
	ListCollections(ctx context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error)
	UpdateCollection(ctx context.Context, args objectkey.UpdateCollectionArgs) (*objectkey.Collection, error)
}

var _ collectionHandler = (*objectkey.Handler)(nil)

// eventSubscriptionHandler is what EventSubscriptionServer needs from *eventsubh.Handler.
type eventSubscriptionHandler interface {
	Create(ctx context.Context, s admindomain.EventSubscription) (*admindomain.EventSubscription, error)
	Delete(ctx context.Context, tenantID, id uuid.UUID, expectedVersion int64) error
	Get(ctx context.Context, tenantID, id uuid.UUID) (*admindomain.EventSubscription, error)
	List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error)
	TestSubscription(ctx context.Context, tenantID, id uuid.UUID) error
	Update(ctx context.Context, tenantID uuid.UUID, s admindomain.EventSubscription, expectedVersion int64, mask []string) (*admindomain.EventSubscription, error)
}

var _ eventSubscriptionHandler = (*eventsubh.Handler)(nil)

// operationHandler is what OperationServer needs from *operation.Handler.
type operationHandler interface {
	CancelOperation(ctx context.Context, opID uuid.UUID) error
	GetOperation(ctx context.Context, opID uuid.UUID) (*operationh.Operation, error)
	ListOperations(ctx context.Context, state *operationh.State, pageSize int32, pageToken, filter string, newestFirst bool) ([]operationh.Operation, string, error)
}

var _ operationHandler = (*operationh.Handler)(nil)

// policyHandler is what PolicyServer needs from *policyh.Handler.
type policyHandler interface {
	GetEffectivePolicy(ctx context.Context, resourceName string, fallbackTenant uuid.UUID) (*policyh.EffectivePolicyOutput, error)
	SimulateAuthz(ctx context.Context, in policyh.SimulateAuthzInput) (*policyh.SimulateAuthzOutput, error)
	ValidatePolicy(ctx context.Context, text string) (bool, string, error)
}

var _ policyHandler = (*policyh.Handler)(nil)

// quotaHandler is what QuotaServer needs from *quotah.Handler.
type quotaHandler interface {
	GetBucketQuota(ctx context.Context, backendID, bucketName string) (*admindomain.Quota, error)
	GetTenantQuota(ctx context.Context, tenantID uuid.UUID) (*admindomain.Quota, error)
	ResetUsage(ctx context.Context, quotaID uuid.UUID) error
	SetQuota(ctx context.Context, q admindomain.Quota, mask []string) (*admindomain.Quota, error)
}

var _ quotaHandler = (*quotah.Handler)(nil)

// systemHandler is what SystemServer needs from *systemh.Handler.
type systemHandler interface {
	DispatcherStats(ctx context.Context) (stats *worker.DeliveryStats, available bool, err error)
	MarshalRedacted(ctx context.Context) (yamlBlob string, sourcePath string, err error)
	PlatformStats(ctx context.Context) (*systemh.PlatformStatsResult, error)
}

var _ systemHandler = (*systemh.Handler)(nil)

// tenantHandler is what TenantServer needs from *tenant.Handler.
type tenantHandler interface {
	ClearDefaultBinding(ctx context.Context, tenantID uuid.UUID) error
	CreateTenant(ctx context.Context, args tenanth.CreateTenantArgs) (*tenanth.Tenant, error)
	DeleteTenant(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (*tenanth.DefaultBinding, error)
	GetTenant(ctx context.Context, tenantID uuid.UUID) (*tenanth.Tenant, error)
	GetTenantBySlug(ctx context.Context, slug string) (*tenanth.Tenant, error)
	GetTenantStorageMigration(ctx context.Context, tenantID uuid.UUID) (*tenanth.StorageMigration, error)
	ListTenants(ctx context.Context, args tenanth.ListTenantsArgs, pageToken string) ([]tenanth.Tenant, string, error)
	MigrateTenantStorageLayout(ctx context.Context, tenantID uuid.UUID, targetBackendID string, cleanupRetentionSeconds int64) (*tenanth.StorageMigration, error)
	PurgeTenant(ctx context.Context, tenantID uuid.UUID) error
	RenameTenantSlug(ctx context.Context, args tenanth.RenameTenantSlugArgs) (*tenanth.Tenant, error)
	ResolveRenamedSlug(ctx context.Context, oldSlug string) (string, time.Time, error)
	RestoreTenant(ctx context.Context, tenantID uuid.UUID) (*tenanth.Tenant, error)
	SetDefaultBinding(ctx context.Context, tenantID uuid.UUID, bucket string) (*tenanth.DefaultBinding, error)
	UpdateTenant(ctx context.Context, args tenanth.UpdateTenantArgs) (*tenanth.Tenant, error)
}

var _ tenantHandler = (*tenanth.Handler)(nil)
