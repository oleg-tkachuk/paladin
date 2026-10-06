// Curated Cedar policy starters surfaced via the "Load template" dropdown
// above the policy editor. Loading one replaces the editor buffer (with a
// confirm dialog when there is unsaved content); nothing is saved until the
// operator does.
//
// Every template must validate against backend/policies/schema.cedarschema —
// templates.test.ts runs Cedar's own validator over each one. Values in braces
// ("{bucket}") are for the operator to fill in.

export interface PolicyTemplate {
  id: string;
  label: string;
  description: string;
  cedar: string;
}

export const POLICY_TEMPLATES: PolicyTemplate[] = [
  {
    id: "read-only-auditor",
    label: "Read-only auditor",
    description:
      "Lets tenant.auditor read objects, the tenant record, quotas and the audit log in its own tenant, and forbids it every write.",
    cedar: `permit (
  principal,
  action in [
    Action::"GetObject",
    Action::"PresignGet",
    Action::"HeadObject",
    Action::"ReadObjectLock",
    Action::"ReadTenant",
    Action::"ReadQuota",
    Action::"ReadAuditLog"
  ],
  resource
) when {
  principal.roles.contains("tenant.auditor") &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};

forbid (
  principal,
  action in [
    Action::"PutObject",
    Action::"PresignPut",
    Action::"DeleteObject",
    Action::"UpdateObject",
    Action::"CopyObject",
    Action::"SetObjectTaint",
    Action::"ManageQuota"
  ],
  resource
) when {
  principal.roles.contains("tenant.auditor")
};`,
  },
  {
    id: "tenant-uploader",
    label: "Tenant uploader (bucket-scoped)",
    description:
      "Lets tenant.uploader upload objects (single-part, presigned or multipart) into one bucket, in its own tenant only. Replace {bucket} with the bucket name.",
    cedar: `permit (
  principal,
  action in [Action::"PutObject", Action::"PresignPut"],
  resource
) when {
  principal.roles.contains("tenant.uploader") &&
  resource has bucket_name &&
  resource.bucket_name == "{bucket}" &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};`,
  },
  {
    id: "agent-with-budget",
    label: "Agent with capability budget",
    description:
      "Lets a capability-authenticated agent read and presign downloads in one collection of its own tenant. The capability's caveats still confine it further and enforce its budget. Replace {collection} with the collection name.",
    cedar: `permit (
  principal,
  action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
  resource
) when {
  principal.kind == "capability" &&
  resource has collection &&
  resource.collection == "{collection}" &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};`,
  },
  {
    id: "forbid-destructive-non-admin",
    label: "Forbid destructive ops to non-admin principals",
    description:
      "Vetoes object deletion and bucket management (which includes deleting a bucket) for any principal without platform.admin. A forbid beats every permit, so this is a hard ceiling — pair it with permits for the rest of the surface.",
    cedar: `// Forbid destructive ops unless the caller is platform.admin.
// A forbid beats every permit, so this is a hard ceiling — pair it
// with permits for the rest of the action surface.
forbid (
  principal,
  action in [Action::"DeleteObject", Action::"ManageBucket"],
  resource
) unless {
  principal.roles.contains("platform.admin")
};`,
  },
];
