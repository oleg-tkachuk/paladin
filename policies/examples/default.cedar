// Default deny-by-default policy template applied to a freshly created tenant.
//
// Admins edit via BucketService.UpdateBucket(policy.cedar_policy).
// Compiled and cached at load time by internal/policy/cedar.

// Members of the tenant may read their own objects.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
    resource
);

// Members of the tenant may write to their own buckets, subject to size and
// content-type constraints below.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    // Hard size cap: 5 GiB per object.
    context.size_bytes <= 5368709120 &&
    // No executables.
    !(resource.key like "*.exe" ||
      resource.key like "*.dll" ||
      resource.key like "*.bat" ||
      resource.key like "*.sh")
};

// Admins (role "bucket:admin") may also delete, update, restore, copy.
permit (
    principal,
    action in [
        Action::"DeleteObject",
        Action::"RestoreObject",
        Action::"UpdateObject",
        Action::"CopyObject"
    ],
    resource
) when {
    principal.roles.contains("bucket:admin")
};

// Explicit forbid always wins: nothing may write outside the tenant's prefix.
// (Belt-and-suspenders — the handler should also verify tenant-ownership
// structurally. But defense in depth.)
forbid (
    principal,
    action,
    resource
) when {
    !(resource in principal.parents)
};
