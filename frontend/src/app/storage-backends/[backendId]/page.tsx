"use client";

// /storage-backends/[backendId] — Backend detail. Identity card + list
// of Buckets registered on this backend. Each bucket row drills into
// /storage-backends/{backendId}/buckets/{bucketId}, where the operator
// can see which tenants store data here and walk down to collections
// and files. This is the "physical layout" half of the IA — the
// tenant-first half is at /tenants/[id]/.../objects.

import React, { useEffect, useMemo } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  ArchiveBoxIcon,
  ArrowLeftIcon,
  ArrowPathIcon,
  ChevronRightIcon,
  CloudIcon,
  PlusIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { BackendActions } from "./BackendActions";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const KIND_LABELS: Record<number, string> = {
  [StorageKind.AWS_S3]: "AWS S3",
  [StorageKind.S3_COMPATIBLE]: "S3-compatible",
  [StorageKind.GCS]: "GCS",
  [StorageKind.UNSPECIFIED]: "—",
};

export default function StorageBackendDetailPage() {
  const params = useParams<{ backendId: string }>();
  const backendId = decodeURIComponent(params.backendId);

  const { backends, loading: loadingBackends } = useBackends();
  const { buckets, loading: loadingBuckets, fetchBuckets } = useBuckets();

  useEffect(() => {
    fetchBuckets();
  }, [fetchBuckets]);

  const backend = useMemo(
    () => backends.find((b) => b.backendId === backendId),
    [backends, backendId],
  );
  const bucketsHere = useMemo(
    () => buckets.filter((b) => b.backendId === backendId),
    [buckets, backendId],
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={backend?.displayName || backendId}
        description={
          backend
            ? `Storage backend · ${KIND_LABELS[backend.kind] || "—"}${backend.region ? ` · ${backend.region}` : ""}`
            : "Storage backend"
        }
        showDefaultActions={false}
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href="/storage-backends">
              <ArrowLeftIcon className="size-4" />
              All backends
            </Link>
          </Button>
        }
      />

      {/* Breadcrumb */}
      <nav className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Link href="/storage-backends" className="hover:text-foreground">
          Storage Backends
        </Link>
        <ChevronRightIcon className="size-3" />
        <span className="text-foreground font-medium">{backendId}</span>
      </nav>

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
              <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
                Storage backend
              </span>
            </div>
          </CardHeader>
          <CardContent className="space-y-2 pt-0 text-xs">
            <Row k="id" v={backend.backendId} mono />
            <Row k="kind" v={KIND_LABELS[backend.kind] || "—"} />
            {backend.region && <Row k="region" v={backend.region} mono />}
            <Row k="endpoint" v={backend.endpoint} mono truncate />
            {backend.publicEndpoint && (
              <Row k="public" v={backend.publicEndpoint} mono truncate />
            )}
            {backend.credentialsSecretRef && (
              <Row k="secret" v={backend.credentialsSecretRef} mono truncate />
            )}
            <div className="border-t border-border pt-3">
              <BackendActions backend={backend} />
            </div>
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
            {bucketsHere.length}
          </span>
        </h2>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void fetchBuckets()}
          aria-label="Refresh"
        >
          <ArrowPathIcon
            className={cn("size-4", loadingBuckets && "animate-spin")}
          />
        </Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[260px]">Bucket</TableHead>
              <TableHead>Display name</TableHead>
              <TableHead className="hidden md:table-cell">Region</TableHead>
              <TableHead className="hidden md:table-cell">State</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loadingBuckets && buckets.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={4} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : bucketsHere.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <CloudIcon className="size-8 opacity-40" />
                    <p className="text-sm">No buckets on this backend yet.</p>
                    <Button size="sm" variant="outline" asChild>
                      <Link
                        href={`/buckets?backend=${encodeURIComponent(backendId)}`}
                      >
                        <PlusIcon className="size-4" />
                        Create bucket on this backend
                      </Link>
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              bucketsHere.map((b) => {
                const href = `/storage-backends/${encodeURIComponent(backendId)}/buckets/${encodeURIComponent(b.bucketId)}`;
                return (
                  <TableRow key={b.bucketId} className="group">
                    <TableCell>
                      <Link
                        href={href}
                        className="flex items-center gap-3 hover:text-primary"
                      >
                        <div className="flex size-8 items-center justify-center rounded-md bg-chart-4/15 text-chart-4 ring-1 ring-chart-4/30">
                          <ArchiveBoxIcon className="size-4" />
                        </div>
                        <span className="font-medium group-hover:underline">
                          {b.bucketId}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell>
                      {b.displayName || (
                        <span className="text-muted-foreground italic">
                          (unnamed)
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <span className="font-mono text-xs text-muted-foreground">
                        {b.region || "—"}
                      </span>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <Badge variant="outline" className={T.labelTight}>
                        {b.provisionState || "ready"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>
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
          truncate && "truncate text-muted-foreground text-[11px]",
        )}
        title={truncate ? v : undefined}
      >
        {v}
      </span>
    </div>
  );
}
