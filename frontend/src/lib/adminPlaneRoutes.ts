/**
 * The console's routes that read nothing from the admin plane. Everything
 * else needs the paladin-admin audience, which IAM issues only to
 * ADMIN_AUDIENCE_ROLES — the sidebar, the command palette and the route
 * guard all take their answer from here.
 */
export const ROUTES_WITHOUT_ADMIN_PLANE: readonly string[] = [
  "/",
  "/health",
  "/profile",
];

const ROOT = "/";

/** Whether rendering `pathname` needs an admin-plane token. */
export function routeNeedsAdminPlane(pathname: string): boolean {
  return !ROUTES_WITHOUT_ADMIN_PLANE.some((route) =>
    route === ROOT
      ? pathname === ROOT
      : pathname === route || pathname.startsWith(`${route}/`),
  );
}
