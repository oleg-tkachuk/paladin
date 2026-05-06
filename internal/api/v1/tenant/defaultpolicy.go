package tenant

import (
	"strings"

	"github.com/google/uuid"
)

// defaultPolicyTemplate is applied to a freshly created tenant when the
// caller does not supply inherited_cedar_policy. The literal "placeholder"
// is substituted with the tenant's UUID at creation time.
//
// Mirrors policies/examples/default.cedar — update both together.
const defaultPolicyTemplate = `// Default deny-by-default policy applied on tenant creation.
// Tenant members may read/write their own objects; admins may manage them.

permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
    resource
);

permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    context.size_bytes <= 5368709120 &&
    !(resource.key like "*.exe" ||
      resource.key like "*.dll" ||
      resource.key like "*.bat" ||
      resource.key like "*.sh")
};

permit (
    principal in Tenant::"placeholder",
    action in [
        Action::"DeleteObject",
        Action::"RestoreObject",
        Action::"UpdateObject",
        Action::"CopyObject"
    ],
    resource
) when {
    principal.roles.contains("objectKey:admin") ||
    principal.roles.contains("platform-admin")
};

permit (
    principal in Tenant::"placeholder",
    action == Action::"AdminBucket",
    resource
) when {
    principal.roles.contains("platform-admin")
};

// IAM: platform admins manage everything. Tenant admins manage users and
// api-keys WITHIN their own tenant — comparison via resource.tenant_id
// (User and ApiKey resource entities both expose it as a string).
permit (
    principal,
    action in [
        Action::"ManageUser",
        Action::"ReadUser",
        Action::"ResetPassword",
        Action::"GrantScopes",
        Action::"ManageApiKey",
        Action::"ReadApiKey",
        Action::"RotateApiKey",
        Action::"MintScopedToken"
    ],
    resource
) when {
    principal.roles.contains("platform-admin") ||
    (principal.roles.contains("tenant-admin") &&
     principal.tenant_id == resource.tenant_id)
};

// Tenant-scoped admin resources (Quota, AuditLog, EventSubscription).
// Same shape as IAM: platform-admin sees all; tenant-admin sees own.
// Compliance roles (audit-readers) are configured by extending the
// permits below in tenant-managed policy.
permit (
    principal,
    action in [
        Action::"ManageQuota",
        Action::"ReadQuota",
        Action::"ResetQuotaUsage",
        Action::"ReadAuditLog",
        Action::"ExportAuditLog",
        Action::"ManageSubscription",
        Action::"ReadSubscription",
        Action::"TestSubscription"
    ],
    resource
) when {
    principal.roles.contains("platform-admin") ||
    (principal.roles.contains("tenant-admin") &&
     principal.tenant_id == resource.tenant_id)
};

// Granular bucket sub-actions. ConfigureLock is callable by a dedicated
// compliance role; the rest stay tied to bucket-admin / platform-admin so
// existing operators keep their old reach until they elect to specialise.
permit (
    principal,
    action in [
        Action::"ConfigureBucketPolicy",
        Action::"ConfigureLifecycle",
        Action::"ConfigureVersioning",
        Action::"ConfigureReplication"
    ],
    resource
) when {
    principal.roles.contains("platform-admin") ||
    principal.roles.contains("bucket-admin")
};
permit (
    principal,
    action == Action::"ConfigureLock",
    resource
) when {
    principal.roles.contains("platform-admin") ||
    principal.roles.contains("bucket-admin") ||
    principal.roles.contains("compliance-officer")
};

// Sensitive backend ops — credential rotation isolated from the broader
// ManageBackend right so a "secrets-rotator" service-account can run
// rotations without grant on backend CRUD.
permit (
    principal,
    action == Action::"RotateBackendCredentials",
    resource
) when {
    principal.roles.contains("platform-admin") ||
    principal.roles.contains("secrets-rotator")
};

// Policy-engine introspection. Avoid open access — admin UIs use this
// for access-preflight, but anonymous SimulateAuthz is an information leak.
permit (
    principal,
    action == Action::"InspectPolicy",
    resource
) when {
    principal.roles.contains("platform-admin") ||
    principal.roles.contains("tenant-admin") ||
    principal.roles.contains("policy-author")
};

// Async operations — any tenant member sees / cancels operations they
// (or someone in their tenant) spawned. Cross-tenant Read/Cancel for
// platform-admin runs through the same permit, gated by tenant_id.
permit (
    principal,
    action in [Action::"ReadOperation", Action::"CancelOperation"],
    resource
) when {
    principal.roles.contains("platform-admin") ||
    principal.tenant_id == resource.tenant_id
};
`

// renderDefaultPolicy returns the default Cedar policy with the placeholder
// Tenant UID substituted for the concrete tenant UUID.
func renderDefaultPolicy(tenantID uuid.UUID) string {
	return strings.ReplaceAll(defaultPolicyTemplate, "placeholder", tenantID.String())
}
