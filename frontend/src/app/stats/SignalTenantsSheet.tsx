"use client";

// The drill-down behind a flagged census count. The stats page shows "3 quotas
// at limit" for the whole fleet; this answers whose, one tenant per row, each
// linking to the tab of that tenant's page where the rows live.
//
// Fetched only when the sheet opens and never polled: it answers one question
// the operator just asked, not a dashboard.

import { useState, type ReactNode } from "react";
import Link from "next/link";
import { useInfiniteQuery } from "@tanstack/react-query";

import { adminSystemClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { formatCount } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { PlatformStatsSignal } from "@/gen/paladin/admin/v1/system_service_pb";

// PageRequest.page_size 0: the server's own page size.
const SERVER_PAGE_SIZE = 0;

// The tenant-page tab each signal's rows live on.
export type TenantTab = "quotas" | "capabilities" | "m2m-tokens";

// Each tab's link spelled out rather than built from the tab name, so the
// console's link check (src/lib/internalLinks.test.ts) sees every destination.
const TAB_LINK: Record<TenantTab, (tenant: string) => { href: string }> = {
  quotas: (t) => ({ href: `/tenants/${t}/quotas` }),
  capabilities: (t) => ({ href: `/tenants/${t}/capabilities` }),
  "m2m-tokens": (t) => ({ href: `/tenants/${t}/m2m-tokens` }),
};

export function SignalTenantsSheet({
  signal,
  title,
  tab,
  count,
  children,
}: {
  signal: PlatformStatsSignal;
  title: string;
  tab: TenantTab;
  count: bigint | undefined;
  // The census row the operator clicks.
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  // Nothing to drill into at zero. While the census is down the card does
  // not render its rows at all, so there is no stale count to click either.
  if (!count || count <= 0n) return <>{children}</>;
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <button
          type="button"
          className="block w-full rounded-sm text-left underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring"
          aria-label={`${title}: show tenants`}
        >
          {children}
        </button>
      </SheetTrigger>
      <SheetContent side="right" className="w-full max-w-md p-0">
        <SheetHeader className="border-b border-border px-4 py-3">
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>
            Tenants behind this count, biggest first.
          </SheetDescription>
        </SheetHeader>
        {open && <SignalTenantsList signal={signal} tab={tab} />}
      </SheetContent>
    </Sheet>
  );
}

function SignalTenantsList({
  signal,
  tab,
}: {
  signal: PlatformStatsSignal;
  tab: TenantTab;
}) {
  const {
    data,
    isLoading,
    error,
    refetch,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ["platformStatsTenants", signal],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      adminSystemClient.listPlatformStatsTenants({
        signal,
        page: { pageSize: SERVER_PAGE_SIZE, pageToken: pageParam },
      }),
    getNextPageParam: (last) => last.page?.nextPageToken || undefined,
    retry: false,
  });

  if (isLoading) {
    return (
      <div className="space-y-2 p-4">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-full" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="p-4">
        <ListLoadError
          what="tenants"
          reason={errorMessage(error)}
          onRetry={() => void refetch()}
        />
      </div>
    );
  }

  const pages = data?.pages ?? [];
  const tenants = pages.flatMap((p) => p.tenants);
  const unattributed = pages[0]?.unattributed ?? 0n;
  return (
    <div className="flex flex-col gap-1 overflow-y-auto p-4">
      <ul className="divide-y divide-border">
        {tenants.map((t) => {
          // slug is the handle operators navigate by; a tenant row purged
          // while its rows linger has only the id.
          const label = t.slug || t.displayName || t.tenantId;
          return (
            <li key={t.tenantId}>
              <Link
                href={
                  TAB_LINK[tab](encodeURIComponent(t.slug || t.tenantId)).href
                }
                className="flex items-baseline justify-between gap-3 py-2 hover:underline"
              >
                <span className="min-w-0">
                  <span className="block truncate font-mono text-sm">
                    {label}
                  </span>
                  {t.slug && t.displayName && (
                    <span className={cn(T.hint, "block truncate")}>
                      {t.displayName}
                    </span>
                  )}
                </span>
                <span className="shrink-0 font-mono text-sm tabular-nums">
                  {formatCount(Number(t.count))}
                </span>
              </Link>
            </li>
          );
        })}
      </ul>
      {unattributed > 0n && (
        <p className={T.hint}>
          {formatCount(Number(unattributed))} on shared buckets, owned by no
          tenant.
        </p>
      )}
      {hasNextPage && (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          onClick={() => void fetchNextPage()}
          disabled={isFetchingNextPage}
        >
          {isFetchingNextPage ? "Loading…" : "Show more"}
        </Button>
      )}
    </div>
  );
}
