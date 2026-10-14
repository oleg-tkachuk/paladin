import { describe, expect, it } from "vitest";

import {
  CHORD_KEY_SEPARATOR,
  NAV_JUMPS,
  NAV_PREFIX,
  chordFor,
  jumpTarget,
  tenantBasePath,
} from "./navShortcuts";

// Before this table the palette and useGlobalShortcuts each served the
// g-prefix with their own map: "g u" went to /users from one and /upload
// from the other, and "g t"/"g b" pushed two routes with a tenant in scope.

const TENANT_BASE = "/tenants/acme";

describe("NAV_JUMPS", () => {
  it("binds each key once, and never the prefix itself", () => {
    const keys = NAV_JUMPS.map((j) => j.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).not.toContain(NAV_PREFIX);
  });

  it("goes to an absolute route", () => {
    for (const j of NAV_JUMPS) expect(j.path.startsWith("/")).toBe(true);
  });
});

describe("tenantBasePath", () => {
  it.each([
    ["no scope", undefined, null],
    ["null scope", null, null],
    ["empty tenant", { slug: "", tenantId: "" }, null],
    ["slug first", { slug: "acme", tenantId: "u-1" }, "/tenants/acme"],
    ["id without slug", { slug: "", tenantId: "u-1" }, "/tenants/u-1"],
    ["escaped", { slug: "a b" }, "/tenants/a%20b"],
  ])("%s", (_name, tenant, want) => {
    expect(tenantBasePath(tenant)).toBe(want);
  });
});

describe("jumpTarget", () => {
  it.each([
    ["d", null, "/"],
    ["t", null, "/tenants"],
    ["b", null, "/buckets"],
    ["k", null, "/collections"],
    ["s", null, "/storage-backends"],
    ["p", null, "/policies"],
    ["a", null, "/audit"],
    ["u", null, "/users"],
    ["h", null, "/health"],
    ["c", null, "/config"],
    ["t", TENANT_BASE, TENANT_BASE],
    ["b", TENANT_BASE, `${TENANT_BASE}/buckets`],
    ["k", TENANT_BASE, `${TENANT_BASE}/collections`],
    ["s", TENANT_BASE, "/storage-backends"],
    ["p", TENANT_BASE, "/policies"],
  ])("g %s with base %s → %s", (key, base, want) => {
    expect(jumpTarget(key, base)).toBe(want);
  });

  it.each(["g", "x", "D", "Enter", "r"])("%s is not a jump", (key) => {
    expect(jumpTarget(key, null)).toBeNull();
  });
});

describe("chordFor", () => {
  const chord = (key: string) =>
    [NAV_PREFIX, key].join(CHORD_KEY_SEPARATOR).toUpperCase();

  it("labels the route a key lands on", () => {
    expect(chordFor("/buckets", null)).toBe(chord("b"));
    expect(chordFor(`${TENANT_BASE}/buckets`, TENANT_BASE)).toBe(chord("b"));
  });

  it("drops the label of a route the scope takes the key away from", () => {
    expect(chordFor("/buckets", TENANT_BASE)).toBeUndefined();
    expect(chordFor("/tenants", TENANT_BASE)).toBeUndefined();
  });

  it("labels no route that no key reaches", () => {
    expect(chordFor("/upload", null)).toBeUndefined();
    expect(chordFor("/stats", TENANT_BASE)).toBeUndefined();
  });
});
