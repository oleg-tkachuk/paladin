"use client";

// /storage-backends/[backendId] — Backend detail. Identity card + list
// of Buckets registered on this backend. Each bucket row drills into
// /storage-backends/{backendId}/buckets/{bucketId}, where the operator
// can see which tenants store data here and walk down to collections
// and files. This is the "physical layout" half of the IA — the
// tenant-first half is at /tenants/[id]/.../objects.

import { CopyableId } from "@/components/ui/CopyableId";
import React, { useEffect, useMemo } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  ArrowPathIcon,
  CloudIcon,
  PlusIcon,
} from "@heroicons/react/24/outline";

import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { useTenants } from "@/hooks/useTenants";
import {
  BucketTable,
  tenantLabelMap,
} from "@/components/features/buckets/BucketTable";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { BackendActions } from "./BackendActions";
import { BackendHeader, kindLabel } from "./BackendHeader";
import { FeaturesSummary } from "./FeaturesSummary";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export default function StorageBackendDetailPage() {
  const params = useParams<{ backendId: string }>();
  const backendId = decodeURIComponent(params.backendId);

  const {
    backends,
    loading: loadingBackends,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const {
    buckets,
    loading: loadingBuckets,
    error: bucketsError,
    fetchBuckets,
  } = useBuckets();

  useEffect(() => {
    // This backend's buckets, narrowed by the server: every bucket on every
    // backend, filtered here, read pages that held none of them.
    void fetchBuckets(backendId);
  }, [fetchBuckets, backendId]);

  const backend = useMemo(
    () => backends.find((b) => b.backendId === backendId),
    [backends, backendId],
  );
  // The rows name owners and grantees; an unknown tenant shows its id.
  const { tenants } = useTenants();
  const tenantNames = useMemo(() => tenantLabelMap(tenants), [tenants]);
  const bucketsHere = useMemo(
    () => buckets.filter((b) => b.backendId === backendId),
    [buckets, backendId],
  );

  return (
    <div className="space-y-6">
      <BackendHeader backendId={backendId} backend={backend} />

      {/* Identity card */}
      {loadingBackends && !backend ? (
        <Skeleton className="h-32 w-full" />
      ) : backend ? (
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-baseline justify-between gap-3">
              <CardTitle className="text-lg leading-tight">
                {backend.displayName || (
                  <span className="text-muted-foreground italic">
                    (unnamed)
                  </span>
                )}
              </CardTitle>
              <span className="text-sm uppercase tracking-wider text-muted-foreground">
                Storage backend
              </span>
            </div>
          </CardHeader>
          <CardContent className="space-y-2 pt-0 text-xs">
            <div className="flex items-center gap-2">
              <span className="w-20 uppercase tracking-wider text-muted-foreground">
                id
              </span>
              <CopyableId value={backend.backendId} label="backend ID" />
            </div>
            <Row k="kind" v={kindLabel(backend)} />
            {backend.region && <Row k="region" v={backend.region} mono />}
            <Row k="endpoint" v={backend.endpoint} mono truncate />
            {backend.publicEndpoint && (
              <Row k="public" v={backend.publicEndpoint} mono truncate />
            )}
            <div className="flex items-center gap-2">
              <span className="w-20 uppercase tracking-wider text-muted-foreground">
                features
              </span>
              <FeaturesSummary backend={backend} />
            </div>
            {backend.credentialsSecretRef && (
              <Row k="secret" v={backend.credentialsSecretRef} mono truncate />
            )}
            <div className="border-t border-border pt-3">
              <BackendActions
                backend={backend}
                onTested={() => void fetchBackends()}
              />
            </div>
          </CardContent>
        </Card>
      ) : backendsError ? (
        // The backend list failed, so this backend is unknown — saying "not
        // found" sent the operator looking for a backend that may exist.
        <Card>
          <CardContent className="py-8">
            <ListLoadError
              what="Backends"
              reason={backendsError}
              onRetry={() => void fetchBackends()}
            />
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            Backend not found.
          </CardContent>
        </Card>
      )}

      {/* Buckets on this backend */}
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold tracking-tight">
          Buckets on this backend
          <span className="ml-2 text-xs font-normal text-muted-foreground">
            {bucketsError ? "—" : bucketsHere.length}
          </span>
        </h2>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void fetchBuckets(backendId)}
          aria-label="Refresh"
        >
          <ArrowPathIcon
            className={cn("size-4", loadingBuckets && "animate-spin")}
          />
        </Button>
      </div>

      <BucketTable
        buckets={bucketsHere}
        tenants={tenantNames}
        loading={loadingBuckets}
        error={bucketsError}
        onRetry={() => void fetchBuckets(backendId)}
        empty={
          <div className="flex flex-col items-center gap-3 text-muted-foreground">
            <CloudIcon className="size-8 opacity-40" />
            <p className="text-sm">No buckets on this backend yet.</p>
            <Button size="sm" variant="outline" asChild>
              <Link href={`/buckets?backend=${encodeURIComponent(backendId)}`}>
                <PlusIcon className="size-4" />
                Create bucket on this backend
              </Link>
            </Button>
          </div>
        }
      />
    </div>
  );
}

function Row({
  k,
  v,
  mono,
  truncate,
}: {
  k: string;
  v: string;
  mono?: boolean;
  truncate?: boolean;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 uppercase tracking-wider text-muted-foreground">
        {k}
      </span>
      <span
        className={cn(
          mono && T.code,
          truncate && "truncate text-muted-foreground text-sm",
        )}
        title={truncate ? v : undefined}
      >
        {v}
      </span>
    </div>
  );
}
