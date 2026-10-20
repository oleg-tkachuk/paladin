"use client";

// /topology — the platform's infrastructure as one picture: every storage
// backend, the buckets on it, and the tenants each bucket serves, its owner
// and the tenants granted it (ADR-0032). A backend's state comes from its
// lifecycle and its last probe, a bucket's from its provisioning, so a
// failing backend shows which tenants stand behind it.
//
// Reads the lists the Storage Backends, Buckets and Tenants pages already
// cache; there is no topology RPC to keep in step with them.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { backendStatus } from "@/app/storage-backends/backendView";
import { PageHeader } from "@/components/layout/PageHeader";
import { TopologyGraph } from "@/components/features/topology/TopologyGraph";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { errorMessage } from "@/hooks/errorContract";
import { backendsQuery } from "@/lib/queries/backends";
import { bucketsQuery } from "@/lib/queries/buckets";
import { tenantsQuery } from "@/lib/queries/tenants";
import { platformTopology } from "@/lib/topology/platform";

export default function PlatformTopologyPage() {
  const backendsRead = useQuery(backendsQuery());
  const bucketsRead = useQuery(bucketsQuery());
  const tenantsRead = useQuery(tenantsQuery());

  const graph = useMemo(
    () =>
      backendsRead.data && bucketsRead.data && tenantsRead.data
        ? platformTopology({
            backends: backendsRead.data,
            buckets: bucketsRead.data,
            tenants: tenantsRead.data,
            backendStatus,
          })
        : null,
    [backendsRead.data, bucketsRead.data, tenantsRead.data],
  );

  const failed = [
    { what: "Storage backends", read: backendsRead },
    { what: "Buckets", read: bucketsRead },
    { what: "Tenants", read: tenantsRead },
  ].find((r) => r.read.error);

  return (
    <div className="space-y-4">
      <PageHeader
        title="Topology"
        description="Every storage backend, the buckets on it and the tenants each bucket serves. A dashed node is registered and used by nothing."
        showDefaultActions={false}
      />
      {failed ? (
        <ListLoadError
          what={failed.what}
          reason={errorMessage(failed.read.error, "The read failed")}
          onRetry={() => void failed.read.refetch()}
        />
      ) : graph ? (
        <TopologyGraph graph={graph} label="Platform topology" />
      ) : (
        <Skeleton className="h-96 w-full" />
      )}
    </div>
  );
}
