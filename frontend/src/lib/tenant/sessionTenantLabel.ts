/** How many characters of a tenant id stand for it when nothing names it. */
const SHORT_ID_LENGTH = 8;

/** A tenant id cut to its leading characters, marked as cut. */
export function shortTenantId(id: string): string {
  return `${id.slice(0, SHORT_ID_LENGTH)}…`;
}

/**
 * The name the console shows for the session tenant: its display name, the
 * token's slug when the tenant record is not readable — a principal without
 * the admin audience never loads it — or its id, cut, when nothing names it.
 * null while no tenant is known.
 */
export function sessionTenantLabel(t: {
  displayName?: string | null;
  slug?: string | null;
  id?: string | null;
}): string | null {
  return (
    t.displayName?.trim() ||
    t.slug?.trim() ||
    (t.id ? shortTenantId(t.id) : null)
  );
}
