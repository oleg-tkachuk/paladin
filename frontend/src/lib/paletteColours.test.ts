import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A Tailwind palette colour (`text-indigo-400`, `bg-slate-800`) is fixed: it
// does not follow the theme, and it says how something looks rather than what
// it means. The theme's tokens (`primary`, `muted-foreground`, `warning`, …)
// carry both. Measured when this was written: 211 palette classes in 34
// files, all converted since: amber, emerald and rose to warning, success and
// destructive; indigo (the active and selected accent) to primary; slate to
// muted-foreground and muted.
//
// RATCHET would hold a file that may keep palette classes for now, with its
// count; a listed file may only go down. It is empty: no file may have any.

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

const RATCHET: Record<string, number> = {};

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

  it("recognises a palette class, so an empty result means none", () => {
    expect(
      "p-2 hover:bg-indigo-500/10 text-sm".match(PALETTE_CLASS),
    ).toHaveLength(1);
    expect(
      "bg-primary/10 text-muted-foreground".match(PALETTE_CLASS),
    ).toBeNull();
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
