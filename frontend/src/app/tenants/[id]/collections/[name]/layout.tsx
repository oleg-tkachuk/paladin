"use client";

// Collection detail layout — applies to every route under
// /tenants/<id>/collections/<name>/. Mirrors BucketDetailLayout:
// fetch once via GetCollection, expose through context, render a
// tab strip; tab pages just read context.
//
// Cross-tenancy guard: Collection resource_name encodes the
// tenant. If the URL's tenant doesn't match the resource's
// tenant, bounce to the canonical path so the address bar tells
// the truth.

import { collectionDetailQuery } from "@/lib/queries/details";
import { useEffect, type Dispatch, type SetStateAction } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notFound, useParams, useRouter } from "next/navigation";
import { Code, ConnectError } from "@connectrpc/connect";
import { ArrowPathIcon, KeyIcon } from "@heroicons/react/24/outline";

import { CollectionTabs } from "@/components/layout/CollectionTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../../tenant-context";
import { CollectionProvider } from "./collection-context";
import { errorMessage } from "@/hooks/errorContract";

export default function CollectionDetailLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const tenant = useTenant();
  // useParams() (sync) avoids the `use(promise)` re-suspension that
  // flashes the layout's loading skeleton on every nested-route
  // change. See TenantLayout for the full rationale.
  const params = useParams<{ name: string }>();
  const rawName = params?.name ?? "";
  const collectionName = decodeURIComponent(rawName);

  const queryClient = useQueryClient();
  // The same query a list row warms on hover (lib/queries/details).
  const detail = collectionDetailQuery(tenant.tenantId, collectionName);
  const collectionCacheKey = detail.queryKey;
  const collectionQuery = useQuery(detail);
  const collection = collectionQuery.data ?? null;
  const loading = collectionQuery.isFetching;
  const notFoundFlag =
    collectionQuery.error instanceof ConnectError &&
    collectionQuery.error.code === Code.NotFound;
  const error =
    collectionQuery.error && !notFoundFlag
      ? errorMessage(collectionQuery.error, "Failed to load collection.")
      : null;
  const refetch = async () => {
    await collectionQuery.refetch();
  };
  // setCollection for child tabs (push a server-updated Collection into cache).
  const setCollection: Dispatch<SetStateAction<Collection | null>> = (next) =>
    queryClient.setQueryData<Collection>(collectionCacheKey, (curr) => {
      const resolved = typeof next === "function" ? next(curr ?? null) : next;
      return resolved ?? undefined;
    });

  // Cross-ownership guard. Collection.tenantId is the canonical
  // owner — if a paste linked the wrong tenant slug, bounce. Hooks
  // before notFound() to keep the count stable.
  useEffect(() => {
    if (!collection) return;
    if (collection.tenantId && collection.tenantId !== tenant.tenantId) {
      router.replace(
        `/tenants/${collection.tenantId}/collections/${encodeURIComponent(
          collectionName,
        )}`,
      );
    }
  }, [collection, tenant.tenantId, collectionName, router]);

  if (notFoundFlag) {
    notFound();
  }

  return (
    // No second PageHeader here — TenantLayout already owns the
    // page chrome (breadcrumbs + separator + tab strip). Inside it the
    // collection is one row: its name, its tabs and a refresh, so the
    // tab's content — an object list above all — starts right below
    // the tenant's chrome rather than under a second header band.
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-x-4 gap-y-2 border-b border-border/60">
        <h2
          className="order-1 flex min-w-0 max-w-full items-center gap-2 pb-2 text-base font-semibold text-foreground"
          title={collection?.displayName || undefined}
        >
          <KeyIcon className="size-4 shrink-0 text-primary" />
          <span className="truncate font-mono">{collectionName}</span>
          {collection?.displayName && (
            <span className={cn(T.helper, "truncate font-normal")}>
              {collection.displayName}
            </span>
          )}
        </h2>
        <CollectionTabs
          tenantId={tenant.slug}
          collection={collectionName}
          // A row of its own on a phone, beside the name from sm up.
          className="order-3 border-b-0 sm:order-2 sm:w-auto sm:flex-1"
        />
        <Button
          variant="ghost"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
          disabled={loading}
          className="order-2 mb-1 ml-auto shrink-0 sm:order-3 sm:ml-0"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      {error && !collection ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
          {error}
        </div>
      ) : loading && !collection ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : collection ? (
        <CollectionProvider value={{ collection, setCollection, refetch }}>
          {children}
        </CollectionProvider>
      ) : null}
    </div>
  );
}
