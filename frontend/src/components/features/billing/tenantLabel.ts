// How billing names a tenant: its display name, else its slug, marked when
// it is in the trash — its charges stay on the ledger, so it is still listed.

/** What a tenant is called, and whether it is in the trash. */
export interface TenantNaming {
  displayName: string;
  slug: string;
  deleted: boolean;
}

/** The suffix that marks a tenant in the trash. */
export const IN_TRASH_SUFFIX = " · in trash";

/** The label billing shows for a tenant. */
export function tenantLabel(t: TenantNaming): string {
  return `${t.displayName || t.slug}${t.deleted ? IN_TRASH_SUFFIX : ""}`;
}
