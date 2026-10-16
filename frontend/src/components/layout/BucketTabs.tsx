"use client";

// BucketTabs — the tabs of a bucket's one page,
// /storage-backends/<backend>/buckets/<bucket>/… (ADR-0033):
//   Overview    — identity, owner, who it is shared with.
//   Collections — what each tenant stores in it.
//   the settings — lifecycle, policy, versioning, object lock, replication;
//                  each reaches every tenant the bucket is shared with.

import Link from "next/link";
import { usePathname } from "next/navigation";

import { bucketHref } from "@/lib/navigation/hrefs";
import { cn } from "@/lib/utils";

type TabSpec = {
  slug: string;
  label: string;
  exact?: boolean;
};

/** The tabs that change the bucket for every tenant in it. */
export const BUCKET_SETTINGS_TABS = [
  "lifecycle",
  "policy",
  "versioning",
  "object-lock",
  "replication",
] as const;

const TABS: TabSpec[] = [
  { slug: "", label: "Overview", exact: true },
  { slug: "collections", label: "Collections" },
  { slug: "lifecycle", label: "Lifecycle" },
  { slug: "policy", label: "Policy" },
  { slug: "versioning", label: "Versioning" },
  { slug: "object-lock", label: "Object lock" },
  { slug: "replication", label: "Replication" },
];

/** Whether pathname is one of a bucket's settings tabs. */
export function isBucketSettingsTab(pathname: string): boolean {
  const last = pathname.split("/").filter(Boolean).pop() ?? "";
  return (BUCKET_SETTINGS_TABS as readonly string[]).includes(last);
}

export function BucketTabs({
  backendId,
  bucketId,
}: {
  backendId: string;
  bucketId: string;
}) {
  const pathname = usePathname();
  const base = bucketHref(backendId, bucketId);

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
