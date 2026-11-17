import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { RetiredTenantSlugSchema } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { retiredSlugReleaseDate } from "./retiredSlug";

describe("retiredSlugReleaseDate", () => {
  it("is the release time the server set", () => {
    const at = new Date("2027-02-15T12:00:00Z");
    const r = create(RetiredTenantSlugSchema, {
      slug: "acme",
      releaseTime: timestampFromDate(at),
    });
    expect(retiredSlugReleaseDate(r)).toEqual(at);
  });

  it("is null when no reservation period is set", () => {
    const r = create(RetiredTenantSlugSchema, { slug: "acme" });
    expect(retiredSlugReleaseDate(r)).toBeNull();
  });
});
