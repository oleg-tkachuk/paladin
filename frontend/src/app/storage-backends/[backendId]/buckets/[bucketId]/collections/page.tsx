"use client";

// What a bucket holds, by tenant: each tenant that bound a Collection to it —
// the owner, or a tenant it granted the bucket — and those Collections, each
// linking to its page in its tenant's workspace.

import React, { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import {
  ArrowPathIcon,
  BuildingOfficeIcon,
  ChevronRightIcon,
  FolderIcon,
  MagnifyingGlassIcon,
  ServerStackIcon,
  TagIcon,
} from "@heroicons/react/24/outline";

import { useTenants } from "@/hooks/useTenants";
import { collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader } from "@/components/ui/Card";
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
import { collectionHref, tenantHref } from "@/lib/navigation/hrefs";

import { useBucket } from "../bucket-context";

/** How many skeleton cards stand in while the first read is in flight. */
const SKELETON_CARDS = 2;

interface TenantCollections {
  tenantId: string;
  slug: string;
  displayName: string;
  collections: Collection[];
}

export default function BucketCollectionsPage() {
  const { bucket } = useBucket();
  const { tenants, fetchTenants } = useTenants();
  const [search, setSearch] = useState("");

  // useTenants has no auto-fetch; names fall back to the tenant id.
  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);

  // Narrowed server-side by the `bucket` field of ListCollectionsRequest
  // (platform.admin gated), so only this bucket's Collections are read.
  const collectionsQuery = useQuery({
    queryKey: ["bucketCollections", bucket.name],
    queryFn: ({ signal }) =>
      collectionClient
        .listCollections(
          {
            parent: "",
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
            filter: "",
            bucket: bucket.name,
          },
          { signal },
        )
        .then((res) => res.collections),
  });
  const collections = useMemo(
    () => collectionsQuery.data ?? [],
    [collectionsQuery.data],
  );
  const loading = collectionsQuery.isFetching;
  const listError = collectionsQuery.error
    ? errorMessage(collectionsQuery.error, "Failed to load")
    : null;

  // By tenant: who stored what here.
  const groups = useMemo(() => {
    const tenantByID = new Map(tenants.map((t) => [t.tenantId, t]));
    const byTenant = new Map<string, TenantCollections>();
    for (const c of collections) {
      const t = tenantByID.get(c.tenantId);
      const slug = t?.slug || c.tenantId;
      const group = byTenant.get(c.tenantId) ?? {
        tenantId: c.tenantId,
        slug,
        displayName: t?.displayName || slug,
        collections: [],
      };
      group.collections.push(c);
      byTenant.set(c.tenantId, group);
    }
    return [...byTenant.values()].sort((a, b) =>
      a.displayName.localeCompare(b.displayName),
    );
  }, [collections, tenants]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return groups;
    return groups
      .map((g) => ({
        ...g,
        collections: g.collections.filter(
          (c) =>
            c.collection.toLowerCase().includes(q) ||
            (c.displayName || "").toLowerCase().includes(q),
        ),
      }))
      .filter(
        (g) =>
          g.collections.length > 0 ||
          g.slug.toLowerCase().includes(q) ||
          g.displayName.toLowerCase().includes(q),
      );
  }, [groups, search]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-60">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search tenant or collection…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void collectionsQuery.refetch()}
          aria-label="Refresh collections"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      {listError ? (
        <Card>
          <CardContent className="py-6 text-center text-sm text-destructive">
            {listError}
          </CardContent>
        </Card>
      ) : loading && collections.length === 0 ? (
        <div className="space-y-3">
          {Array.from({ length: SKELETON_CARDS }, (_, i) => (
            <Skeleton key={i} className="h-32 w-full" />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <div className="flex flex-col items-center gap-2 text-muted-foreground">
              <ServerStackIcon className="size-10 opacity-40" />
              <p className="text-sm">
                {search
                  ? "No tenants or collections match your search."
                  : "No collection is bound to this bucket yet."}
              </p>
              <p className="text-xs">
                A tenant lands here when it binds a Collection to this bucket:
                the owner, or a tenant it granted the bucket.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {filtered.map((g) => (
            <Card key={g.tenantId} className="p-0 overflow-hidden">
              <CardHeader className="bg-muted/40 py-3 px-4">
                <div className="flex items-center justify-between gap-3">
                  <Link
                    href={tenantHref(g.slug)}
                    className="flex items-center gap-2 text-sm font-semibold hover:text-primary"
                  >
                    <div className="flex size-7 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
                      <BuildingOfficeIcon className="size-4" />
                    </div>
                    {g.displayName}
                    <Badge variant="outline" className={T.codeSmall}>
                      {g.slug}
                    </Badge>
                  </Link>
                  <span className="text-xs text-muted-foreground">
                    {g.collections.length} collection
                    {g.collections.length === 1 ? "" : "s"}
                  </span>
                </div>
              </CardHeader>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="@md:w-65">Collection</TableHead>
                    <TableHead>Display name</TableHead>
                    <TableHead className="@md:w-45 text-right">
                      Browse
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {g.collections.map((c) => {
                    const page = collectionHref(g.slug, c.collection);
                    return (
                      <TableRow key={c.collection} className="group">
                        <TableCell>
                          <Link
                            href={page}
                            className="flex items-center gap-2 hover:text-primary"
                          >
                            <FolderIcon className="size-4 text-chart-2" />
                            <span className={T.codeSmall}>{c.collection}</span>
                          </Link>
                        </TableCell>
                        <TableCell className="text-sm">
                          {c.displayName || (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="sm"
                            asChild
                            className="h-7 text-xs"
                          >
                            <Link href={`${page}/objects`}>
                              <TagIcon className="size-3" />
                              Objects
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
