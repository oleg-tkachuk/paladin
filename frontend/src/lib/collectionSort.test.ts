import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CollectionSchema,
  CollectionUsageSchema,
} from "@/gen/paladin/admin/v1/types_pb";

import { collectionSortValue, sortCollections } from "./collectionSort";

const col = (collection: string, objects: number, bytes: number, at?: number) =>
  create(CollectionSchema, {
    collection,
    displayName: collection.toUpperCase(),
    bucket: `storageBackends/b/buckets/${collection}`,
    usage: create(CollectionUsageSchema, {
      objectCount: BigInt(objects),
      totalBytes: BigInt(bytes),
      lastCommittedAt:
        at === undefined ? undefined : { seconds: BigInt(at), nanos: 0 },
    }),
  });

describe("collection sorting", () => {
  const big = col("big", 10, 5_000, 200);
  const small = col("small", 2, 9_000, 100);
  const empty = create(CollectionSchema, { collection: "empty" });

  it.each([
    ["objects", ["empty", "small", "big"]],
    ["size", ["empty", "big", "small"]],
    ["lastUpload", ["empty", "small", "big"]],
    ["name", ["big", "empty", "small"]],
  ] as const)("by %s, an empty collection first", (column, want) => {
    expect(
      sortCollections([big, small, empty], column, "asc").map(
        (c) => c.collection,
      ),
    ).toEqual(want);
  });

  it("descends, and leaves the list as it was without a sort", () => {
    const list = [small, big];
    expect(
      sortCollections(list, "objects", "desc").map((c) => c.collection),
    ).toEqual(["big", "small"]);
    expect(sortCollections(list, null, null)).toEqual(list);
    expect(list.map((c) => c.collection)).toEqual(["small", "big"]);
  });

  it("reads a collection with no usage as holding nothing", () => {
    expect(collectionSortValue(empty, "objects")).toBe(0);
    expect(collectionSortValue(empty, "lastUpload")).toBe(0);
    expect(collectionSortValue(empty, "displayName")).toBe("");
  });
});
