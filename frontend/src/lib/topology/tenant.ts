// A tenant's workspace as a graph (ADR-0033): from the tenant out, through
// its collections and the buckets they are bound to, to the backends those
// live on.

import type {
  Bucket,
  Collection,
  StorageBackend,
  Tenant,
} from "@/gen/paladin/admin/v1/types_pb";
import { provisionStatus } from "@/lib/bucketProvision";
import { bucketHref, collectionHref, tenantHref } from "@/lib/navigation/hrefs";
import {
  parseBucketResourceName,
  type BucketRef,
} from "@/lib/resources/bucket-name";

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

const collectionNodeId = (c: string) => `collection:${c}`;

export interface TenantTopologyInput {
  tenant: Tenant;
  collections: readonly Collection[];
  /** The buckets the tenant may bind to: owned and granted (ADR-0032). */
  buckets: readonly Bucket[];
  backends: readonly StorageBackend[];
  backendStatus: BackendStatusOf;
}

/**
 * Every bucket the tenant may bind to is drawn, so one it may use but does
 * not is there to see — as an orphan, unless it is the default bucket, which
 * a collection created without naming a bucket lands in.
 */
export function tenantTopology({
  tenant,
  collections,
  buckets,
  backends,
  backendStatus,
}: TenantTopologyInput): TopologyGraph {
  const handle = tenant.slug || tenant.tenantId;
  const root: TopologyNode = {
    id: tenantNodeId(tenant.tenantId),
    kind: "tenant",
    label: tenant.displayName || handle,
    detail: layoutDetail(tenant.storageLayout),
    status: tenant.deletedAt ? TRASHED : undefined,
    href: tenantHref(handle),
  };
  const nodes: TopologyNode[] = [root];
  const edges: TopologyEdge[] = [];

  // The accessible buckets, then any a collection or the default binding
  // names that the list did not hold — a grant revoked under a bound
  // collection — so no edge points at nothing.
  const refs = new Map<string, BucketRef>();
  for (const b of buckets) refs.set(bucketNodeId(b.backendId, b.bucketId), b);

  const used = new Set<string>();
  for (const c of collections) {
    const id = collectionNodeId(c.collection);
    const href = collectionHref(handle, c.collection);
    nodes.push({
      id,
      kind: "collection",
      label: c.collection,
      detail:
        c.displayName && c.displayName !== c.collection
          ? c.displayName
          : "Collection",
      href,
      links: [{ label: "Objects", href: `${href}/objects` }],
    });
    edges.push({ from: root.id, to: id });
    const ref = parseBucketResourceName(c.bucket);
    if (!ref) continue;
    const to = bucketNodeId(ref.backendId, ref.bucketId);
    refs.set(to, refs.get(to) ?? ref);
    used.add(to);
    edges.push({ from: id, to });
  }

  const defaultRef = parseBucketResourceName(tenant.defaultBucket);
  const defaultId = defaultRef
    ? bucketNodeId(defaultRef.backendId, defaultRef.bucketId)
    : null;
  if (defaultRef && defaultId && !refs.has(defaultId)) {
    refs.set(defaultId, defaultRef);
  }

  const listed = new Map(
    buckets.map((b) => [bucketNodeId(b.backendId, b.bucketId), b]),
  );
  const backendIds = new Set<string>();
  for (const [id, ref] of [...refs].sort(([a], [b]) => a.localeCompare(b))) {
    const bucket = listed.get(id);
    const owned = bucket?.ownerTenantId === tenant.tenantId;
    const words = [
      bucket ? (owned ? "Owned" : "Granted") : "Not accessible",
      id === defaultId ? "Default bucket" : "",
      bucket?.publicRead ? "Public" : "",
    ].filter(Boolean);
    nodes.push({
      id,
      kind: "bucket",
      label: ref.bucketId,
      detail: words.join(DETAIL_SEPARATOR),
      status: bucket ? provisionStatus(bucket.provisionState) : undefined,
      href: bucketHref(ref.backendId, ref.bucketId),
      links: bucketLinks(ref),
      orphan: !used.has(id) && id !== defaultId,
    });
    backendIds.add(ref.backendId);
    edges.push({ from: id, to: backendNodeId(ref.backendId) });
  }

  const byId = new Map(backends.map((b) => [b.backendId, b]));
  for (const id of [...backendIds].sort()) {
    nodes.push(backendNode(id, byId.get(id), backendStatus));
  }

  return {
    nodes,
    edges,
    columns: ["tenant", "collection", "bucket", "backend"],
  };
}
