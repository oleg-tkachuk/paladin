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

import { use, useCallback, useEffect, useState } from "react";
import { notFound, useRouter } from "next/navigation";
import { ConnectError, Code } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
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

const bucketResourceName = (backendId: string, bucketName: string) =>
  `storageBackends/${backendId}/buckets/${bucketName}`;

export default function BucketDetailLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ id: string; backendId: string; bucketName: string }>;
}) {
  const router = useRouter();
  const { backendId: rawBackend, bucketName: rawName } = use(params);
  const backendId = decodeURIComponent(rawBackend);
  const bucketName = decodeURIComponent(rawName);
  const tenant = useTenant();

  const [bucket, setBucket] = useState<Bucket | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notFoundFlag, setNotFoundFlag] = useState(false);

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const fresh = await bucketClient.getBucket({
        name: bucketResourceName(backendId, bucketName),
      });
      setBucket(fresh);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.NotFound) {
        setNotFoundFlag(true);
        return;
      }
      setError(
        err instanceof ConnectError ? err.rawMessage : "Failed to load bucket.",
      );
    } finally {
      setLoading(false);
    }
  }, [backendId, bucketName]);

  useEffect(() => {
    void refetch();
  }, [refetch]);

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
        )}/${encodeURIComponent(bucketName)}`,
      );
    }
  }, [bucket, tenant.tenantId, backendId, bucketName, router]);

  if (notFoundFlag) {
    notFound();
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title={
          <div className="space-y-1">
            <Link
              href={`/tenants/${tenant.slug}/buckets`}
              className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
            >
              <ChevronLeftIcon className="size-4" />
              All buckets
            </Link>
            <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
              {bucket?.displayName || bucketName}
            </h1>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="info" className={T.code}>
                {backendId}
              </Badge>
              <span className={T.codeSmall}>{bucketName}</span>
            </div>
          </div>
        }
        description={
          bucket?.region
            ? `Region ${bucket.region}`
            : "Per-bucket configuration"
        }
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="icon"
            onClick={() => void refetch()}
            aria-label="Refresh"
            disabled={loading}
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
          </Button>
        }
      />

      <BucketTabs
        tenantId={tenant.slug}
        backendId={backendId}
        bucketName={bucketName}
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
