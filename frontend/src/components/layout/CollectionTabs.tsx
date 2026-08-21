"use client";

// CollectionTabs — second-level sub-nav rendered inside the
// Collection detail layout (`/tenants/<id>/collections/<collection>/...`).
//
// Tab order reflects how operators reason about a Collection:
//   1. Overview   — identity, completion mode, bucket binding.
//   2. Objects    — live object list (move from /objects).
//   3. Trash      — soft-deleted objects awaiting purge.
//   4. Policy     — per-Collection Cedar overlay.
//
// Object detail (`objects/<id>`) hangs off the Objects tab and
// keeps Objects highlighted while it's open — the tab definition
// for `objects` is `exact: false` so the strip behaves correctly.

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

type TabSpec = {
  slug: string;
  label: string;
  exact?: boolean;
};

const TABS: TabSpec[] = [
  { slug: "", label: "Overview", exact: true },
  { slug: "objects", label: "Objects" },
  { slug: "trash", label: "Trash" },
  { slug: "policy", label: "Policy" },
];

export function CollectionTabs({
  tenantId,
  collection,
}: {
  tenantId: string;
  collection: string;
}) {
  const pathname = usePathname();
  const base = `/tenants/${tenantId}/collections/${encodeURIComponent(
    collection,
  )}`;

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
