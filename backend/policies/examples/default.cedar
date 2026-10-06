// Default policy a tenant gets at creation when the caller supplies none
// (tenanth.renderDefaultPolicy). Anything it does not permit is denied.
// The Tenant UID below is the tenant's slug (e.g. "acme") — a human-readable
// kebab-case handle. The tenant's UUID is also accessible via
// principal.tenant_id for policies that prefer the UUID form.
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
    // Guard resource.key: a PresignPut whose object key is server-generated has
    // no key at authz time, making the resource a Collection entity with no key
    // attribute. Reading resource.key then raises an evaluation error and Cedar
    // fails closed (denies a legitimate upload). "resource has key" skips the
    // filename blocklist for keyless resources; the block still applies when a
    // client supplies a key.
    (!(resource has key) ||
     !(resource.key like "*.exe" ||
       resource.key like "*.dll" ||
       resource.key like "*.bat" ||
       resource.key like "*.sh"))
};

permit (
    principal in Tenant::"placeholder",
    action in [
        Action::"DeleteObject",
        Action::"RestoreObject",
        Action::"UpdateObject",
        Action::"CopyObject",
        Action::"SetObjectTaint"
    ],
    resource
) when {
    principal.roles.contains("collection:admin") ||
    principal.roles.contains("platform.admin")
};

// (EnsureTenantStorage self-service is a BUILT-IN permit — see
// cedar.builtinPolicy — so it applies to every tenant uniformly, including
// existing ones whose stored policy predates it, without a per-tenant seed.)

// User settings — cross-user access (an admin viewing a teammate's timezone)
// is platform-admin or tenant-admin only. A user's OWN settings are a
// built-in permit (cedar.builtinPolicy), keyed on subject and tenant.
permit (
    principal,
    action in [Action::"ReadUserSettings", Action::"ManageUserSettings"],
    resource
) when {
    principal.roles.contains("platform.admin") ||
    (principal.roles.contains("tenant.admin") &&
     principal.tenant_id == resource.tenant_id)
};

// IAM: platform admins manage everything. Tenant admins manage users
// WITHIN their own tenant — comparison via resource.tenant_id (the User
// resource entity exposes it as a string).
permit (
    principal,
    action in [
        Action::"ManageUser",
        Action::"ReadUser",
        Action::"ResetPassword",
        Action::"GrantScopes"
    ],
    resource
) when {
    principal.roles.contains("platform.admin") ||
    (principal.roles.contains("tenant.admin") &&
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
    principal.roles.contains("platform.admin") ||
    // A bucket quota's resource is the Bucket, which has no tenant_id.
    (principal.roles.contains("tenant.admin") &&
     resource has tenant_id &&
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
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("bucket.admin")
};
permit (
    principal,
    action == Action::"ConfigureLock",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("bucket.admin") ||
    principal.roles.contains("compliance.officer")
};

// Sensitive backend ops — credential rotation isolated from the broader
// ManageBackend right so a "secrets.rotator" service-account can run
// rotations without grant on backend CRUD.
permit (
    principal,
    action == Action::"RotateBackendCredentials",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("secrets.rotator")
};

// Policy-engine introspection. Avoid open access — admin UIs use this
// for access-preflight, but anonymous SimulateAuthz is an information leak.
permit (
    principal,
    action == Action::"InspectPolicy",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("tenant.admin") ||
    principal.roles.contains("policy.author")
};

// The tenant record itself — any member may read their own tenant's
// metadata (the UI resolves display name / slug for every signed-in
// user, so this is a member-level need, not an admin one). Cross-tenant
// reads are additionally platform-admin-gated in the handler before
// Cedar runs. Tenant lifecycle (ManageTenant) is deliberately NOT
// granted here — creating/renaming/deleting tenants stays a platform
// concern.
permit (
    principal,
    action == Action::"ReadTenant",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.tenant_id == resource.tenant_id
};

// Async operations — any tenant member sees / cancels operations they
// (or someone in their tenant) spawned. Cross-tenant Read/Cancel for
// platform-admin runs through the same permit, gated by tenant_id.
permit (
    principal,
    action in [Action::"ReadOperation", Action::"CancelOperation"],
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.tenant_id == resource.tenant_id
};
