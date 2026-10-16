"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { PageHeader } from "@/components/layout/PageHeader";
import {
  StorageKind,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { backendHref } from "@/lib/navigation/hrefs";
import { cn } from "@/lib/utils";

const KIND_LABELS: Record<number, string> = {
  [StorageKind.AWS_S3]: "AWS S3",
  [StorageKind.S3_COMPATIBLE]: "S3-compatible",
  [StorageKind.GCS]: "GCS",
  [StorageKind.UNSPECIFIED]: "—",
};

/** The backend's S3 feature probe: a tab of its own, read when needed. */
export const FEATURES_TAB = "features";

const TABS = [
  { slug: "", label: "Overview" },
  { slug: FEATURES_TAB, label: "S3 features" },
] as const;

/** The backend's kind, as the console names it. */
export function kindLabel(backend: StorageBackend): string {
  return KIND_LABELS[backend.kind] || "—";
}

/** What the backend is, in one line under its name. */
export function backendSummary(backend: StorageBackend | undefined): string {
  if (!backend) return "Storage backend";
  const kind = kindLabel(backend);
  return `Storage backend · ${kind}${backend.region ? ` · ${backend.region}` : ""}`;
}

/**
 * A backend page's header and tabs: Overview (identity, actions, buckets)
 * and S3 features (the probe), which an operator reads when a capability is
 * in question, not on every visit.
 */
export function BackendHeader({
  backendId,
  backend,
}: {
  backendId: string;
  backend: StorageBackend | undefined;
}) {
  const pathname = usePathname();
  const base = backendHref(backendId);
  return (
    <div className="space-y-4">
      <PageHeader
        title={backend?.displayName || backendId}
        description={backendSummary(backend)}
        showDefaultActions={false}
      />
      <div
        className="flex w-full items-center gap-0.5 overflow-x-auto border-b border-border/60"
        role="tablist"
        aria-label="Backend"
      >
        {TABS.map((tab) => {
          const href = tab.slug ? `${base}/${tab.slug}` : base;
          const active = pathname === href;
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
    </div>
  );
}
