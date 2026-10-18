import { describe, expect, it } from "vitest";

import { sessionTenantLabel, shortTenantId } from "./sessionTenantLabel";

const ID = "01a0f4a9-7600-77a9-862e-11ea31d523ca";

describe("sessionTenantLabel", () => {
  it.each([
    [
      "the display name first",
      { displayName: "Acme", slug: "acme", id: ID },
      "Acme",
    ],
    [
      "the slug without one",
      { displayName: " ", slug: "acme", id: ID },
      "acme",
    ],
    ["the id, cut, without either", { id: ID }, shortTenantId(ID)],
    ["nothing without a tenant", {}, null],
  ])("names %s", (_, t, want) => {
    expect(sessionTenantLabel(t)).toBe(want);
  });

  it("cuts an id to its leading characters", () => {
    expect(shortTenantId(ID)).toBe("01a0f4a9…");
  });
});
