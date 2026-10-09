import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

// DialogContent caps its width at `sm:max-w-sm`. A dialog that widens itself
// with a bare `max-w-2xl` loses to that from the sm breakpoint up — a variant
// beats a base class — and renders 24rem wide, so identifiers and JSON spill
// out of it. The width has to name the breakpoint too: `sm:max-w-2xl`.

const SRC = join(__dirname, "..", "..");
const DIALOG_CONTENT = /<DialogContent\b[^>]*?className="([^"]*)"/g;
// A max-w-* token with no variant in front of it. The base class's own
// phone-width cap is the one bare width that is not a desktop width.
const BARE_WIDTH = /(?:^|\s)(max-w-(?!\[calc\(100%-2rem\)\])\S+)/;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sourceFiles(p);
    return /\.tsx$/.test(e.name) && !/\.test\.tsx$/.test(e.name) ? [p] : [];
  });
}

/** "file: width" for every DialogContent whose width has no breakpoint. */
function bareWidths(files: { path: string; text: string }[]): string[] {
  const found: string[] = [];
  for (const { path, text } of files) {
    for (const [, classes] of text.matchAll(DIALOG_CONTENT)) {
      const bare = BARE_WIDTH.exec(classes);
      if (bare) found.push(`${path}: ${bare[1]}`);
    }
  }
  return found;
}

describe("dialog widths", () => {
  it("flags a width the base sm:max-w-sm overrides", () => {
    const files = [
      { path: "a.tsx", text: '<DialogContent className="max-w-2xl">' },
      { path: "b.tsx", text: '<DialogContent className="gap-0 max-w-lg p-0">' },
    ];
    expect(bareWidths(files)).toEqual(["a.tsx: max-w-2xl", "b.tsx: max-w-lg"]);
  });

  it("accepts a breakpoint width and the phone-width cap", () => {
    const files = [
      { path: "a.tsx", text: '<DialogContent className="sm:max-w-2xl">' },
      {
        path: "b.tsx",
        text: '<DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">',
      },
      { path: "c.tsx", text: "<DialogContent>" },
    ];
    expect(bareWidths(files)).toEqual([]);
  });

  it("no dialog in the console sets a width without a breakpoint", () => {
    const files = sourceFiles(SRC).map((p) => ({
      path: relative(SRC, p),
      text: readFileSync(p, "utf8"),
    }));
    expect(bareWidths(files)).toEqual([]);
  });
});
