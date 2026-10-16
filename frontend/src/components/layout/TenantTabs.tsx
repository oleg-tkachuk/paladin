"use client";

// TenantTabs — a tenant workspace's navigation (ADR-0033). The pages are
// grouped by what an operator comes for; a group with several pages shows
// them as a second row. Each page keeps its own URL under /tenants/<id>/, so
// bookmarks survive the grouping.

import Link from "next/link";
import { usePathname } from "next/navigation";

import { scopeFor } from "@/lib/navigation/sections";
import {
  TENANT_TAB_GROUPS,
  tenantPageHref,
  tenantTabState,
} from "@/lib/navigation/tenantTabs";
import { cn } from "@/lib/utils";

const TAB_CLASS = "shrink-0 px-3 py-2 text-sm font-medium transition-colors";

export function TenantTabs({ tenantId }: { tenantId: string }) {
  const pathname = usePathname();
  const base = `/tenants/${tenantId}`;
  const scope = scopeFor(pathname);
  const { group, page } = tenantTabState(
    scope.kind === "tenant" ? scope.tab : "",
  );

  return (
    <div className="space-y-1">
      <div
        className="flex w-full items-center gap-0.5 overflow-x-auto border-b border-border/60"
        role="tablist"
        aria-label="Workspace"
      >
        {TENANT_TAB_GROUPS.map((g) => {
          const active = g === group;
          return (
            <Link
              key={g.label}
              href={tenantPageHref(base, g.pages[0])}
              role="tab"
              aria-selected={active}
              className={cn(
                TAB_CLASS,
                "border-b-2 -mb-px",
                active
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground hover:border-border",
              )}
            >
              {g.label}
            </Link>
          );
        })}
      </div>
      {group.pages.length > 1 && (
        <div
          className="flex w-full items-center gap-1 overflow-x-auto"
          role="tablist"
          aria-label={group.label}
        >
          {group.pages.map((p) => {
            const active = p === page;
            return (
              <Link
                key={p.slug}
                href={tenantPageHref(base, p)}
                role="tab"
                aria-selected={active}
                className={cn(
                  "shrink-0 rounded-md px-2.5 py-1 text-sm transition-colors",
                  active
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                )}
              >
                {p.label}
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}
