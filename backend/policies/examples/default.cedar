// Default deny-by-default policy template applied to a freshly created tenant.
//
// Mirrors internal/api/v1/tenant/defaultpolicy.go — keep both in sync.
// Substitute "placeholder" with the concrete tenant UUID at load time.
//
// Roles use dot-form (canonical, matching the JWT-issuer mint shape):
//   platform.admin · tenant.admin · bucket.admin · compliance.officer ·
//   secrets.rotator · policy.author

// ─── Data plane ─────────────────────────────────────────────────────────────

// Members of the tenant may read their own objects.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
    resource
);

// Members may write to their own buckets, subject to size and
// content-type constraints below.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    // Hard size cap: 5 GiB per object.
    context.size_bytes <= 5368709120 &&
    // No executables.
    // "resource has key" guard: a PresignPut with a server-generated key has no
    // key at authz time (ObjectKey entity, no `key` attr); reading resource.key
    // would raise an eval error → fail-closed deny. Skip the blocklist for
    // keyless resources; it still applies when a client supplies a key.
    (!(resource has key) ||
     !(resource.key like "*.exe" ||
       resource.key like "*.dll" ||
       resource.key like "*.bat" ||
       resource.key like "*.sh"))
};

// Mutating data-plane ops require a bucket admin.
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
    principal.roles.contains("bucket.admin") ||
    principal.roles.contains("platform.admin")
};

// Storage self-provisioning (data plane). A tenant member — including an
// api_token principal (Kind=ApiKey) — may idempotently ensure its own PALADIN
// bucket + object-keys with its data-plane PAT. The handler pins the resource
// tenant to the caller's own, so `principal in Tenant::"placeholder"` (true
// only on trusted tenant-UUID equality) is the self-scoping guarantee — the
// same membership gate PresignPut relies on to admit this PAT.
permit (
    principal in Tenant::"placeholder",
    action == Action::"EnsureTenantStorage",
    resource
);

// ─── IAM ────────────────────────────────────────────────────────────────────

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

// ─── Tenant-scoped admin resources (Quota / AuditLog / EventSubscription) ──

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
    (principal.roles.contains("tenant.admin") &&
     principal.tenant_id == resource.tenant_id)
};

// ─── Granular bucket sub-actions ────────────────────────────────────────────
// ConfigureLock additionally allows compliance.officer (Object Lock is a
// compliance concern). The rest stay tied to bucket-admin / platform-admin.

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

// ─── Sensitive backend ops ─────────────────────────────────────────────────
// Credential rotation isolated from the broader ManageBackend right.

permit (
    principal,
    action == Action::"RotateBackendCredentials",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("secrets.rotator")
};

// ─── Policy-engine introspection ───────────────────────────────────────────
// Avoid open access — anonymous SimulateAuthz is an information leak.

permit (
    principal,
    action == Action::"InspectPolicy",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.roles.contains("tenant.admin") ||
    principal.roles.contains("policy.author")
};

// ─── Tenant record ──────────────────────────────────────────────────────────
// Any member may read their own tenant's metadata (the UI resolves the
// display name / slug for every signed-in user). Cross-tenant reads are
// additionally platform-admin-gated in the handler; ManageTenant is
// deliberately not granted here (tenant lifecycle is a platform concern).

permit (
    principal,
    action == Action::"ReadTenant",
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.tenant_id == resource.tenant_id
};

// ─── Async operations ──────────────────────────────────────────────────────

permit (
    principal,
    action in [Action::"ReadOperation", Action::"CancelOperation"],
    resource
) when {
    principal.roles.contains("platform.admin") ||
    principal.tenant_id == resource.tenant_id
};

// ─── Belt-and-suspenders: never write outside the tenant's prefix ──────────

forbid (
    principal,
    action,
    resource
) when {
    !(resource in principal.parents)
};
