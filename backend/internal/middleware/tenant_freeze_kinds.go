package middleware

import (
	admin "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	data "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	iam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// Kind is what an RPC does to the tenants it names, as the tenant freeze sees
// it: a tenant in the trash takes every kind but Changes.
type Kind int

const (
	// Reads changes nothing: reading, listing, downloading, validating.
	Reads Kind = iota + 1
	// Withdraws only takes away: revoking a credential, cancelling work,
	// aborting an upload. Allowed on a trashed tenant, where taking access
	// away is exactly what is wanted.
	Withdraws
	// Lifecycle moves the tenant itself to or out of the trash, or removes
	// it: the decision a trashed tenant waits for.
	Lifecycle
	// Changes alters a tenant's data, configuration or credentials. Refused
	// while the tenant is in the trash, so restoring it returns exactly what
	// was trashed.
	Changes
)

// procedureKinds classifies every data-, admin- and IAM-plane RPC. Explicit, and
// held complete by TestEveryRPCIsClassified: an idempotency level cannot do
// it — DownloadObject and PresignDownload read and are not NO_SIDE_EFFECTS.
var procedureKinds = map[string]Kind{
	// ─── data plane ────────────────────────────────────────────────────────
	data.BatchServiceBatchDeleteObjectsProcedure:  Changes,
	data.BatchServiceBatchCopyObjectsProcedure:    Changes,
	data.BatchServiceBatchRestoreObjectsProcedure: Changes,
	data.BatchServiceBatchUpdateTagsProcedure:     Changes,

	data.MultipartUploadServiceInitiateMultipartUploadProcedure: Changes,
	data.MultipartUploadServicePresignPartProcedure:             Changes,
	data.MultipartUploadServiceCompleteMultipartUploadProcedure: Changes,
	data.MultipartUploadServiceAbortMultipartUploadProcedure:    Withdraws,
	data.MultipartUploadServiceListPartsProcedure:               Reads,

	data.ObjectServiceUploadObjectProcedure:                  Changes,
	data.ObjectServiceDownloadObjectProcedure:                Reads,
	data.ObjectServiceGetObjectProcedure:                     Reads,
	data.ObjectServiceLookupObjectProcedure:                  Reads,
	data.ObjectServiceUpdateObjectProcedure:                  Changes,
	data.ObjectServiceCompleteObjectProcedure:                Changes,
	data.ObjectServiceDeleteObjectProcedure:                  Changes,
	data.ObjectServiceRestoreObjectProcedure:                 Changes,
	data.ObjectServiceCopyObjectProcedure:                    Changes,
	data.ObjectServiceListObjectsProcedure:                   Reads,
	data.ObjectServiceCountObjectsProcedure:                  Reads,
	data.ObjectServiceListObjectVersionsProcedure:            Reads,
	data.ObjectServiceGetObjectVersionProcedure:              Reads,
	data.ObjectServiceRestoreObjectVersionProcedure:          Changes,
	data.ObjectServiceSetObjectRetentionProcedure:            Changes,
	data.ObjectServiceSetObjectLegalHoldProcedure:            Changes,
	data.ObjectServiceGetObjectLockProcedure:                 Reads,
	data.ObjectServiceSetObjectTaintProcedure:                Changes,
	data.ObjectTagServiceGetObjectTagsProcedure:              Reads,
	data.ObjectTagServicePutObjectTagsProcedure:              Changes,
	data.ObjectTagServiceDeleteObjectTagsProcedure:           Changes,
	data.ObjectTagServiceListDistinctTagsProcedure:           Reads,
	data.OperationServiceGetOperationProcedure:               Reads,
	data.OperationServiceListOperationsProcedure:             Reads,
	data.OperationServiceCancelOperationProcedure:            Withdraws,
	data.PresignServiceRegenerateUploadUrlProcedure:          Changes,
	data.PresignServicePresignDownloadProcedure:              Reads,
	data.StorageBootstrapServiceEnsureTenantStorageProcedure: Changes,

	// ─── admin plane ───────────────────────────────────────────────────────
	admin.APITokenServiceCreateProcedure:   Changes,
	admin.APITokenServiceRevokeProcedure:   Withdraws,
	admin.APITokenServiceListProcedure:     Reads,
	admin.APITokenServiceGetSelfProcedure:  Reads,
	admin.APITokenServiceGetUsageProcedure: Reads,

	admin.AuditLogServiceListAuditLogProcedure:     Reads,
	admin.AuditLogServiceGetAuditLogEntryProcedure: Reads,
	admin.AuditLogServiceExportAuditLogProcedure:   Reads,

	admin.BackendServiceCreateBackendProcedure:         Changes,
	admin.BackendServiceGetBackendProcedure:            Reads,
	admin.BackendServiceUpdateBackendProcedure:         Changes,
	admin.BackendServiceDeleteBackendProcedure:         Changes,
	admin.BackendServiceListBackendsProcedure:          Reads,
	admin.BackendServiceRotateCredentialsProcedure:     Changes,
	admin.BackendServiceTestBackendProcedure:           Reads,
	admin.BackendServiceSetBackendEnabledProcedure:     Changes,
	admin.BackendServiceSetBackendReadOnlyProcedure:    Changes,
	admin.BackendServiceSetBackendMaintenanceProcedure: Changes,

	admin.BillingServiceGetTenantSummaryProcedure:    Reads,
	admin.BillingServiceGetTenantTimeSeriesProcedure: Reads,

	admin.BucketServiceCreateBucketProcedure:          Changes,
	admin.BucketServiceGetBucketProcedure:             Reads,
	admin.BucketServiceUpdateBucketProcedure:          Changes,
	admin.BucketServiceDeleteBucketProcedure:          Changes,
	admin.BucketServiceListBucketsProcedure:           Reads,
	admin.BucketServiceSetBucketPolicyProcedure:       Changes,
	admin.BucketServiceSetLifecycleRulesProcedure:     Changes,
	admin.BucketServiceSetObjectLockProcedure:         Changes,
	admin.BucketServiceSetVersioningProcedure:         Changes,
	admin.BucketServiceSetReplicationProcedure:        Changes,
	admin.BucketServiceListAccessibleBucketsProcedure: Reads,

	admin.CapabilityServiceIssueProcedure:           Changes,
	admin.CapabilityServiceDelegateProcedure:        Changes,
	admin.CapabilityServiceRevokeProcedure:          Withdraws,
	admin.CapabilityServiceRevokeBiscuitProcedure:   Withdraws,
	admin.CapabilityServiceGetBiscuitUsageProcedure: Reads,
	admin.CapabilityServiceListProcedure:            Reads,
	admin.CapabilityServiceGetUsageProcedure:        Reads,

	admin.CELServiceValidateProcedure: Reads,

	admin.CollectionServiceCreateCollectionProcedure:       Changes,
	admin.CollectionServiceGetCollectionProcedure:          Reads,
	admin.CollectionServiceUpdateCollectionProcedure:       Changes,
	admin.CollectionServiceDeleteCollectionProcedure:       Changes,
	admin.CollectionServiceListCollectionsProcedure:        Reads,
	admin.CollectionServiceSetCollectionPolicyProcedure:    Changes,
	admin.CollectionServiceBindCollectionToBucketProcedure: Changes,

	admin.EventSubscriptionServiceCreateSubscriptionProcedure: Changes,
	admin.EventSubscriptionServiceGetSubscriptionProcedure:    Reads,
	admin.EventSubscriptionServiceUpdateSubscriptionProcedure: Changes,
	admin.EventSubscriptionServiceDeleteSubscriptionProcedure: Changes,
	admin.EventSubscriptionServiceListSubscriptionsProcedure:  Reads,
	// Both send events to a subscriber on the tenant's behalf.
	admin.EventSubscriptionServiceTestSubscriptionProcedure:        Changes,
	admin.EventSubscriptionServiceRedriveFailedDeliveriesProcedure: Changes,

	admin.MCPInspectServiceInspectProcedure:         Reads,
	admin.MCPInspectServiceListSessionsProcedure:    Reads,
	admin.MCPInspectServiceGetBridgeStatusProcedure: Reads,

	admin.PlatformOperationServiceGetOperationProcedure:    Reads,
	admin.PlatformOperationServiceListOperationsProcedure:  Reads,
	admin.PlatformOperationServiceCancelOperationProcedure: Withdraws,

	admin.PolicyServiceValidateProcedure:           Reads,
	admin.PolicyServiceSimulateAuthzProcedure:      Reads,
	admin.PolicyServiceGetEffectivePolicyProcedure: Reads,

	admin.QuotaServiceGetQuotaProcedure:   Reads,
	admin.QuotaServiceSetQuotaProcedure:   Changes,
	admin.QuotaServiceResetUsageProcedure: Changes,

	admin.SystemServiceGetConfigProcedure:                Reads,
	admin.SystemServiceGetDispatcherStatsProcedure:       Reads,
	admin.SystemServiceGetPlatformStatsProcedure:         Reads,
	admin.SystemServiceListPlatformStatsTenantsProcedure: Reads,

	admin.TenantBudgetServiceGetProcedure:       Reads,
	admin.TenantBudgetServiceSetProcedure:       Changes,
	admin.TenantBudgetServiceSummarizeProcedure: Reads,

	admin.TenantServiceCreateTenantProcedure:               Changes,
	admin.TenantServiceGetTenantProcedure:                  Lifecycle,
	admin.TenantServiceUpdateTenantProcedure:               Changes,
	admin.TenantServiceDeleteTenantProcedure:               Lifecycle,
	admin.TenantServiceListTenantsProcedure:                Lifecycle,
	admin.TenantServiceSetInheritedPolicyProcedure:         Changes,
	admin.TenantServiceRestoreTenantProcedure:              Lifecycle,
	admin.TenantServicePurgeTenantProcedure:                Lifecycle,
	admin.TenantServiceRenameTenantSlugProcedure:           Changes,
	admin.TenantServiceMigrateTenantStorageLayoutProcedure: Changes,
	admin.TenantServiceGetTenantStorageMigrationProcedure:  Reads,
	admin.TenantServiceResolveRenamedSlugProcedure:         Reads,
	admin.TenantServiceGetTenantDefaultBindingProcedure:    Reads,
	admin.TenantServiceSetTenantDefaultBindingProcedure:    Changes,
	admin.TenantServiceClearTenantDefaultBindingProcedure:  Changes,

	// ─── IAM plane ─────────────────────────────────────────────────────────
	// Login, RefreshToken and ExchangeAudience carry no principal and so
	// pass; the gate refuses whatever they issue for a trashed tenant.
	iam.AuthServiceLoginProcedure:             Changes,
	iam.AuthServiceRefreshTokenProcedure:      Changes,
	iam.AuthServiceRevokeProcedure:            Withdraws,
	iam.AuthServiceWhoAmIProcedure:            Reads,
	iam.AuthServiceChangePasswordProcedure:    Changes,
	iam.AuthServiceExchangeAudienceProcedure:  Changes,
	iam.AuthServiceListMyMembershipsProcedure: Reads,
	iam.AuthServiceSwitchTenantProcedure:      Changes,

	iam.HealthServiceGetVersionProcedure: Reads,
	iam.HealthServiceGetHealthProcedure:  Reads,

	iam.UserServiceCreateUserProcedure:    Changes,
	iam.UserServiceGetUserProcedure:       Reads,
	iam.UserServiceUpdateUserProcedure:    Changes,
	iam.UserServiceDeleteUserProcedure:    Changes,
	iam.UserServiceListUsersProcedure:     Reads,
	iam.UserServiceGrantScopesProcedure:   Changes,
	iam.UserServiceRevokeScopesProcedure:  Withdraws,
	iam.UserServiceResetPasswordProcedure: Changes,

	iam.UserSettingsServiceGetMineProcedure:       Reads,
	iam.UserSettingsServiceUpdateMineProcedure:    Changes,
	iam.UserSettingsServiceGetForUserProcedure:    Reads,
	iam.UserSettingsServiceListByTenantProcedure:  Reads,
	iam.UserSettingsServiceDeleteForUserProcedure: Changes,
}
