import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

import { CREATE_ACTION } from "./paletteActions";

// A page that opens a create dialog offers it in the command palette too
// (usePageAction), under an id of CREATE_ACTION. A new page with a create
// button and no palette entry fails here rather than going unnoticed.

const APP = join(__dirname, "..", "app");
/** What a page calls to open its create dialog. */
const OPENS_CREATE = "setCreateOpen(true)";

function pages(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return pages(path);
    return e.name === "page.tsx" ? [path] : [];
  });
}

const createPages = pages(APP).filter((p) =>
  readFileSync(p, "utf8").includes(OPENS_CREATE),
);

describe("create actions in the palette", () => {
  it("finds the pages that create", () => {
    expect(createPages.length).toBeGreaterThan(0);
  });

  it.each(createPages.map((p) => [relative(APP, p), p]))(
    "%s offers its create in the palette",
    (_, path) => {
      const src = readFileSync(path, "utf8");
      expect(src).toContain("usePageAction(");
      expect(src).toMatch(/id: CREATE_ACTION\.\w+/);
    },
  );

  it("gives each page its own id", () => {
    const used = createPages.flatMap((p) =>
      [...readFileSync(p, "utf8").matchAll(/id: CREATE_ACTION\.(\w+)/g)].map(
        (m) => m[1],
      ),
    );
    expect(new Set(used).size).toBe(used.length);
    expect(new Set(used)).toEqual(new Set(Object.keys(CREATE_ACTION)));
  });
});
