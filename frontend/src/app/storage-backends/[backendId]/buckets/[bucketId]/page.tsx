"use client";

// /storage-backends/[backendId]/buckets/[bucketId] — physical-layout
// bucket browser. Shows the tenants that have Collections here (= what
// data physically lives in this bucket), grouped by tenant. Each row
// links into the tenant-first view at
// /tenants/{slug}/collections/{collection}/objects where files are
// listed. This page is the "show me what's actually in this bucket"
// view that platform-admins reach for during an incident or audit.

import React, { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArchiveBoxIcon,
  ArrowLeftIcon,
  ArrowPathIcon,
  BuildingOfficeIcon,
  ChevronRightIcon,
  FolderIcon,
  MagnifyingGlassIcon,
  ServerStackIcon,
  TagIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBackends } from "@/hooks/useBackends";
import { useTenants } from "@/hooks/useTenants";
import { bucketClient, collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { errorMessage } from "@/hooks/errorContract";

export default function BucketDetailPage() {
  const params = useParams<{ backendId: string; bucketId: string }>();
  const backendId = decodeURIComponent(params.backendId);
  const bucketId = decodeURIComponent(params.bucketId);

  const { backends } = useBackends();
  const { tenants, fetchTenants } = useTenants();

  const [search, setSearch] = useState("");

  // Server-side narrow via the new `bucket` field on
  // ListCollectionsRequest (platform.admin gated; checked by the
  // handler). Returns only the collections bound to this (backend, bucket)
  // pair so we don't pull every OK platform-wide just to filter a
  // handful client-side.
  const bucketRef = `storageBackends/${backendId}/buckets/${bucketId}`;
  const collectionsQuery = useQuery({
    queryKey: ["bucketCollections", bucketRef],
    queryFn: ({ signal }) =>
      collectionClient
        .listCollections(
          {
            parent: "",
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
            filter: "",
            bucket: bucketRef,
          },
          { signal },
        )
        .then((res) => res.collections),
  });
  const collections = useMemo(
    () => collectionsQuery.data ?? [],
    [collectionsQuery.data],
  );
  const loadingCollections = collectionsQuery.isFetching;
  const okError = collectionsQuery.error
    ? errorMessage(collectionsQuery.error, "Failed to load")
    : null;
  const reloadCollections = () => collectionsQuery.refetch();

  // Bucket identity via a direct GetBucket — the page already knows the
  // (backend, bucket) pair, so fetch exactly this row rather than listing
  // every bucket and filtering. (The prior list-and-find read
  // `useBuckets().buckets` but never called fetchBuckets, so the array was
  // always empty and the identity card always read "Bucket not found".) A
  // genuine NotFound is distinguished from a transient load error below.
  const bucketQuery = useQuery({
    queryKey: ["bucket", backendId, bucketId],
    retry: false, // NotFound won't resolve on retry.
    queryFn: ({ signal }) =>
      bucketClient.getBucket({ name: bucketRef }, { signal }),
  });
  const bucket = bucketQuery.data ?? undefined;
  const loadingBucket = bucketQuery.isFetching;
  const bucketNotFound =
    bucketQuery.error instanceof ConnectError &&
    bucketQuery.error.code === Code.NotFound;
  const bucketError =
    bucketQuery.error && !bucketNotFound
      ? errorMessage(bucketQuery.error, "Failed to load bucket.")
      : null;

  // useTenants has no auto-fetch; kick it on mount (unflagged cross-module).
  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);

  const backend = useMemo(
    () => backends.find((b) => b.backendId === backendId),
    [backends, backendId],
  );

  // Server already narrowed by (backend, bucket) — `collections` IS
  // the in-bucket set. No client-side filter needed.
  const collectionsHere = collections;

  // Group Collections by tenant — operator's mental model of bucket
  // browsing is "which tenant stored what here?". Tenant slug is
  // resolved via the loaded /tenants table.
  const groupedByTenant = useMemo(() => {
    const tenantByID = new Map(tenants.map((t) => [t.tenantId, t]));
    const groups = new Map<
      string,
      { tenantId: string; slug: string; displayName: string; oks: Collection[] }
    >();
    for (const ok of collectionsHere) {
      const t = tenantByID.get(ok.tenantId);
      const slug = t?.slug || ok.tenantId;
      const displayName = t?.displayName || slug;
      const existing = groups.get(ok.tenantId);
      if (existing) existing.oks.push(ok);
      else
        groups.set(ok.tenantId, {
          tenantId: ok.tenantId,
          slug,
          displayName,
          oks: [ok],
        });
    }
    return [...groups.values()].sort((a, b) =>
      a.displayName.localeCompare(b.displayName),
    );
  }, [collectionsHere, tenants]);

  const filteredGroups = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return groupedByTenant;
    return groupedByTenant
      .map((g) => ({
        ...g,
        oks: g.oks.filter(
          (ok) =>
            ok.collection.toLowerCase().includes(q) ||
            (ok.displayName || "").toLowerCase().includes(q),
        ),
      }))
      .filter(
        (g) =>
          g.oks.length > 0 ||
          g.slug.toLowerCase().includes(q) ||
          g.displayName.toLowerCase().includes(q),
      );
  }, [groupedByTenant, search]);

  const totalCollections = collectionsHere.length;
  const totalTenants = groupedByTenant.length;

  return (
    <div className="space-y-6">
      <PageHeader
        title={bucketId}
        description={
          backend
            ? `Bucket on ${backend.displayName || backend.backendId}`
            : "Bucket"
        }
        showDefaultActions={false}
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href={`/storage-backends/${encodeURIComponent(backendId)}`}>
              <ArrowLeftIcon className="size-4" />
              Backend
            </Link>
          </Button>
        }
      />

      {/* Breadcrumb */}
      <nav className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
        <Link href="/storage-backends" className="hover:text-foreground">
          Storage Backends
        </Link>
        <ChevronRightIcon className="size-3" />
        <Link
          href={`/storage-backends/${encodeURIComponent(backendId)}`}
          className="hover:text-foreground"
        >
          {backendId}
        </Link>
        <ChevronRightIcon className="size-3" />
        <span className="text-foreground font-medium">{bucketId}</span>
      </nav>

      {/* Identity card */}
      {loadingBucket && !bucket ? (
        <Skeleton className="h-28 w-full" />
      ) : bucket ? (
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-baseline justify-between gap-3">
              <CardTitle className="text-lg leading-tight flex items-center gap-2">
                <ArchiveBoxIcon className="size-5 text-chart-4" />
                {bucket.displayName || bucketId}
              </CardTitle>
              <span className="text-tiny uppercase tracking-wider text-muted-foreground">
                Bucket
              </span>
            </div>
          </CardHeader>
          <CardContent className="space-y-2 pt-0 text-xs">
            <Row k="bucket" v={bucket.bucketId} mono />
            <Row k="backend" v={bucket.backendId} mono />
            {bucket.region && <Row k="region" v={bucket.region} mono />}
            {bucket.ownerTenantId && (
              <Row
                k="owner"
                v={`tenant ${bucket.ownerTenantId.slice(0, 8)}…`}
                mono
              />
            )}
            <div className="flex items-center gap-4 pt-2 text-muted-foreground">
              <span>
                <strong className="text-foreground">{totalTenants}</strong>{" "}
                tenant{totalTenants === 1 ? "" : "s"}
              </span>
              <span>
                <strong className="text-foreground">{totalCollections}</strong>{" "}
                object key{totalCollections === 1 ? "" : "s"}
              </span>
            </div>
          </CardContent>
        </Card>
      ) : bucketError ? (
        <Card>
          <CardContent className="py-8 text-center text-sm text-destructive">
            {bucketError}
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            Bucket not found.
          </CardContent>
        </Card>
      )}

      {/* Tenants + Collections (folders) */}
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold tracking-tight">
          What&apos;s stored here
          <span className="ml-2 text-xs font-normal text-muted-foreground">
            Tenants → Collections → files
          </span>
        </h2>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void reloadCollections()}
          aria-label="Refresh"
        >
          <ArrowPathIcon
            className={cn("size-4", loadingCollections && "animate-spin")}
          />
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-60">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search tenant or folder path…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
      </div>

      {okError ? (
        <Card>
          <CardContent className="py-6 text-center text-sm text-destructive">
            {okError}
          </CardContent>
        </Card>
      ) : loadingCollections && collections.length === 0 ? (
        <div className="space-y-3">
          {[0, 1].map((i) => (
            <Skeleton key={i} className="h-32 w-full" />
          ))}
        </div>
      ) : filteredGroups.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <div className="flex flex-col items-center gap-2 text-muted-foreground">
              <ServerStackIcon className="size-10 opacity-40" />
              <p className="text-sm">
                {search
                  ? "No tenants or folders match your search."
                  : "No data stored in this bucket yet."}
              </p>
              <p className="text-xs">
                Tenants land here when a Collection is bound to this bucket or
                when they pick it as their default at creation.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {filteredGroups.map((g) => (
            <Card key={g.tenantId} className="p-0 overflow-hidden">
              <CardHeader className="bg-muted/40 py-3 px-4">
                <div className="flex items-center justify-between gap-3">
                  <Link
                    href={`/tenants/${encodeURIComponent(g.slug)}?from=storage&backend=${encodeURIComponent(backendId)}&bucket=${encodeURIComponent(bucketId)}`}
                    className="flex items-center gap-2 text-sm font-semibold hover:text-primary"
                  >
                    <div className="flex size-7 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
                      <BuildingOfficeIcon className="size-4" />
                    </div>
                    {g.displayName}
                    <Badge variant="outline" className={T.labelTight}>
                      {g.slug}
                    </Badge>
                  </Link>
                  <span className="text-xs text-muted-foreground">
                    {g.oks.length} folder{g.oks.length === 1 ? "" : "s"}
                  </span>
                </div>
              </CardHeader>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="@md:w-65">Folder</TableHead>
                    <TableHead>Display name</TableHead>
                    <TableHead className="@md:w-45 text-right">
                      Browse
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {g.oks.map((ok) => {
                    const filesHref = `/tenants/${encodeURIComponent(g.slug)}/collections/${encodeURIComponent(ok.collection)}/objects?from=storage&backend=${encodeURIComponent(backendId)}&bucket=${encodeURIComponent(bucketId)}`;
                    return (
                      <TableRow key={ok.collection} className="group">
                        <TableCell>
                          <Link
                            href={filesHref}
                            className="flex items-center gap-2 hover:text-primary"
                          >
                            <FolderIcon className="size-4 text-chart-2" />
                            <span className="font-mono text-xs">
                              {ok.collection}
                            </span>
                          </Link>
                        </TableCell>
                        <TableCell>
                          <span className="text-sm">
                            {ok.displayName || (
                              <span className="text-muted-foreground italic">
                                (unnamed)
                              </span>
                            )}
                          </span>
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="sm"
                            asChild
                            className="h-7 text-xs"
                          >
                            <Link href={filesHref}>
                              <TagIcon className="size-3" />
                              Files
                              <ChevronRightIcon className="size-3" />
                            </Link>
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 uppercase tracking-wider text-muted-foreground">
        {k}
      </span>
      <span className={cn(mono && T.code)}>{v}</span>
    </div>
  );
}
