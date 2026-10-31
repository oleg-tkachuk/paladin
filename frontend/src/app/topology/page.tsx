"use client";

// /topology — the platform's infrastructure as one picture: every storage
// backend, the buckets on it, and the tenants each bucket serves, its owner
// and the tenants granted it (ADR-0032). A backend's state comes from its
// lifecycle and its last probe, a bucket's from its provisioning, so a
// failing backend shows which tenants stand behind it.
//
// Reads the lists the Storage Backends, Buckets and Tenants pages already
// cache; there is no topology RPC to keep in step with them. A tenant opens
// down to its collections on demand, reading that tenant's alone.

import { useMemo, useState } from "react";
import {
  useQueries,
  useQuery,
  type UseQueryResult,
} from "@tanstack/react-query";

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";

import { backendStatus } from "@/app/storage-backends/backendView";
import { PageHeader } from "@/components/layout/PageHeader";
import { TopologyGraph } from "@/components/features/topology/TopologyGraph";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { errorMessage } from "@/hooks/errorContract";
import { backendsQuery } from "@/lib/queries/backends";
import { bucketsQuery } from "@/lib/queries/buckets";
import { collectionsQuery } from "@/lib/queries/collections";
import { tenantsQuery } from "@/lib/queries/tenants";
import { platformTopology } from "@/lib/topology/platform";

export default function PlatformTopologyPage() {
  const backendsRead = useQuery(backendsQuery());
  const bucketsRead = useQuery(bucketsQuery());
  const tenantsRead = useQuery(tenantsQuery());
  // The tenants opened down to their collections, each read on its own.
  const [open, setOpen] = useState<readonly string[]>([]);
  const reads = useQueries({
    queries: open.map((id) => collectionsQuery(`tenants/${id}`)),
    combine: openCollections,
  });
  const toggle = (id: string) =>
    setOpen((prev) =>
      prev.includes(id) ? prev.filter((t) => t !== id) : [...prev, id],
    );

  const graph = useMemo(
    () =>
      backendsRead.data && bucketsRead.data && tenantsRead.data
        ? platformTopology({
            backends: backendsRead.data,
            buckets: bucketsRead.data,
            tenants: tenantsRead.data,
            backendStatus,
            collections: reads.collections(open),
          })
        : null,
    [backendsRead.data, bucketsRead.data, tenantsRead.data, reads, open],
  );
  // A tenant whose collections could not be read stays closed, and says so.
  const collectionsFailed = reads.failed;

  const failed = [
    { what: "Storage backends", read: backendsRead },
    { what: "Buckets", read: bucketsRead },
    { what: "Tenants", read: tenantsRead },
  ].find((r) => r.read.error);

  return (
    <div className="space-y-4">
      <PageHeader
        title="Topology"
        description="Every storage backend, the buckets on it and the tenants each bucket serves; a tenant's menu shows its collections. A dashed node is registered and used by nothing."
        showDefaultActions={false}
      />
      {failed ? (
        <ListLoadError
          what={failed.what}
          reason={errorMessage(failed.read.error, "The read failed")}
          onRetry={() => void failed.read.refetch()}
        />
      ) : graph ? (
        <>
          {collectionsFailed ? (
            <ListLoadError
              variant="inline"
              what="A tenant's collections"
              reason={errorMessage(collectionsFailed.error, "The read failed")}
              onRetry={() => void collectionsFailed.refetch()}
            />
          ) : null}
          <TopologyGraph
            graph={graph}
            label="Platform topology"
            onToggle={toggle}
          />
        </>
      ) : (
        <Skeleton className="h-96 w-full" />
      )}
    </div>
  );
}

/**
 * The open tenants' reads, as the graph takes them: the collections of each
 * read so far, by tenant, and the first read that failed. A module-level
 * combine, so its answer holds still while no read changes.
 */
function openCollections(results: UseQueryResult<Collection[]>[]): {
  collections: (open: readonly string[]) => Map<string, Collection[]>;
  failed: UseQueryResult<Collection[]> | undefined;
} {
  return {
    collections: (open) =>
      new Map(
        open.flatMap((id, i) => {
          const data = results[i]?.data;
          return data ? [[id, data] as const] : [];
        }),
      ),
    failed: results.find((r) => r.error),
  };
}
