import { describe, expect, it } from "vitest";

import {
  CollectionSchema,
  TenantSchema,
} from "@/gen/paladin/admin/v1/types_pb";

import { fieldMask } from "./fieldMask";

describe("fieldMask", () => {
  it("sends the proto names of the fields it is given", () => {
    expect(fieldMask(TenantSchema, "displayName", "labels").paths).toEqual([
      "display_name",
      "labels",
    ]);
    expect(
      fieldMask(CollectionSchema, "displayName", "cedarPolicy").paths,
    ).toEqual(["display_name", "cedar_policy"]);
  });

  it("is empty when no field is named", () => {
    expect(fieldMask(TenantSchema).paths).toEqual([]);
  });

  it("refuses, at compile time, a name that is not a field", () => {
    // @ts-expect-error — "displayNam" is not a field of Tenant.
    expect(() => fieldMask(TenantSchema, "displayNam")).toThrow();
  });
});
