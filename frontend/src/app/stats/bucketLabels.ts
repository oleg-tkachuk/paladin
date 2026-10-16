// Labels for BucketStats (ADR-0032). Every bucket has an owner tenant;
// what tells two buckets apart is whether the owner granted it to others.

/** BucketStats.shared: buckets whose owner granted at least one tenant. */
export const SHARED_BUCKETS_LABEL = "Shared with other tenants";
/** BucketStats.tenant_owned: buckets with no grant, bound by the owner only. */
export const OWNER_ONLY_BUCKETS_LABEL = "Owner only";
