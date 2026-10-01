import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// An arbitrary value that the scale already has is a second spelling of it:
// `w-[180px]` is `w-45`, `rounded-[32px]` is `rounded-4xl`. Measured before the
// pass: 143 of them outside the vendored primitives, 64 of those text sizes
// with no name at all. A text size below the scale gets a token in
// globals.css (`text-tiny`, `text-caption`, …); a length the spacing scale
// cannot express exactly (`w-[150px]`) stays as it is.

const SRC = join(__dirname, "..");
// Vendored shadcn/ui primitives keep upstream's spelling, so they diff cleanly
// against it.
const SKIPPED_DIRS = new Set(["gen", "ui"]);
const ROOT_PX = 16;
// Tailwind v4's spacing step: --spacing is 0.25rem.
const SPACING_STEP_PX = 4;
const HAIRLINE_PX = 1;
const RADIUS_4XL_PX = 32;

const SPACING_UTILITIES = new Set([
  "w",
  "h",
  "size",
  "min-w",
  "max-w",
  "min-h",
  "max-h",
  "m",
  "mx",
  "my",
  "mt",
  "mb",
  "ml",
  "mr",
  "p",
  "px",
  "py",
  "pt",
  "pb",
  "pl",
  "pr",
  "gap",
  "top",
  "left",
  "right",
  "bottom",
  "inset",
]);

const ARBITRARY =
  /(?:^|[\s:])-?([a-z][a-z0-9-]*)-\[([0-9.]+)(px|rem)\](?=$|[\s])/g;

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return SKIPPED_DIRS.has(e.name) ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/** Why an arbitrary value is a defect, or null when it is not one. */
function defect(utility: string, px: number): string | null {
  if (utility === "text") return "name the size in globals.css @theme";
  if (utility === "ring" && Number.isInteger(px)) return `ring-${px}`;
  if (utility === "rounded" && px === RADIUS_4XL_PX) return "rounded-4xl";
  if (SPACING_UTILITIES.has(utility)) {
    if (px === HAIRLINE_PX) return `${utility}-px`;
    if (px % SPACING_STEP_PX === 0) return `${utility}-${px / SPACING_STEP_PX}`;
  }
  return null;
}

function findings(): string[] {
  const found: string[] = [];
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const visit = (node: ts.Node) => {
      if (
        ts.isStringLiteral(node) ||
        ts.isNoSubstitutionTemplateLiteral(node)
      ) {
        for (const m of node.text.matchAll(ARBITRARY)) {
          const px = Number(m[2]) * (m[3] === "rem" ? ROOT_PX : 1);
          const fix = defect(m[1], px);
          if (fix) {
            const line = sf.getLineAndCharacterOfPosition(
              node.getStart(sf),
            ).line;
            found.push(
              `${relative(SRC, file)}:${line + 1} ${m[0].trim()} → ${fix}`,
            );
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("arbitrary values", () => {
  it("classifies a value the scale has, and leaves one it has not", () => {
    expect(defect("w", 180)).toBe("w-45");
    expect(defect("mt", 1)).toBe("mt-px");
    expect(defect("rounded", 32)).toBe("rounded-4xl");
    expect(defect("ring", 3)).toBe("ring-3");
    expect(defect("text", 10)).not.toBeNull();
    expect(defect("w", 150)).toBeNull();
    expect(defect("rounded", 4)).toBeNull();
  });

  it("no class spells a value the scale or the theme already names", () => {
    expect(findings()).toEqual([]);
  });
});
