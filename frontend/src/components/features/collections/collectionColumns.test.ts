import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CollectionAccess,
  CollectionSchema,
  CollectionUsageSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { exportColumns } from "@/lib/table/columns";

import { collectionColumns, TENANT_COLUMN } from "./collectionColumns";

const TENANT = "11111111-1111-1111-1111-111111111111";
const tenants = new Map([[TENANT, { slug: "acme", displayName: "Acme" }]]);

const collection = create(CollectionSchema, {
  collection: "docs",
  tenantId: TENANT,
  displayName: "Docs",
  bucket: "storageBackends/primary/buckets/b1",
  access: CollectionAccess.PUBLIC_READ,
  usage: create(CollectionUsageSchema, {
    objectCount: 1234n,
    totalBytes: 2048n,
    lastCommittedAt: { seconds: 1_700_000_000n, nanos: 0 },
  }),
});

describe("collectionColumns", () => {
  it("writes a collection as the table shows it, the counts as numbers", () => {
    const exported = exportColumns(collectionColumns(tenants));
    expect(exported.map((c) => c.header)).toEqual([
      "Collection",
      "Tenant",
      "Display name",
      "Bucket",
      "Objects",
      "Size",
      "Last upload",
    ]);
    expect(exported.map((c) => c.text(collection))).toEqual([
      "docs public",
      "Acme",
      "Docs",
      "storageBackends/primary/buckets/b1",
      "1234",
      "2048",
      "2023-11-14 22:13:20Z",
    ]);
  });

  it("leaves the tenant out of a list within one tenant", () => {
    const exported = exportColumns(collectionColumns(new Map()), [
      TENANT_COLUMN,
    ]);
    expect(exported.map((c) => c.header)).not.toContain("Tenant");
  });

  it("writes a collection with no usage as empty", () => {
    const empty = create(CollectionSchema, { collection: "e" });
    const [, , , , objects, size, last] = exportColumns(
      collectionColumns(tenants),
    ).map((c) => c.text(empty));
    expect([objects, size, last]).toEqual(["0", "0", "—"]);
  });
});
