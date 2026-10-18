// Every table's search box is the toolbar's. Ten pages each carried their own
// copy of the same markup — icon, input, padding — and none named the box for
// assistive technology; the copies drifted in width and spacing. A new
// hand-rolled one fails here.
import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = join(__dirname, "..");
const GENERATED = "gen";
const TEST_FILE = /\.test\.tsx?$/;
const SEARCH_INPUT = /type=["']search["']/;
// "<file under src>" → why it renders a search input of its own.
const OWNERS: Record<string, string> = {
  "components/ui/TableToolbar.tsx": "the shared search box itself",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !TEST_FILE.test(e.name) ? [p] : [];
  });
}

describe("search boxes", () => {
  it("are rendered by TableToolbar alone", () => {
    const own = sources(SRC)
      .filter((p) => SEARCH_INPUT.test(readFileSync(p, "utf8")))
      .map((p) => relative(SRC, p));
    expect(own.sort()).toEqual(Object.keys(OWNERS).sort());
  });
});
