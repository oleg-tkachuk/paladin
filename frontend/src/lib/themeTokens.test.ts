import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A colour class that names a theme token the stylesheet does not define
// compiles to nothing, and nothing says so. Measured: eleven solid destructive
// buttons asked for `text-destructive-foreground`, which did not exist; their
// white text was the parent's colour, inherited by accident, and would have
// turned dark on red in the light theme.

const SRC = join(__dirname, "..");
const GLOBALS = join(SRC, "app", "globals.css");
const GENERATED = "gen";

// Utilities that take a colour, and the token families the theme defines.
const COLOUR_UTILITY =
  "bg|text|border|ring|from|to|via|fill|stroke|shadow|outline|divide|placeholder|decoration|caret|accent";
const TOKEN_FAMILY =
  "primary|secondary|muted|accent|destructive|warning|success|info|danger|card|popover|background|foreground|border|input|ring|sidebar|chart|surface";
const TOKEN_CLASS = new RegExp(
  `(?:^|[\\s:])(?:${COLOUR_UTILITY})-((?:${TOKEN_FAMILY})[a-z0-9-]*)(?=$|[\\s/])`,
  "g",
);

function definedTokens(): Set<string> {
  const css = readFileSync(GLOBALS, "utf8");
  return new Set(
    [...css.matchAll(/--color-([a-z0-9-]+)\s*:/g)].map((m) => m[1]),
  );
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/** Every token a class string in the source names, with where. */
function usedTokens(): { token: string; at: string }[] {
  const used: { token: string; at: string }[] = [];
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
        for (const m of node.text.matchAll(TOKEN_CLASS)) {
          const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line;
          used.push({
            token: m[1],
            at: `${file.slice(SRC.length + 1)}:${line + 1}`,
          });
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return used;
}

describe("theme tokens", () => {
  const defined = definedTokens();
  const used = usedTokens();

  it("reads the stylesheet's tokens and the source's classes", () => {
    expect(defined.has("destructive")).toBe(true);
    expect(used.length).toBeGreaterThan(100);
  });

  it("every colour class names a token globals.css defines", () => {
    const missing = used
      .filter(({ token }) => !defined.has(token))
      .map(({ token, at }) => `${at} ${token}`);
    expect(missing, "define --color-<token> in globals.css").toEqual([]);
  });
});
