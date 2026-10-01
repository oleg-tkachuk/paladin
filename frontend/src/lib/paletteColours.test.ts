import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A Tailwind palette colour (`text-indigo-400`, `bg-slate-800`) is fixed: it
// does not follow the theme, and it says how something looks rather than what
// it means. The theme's tokens (`primary`, `muted-foreground`, `warning`, …)
// carry both. Measured when this was written: 211 palette classes in 34
// files. Status colours went first — amber, emerald and rose became warning,
// success and destructive.
//
// RATCHET holds the files not yet converted, each with the count it has now.
// A file may only go down: more palette classes than its entry fails, and so
// does fewer, until the entry is lowered or removed. A file not listed may
// have none.

const SRC = join(__dirname, "..");
const GENERATED = "gen";

const COLOUR_UTILITY =
  "bg|text|border|ring|from|to|via|fill|stroke|shadow|outline|divide|placeholder|decoration|caret|accent";
const PALETTE =
  "red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone";
const PALETTE_CLASS = new RegExp(
  `(?:^|[\\s:])(?:${COLOUR_UTILITY})-(?:${PALETTE})-\\d{2,3}(?=$|[\\s/])`,
  "g",
);

const RATCHET: Record<string, number> = {
  "app/error.tsx": 5,
  "app/not-found.tsx": 4,
  "app/tenants/[id]/buckets/[backendId]/[bucketId]/object-lock/page.tsx": 4,
  "app/tenants/[id]/buckets/[backendId]/[bucketId]/versioning/page.tsx": 3,
  "app/tenants/[id]/not-found.tsx": 4,
  "components/features/ObjectTagBadge.tsx": 6,
  "components/features/objects/BulkActionsToolbar.tsx": 10,
  "components/features/objects/BulkEditModal.tsx": 9,
  "components/features/objects/ObjectTableRow.tsx": 21,
  "components/features/objects/ObjectsFilterBar.tsx": 18,
  "components/features/objects/SaveViewModal.tsx": 7,
  "components/features/tenants/RenamedSlugHint.tsx": 5,
  "components/layout/CommandPalette.tsx": 23,
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function paletteCounts(): Map<string, number> {
  const counts = new Map<string, number>();
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    let n = 0;
    const visit = (node: ts.Node) => {
      if (
        ts.isStringLiteral(node) ||
        ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) ||
        ts.isTemplateMiddle(node) ||
        ts.isTemplateTail(node)
      ) {
        n += [...node.text.matchAll(PALETTE_CLASS)].length;
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
    if (n > 0) counts.set(relative(SRC, file), n);
  }
  return counts;
}

describe("palette colours", () => {
  const counts = paletteCounts();

  it("finds the palette classes it is meant to count", () => {
    expect(Object.keys(RATCHET).length).toBeGreaterThan(0);
  });

  it("no file gains palette classes, and the ratchet only goes down", () => {
    const wrong: string[] = [];
    for (const [file, n] of counts) {
      const allowed = RATCHET[file] ?? 0;
      if (n !== allowed)
        wrong.push(`${file}: ${n} palette classes, ratchet ${allowed}`);
    }
    for (const [file, allowed] of Object.entries(RATCHET)) {
      if (!counts.has(file))
        wrong.push(
          `${file}: none left, remove its ratchet entry (was ${allowed})`,
        );
    }
    expect(
      wrong,
      "use a theme token; lower or remove the entry when a file improves",
    ).toEqual([]);
  });
});
