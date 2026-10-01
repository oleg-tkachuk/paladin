"use client";

// Collections routed to this bucket. Lists Collections for the tenant
// (server-side filter via parent=tenants/<id>) and narrows to the
// ones bound to THIS bucket (client-side filter on bucket field).
//
// Why client-side for the bucket-narrow: the tenant Collections
// listing is already small (1-100 collections typical), and pushing a CEL
// filter to ListCollections would require either a `filter` field
// the backend doesn't accept yet or a per-bucket index that doesn't
// exist. Tracked in BACKLOG if it ever becomes a hot path.

import { useEffect, useState } from "react";
import Link from "next/link";
import { ArrowPathIcon, ServerStackIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";

import { collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../../../../tenant-context";
import { useBucket } from "../bucket-context";
import { errorMessage } from "@/hooks/errorContract";

export default function BucketCollectionsPage() {
  const tenant = useTenant();
  const { bucket } = useBucket();
  const { showNotification } = useNotification();

  const [list, setList] = useState<Collection[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    // Synchronous setState in effect: gated on `tenant.tenantId`
    // so it runs once per tenant, not in a loop. The cleaner
    // alternative (reducer + transition) is overkill for a single
    // boolean.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    collectionClient
      .listCollections({
        parent: `tenants/${tenant.tenantId}`,
        page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
        filter: "",
      })
      .then((res) => {
        if (cancelled) return;
        setList(res.collections);
      })
      .catch((err) => {
        if (cancelled) return;
        const msg = errorMessage(err, "Failed to fetch collections");
        showNotification({ type: "error", title: "Load failed", message: msg });
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [tenant.tenantId, showNotification]);

  // Filter to Collections bound to THIS bucket. Bucket field on the
  // Collection is
  // the full resource_name (storageBackends/<b>/buckets/<n>) — the
  // same string format we get from useBucket().bucket.name.
  const bound = list.filter((c) => c.bucket === bucket.name);

  const detailHref = (c: Collection) =>
    `/tenants/${tenant.slug}/collections/${encodeURIComponent(c.collection)}`;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Collections</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Collections routed to this bucket. Tenant-scoped listing filtered
            client-side by{" "}
            <span className={T.code}>bucket = {bucket.bucketId}</span>.
            Cross-tenant index lives at{" "}
            <Link
              href="/collections"
              className="text-primary hover:underline font-mono"
            >
              /collections
            </Link>
            .
          </p>
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => {
            // Re-fire the effect by toggling a key. Simplest path —
            // use the existing effect's tenantId dep is fine here
            // because it's stable; we just remount the page on
            // navigation. Manual refresh: re-run the same fetch
            // via a new effect-trigger hook would be cleaner, but
            // for a small list this is enough.
            setList([]);
            setLoading(true);
            collectionClient
              .listCollections({
                parent: `tenants/${tenant.tenantId}`,
                page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
                filter: "",
              })
              .then((res) => setList(res.collections))
              .catch((err) => {
                const msg = errorMessage(err, "Failed to fetch collections");
                showNotification({
                  type: "error",
                  title: "Load failed",
                  message: msg,
                });
              })
              .finally(() => setLoading(false));
          }}
          aria-label="Refresh"
          disabled={loading}
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Collection</TableHead>
              <TableHead className="hidden sm:table-cell">
                Display name
              </TableHead>
              <TableHead className="hidden md:table-cell w-[220px]">
                Resource version
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && bound.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={3} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : bound.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <ServerStackIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      No Collections bound to this bucket yet.
                    </p>
                    <p className={T.hint}>
                      Provision one from{" "}
                      <Link
                        href={`/tenants/${tenant.slug}/collections`}
                        className="text-primary hover:underline"
                      >
                        the Collections tab
                      </Link>{" "}
                      and bind it to{" "}
                      <span className={T.code}>{bucket.bucketId}</span>.
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              bound.map((c) => (
                <TableRow key={c.collection} className="group">
                  <TableCell>
                    <Link
                      href={detailHref(c)}
                      className="flex items-center gap-3 hover:text-primary"
                    >
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <span className="font-mono text-xs">{c.collection}</span>
                    </Link>
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    {c.displayName || (
                      <span className="text-muted-foreground italic">—</span>
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      "hidden md:table-cell",
                      T.codeSmall,
                      "text-muted-foreground",
                    )}
                  >
                    {c.resourceVersion || "—"}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}
