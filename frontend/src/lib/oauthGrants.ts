import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";
import { tableParam, type TableUrl } from "@/lib/table/urlState";
import { userHref, userResourceName } from "@/lib/userPath";

/**
 * The table of a user's OAuth grants on their page, namespaced so it sits
 * beside the page's other tables; a link names a grant by its row.
 */
export const OAUTH_GRANTS_TABLE: TableUrl = { namespace: "apps" };

/** A grant on its user's page, opened in the grants table's panel. */
export function grantHref(tenant: string, user: string, grant: string): string {
  const params = new URLSearchParams({
    [tableParam("row", OAUTH_GRANTS_TABLE)]: grant,
  });
  return `${userHref(userResourceName(tenant, user))}?${params}`;
}

/** The OAuth grant a capability was exchanged under. */
export interface CapabilityGrant {
  /** The grant's id: its session, the refresh-token family. */
  id: string;
  /** The user who gave the grant, by id, and their tenant. */
  userId: string;
  tenantId: string;
  /** The client it was granted to; empty when unknown. */
  clientId: string;
  href: string;
}

/**
 * The grant a capability belongs to: one exchanged under a session, for a
 * user. Null for one issued outside an OAuth grant.
 */
export function capabilityGrant(cap: Capability): CapabilityGrant | null {
  const user = cap.onBehalfOf;
  if (!cap.session || user?.kind !== PrincipalKind.USER || !user.subject) {
    return null;
  }
  const tenantId = user.tenantId || cap.subject?.tenantId || "";
  if (!tenantId) return null;
  return {
    id: cap.session,
    userId: user.subject,
    tenantId,
    clientId: cap.clientId,
    href: grantHref(tenantId, user.subject, cap.session),
  };
}
