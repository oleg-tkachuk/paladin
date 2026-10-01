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
// The shared components are held by components/ui/paletteColours.test.ts,
// with its own reasons for the few fixed colours they keep.
const SHARED_UI = join("components", "ui");

const COLOUR_UTILITY =
  "bg|text|border|ring|from|to|via|fill|stroke|shadow|outline|divide|placeholder|decoration|caret|accent";
const PALETTE =
  "red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone";
const PALETTE_CLASS = new RegExp(
  `(?:^|[\\s:])(?:${COLOUR_UTILITY})-(?:${PALETTE})-\\d{2,3}(?=$|[\\s/])`,
  "g",
);

const RATCHET: Record<string, number> = {};

// White, black and hex colours do not follow the theme either. White over the
// dark theme was a tint of the foreground, so it became the foreground token at
// the same opacity — and turns into the right darkening in a light theme. The
// one fixed colour kept is the black scrim behind a dialog, recognised by the
// `inset-0` it always sits with.
const NEUTRAL_CLASS = new RegExp(
  `(?:^|[\\s:])(?:${COLOUR_UTILITY})-(?:white|black|\\[#[0-9a-fA-F]{3,8}\\])(?=$|[\\s/])`,
  "g",
);
const SCRIM = /(?:^|\s)inset-0(?:\s|$)/;
const SCRIM_CLASS = /(?:^|\s)bg-black(?=$|[\s/])/g;

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
        const neutral = [...node.text.matchAll(NEUTRAL_CLASS)].length;
        const scrims = SCRIM.test(node.text)
          ? [...node.text.matchAll(SCRIM_CLASS)].length
          : 0;
        n += neutral - scrims;
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
    const rel = relative(SRC, file);
    if (n > 0 && !rel.startsWith(SHARED_UI)) counts.set(rel, n);
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
    expect("border-white/10 bg-[#0A0C10]".match(NEUTRAL_CLASS)).toHaveLength(2);
    expect("text-foreground/60 bg-background".match(NEUTRAL_CLASS)).toBeNull();
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
