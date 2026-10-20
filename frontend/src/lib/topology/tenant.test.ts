import { create } from "@bufbuild/protobuf";
import { timestampNow } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import {
  BucketSchema,
  CollectionSchema,
  StorageBackendSchema,
  TenantSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { StatusTone } from "@/lib/ui/status";

import type { TopologyGraph } from "./model";
import { tenantTopology } from "./tenant";

const ACME = "11111111-1111-1111-1111-111111111111";
const POOL = "22222222-2222-2222-2222-222222222222";

const tenant = (id: string, slug: string, extra = {}) =>
  create(TenantSchema, { tenantId: id, slug, displayName: slug, ...extra });
const bucket = (backendId: string, bucketId: string, extra = {}) =>
  create(BucketSchema, {
    backendId,
    bucketId,
    provisionState: "ready",
    ...extra,
  });
const collection = (name: string, bucketName: string) =>
  create(CollectionSchema, {
    tenantId: ACME,
    collection: name,
    bucket: bucketName,
  });
const backend = (id: string) => create(StorageBackendSchema, { backendId: id });

const backendStatus = () => ({ label: "Healthy", tone: StatusTone.Success });

const node = (g: TopologyGraph, id: string) => {
  const n = g.nodes.find((x) => x.id === id);
  if (!n) throw new Error(`no node ${id}`);
  return n;
};
const edges = (g: TopologyGraph) => g.edges.map((e) => `${e.from} → ${e.to}`);

describe("tenantTopology", () => {
  const acme = tenant(ACME, "acme", {
    defaultBucket: "storageBackends/be/buckets/home",
    storageLayout: "dedicated",
  });
  const g = tenantTopology({
    tenant: acme,
    collections: [
      collection("docs", "storageBackends/be/buckets/home"),
      collection("media", "storageBackends/be/buckets/common"),
    ],
    buckets: [
      bucket("be", "home", { ownerTenantId: ACME }),
      bucket("be", "common", {
        ownerTenantId: POOL,
        provisionState: "pending",
      }),
      bucket("be", "spare", { ownerTenantId: ACME }),
    ],
    backends: [backend("be")],
    backendStatus,
  });

  it("reads tenant → collections → buckets → backends", () => {
    expect(g.columns).toEqual(["tenant", "collection", "bucket", "backend"]);
    expect(edges(g)).toEqual(
      expect.arrayContaining([
        `tenant:${ACME} → collection:docs`,
        "collection:docs → bucket:be/home",
        "collection:media → bucket:be/common",
        "bucket:be/home → backend:be",
        "bucket:be/spare → backend:be",
      ]),
    );
  });

  it("links every node to its one page", () => {
    expect(node(g, `tenant:${ACME}`).href).toBe("/tenants/acme");
    expect(node(g, "collection:docs").href).toBe(
      "/tenants/acme/collections/docs",
    );
    expect(node(g, "bucket:be/home").href).toBe(
      "/storage-backends/be/buckets/home",
    );
    expect(node(g, "backend:be").href).toBe("/storage-backends/be");
  });

  it("says which bucket is owned, which granted and which the default", () => {
    expect(node(g, "bucket:be/home").detail).toBe("Owned · Default bucket");
    expect(node(g, "bucket:be/common").detail).toBe("Granted");
  });

  it("tones a bucket by its provisioning and a backend by its state", () => {
    expect(node(g, "bucket:be/home").status?.tone).toBe(StatusTone.Success);
    expect(node(g, "bucket:be/common").status).toEqual({
      label: "Provisioning",
      tone: StatusTone.Progress,
    });
    expect(node(g, "backend:be").status?.label).toBe("Healthy");
  });

  // A collection has no state of its own; nothing is made up for it.
  it("gives a collection no state", () => {
    expect(node(g, "collection:docs").status).toBeUndefined();
  });

  it("marks a bucket nothing uses as an orphan, but not the default", () => {
    expect(node(g, "bucket:be/spare").orphan).toBe(true);
    expect(node(g, "bucket:be/home").orphan).toBe(false);
    expect(node(g, "bucket:be/common").orphan).toBe(false);
  });

  // A grant revoked under a bound collection: the edge still lands on a
  // node, which says the tenant can no longer reach it.
  it("draws a bucket a collection names that the tenant may not bind", () => {
    const lost = tenantTopology({
      tenant: acme,
      collections: [collection("old", "storageBackends/be/buckets/gone")],
      buckets: [],
      backends: [],
      backendStatus,
    });
    expect(node(lost, "bucket:be/gone")).toMatchObject({
      detail: "Not accessible",
      status: undefined,
    });
    // Nor is a backend it cannot read given a made-up state.
    expect(node(lost, "backend:be").status).toBeUndefined();
  });

  it("marks a tenant in the trash as suspended", () => {
    const trashed = tenantTopology({
      tenant: tenant(ACME, "acme", { deletedAt: timestampNow() }),
      collections: [],
      buckets: [],
      backends: [],
      backendStatus,
    });
    expect(node(trashed, `tenant:${ACME}`).status?.tone).toBe(
      StatusTone.Suspended,
    );
  });
});
