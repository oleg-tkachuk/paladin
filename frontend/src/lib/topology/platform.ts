// The platform's infrastructure as a graph (ADR-0033): from the backends
// in, through the buckets on them, to the tenants each bucket serves.

import type {
  Bucket,
  StorageBackend,
  Tenant,
} from "@/gen/paladin/admin/v1/types_pb";
import { provisionStatus } from "@/lib/bucketProvision";
import { bucketHref, tenantHref } from "@/lib/navigation/hrefs";

import type { TopologyEdge, TopologyGraph, TopologyNode } from "./model";
import {
  DETAIL_SEPARATOR,
  TRASHED,
  backendNode,
  backendNodeId,
  bucketLinks,
  bucketNodeId,
  layoutDetail,
  tenantNodeId,
  type BackendStatusOf,
} from "./nodes";

export interface PlatformTopologyInput {
  backends: readonly StorageBackend[];
  buckets: readonly Bucket[];
  tenants: readonly Tenant[];
  backendStatus: BackendStatusOf;
}

/**
 * Every backend, the buckets on it, and each bucket's owner and grantees
 * (ADR-0032). A backend that holds no bucket and a tenant that may bind to
 * none are orphans: registered, and used by nothing.
 */
export function platformTopology({
  backends,
  buckets,
  tenants,
  backendStatus,
}: PlatformTopologyInput): TopologyGraph {
  const nodes: TopologyNode[] = [];
  const edges: TopologyEdge[] = [];
  const known = new Set(tenants.map((t) => t.tenantId));
  const holding = new Set(buckets.map((b) => b.backendId));

  const byId = new Map(backends.map((b) => [b.backendId, b]));
  const backendIds = [...new Set([...byId.keys(), ...holding])].sort();
  for (const id of backendIds) {
    nodes.push({
      ...backendNode(id, byId.get(id), backendStatus),
      orphan: !holding.has(id),
    });
  }

  const served = new Set<string>();
  const sorted = [...buckets].sort(
    (a, b) =>
      a.backendId.localeCompare(b.backendId) ||
      a.bucketId.localeCompare(b.bucketId),
  );
  for (const b of sorted) {
    const id = bucketNodeId(b.backendId, b.bucketId);
    const grants = b.grantedTenantIds.length;
    nodes.push({
      id,
      kind: "bucket",
      label: b.bucketId,
      detail: [
        grants ? `Shared with ${grants}` : "Owner only",
        b.publicRead ? "Public" : "",
      ]
        .filter(Boolean)
        .join(DETAIL_SEPARATOR),
      status: provisionStatus(b.provisionState),
      href: bucketHref(b.backendId, b.bucketId),
      links: bucketLinks(b),
    });
    edges.push({ from: backendNodeId(b.backendId), to: id });
    // A tenant the list does not hold (in the trash, or past the reader's
    // view) is left out rather than drawn as a bare id.
    for (const t of new Set([b.ownerTenantId, ...b.grantedTenantIds])) {
      if (!known.has(t)) continue;
      served.add(t);
      edges.push({ from: id, to: tenantNodeId(t) });
    }
  }

  const name = (t: Tenant) => t.displayName || t.slug || t.tenantId;
  for (const t of [...tenants].sort((a, b) => name(a).localeCompare(name(b)))) {
    const handle = t.slug || t.tenantId;
    nodes.push({
      id: tenantNodeId(t.tenantId),
      kind: "tenant",
      label: name(t),
      detail: layoutDetail(t.storageLayout),
      status: t.deletedAt ? TRASHED : undefined,
      href: tenantHref(handle),
      links: [{ label: "Topology", href: `${tenantHref(handle)}/topology` }],
      orphan: !served.has(t.tenantId),
    });
  }

  return { nodes, edges, columns: ["backend", "bucket", "tenant"] };
}
