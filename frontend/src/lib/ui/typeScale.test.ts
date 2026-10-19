import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

import { CUSTOM_TEXT_SIZES } from "@/lib/utils";

// The console reads in one type scale, and a column of figures reads in
// tabular figures, right-aligned, its units muted (Bytes): a page that picks
// its own does not line up with the next.

const SRC = join(__dirname, "..", "..");

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
    return /\.tsx$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
  });
}

const files = sources(SRC);

/** Tailwind's sizes the console uses, smallest to largest. */
const SCALE = ["xs", "sm", "base", "lg", "xl", "2xl", "3xl"];
const SIZES = new Set([...SCALE, ...CUSTOM_TEXT_SIZES]);
const TAILWIND_SIZES =
  /(?<![\w-])text-(xs|sm|base|lg|[0-9]?xl|[0-9]xl|display)(?![\w-])/g;

/** The calls and elements whose output is a figure. */
const FIGURES = new Set([
  "formatBytes",
  "formatCount",
  "formatCompactCount",
  "formatMoney",
  "Bytes",
  // stats/page's count, through formatCount.
  "num",
]);

function nameOf(node: ts.Node): string | undefined {
  if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
    return node.expression.text;
  }
  if (ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) {
    return node.tagName.getText();
  }
  return undefined;
}

function holdsFigure(node: ts.Node): boolean {
  if (FIGURES.has(nameOf(node) ?? "")) return true;
  return (
    ts.forEachChild(node, (c) => (holdsFigure(c) ? true : undefined)) ?? false
  );
}

/** Table cells that show a figure without being a numeric cell. */
function unalignedFigures(): string[] {
  const out: string[] = [];
  for (const file of files) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );
    const visit = (node: ts.Node) => {
      if (
        ts.isJsxElement(node) &&
        node.openingElement.tagName.getText() === "TableCell" &&
        holdsFigure(node)
      ) {
        const numeric = node.openingElement.attributes.properties.some(
          (a) => ts.isJsxAttribute(a) && a.name.getText() === "numeric",
        );
        if (!numeric) {
          const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
          out.push(`${relative(SRC, file)}:${line + 1}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return out;
}

describe("type scale", () => {
  it("sets text only in the scale's sizes", () => {
    const off = files.flatMap((file) =>
      [...readFileSync(file, "utf8").matchAll(TAILWIND_SIZES)]
        .filter(([, size]) => !SIZES.has(size))
        .map(([cls]) => `${relative(SRC, file)}: ${cls}`),
    );
    expect(off).toEqual([]);
  });

  it("defines in globals.css every size it adds to Tailwind's", () => {
    const css = readFileSync(join(SRC, "app", "globals.css"), "utf8");
    for (const size of CUSTOM_TEXT_SIZES)
      expect(css).toContain(`--text-${size}:`);
  });
});

describe("columns of figures", () => {
  it("aligns every table cell that shows a figure", () => {
    expect(unalignedFigures()).toEqual([]);
  });
});

describe("line heights", () => {
  const css = readFileSync(join(SRC, "app", "globals.css"), "utf8");
  const pinned = new Set(
    [...css.matchAll(/--leading-([a-z]+):/g)].map((m) => m[1]),
  );

  it("sets lines only at the heights globals.css pins", () => {
    expect(pinned.size).toBeGreaterThan(0);
    const off = files.flatMap((file) =>
      [...readFileSync(file, "utf8").matchAll(/(?<![\w-])leading-([\w.[\]]+)/g)]
        .filter(([, h]) => !pinned.has(h))
        .map(([cls]) => `${relative(SRC, file)}: ${cls}`),
    );
    expect(off).toEqual([]);
  });
});
