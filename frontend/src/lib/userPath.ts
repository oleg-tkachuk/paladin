/**
 * Where a user's page lives. Its path mirrors the resource name
 * "tenants/<tenant>/users/<user>" as /users/<tenant>/<user>.
 */

export const USERS_INDEX_HREF = "/users";

const USER_NAME = /^tenants\/([^/]+)\/users\/([^/]+)$/;

export function userResourceName(tenant: string, user: string): string {
  return `tenants/${tenant}/users/${user}`;
}

/** The tenant of a user's resource name; null when it is not a user's. */
export function userTenant(name: string): string | null {
  return USER_NAME.exec(name)?.[1] ?? null;
}

/** The user's page; the index when the name is not a user's. */
export function userHref(name: string): string {
  const match = USER_NAME.exec(name);
  if (!match) return USERS_INDEX_HREF;
  const [, tenant, user] = match;
  return `${USERS_INDEX_HREF}/${encodeURIComponent(tenant)}/${encodeURIComponent(user)}`;
}
