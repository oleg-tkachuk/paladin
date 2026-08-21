"use client";

// Bucket detail layout — applies to every route under
// /tenants/<id>/buckets/<backend>/<name>/. Mirrors the design of
// the parent TenantLayout: fetch once, expose via context, render
// a tab strip; children just read context and render their tab.
//
// Tenant resolution already happened in the parent TenantLayout —
// useTenant() here is non-nullable. We only fetch the Bucket.
//
// On 404 / not-found we surface notFound() so the operator gets the
// standard 404 chrome instead of a generic "load failed" toast.

import { useEffect, type Dispatch, type SetStateAction } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notFound, useParams, useRouter } from "next/navigation";
import { ConnectError, Code } from "@connectrpc/connect";

import { BucketTabs } from "@/components/layout/BucketTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ArrowPathIcon, ChevronLeftIcon } from "@heroicons/react/24/outline";
import Link from "next/link";

import { bucketClient } from "@/lib/connect/client";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { useTenant } from "../../../tenant-context";
import { BucketProvider } from "./bucket-context";

const bucketResourceName = (backendId: string, bucketId: string) =>
  `storageBackends/${backendId}/buckets/${bucketId}`;

export default function BucketDetailLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  // useParams() (sync) instead of `use(promise)` to avoid layout
  // re-suspension on nested route changes — see TenantLayout.
  const params = useParams<{ backendId: string; bucketId: string }>();
  const backendId = decodeURIComponent(params?.backendId ?? "");
  const bucketId = decodeURIComponent(params?.bucketId ?? "");
  const tenant = useTenant();

  const queryClient = useQueryClient();
  const bucketKey = ["bucket", backendId, bucketId] as const;
  const bucketQuery = useQuery({
    queryKey: bucketKey,
    retry: false, // NotFound short-circuits to notFound(); a retry won't help.
    queryFn: ({ signal }) =>
      bucketClient.getBucket(
        { name: bucketResourceName(backendId, bucketId) },
        { signal },
      ),
  });
  const bucket = bucketQuery.data ?? null;
  const loading = bucketQuery.isFetching;
  const notFoundFlag =
    bucketQuery.error instanceof ConnectError &&
    bucketQuery.error.code === Code.NotFound;
  const error =
    bucketQuery.error && !notFoundFlag
      ? bucketQuery.error instanceof ConnectError
        ? bucketQuery.error.rawMessage
        : "Failed to load bucket."
      : null;
  const refetch = async () => {
    await bucketQuery.refetch();
  };
  // setBucket: child tabs push a server-updated Bucket into the query cache
  // (e.g. SetLifecycleRules returns the new Bucket) — same Dispatch contract.
  const setBucket: Dispatch<SetStateAction<Bucket | null>> = (next) =>
    queryClient.setQueryData<Bucket>(bucketKey, (curr) => {
      const resolved = typeof next === "function" ? next(curr ?? null) : next;
      return resolved ?? undefined;
    });

  // Cross-ownership guard: an operator who pasted a bucket URL
  // belonging to a different tenant lands here with the bucket
  // load succeeding but `ownerTenantId` not matching the URL's
  // tenant. Bounce to the bucket's owning tenant so the address
  // bar tells the truth.
  // Placed BEFORE the notFound() short-circuit so the hook count
  // is stable across the render where notFoundFlag flips.
  useEffect(() => {
    if (!bucket) return;
    const owner = bucket.ownerTenantId;
    if (owner && owner !== tenant.tenantId) {
      router.replace(
        `/tenants/${owner}/buckets/${encodeURIComponent(
          backendId,
        )}/${encodeURIComponent(bucketId)}`,
      );
    }
  }, [bucket, tenant.tenantId, backendId, bucketId, router]);

  if (notFoundFlag) {
    notFound();
  }

  return (
    // No second PageHeader — TenantLayout already owns the page
    // chrome. Bucket header is a focused band with back-link +
    // identity badges + refresh action; the parent already drew the
    // separator. Same pattern as the Collection detail layout.
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <Link
            href={`/tenants/${tenant.slug}/buckets`}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeftIcon className="size-4" />
            All buckets
          </Link>
          <h2 className="truncate text-xl font-semibold tracking-tight text-foreground sm:text-2xl">
            {bucket?.displayName || bucketId}
          </h2>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="info" className={T.code}>
              {backendId}
            </Badge>
            <span className={T.codeSmall}>{bucketId}</span>
            {bucket?.region && (
              <span className={cn(T.hint, "ml-1")}>
                · region {bucket.region}
              </span>
            )}
          </div>
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

      <BucketTabs
        tenantId={tenant.slug}
        backendId={backendId}
        bucketId={bucketId}
      />

      {error && !bucket ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
          {error}
        </div>
      ) : loading && !bucket ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : bucket ? (
        <BucketProvider value={{ bucket, setBucket, refetch }}>
          {children}
        </BucketProvider>
      ) : null}
    </div>
  );
}
