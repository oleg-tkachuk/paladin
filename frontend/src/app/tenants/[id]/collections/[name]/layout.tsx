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

import { useEffect, type Dispatch, type SetStateAction } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notFound, useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  ChevronLeftIcon,
  KeyIcon,
} from "@heroicons/react/24/outline";

import { CollectionTabs } from "@/components/layout/CollectionTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import { collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../../tenant-context";
import { CollectionProvider } from "./collection-context";
import { errorMessage } from "@/hooks/errorContract";

const collectionResourceName = (tenantId: string, collection: string) =>
  `tenants/${tenantId}/collections/${collection}`;

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
  const collectionCacheKey = [
    "collection",
    tenant.tenantId,
    collectionName,
  ] as const;
  const collectionQuery = useQuery({
    queryKey: collectionCacheKey,
    retry: false, // NotFound short-circuits to notFound(); a retry won't help.
    queryFn: ({ signal }) =>
      collectionClient.getCollection(
        { name: collectionResourceName(tenant.tenantId, collectionName) },
        { signal },
      ),
  });
  const collection = collectionQuery.data ?? null;
  const loading = collectionQuery.isFetching;
  const notFoundFlag =
    collectionQuery.error instanceof ConnectError &&
    collectionQuery.error.code === Code.NotFound;
  const error =
    collectionQuery.error && !notFoundFlag
      ? errorMessage(collectionQuery.error, "Failed to load object key.")
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
    // page chrome (breadcrumbs + separator + tab strip). Inside
    // that we render a focused OK header band: back-link + title +
    // refresh action, no separator. Keeps the visual hierarchy
    // unambiguous (one PageHeader per page).
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <Link
            href={`/tenants/${tenant.slug}/collections`}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeftIcon className="size-4" />
            All Object Keys
          </Link>
          <h2 className="flex items-center gap-2 truncate text-xl font-semibold tracking-tight text-foreground sm:text-2xl">
            <KeyIcon className="size-5 text-primary" />
            <span className="truncate font-mono">{collectionName}</span>
          </h2>
          {collection?.displayName && (
            <p className={cn(T.helper, "truncate")}>{collection.displayName}</p>
          )}
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
          disabled={loading}
          className="shrink-0"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <CollectionTabs tenantId={tenant.slug} collection={collectionName} />

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
