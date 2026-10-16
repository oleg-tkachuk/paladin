/**
 * The one console URL of each entity (ADR-0033), built from the parts of its
 * resource name. Pages link through these rather than spelling paths, so an
 * entity cannot be reached at two addresses that drift apart.
 */

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
