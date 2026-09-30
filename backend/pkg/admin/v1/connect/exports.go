// Package adminv1connect re-exports Connect server/client constructors for
// paladin.admin.v1 services so external code can wire admin-plane clients
// without touching internal packages.
package adminv1connect

import internal "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1/paladinadminv1connect"

// Service handler interfaces.
type (
	BackendServiceHandler           = internal.BackendServiceHandler
	BucketServiceHandler            = internal.BucketServiceHandler
	TenantServiceHandler            = internal.TenantServiceHandler
	CollectionServiceHandler        = internal.CollectionServiceHandler
	PolicyServiceHandler            = internal.PolicyServiceHandler
	OperationServiceHandler         = internal.PlatformOperationServiceHandler
	AuditLogServiceHandler          = internal.AuditLogServiceHandler
	QuotaServiceHandler             = internal.QuotaServiceHandler
	EventSubscriptionServiceHandler = internal.EventSubscriptionServiceHandler
)

// Client constructors. Each takes a connect-go HTTP client + base URL.
var (
	NewBackendServiceClient           = internal.NewBackendServiceClient
	NewBucketServiceClient            = internal.NewBucketServiceClient
	NewTenantServiceClient            = internal.NewTenantServiceClient
	NewCollectionServiceClient        = internal.NewCollectionServiceClient
	NewPolicyServiceClient            = internal.NewPolicyServiceClient
	NewOperationServiceClient         = internal.NewPlatformOperationServiceClient
	NewAuditLogServiceClient          = internal.NewAuditLogServiceClient
	NewQuotaServiceClient             = internal.NewQuotaServiceClient
	NewEventSubscriptionServiceClient = internal.NewEventSubscriptionServiceClient
)
