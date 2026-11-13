import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

import { PLACEHOLDER_SEGMENT, placeholderParams } from "./staticParams";

describe("placeholderParams", () => {
  it("names the segment's one page", () => {
    expect(placeholderParams("id")).toEqual([{ id: PLACEHOLDER_SEGMENT }]);
  });
});

// `next build` refuses a dynamic segment without its params, but only on the
// production build; this says which one is missing before that.
describe("dynamic segments", () => {
  const APP = join(__dirname, "..", "..", "app");
  const DYNAMIC = /^\[([A-Za-z]\w*)\]$/;

  function dynamicDirs(dir: string): string[] {
    return readdirSync(dir).flatMap((name) => {
      const path = join(dir, name);
      if (!statSync(path).isDirectory()) return [];
      return [...(DYNAMIC.test(name) ? [path] : []), ...dynamicDirs(path)];
    });
  }

  it.each(dynamicDirs(APP).map((d) => [relative(APP, d), d]))(
    "%s exports its placeholder params from a server layout",
    (_, dir) => {
      const param = DYNAMIC.exec(dir.split("/").pop()!)![1];
      const layout = readFileSync(join(dir, "layout.tsx"), "utf8");
      expect(layout).not.toMatch(/^"use client"/m);
      expect(layout).toContain(`placeholderParams("${param}")`);
    },
  );
});
