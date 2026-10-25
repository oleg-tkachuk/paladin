import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { CollectionSchema } from "@/gen/paladin/admin/v1/types_pb";
import { CompletionMode } from "@/gen/paladin/common/v1/resource_pb";
import { countOptions } from "@/lib/table/facets";

import {
  COLLECTION_FACET_TENANT,
  TENANT_COLLECTION_FACETS,
  bucketLabel,
  collectionFacets,
} from "./collectionFacets";

describe("collection facets", () => {
  it("puts a bucket in words", () => {
    expect(bucketLabel("storageBackends/primary/buckets/docs")).toBe(
      "primary / docs",
    );
    expect(bucketLabel("")).toBe("No bucket");
    expect(bucketLabel("odd")).toBe("odd");
  });

  it("names tenants as the map does and counts completion modes", () => {
    const [tenant, , completion] = collectionFacets(
      new Map([["t-a", { slug: "a", displayName: "Tenant A" }]]),
    );
    const rows = [
      create(CollectionSchema, {
        tenantId: "t-a",
        completionMode: CompletionMode.EXPLICIT,
      }),
      create(CollectionSchema, {
        tenantId: "t-z",
        completionMode: CompletionMode.IMPLICIT,
      }),
    ];
    expect(countOptions(rows, tenant, []).map((o) => o.label)).toEqual([
      "Tenant A",
      "t-z",
    ]);
    expect(countOptions(rows, completion, [])).toMatchObject([
      { label: "Implicit", count: 1 },
      { label: "Explicit", count: 1 },
    ]);
  });

  it("leaves the tenant out of one tenant's facets", () => {
    expect(TENANT_COLLECTION_FACETS.map((f) => f.id)).not.toContain(
      COLLECTION_FACET_TENANT,
    );
    expect(TENANT_COLLECTION_FACETS).toHaveLength(2);
  });
});
