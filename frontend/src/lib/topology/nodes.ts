// The nodes both topologies draw alike: a backend, a bucket's further pages,
// a tenant's layout, and the ids that keep one entity one node. ADR-0033's two
// spaces each read the graph their own way round (tenant.ts, platform.ts),
// but a backend is the same backend in either.

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { backendHref, bucketHref } from "@/lib/navigation/hrefs";
import { isDedicatedLayout } from "@/lib/storageLayout";
import { StatusTone } from "@/lib/ui/status";

import type { NodeLink, NodeStatus, TopologyNode } from "./model";

/** How a backend's state reads; the storage-backends pages decide it. */
export type BackendStatusOf = (b: StorageBackend) => NodeStatus;

export const DETAIL_SEPARATOR = " · ";
export const TRASHED: NodeStatus = {
  label: "In the trash",
  tone: StatusTone.Suspended,
};

export const tenantNodeId = (t: string) => `tenant:${t}`;
export const bucketNodeId = (backend: string, bucket: string) =>
  `bucket:${backend}/${bucket}`;
export const backendNodeId = (b: string) => `backend:${b}`;

/** A tenant's layout (ADR-0015), as its node's second line says it. */
export function layoutDetail(storageLayout: string): string {
  return isDedicatedLayout(storageLayout)
    ? "Dedicated layout"
    : "Shared layout";
}

export function backendNode(
  id: string,
  backend: StorageBackend | undefined,
  statusOf: BackendStatusOf,
): TopologyNode {
  return {
    id: backendNodeId(id),
    kind: "backend",
    label: id,
    detail:
      backend?.displayName && backend.displayName !== id
        ? backend.displayName
        : "Storage backend",
    // A backend the reader may not list has no state to show, rather than
    // one made up for it.
    status: backend ? statusOf(backend) : undefined,
    href: backendHref(id),
    links: [{ label: "Buckets", href: `${backendHref(id)}/buckets` }],
  };
}

/** The bucket tabs a node's menu offers beside the bucket's own page. */
export function bucketLinks(b: {
  backendId: string;
  bucketId: string;
}): NodeLink[] {
  return [
    {
      label: "Collections",
      href: bucketHref(b.backendId, b.bucketId, "collections"),
    },
    {
      label: "Lifecycle rules",
      href: bucketHref(b.backendId, b.bucketId, "lifecycle"),
    },
  ];
}
