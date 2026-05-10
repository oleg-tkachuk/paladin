"use client";

// TenantTabs — sub-navigation rendered by TenantLayout. Each tab is
// a sibling sub-route under /tenants/<id>/, so the layout stays put
// while content swaps. URLs are bookmarkable per-tab.
//
// Tab order pinned by mental model, not alphabetical:
//   1. Overview — what the tenant IS (counts, recent activity).
//   2. Resources operators care about most often: Buckets, Object Keys.
//   3. Authorisation surfaces: Policies (effective Cedar graph),
//      Quotas, Capabilities, M2M Tokens, Event Subscriptions.
//   4. Observability: Audit, Budget / Billing.
//
// New tabs land in the appropriate group without re-flowing existing
// muscle memory.

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

type TabSpec = {
  /** URL slug appended after `/tenants/<id>/`. Empty for Overview. */
  slug: string;
  label: string;
  /** When true, the tab is highlighted only on its own pathname; when
   *  false (default), it matches any descendant route too. Overview
   *  needs `exact: true` so it doesn't claim the highlight on every
   *  /tenants/<id>/<subroute>/. */
  exact?: boolean;
};

const TABS: TabSpec[] = [
  { slug: "", label: "Overview", exact: true },
  { slug: "buckets", label: "Buckets" },
  { slug: "object-keys", label: "Object Keys" },
  { slug: "policies", label: "Policies" },
  { slug: "quotas", label: "Quotas" },
  { slug: "capabilities", label: "Capabilities" },
  { slug: "m2m-tokens", label: "M2M Tokens" },
  { slug: "event-subscriptions", label: "Events" },
  { slug: "audit-log", label: "Audit" },
  { slug: "budget", label: "Budget" },
];

export function TenantTabs({ tenantId }: { tenantId: string }) {
  const pathname = usePathname();
  const base = `/tenants/${tenantId}`;

  return (
    <div
      className="flex w-full items-center gap-0.5 overflow-x-auto border-b border-border/60"
      role="tablist"
    >
      {TABS.map((tab) => {
        const href = tab.slug ? `${base}/${tab.slug}` : base;
        const active = tab.exact
          ? pathname === href
          : pathname === href || pathname.startsWith(`${href}/`);
        return (
          <Link
            key={tab.slug || "overview"}
            href={href}
            role="tab"
            aria-selected={active}
            className={cn(
              "shrink-0 px-3 py-2 text-sm font-medium transition-colors",
              "border-b-2 -mb-px",
              active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground hover:border-border",
            )}
          >
            {tab.label}
          </Link>
        );
      })}
    </div>
  );
}
