// Tenant.storage_layout values (ADR-0015).

/** The tenant binds its collections into shared buckets; it owns none. */
export const STORAGE_LAYOUT_SHARED = "shared";
/** The tenant owns its bucket (owner_tenant_id = the tenant). */
export const STORAGE_LAYOUT_DEDICATED = "dedicated";

/** Whether the tenant owns the buckets it stores in. An empty layout is shared. */
export function isDedicatedLayout(storageLayout: string): boolean {
  return storageLayout === STORAGE_LAYOUT_DEDICATED;
}
