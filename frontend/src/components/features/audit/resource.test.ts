import { describe, expect, it } from "vitest";

import { resourceInTenant } from "./resource";

describe("resourceInTenant", () => {
  it.each([
    [
      "tenants/01a0fcfc-281e-7c59-963c-8a946fe82a47/capabilities/01a15d9a",
      "capabilities/01a15d9a",
    ],
    // A tenant itself has nothing after its prefix to show instead.
    [
      "tenants/01a0fcfc-281e-7c59-963c-8a946fe82a47",
      "tenants/01a0fcfc-281e-7c59-963c-8a946fe82a47",
    ],
    ["buckets/media/collections/raw", "buckets/media/collections/raw"],
    ["", ""],
  ])("%s → %s", (name, want) => {
    expect(resourceInTenant(name)).toBe(want);
  });
});
