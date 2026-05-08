import React from "react";
import Link from "next/link";
import { ArrowUpTrayIcon, CubeIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { useTenant } from "@/context/TenantContext";

import { Breadcrumbs } from "./Breadcrumbs";

interface PageHeaderProps {
  title: React.ReactNode;
  description?: string;
  tenantId?: string;
  actions?: React.ReactNode;
  showDefaultActions?: boolean;
  showBreadcrumbs?: boolean;
}

/**
 * Standard page header. Hierarchy:
 *
 *   Breadcrumbs
 *   ── ── ── ── ── ── ── ── ── ── ── ── ── ── ── ──
 *   Title              [actions...] [defaultActions]
 *   Description / tenant pill
 */
export const PageHeader: React.FC<PageHeaderProps> = ({
  title,
  description,
  tenantId: propTenantId,
  actions,
  showDefaultActions = true,
  showBreadcrumbs = true,
}) => {
  const { tenantId: ctxTenantId, tenant } = useTenant();
  const displayTenant = propTenantId || ctxTenantId || "default";
  const shortId =
    displayTenant.length > 12 ? `${displayTenant.slice(0, 8)}…` : displayTenant;

  return (
    <div className="space-y-4">
      {showBreadcrumbs && <Breadcrumbs />}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1 space-y-2">
          {typeof title === "string" ? (
            <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
              {title}
            </h1>
          ) : (
            title
          )}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
            <Badge variant="outline" className="font-mono text-xs">
              {tenant?.displayName || shortId}
            </Badge>
            {description && <span>{description}</span>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2 shrink-0">
          {actions}
          {showDefaultActions && (
            <>
              {actions && (
                <Separator
                  orientation="vertical"
                  className="hidden h-6 sm:block"
                />
              )}
              <Button variant="outline" size="sm" asChild>
                <Link href="/upload">
                  <ArrowUpTrayIcon className="size-4" />
                  Upload
                </Link>
              </Button>
              <Button size="sm" asChild>
                <Link href="/objects">
                  <CubeIcon className="size-4" />
                  Explore
                </Link>
              </Button>
            </>
          )}
        </div>
      </div>
      <Separator />
    </div>
  );
};
