import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { PALETTES, THEME_LIGHT } from "@/lib/theme";

// A palette that leaves out a token the @theme block reads falls back to the
// light :root value under it, and nothing says so: one light card on a dark
// page. And a dark palette the `dark:` variant does not name gets the light
// variant of every `dark:` class.

const CSS = readFileSync(join(__dirname, "globals.css"), "utf8");

/** The body of the first rule whose selector is exactly `selector`. */
function block(selector: string): string {
  const start = CSS.indexOf(`\n${selector} {`);
  if (start < 0) return "";
  const open = CSS.indexOf("{", start);
  return CSS.slice(open + 1, CSS.indexOf("\n}", open));
}

/** Custom properties the @theme block maps to colour utilities. */
function tokensRead(): string[] {
  const theme = block("@theme inline");
  return [
    ...theme.matchAll(/--color-[a-z0-9-]+:\s*var\((--[a-z0-9-]+)\)/g),
  ].map((m) => m[1]);
}

function defines(body: string, token: string): boolean {
  return new RegExp(`(^|\\s)${token}\\s*:`).test(body);
}

describe("theme palettes", () => {
  const read = tokensRead();
  const selectors = [
    ":root",
    ...PALETTES.filter((p) => p !== THEME_LIGHT).map((p) => `.${p}`),
  ];

  it("reads the @theme block", () => {
    expect(read).toContain("--primary");
    expect(read.length).toBeGreaterThan(30);
  });

  it.each(selectors)("%s defines every token @theme reads", (selector) => {
    const body = block(selector);
    expect(body, `no ${selector} block`).not.toBe("");
    expect(read.filter((t) => !defines(body, t))).toEqual([]);
  });

  it("the dark variant covers every dark palette", () => {
    const variant = CSS.match(/@custom-variant dark \((.*)\);/)?.[1] ?? "";
    const missing = PALETTES.filter((p) => p !== THEME_LIGHT).filter(
      (p) => !variant.includes(`.${p} *`),
    );
    expect(missing).toEqual([]);
  });
});
