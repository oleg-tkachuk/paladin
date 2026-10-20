import { describe, expect, it } from "vitest";

import { IN_TRASH_SUFFIX, tenantLabel } from "./tenantLabel";

describe("tenantLabel", () => {
  it("prefers the display name, else the slug, and marks the trash", () => {
    expect(
      tenantLabel({ displayName: "Acme", slug: "acme", deleted: false }),
    ).toBe("Acme");
    expect(tenantLabel({ displayName: "", slug: "acme", deleted: false })).toBe(
      "acme",
    );
    expect(
      tenantLabel({ displayName: "Gone", slug: "gone", deleted: true }),
    ).toBe(`Gone${IN_TRASH_SUFFIX}`);
  });
});
