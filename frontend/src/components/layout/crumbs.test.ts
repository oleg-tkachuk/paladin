import { readdirSync } from "node:fs";
import { join, relative, sep } from "node:path";

import { describe, expect, it } from "vitest";

import { HOME_LABEL, PRODUCT_NAME, crumbs, pageTitle } from "./crumbs";

const APP = join(__dirname, "..", "..", "app");

describe("pageTitle", () => {
  it.each([
    ["/", `${HOME_LABEL} · ${PRODUCT_NAME}`],
    ["/buckets", `Buckets · ${PRODUCT_NAME}`],
    ["/tenants/acme/quotas", `Quotas · acme · Tenants · ${PRODUCT_NAME}`],
    [
      "/tenants/acme/m2m-tokens",
      `M2M tokens · acme · Tenants · ${PRODUCT_NAME}`,
    ],
    ["/login", `Sign in · ${PRODUCT_NAME}`],
  ])("%s → %s", (path, title) => {
    expect(pageTitle(path)).toBe(title);
  });

  it("writes a resource id as decoded, and an object key as its leaf", () => {
    expect(
      crumbs("/tenants/acme/collections/docs/objects/a%2Fb%2Freport.pdf"),
    ).toEqual(
      expect.arrayContaining([
        { segment: "a%2Fb%2Freport.pdf", label: "report.pdf", mono: true },
      ]),
    );
  });
});

// Every page used to share the root layout's one title, so ten open tabs, the
// history list and a screen reader's page announcement all said "Paladin".
describe("page titles", () => {
  function pages(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
      const p = join(dir, e.name);
      if (e.isDirectory()) return pages(p);
      return e.name === "page.tsx" ? [p] : [];
    });
  }
  // A route as a URL: each [param] becomes a sample value named after it.
  const routes = pages(APP).map(
    (file) =>
      "/" +
      relative(APP, file)
        .split(sep)
        .slice(0, -1)
        .filter((s) => !/^\(.*\)$/.test(s))
        .map((s) => s.replace(/^\[+(?:\.\.\.)?(\w+)\]+$/, "sample-$1"))
        .join("/"),
  );

  it("tells every page apart", () => {
    const byTitle = new Map<string, string[]>();
    for (const r of routes) {
      const t = pageTitle(r);
      byTitle.set(t, [...(byTitle.get(t) ?? []), r]);
    }
    const shared = [...byTitle].filter(([, rs]) => rs.length > 1);
    expect(shared).toEqual([]);
    expect(routes.length).toBeGreaterThan(30);
  });
});
