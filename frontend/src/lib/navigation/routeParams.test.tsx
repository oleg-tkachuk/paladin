import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { matchRoute, useRouteParams } from "./routeParams";

const pathname = vi.hoisted(() => ({ value: "/" }));
vi.mock("next/navigation", () => ({ usePathname: () => pathname.value }));

describe("matchRoute", () => {
  it.each([
    {
      name: "the pattern's own path",
      pattern: "/tenants/[id]",
      path: "/tenants/acme",
      want: { id: "acme" },
    },
    {
      name: "a path below the pattern, as a layout sees it",
      pattern: "/tenants/[id]",
      path: "/tenants/acme/billing",
      want: { id: "acme" },
    },
    {
      name: "several params",
      pattern: "/tenants/[id]/collections/[name]",
      path: "/tenants/acme/collections/docs/objects/k",
      want: { id: "acme", name: "docs" },
    },
    {
      name: "an encoded slash stays inside its segment",
      pattern: "/tenants/[id]/collections/[name]/objects/[objectId]",
      path: "/tenants/acme/collections/x%2Fy/objects/k%2Fey%20z",
      want: { id: "acme", name: "x/y", objectId: "k/ey z" },
    },
    {
      name: "a malformed escape is kept as written",
      pattern: "/users/[tenant]/[user]",
      path: "/users/acme/bad%zz",
      want: { tenant: "acme", user: "bad%zz" },
    },
    {
      name: "a trailing slash",
      pattern: "/storage-backends/[backendId]",
      path: "/storage-backends/b1/",
      want: { backendId: "b1" },
    },
  ])("reads $name", ({ pattern, path, want }) => {
    expect(matchRoute(pattern, path)).toEqual(want);
  });

  it.each([
    { name: "a static segment that differs", path: "/users/acme" },
    { name: "a path shorter than the pattern", path: "/tenants" },
    { name: "the root", path: "/" },
  ])("is null for $name", ({ path }) => {
    expect(matchRoute("/tenants/[id]", path)).toBeNull();
  });
});

describe("useRouteParams", () => {
  beforeEach(() => {
    pathname.value = "/";
  });

  it("reads the address bar's path", () => {
    pathname.value = "/storage-backends/b%201/buckets/k";
    const { result } = renderHook(() =>
      useRouteParams("/storage-backends/[backendId]/buckets/[bucketId]"),
    );
    expect(result.current).toEqual({ backendId: "b 1", bucketId: "k" });
  });

  it("answers empty params outside the pattern rather than throwing", () => {
    pathname.value = "/health";
    const { result } = renderHook(() => useRouteParams("/tenants/[id]"));
    expect(result.current).toEqual({ id: "" });
  });
});

// The patterns are spelled by hand at each call site; a typo would match no
// path and read every param as "". Each one has to be a route under src/app.
describe("route patterns in the source", () => {
  const SRC = join(__dirname, "..", "..");
  const APP = join(SRC, "app");
  const CALL = /useRouteParams\(\s*"([^"]+)"/g;
  const SOURCE_FILE = /\.(ts|tsx)$/;

  function sources(dir: string): string[] {
    return readdirSync(dir).flatMap((name) => {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) return sources(path);
      return SOURCE_FILE.test(name) ? [path] : [];
    });
  }

  const files = sources(SRC);
  const calls = files.flatMap((file) =>
    [...readFileSync(file, "utf8").matchAll(CALL)].map((m) => ({
      file: relative(SRC, file),
      pattern: m[1],
    })),
  );

  it("finds the call sites", () => {
    expect(calls.length).toBeGreaterThan(0);
  });

  it.each(calls)("$file reads a route that exists: $pattern", ({ pattern }) => {
    expect(existsSync(join(APP, pattern))).toBe(true);
  });

  it("leaves useParams to the router: its values are the export's placeholder", () => {
    const users = files
      .filter((f) => !/routeParams(\.test)?\.tsx?$/.test(f))
      .filter((f) => /\buseParams\b/.test(readFileSync(f, "utf8")))
      .map((f) => relative(SRC, f));
    expect(users).toEqual([]);
  });
});
