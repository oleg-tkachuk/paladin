import { describe, expect, it } from "vitest";

import {
  PINNED_MAX,
  groupedLinks,
  RECENT_MAX,
  readLinks,
  resourceOf,
  sameLinks,
  togglePinned,
  withVisit,
} from "./resourceLinks";

const link = (n: number) => ({
  href: `/tenants/t${n}`,
  label: `t${n}`,
  kind: "Tenant",
});

describe("resourceOf", () => {
  it.each([
    ["/tenants/acme", { href: "/tenants/acme", label: "acme", kind: "Tenant" }],
    [
      "/tenants/acme/collections/docs",
      {
        href: "/tenants/acme/collections/docs",
        label: "docs",
        kind: "Collection",
      },
    ],
  ])("names the resource %s shows", (path, want) => {
    expect(resourceOf(path)).toEqual(want);
  });
  it.each([
    "/",
    "/tenants",
    "/tenants/acme/collections/docs/objects",
    "/profile",
  ])("names none on %s", (path) => {
    expect(resourceOf(path)).toBeNull();
  });
  it("takes the name the page gave its id", () => {
    expect(
      resourceOf("/tenants/0199", { "0199": { label: "Acme Corp" } })?.label,
    ).toBe("Acme Corp");
  });
});

describe("withVisit", () => {
  it("puts the visit first, once", () => {
    expect(withVisit([link(1), link(2)], link(2)).map((l) => l.href)).toEqual([
      "/tenants/t2",
      "/tenants/t1",
    ]);
  });
  it(`keeps ${RECENT_MAX}`, () => {
    const many = Array.from({ length: RECENT_MAX }, (_, i) => link(i));
    const after = withVisit(many, link(99));
    expect(after).toHaveLength(RECENT_MAX);
    expect(after[0].href).toBe("/tenants/t99");
  });
});

describe("togglePinned", () => {
  it("pins and unpins", () => {
    const pinned = togglePinned([], link(1));
    expect(pinned).toEqual([link(1)]);
    expect(togglePinned(pinned, link(1))).toEqual([]);
  });
  it(`keeps the last ${PINNED_MAX}`, () => {
    const many = Array.from({ length: PINNED_MAX }, (_, i) => link(i));
    const after = togglePinned(many, link(99));
    expect(after).toHaveLength(PINNED_MAX);
    expect(after.at(-1)?.href).toBe("/tenants/t99");
  });
});

describe("readLinks and sameLinks", () => {
  it("reads a stored list and nothing else", () => {
    expect(readLinks([link(1)])).toEqual([link(1)]);
    expect(readLinks("x")).toEqual([]);
    expect(
      readLinks([{ href: "https://evil.test", label: "x", kind: "y" }]),
    ).toEqual([]);
    expect(readLinks(undefined)).toEqual([]);
    // Protocol-relative and backslash forms leave the console.
    for (const href of ["//evil.test/x", "/\\evil.test"]) {
      expect(readLinks([{ href, label: "x", kind: "y" }])).toEqual([]);
    }
  });
  it("tells an unchanged list", () => {
    expect(sameLinks([link(1)], [link(1)])).toBe(true);
    expect(sameLinks([link(1)], [link(2)])).toBe(false);
  });
});

describe("groupedLinks", () => {
  it("lists the pinned first and a pinned page once", () => {
    expect(
      groupedLinks([link(2)], [link(1), link(2), link(3)]).map(
        (e) => `${e.group}:${e.link.href}`,
      ),
    ).toEqual([
      "pinned:/tenants/t2",
      "recent:/tenants/t1",
      "recent:/tenants/t3",
    ]);
  });
});
