"use client";

import { usePathname } from "next/navigation";
import {
  BuildingOffice2Icon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { useTenantOptional } from "@/app/tenants/[id]/tenant-context";
import { scopeFor } from "@/lib/navigation/sections";

/** What the badge says on a platform page. */
export const PLATFORM_SCOPE_LABEL = "Platform";
/** What the badge says before a tenant's name on its workspace pages. */
export const TENANT_SCOPE_PREFIX = "Tenant";

/**
 * The space the page is in (ADR-0033): the platform's, or one tenant's
 * workspace — named, so an operator acting on a tenant other than the one
 * the session is signed in to sees which.
 */
export function ScopeBadge() {
  const scope = scopeFor(usePathname());
  const tenant = useTenantOptional();

  if (scope.kind === "platform") {
    return (
      <Badge variant="outline" className="gap-1" data-scope="platform">
        <ServerStackIcon className="size-3.5" aria-hidden="true" />
        {PLATFORM_SCOPE_LABEL}
      </Badge>
    );
  }
  const name = tenant?.displayName || tenant?.slug || scope.tenant;
  return (
    <Badge
      variant="accent"
      className="max-w-60 gap-1"
      data-scope="tenant"
      title={tenant?.tenantId}
    >
      <BuildingOffice2Icon className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="shrink-0">{TENANT_SCOPE_PREFIX} ·</span>
      <span className="truncate">{name}</span>
    </Badge>
  );
}
