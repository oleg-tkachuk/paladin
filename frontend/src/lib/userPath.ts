/**
 * Where a user's page lives. Its path mirrors the resource name
 * "tenants/<tenant>/users/<user>" as /users/<tenant>/<user>.
 */

export const USERS_INDEX_HREF = "/users";

const USER_NAME = /^tenants\/([^/]+)\/users\/([^/]+)$/;

export function userResourceName(tenant: string, user: string): string {
  return `tenants/${tenant}/users/${user}`;
}

/** The user's page; the index when the name is not a user's. */
export function userHref(name: string): string {
  const match = USER_NAME.exec(name);
  if (!match) return USERS_INDEX_HREF;
  const [, tenant, user] = match;
  return `${USERS_INDEX_HREF}/${encodeURIComponent(tenant)}/${encodeURIComponent(user)}`;
}
