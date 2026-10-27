"use client";

// /storage-backends/[backendId] — Backend detail. Identity card + list
// of Buckets registered on this backend. Each bucket row drills into
// /storage-backends/{backendId}/buckets/{bucketId}, where the operator
// can see which tenants store data here and walk down to collections
// and files. This is the "physical layout" half of the IA — the
// tenant-first half is at /tenants/[id]/.../objects.

import { CopyableId } from "@/components/ui/CopyableId";
import React, { useMemo } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  ArrowPathIcon,
  CloudIcon,
  PlusIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";

import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { useTenants } from "@/hooks/useTenants";
import {
  BucketTable,
  BUCKET_FACETS,
  tenantLabelMap,
} from "@/components/features/buckets/BucketTable";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { TablePagination } from "@/components/ui/TablePagination";
import { useTableFacets } from "@/hooks/useTableFacets";
import { useTablePage } from "@/hooks/useTablePage";
import { useTableQuery } from "@/hooks/useTableUrl";
import type { Facet } from "@/lib/table/facets";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { BackendActions } from "./BackendActions";
import { PolicyHistory } from "@/components/features/policy/PolicyHistory";
import { backendResourceName } from "@/lib/queries/backends";
import { BackendHeader, kindLabel } from "./BackendHeader";
import { FeaturesSummary } from "./FeaturesSummary";
import { healthView } from "../backendView";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusPanel, type StatusCardSpec } from "@/components/ui/StatusPanel";
import { RelativeTime } from "@/components/RelativeTime";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import {
  backendLifecycleStatus,
  maintenanceStatus,
} from "@/lib/resourceStatus";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

/** The bucket facets but the backend: every bucket here is on this one. */
const BACKEND_BUCKET_FACETS: readonly Facet<Bucket>[] = BUCKET_FACETS.filter(
  (f) => f.id !== "backend",
);

/** Whether a bucket's name or label holds q, lower-cased. */
function bucketMatches(b: Bucket, q: string): boolean {
  return (
    b.bucketId.toLowerCase().includes(q) ||
    (b.displayName || "").toLowerCase().includes(q)
  );
}

export default function StorageBackendDetailPage() {
  const params = useParams<{ backendId: string }>();
  const backendId = decodeURIComponent(params.backendId);

  const {
    backends,
    loading: loadingBackends,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();
  const {
    buckets,
    loading: loadingBuckets,
    error: bucketsError,
    refetch: refetchBuckets,
    // This backend's buckets, narrowed by the server: every bucket on every
    // backend, filtered here, read pages that held none of them.
  } = useBuckets({ backendId });

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
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const facets = useTableFacets(bucketsHere, BACKEND_BUCKET_FACETS, PAGE_TABLE);
  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    return q ? facets.rows.filter((b) => bucketMatches(b, q)) : facets.rows;
  }, [facets.rows, search]);
  const paged = useTablePage(shown, {
    url: PAGE_TABLE,
    resetOn: [search, facets.selection],
  });

  return (
    <div className="space-y-6">
      <BackendHeader backendId={backendId} backend={backend} />

      {backend && <BackendStatusPanel backend={backend} />}

      {/* Identity card */}
      {loadingBackends && !backend ? (
        <Skeleton className="h-32 w-full" />
      ) : backend ? (
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-baseline justify-between gap-3">
              <CardTitle
                className="text-lg leading-tight"
                icon={ServerStackIcon}
              >
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
                onTested={() => void refetchBackends()}
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
              onRetry={() => void refetchBackends()}
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

      {/* The backend's policy layer is edited in its Edit dialog; its earlier
          texts are compared here. */}
      {backend && (
        <Card className="p-4">
          <PolicyHistory resourceName={backendResourceName(backendId)} />
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
          onClick={refetchBuckets}
          aria-label="Refresh"
        >
          <ArrowPathIcon
            className={cn("size-4", loadingBuckets && "animate-spin")}
          />
        </Button>
      </div>

      <TableToolbar
        query={search}
        onQueryChange={setSearch}
        searchLabel="Search this backend's buckets"
        placeholder="Search by name or display label…"
        facets={facets.controls}
        count={{ shown: shown.length, total: bucketsHere.length }}
      />
      <BucketTable
        buckets={paged.rows}
        tenants={tenantNames}
        loading={loadingBuckets}
        error={bucketsError}
        onRetry={refetchBuckets}
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
      <TablePagination {...paged.pagination} />
    </div>
  );
}

/**
 * Where the backend stands, after Argo CD's status panel: what it lets
 * through, what its last probe found of it, and whether an operator marked
 * it as under maintenance. Its S3 features have a tab of their own, so the
 * panel does not repeat them.
 */
function BackendStatusPanel({ backend }: { backend: StorageBackend }) {
  const health = healthView(backend);
  const cards: StatusCardSpec[] = [
    { title: "Lifecycle", ...backendLifecycleStatus(backend) },
    {
      title: "Connectivity",
      tone: health.tone,
      value: health.label,
      detail: backend.healthCheckedAt ? (
        <>
          {health.title}, <RelativeTime ts={backend.healthCheckedAt} />
        </>
      ) : (
        health.title
      ),
      help: "storageFeatures",
    },
    { title: "Maintenance", ...maintenanceStatus(backend.maintenance) },
  ];
  return <StatusPanel label="Backend status" cards={cards} />;
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
