import { describe, expect, it } from "vitest";

import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";

import { toSortOrder } from "./sortOrder";

describe("toSortOrder", () => {
  it.each([
    ["asc", SortOrder.ASC],
    ["desc", SortOrder.DESC],
    [null, SortOrder.UNSPECIFIED],
    [undefined, SortOrder.UNSPECIFIED],
  ] as const)("maps %s", (d, want) => {
    expect(toSortOrder(d)).toBe(want);
  });
});
