import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  BucketSchema,
  CollectionSchema,
  StorageBackendSchema,
  TenantSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { StatusTone } from "@/lib/ui/status";

import type { TopologyGraph } from "./model";
import {
  HIDE_COLLECTIONS,
  SHOW_COLLECTIONS,
  platformTopology,
} from "./platform";

const ACME = "11111111-1111-1111-1111-111111111111";
const POOL = "22222222-2222-2222-2222-222222222222";
const LONELY = "33333333-3333-3333-3333-333333333333";

const tenant = (id: string, slug: string) =>
  create(TenantSchema, { tenantId: id, slug, displayName: slug });
const bucket = (backendId: string, bucketId: string, extra = {}) =>
  create(BucketSchema, {
    backendId,
    bucketId,
    provisionState: "ready",
    ...extra,
  });
const backend = (id: string) => create(StorageBackendSchema, { backendId: id });

const backendStatus = () => ({ label: "Healthy", tone: StatusTone.Success });

const node = (g: TopologyGraph, id: string) => {
  const n = g.nodes.find((x) => x.id === id);
  if (!n) throw new Error(`no node ${id}`);
  return n;
};

describe("platformTopology", () => {
  const g = platformTopology({
    backends: [backend("be"), backend("idle")],
    buckets: [
      bucket("be", "pool", { ownerTenantId: POOL, grantedTenantIds: [ACME] }),
      bucket("be", "broken", {
        ownerTenantId: POOL,
        provisionState: "failed",
      }),
    ],
    tenants: [
      tenant(ACME, "acme"),
      tenant(POOL, "platform"),
      tenant(LONELY, "lonely"),
    ],
    backendStatus,
  });

  it("reads backends → buckets → tenants, owner and grantees", () => {
    expect(g.columns).toEqual(["backend", "bucket", "tenant", "collection"]);
    expect(g.edges.map((e) => `${e.from} → ${e.to}`)).toEqual([
      "backend:be → bucket:be/broken",
      `bucket:be/broken → tenant:${POOL}`,
      "backend:be → bucket:be/pool",
      `bucket:be/pool → tenant:${POOL}`,
      `bucket:be/pool → tenant:${ACME}`,
    ]);
  });

  it("says who a bucket is shared with, and tones it by provisioning", () => {
    expect(node(g, "bucket:be/pool").detail).toBe("Shared with 1");
    expect(node(g, "bucket:be/broken").detail).toBe("Owner only");
    expect(node(g, "bucket:be/broken").status?.tone).toBe(StatusTone.Danger);
  });

  it("marks a backend with no bucket and a tenant with none as orphans", () => {
    expect(node(g, "backend:idle").orphan).toBe(true);
    expect(node(g, "backend:be").orphan).toBe(false);
    expect(node(g, `tenant:${LONELY}`).orphan).toBe(true);
    expect(node(g, `tenant:${ACME}`).orphan).toBe(false);
  });

  // A tenant in the trash, or past what the reader may list.
  it("leaves out a tenant the list does not hold", () => {
    const g2 = platformTopology({
      backends: [backend("be")],
      buckets: [bucket("be", "b", { ownerTenantId: "gone" })],
      tenants: [],
      backendStatus,
    });
    expect(g2.edges).toEqual([{ from: "backend:be", to: "bucket:be/b" }]);
  });

  // The buckets name their backend even when the reader may not list it.
  it("draws a backend a bucket names but the list lacks, with no state", () => {
    const g2 = platformTopology({
      backends: [],
      buckets: [bucket("hidden", "b", { ownerTenantId: ACME })],
      tenants: [],
      backendStatus,
    });
    expect(node(g2, "backend:hidden").status).toBeUndefined();
  });

  it("links a tenant to its own topology", () => {
    expect(node(g, `tenant:${ACME}`).links).toContainEqual({
      label: "Topology",
      href: "/tenants/acme/topology",
    });
  });
});

// A tenant opens down to its collections on demand: closed, its menu offers
// them; open, they are drawn beside it, each a link to its page.
describe("platformTopology's collections on demand", () => {
  const input = {
    backends: [backend("be")],
    buckets: [
      bucket("be", "home", { ownerTenantId: ACME, grantedTenantIds: [] }),
    ],
    tenants: [tenant(ACME, "acme"), tenant(POOL, "pool")],
    backendStatus,
  };

  it("draws no collection until a tenant is opened, and offers to open each", () => {
    const g = platformTopology(input);
    expect(g.nodes.some((n) => n.kind === "collection")).toBe(false);
    expect(node(g, `tenant:${ACME}`).toggle).toEqual({
      key: ACME,
      label: SHOW_COLLECTIONS,
    });
  });

  it("draws an opened tenant's collections beside it, and offers to hide them", () => {
    const g = platformTopology({
      ...input,
      collections: new Map([
        [
          ACME,
          [
            create(CollectionSchema, { tenantId: ACME, collection: "media" }),
            create(CollectionSchema, {
              tenantId: ACME,
              collection: "docs",
              displayName: "Documents",
            }),
          ],
        ],
      ]),
    });
    const drawn = g.nodes.filter((n) => n.kind === "collection");
    expect(drawn.map((n) => [n.label, n.detail, n.href])).toEqual([
      ["docs", "Documents", "/tenants/acme/collections/docs"],
      ["media", "Collection", "/tenants/acme/collections/media"],
    ]);
    for (const n of drawn) {
      expect(g.edges).toContainEqual({ from: `tenant:${ACME}`, to: n.id });
    }
    expect(node(g, `tenant:${ACME}`).toggle?.label).toBe(HIDE_COLLECTIONS);
    expect(node(g, `tenant:${POOL}`).toggle?.label).toBe(SHOW_COLLECTIONS);
    expect(g.columns.at(-1)).toBe("collection");
  });
});
