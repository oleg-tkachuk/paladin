"use client";

import { useAuth } from "@/context/AuthContext";
import { useScope } from "@/context/ScopeContext";
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

/** The tenant a page acts on, as the shell names and links it. */
export interface ActingTenant {
  tenantId: string;
  slug: string;
  displayName: string;
}

/**
 * The tenant a page acts on, as a record: the one in the route under
 * /tenants/<id>/, or the session's everywhere else (ADR-0033). The command
 * palette and the g-jumps lead into this tenant; leading into the session's
 * from another tenant's page sent an operator somewhere they were not.
 */
export function useActingTenant(): ActingTenant | null {
  const routed = useTenantOptional();
  const { tenant } = useScope();
  if (routed) return routed;
  return tenant
    ? {
        tenantId: tenant.tenantId,
        slug: tenant.slug,
        displayName: tenant.displayName,
      }
    : null;
}
