/**
 * An audited resource's name as a row shows it: without the tenant it sits
 * in, which the actor and the page already say and which takes most of the
 * width. The full name stays in the tooltip and the entry's detail.
 */

// tenants/<id>/<the rest>
const TENANT_PREFIX = /^tenants\/[^/]+\/(.+)$/;

/** The name after its tenants/<id>/ prefix; a name without one, as it is. */
export function resourceInTenant(name: string): string {
  return TENANT_PREFIX.exec(name)?.[1] ?? name;
}
