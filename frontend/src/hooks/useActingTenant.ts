"use client";

import { useAuth } from "@/context/AuthContext";
import { useTenantOptional } from "@/app/tenants/[id]/tenant-context";

/**
 * The tenant a page acts on: the one in the route under /tenants/<id>/, or
 * the signed-in user's own everywhere else.
 *
 * Hooks that name tenant-scoped resources read it here. Reading the user's
 * tenant instead sent a platform admin's calls on another tenant's pages to
 * the admin's own tenant: lists came back empty, and a write would have
 * landed in a same-named collection of the wrong tenant.
 */
export function useActingTenantId(): string {
  const { user } = useAuth();
  const routed = useTenantOptional();
  return routed?.tenantId || user?.tenantId || "";
}
