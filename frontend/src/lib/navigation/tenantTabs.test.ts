import { describe, expect, it } from "vitest";

import {
  TENANT_TAB_GROUPS,
  tenantPageHref,
  tenantTabState,
} from "./tenantTabs";

describe("tenantTabState", () => {
  it.each([
    ["", "Overview", "Overview"],
    ["topology", "Overview", "Topology"],
    ["collections", "Collections", "Collections"],
    ["buckets", "Storage", "Buckets"],
    ["default-binding", "Storage", "Default bucket"],
    ["capabilities", "Access", "Capabilities"],
    ["audit-log", "Activity", "Audit"],
    ["budget", "Limits", "Budget"],
  ])("%s → %s / %s", (tab, group, page) => {
    const got = tenantTabState(tab);
    expect([got.group.label, got.page.label]).toEqual([group, page]);
  });

  it("puts an unknown page under the overview, not a guessed group", () => {
    expect(tenantTabState("nowhere").group.label).toBe("Overview");
  });
});

describe("TENANT_TAB_GROUPS", () => {
  // Every page the workspace had as a tab is still reachable, once.
  it("holds each workspace page exactly once", () => {
    const slugs = TENANT_TAB_GROUPS.flatMap((g) => g.pages.map((p) => p.slug));
    expect(new Set(slugs).size).toBe(slugs.length);
    expect(slugs.sort()).toEqual(
      [
        "",
        "audit-log",
        "budget",
        "buckets",
        "capabilities",
        "collections",
        "default-binding",
        "event-subscriptions",
        "m2m-tokens",
        "policies",
        "quotas",
        "topology",
      ].sort(),
    );
  });
});

describe("tenantPageHref", () => {
  it("is the base for the overview and base/slug otherwise", () => {
    expect(tenantPageHref("/tenants/acme", { slug: "", label: "" })).toBe(
      "/tenants/acme",
    );
    expect(tenantPageHref("/tenants/acme", { slug: "quotas", label: "" })).toBe(
      "/tenants/acme/quotas",
    );
  });
});
