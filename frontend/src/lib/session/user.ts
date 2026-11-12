import type { WhoAmIResponse } from "@/gen/paladin/iam/v1/auth_service_pb";

/** The signed-in user, as the console keeps it. */
export type AuthUser = {
  userId: string;
  tenantId: string;
  // The tenant's slug, from WhoAmIResponse.tenant_slug. Empty for a session
  // minted before the slug claim; callers fall back to tenantId in URLs.
  tenantSlug: string;
  subject: string;
  displayName: string;
  roles: string[];
  resourceVersion: string;
};

/** The user WhoAmI names; an answer without one is a session with no user. */
export function userFromWhoAmI(res: WhoAmIResponse): AuthUser {
  const u = res.user;
  if (!u) {
    throw new Error("WhoAmI answered without a user");
  }
  return {
    userId: u.userId,
    tenantId: u.tenantId,
    tenantSlug: res.tenantSlug,
    subject: u.subject,
    displayName: u.displayName,
    roles: [...u.roles],
    resourceVersion: u.resourceVersion,
  };
}
