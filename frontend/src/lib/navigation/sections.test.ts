import { describe, expect, it } from "vitest";

import { SECTION, scopeFor, sectionFor } from "./sections";

describe("sectionFor", () => {
  it.each([
    ["/", SECTION.dashboard],
    ["/storage-backends", SECTION.storageBackends],
    ["/storage-backends/primary", SECTION.storageBackends],
    // A bucket is a Buckets page, wherever its URL sits.
    ["/storage-backends/primary/buckets/b1", SECTION.buckets],
    ["/storage-backends/primary/buckets/b1/lifecycle", SECTION.buckets],
    ["/storage-backends/primary/buckets", SECTION.buckets],
    ["/buckets", SECTION.buckets],
    ["/collections", SECTION.collections],
    ["/tenants", SECTION.tenants],
    ["/tenants/acme/collections/docs", SECTION.tenants],
    // A tenant's own topology is its workspace's, not the platform's.
    ["/tenants/acme/topology", SECTION.tenants],
    ["/topology", SECTION.topology],
    ["/audit", SECTION.audit],
    // The MCP group's pages each own their row; anything deeper is the
    // overview's.
    ["/mcp", SECTION.mcp],
    ["/mcp/tools", SECTION.mcpTools],
    ["/mcp/access", SECTION.mcpAccess],
    ["/mcp/connect", SECTION.mcpConnect],
    ["/mcp/nowhere", SECTION.mcp],
  ])("%s is owned by %s", (path, want) => {
    expect(sectionFor(path)).toBe(want);
  });

  it("owns no page outside its sections", () => {
    expect(sectionFor("/nowhere")).toBeNull();
  });

  // A prefix is not a section: /bucketsx is not the Buckets page.
  it("matches whole segments, not prefixes", () => {
    expect(sectionFor("/bucketsx")).toBeNull();
  });
});

describe("scopeFor", () => {
  it.each([
    ["/", { kind: "platform" }],
    ["/buckets", { kind: "platform" }],
    ["/storage-backends/primary/buckets/b1", { kind: "platform" }],
    // The tenants index is the platform's catalogue, not a workspace.
    ["/tenants", { kind: "platform" }],
    ["/tenants/acme", { kind: "tenant", tenant: "acme", tab: "" }],
    [
      "/tenants/acme/collections/docs",
      { kind: "tenant", tenant: "acme", tab: "collections" },
    ],
    ["/tenants/a%20b/quotas", { kind: "tenant", tenant: "a b", tab: "quotas" }],
  ])("%s is %o", (path, want) => {
    expect(scopeFor(path)).toEqual(want);
  });
});
