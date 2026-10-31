"use client";

// /tenants/<id>/topology — the tenant's workspace as one picture: its
// collections, the buckets they are bound to and the backends those live on,
// with the default bucket marked and the buckets it may use but does not
// drawn apart; and its access as a branch off the tenant — the layers with a
// Cedar policy of their own and the principals holding live capabilities.
// Every other page shows these relations one link at a time.
//
// Reads the lists the workspace's other tabs already cache — the tenant, its
// collections, the buckets it may bind to, the backends — so opening it after
// those tabs costs no request.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { backendStatus } from "@/app/storage-backends/backendView";
import { TopologyGraph } from "@/components/features/topology/TopologyGraph";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { errorMessage } from "@/hooks/errorContract";
import { backendsQuery } from "@/lib/queries/backends";
import { bucketsQuery } from "@/lib/queries/buckets";
import { collectionsQuery } from "@/lib/queries/collections";
import { liveCapabilitiesQuery } from "@/lib/queries/tenantCapabilities";
import { tenantDetailQuery } from "@/lib/queries/details";
import { tenantTopology } from "@/lib/topology/tenant";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { useTenant } from "../tenant-context";

export default function TenantTopologyPage() {
  const { tenantId, slug } = useTenant();
  const parent = `tenants/${tenantId}`;
  // The layout resolved the tenant through the same query, by the slug the
  // address now carries; this reads its default bucket from that cache.
  const tenantRead = useQuery(tenantDetailQuery(slug));
  const collectionsRead = useQuery(collectionsQuery(parent));
  const bucketsRead = useQuery(bucketsQuery({ accessibleTo: parent }));
  // A tenant's own admin may not list backends; the drawing goes on without
  // their states rather than failing whole.
  const backendsRead = useQuery(backendsQuery());
  // The principals' branch; a failed read draws the rest without it.
  const capabilitiesRead = useQuery(liveCapabilitiesQuery(tenantId));

  const graph = useMemo(
    () =>
      tenantRead.data && collectionsRead.data && bucketsRead.data
        ? tenantTopology({
            tenant: tenantRead.data,
            collections: collectionsRead.data,
            buckets: bucketsRead.data,
            backends: backendsRead.data ?? [],
            backendStatus,
            capabilities: capabilitiesRead.data,
          })
        : null,
    [
      tenantRead.data,
      collectionsRead.data,
      bucketsRead.data,
      backendsRead.data,
      capabilitiesRead.data,
    ],
  );

  const failed = [
    { what: "The tenant", read: tenantRead },
    { what: "Collections", read: collectionsRead },
    { what: "Buckets", read: bucketsRead },
  ].find((r) => r.read.error);

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Topology</h2>
        <p className={cn(T.helper, "max-w-prose")}>
          This tenant&apos;s collections, the buckets they are bound to and the
          storage backends those live on. A dashed bucket is one the tenant may
          bind to and does not use. Off the tenant, its access: each layer with
          a Cedar policy of its own, and each principal holding live
          capabilities.
        </p>
      </div>
      {failed ? (
        <ListLoadError
          what={failed.what}
          reason={errorMessage(failed.read.error, "The read failed")}
          onRetry={() => void failed.read.refetch()}
        />
      ) : graph ? (
        <>
          {backendsRead.error ? (
            <ListLoadError
              variant="inline"
              what="Storage backends"
              reason={errorMessage(backendsRead.error, "The read failed")}
              onRetry={() => void backendsRead.refetch()}
            />
          ) : null}
          {capabilitiesRead.error ? (
            <ListLoadError
              variant="inline"
              what="Capabilities"
              reason={errorMessage(capabilitiesRead.error, "The read failed")}
              onRetry={() => void capabilitiesRead.refetch()}
            />
          ) : null}
          <TopologyGraph graph={graph} label={`Topology of ${slug}`} />
        </>
      ) : (
        <Skeleton className="h-96 w-full" />
      )}
    </div>
  );
}
