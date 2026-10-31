/**
 * The one console URL of each entity (ADR-0033), built from the parts of its
 * resource name. Pages link through these rather than spelling paths, so an
 * entity cannot be reached at two addresses that drift apart.
 */

import { PAGE_TABLE, tableParam } from "@/lib/table/urlState";

const enc = encodeURIComponent;

/** A storage backend's page. */
export function backendHref(backendId: string): string {
  return `/storage-backends/${enc(backendId)}`;
}

/** A bucket's page, or one of its tabs (`lifecycle`, `policy`, …). */
export function bucketHref(
  backendId: string,
  bucketId: string,
  tab = "",
): string {
  const base = `${backendHref(backendId)}/buckets/${enc(bucketId)}`;
  return tab ? `${base}/${tab}` : base;
}

/** A tenant's workspace, by slug or id. */
export function tenantHref(tenant: string): string {
  return `/tenants/${enc(tenant)}`;
}

/**
 * A capability on its tenant's capabilities page, its row opened: the page
 * reads the row a link points at from the table's own parameter.
 */
export function capabilityHref(tenant: string, capabilityId: string): string {
  const params = new URLSearchParams({
    [tableParam("row", PAGE_TABLE)]: capabilityId,
  });
  return `${tenantHref(tenant)}/capabilities?${params}`;
}

/** A collection's page in its tenant's workspace. */
export function collectionHref(tenant: string, collection: string): string {
  return `${tenantHref(tenant)}/collections/${enc(collection)}`;
}

/** The parts of a bucket resource name, "storageBackends/{b}/buckets/{id}". */
export function parseBucketName(
  name: string,
): { backendId: string; bucketId: string } | null {
  const m = /^storageBackends\/([^/]+)\/buckets\/([^/]+)$/.exec(name);
  return m ? { backendId: m[1], bucketId: m[2] } : null;
}

/** A collection's trash, its trashed objects to purge or restore. */
export function collectionTrashHref(
  tenant: string,
  collection: string,
): string {
  return `${collectionHref(tenant, collection)}/trash`;
}

/** A tenant's budget, where its spending cap is set. */
export function tenantBudgetHref(tenant: string): string {
  return `${tenantHref(tenant)}/budget`;
}

/** A tenant's billing: what its capabilities spent, against its budget. */
export function tenantBillingHref(tenant: string): string {
  return `${tenantHref(tenant)}/billing`;
}
