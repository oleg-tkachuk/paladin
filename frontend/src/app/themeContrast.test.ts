import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

// Text in the light palette has to be readable at body size. The scheme it
// follows put the accent and the status colours at 3.2–3.7:1 against the
// ground, and white on the accent at 3.74:1 — legible to some, below WCAG AA
// for everyone else. These pairs are the ones the console actually draws.

const CSS = readFileSync(join(__dirname, "globals.css"), "utf8");

// WCAG 2.x AA for normal-size text.
const MIN_TEXT_CONTRAST = 4.5;

/** The hex custom properties of the :root block. */
function rootPalette(): Map<string, string> {
  const start = CSS.indexOf("\n:root {");
  const body = CSS.slice(start, CSS.indexOf("\n}", start));
  return new Map(
    [...body.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-f]{6});/g)].map((m) => [
      m[1],
      m[2],
    ]),
  );
}

/** WCAG relative luminance of a #rrggbb colour. */
function luminance(hex: string): number {
  const channel = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// [text, surface]: coloured text on the ground and the cards, and the
// foreground token on each solid fill.
const SURFACES = ["--background", "--card", "--muted"];
const TEXT_ON_SURFACES = [
  "--foreground",
  "--muted-foreground",
  "--primary",
  "--destructive",
  "--success",
  "--warning",
  "--info",
];
const FILLS: [string, string][] = [
  ["--primary-foreground", "--primary"],
  ["--destructive-foreground", "--destructive"],
  ["--warning-foreground", "--warning"],
  ["--sidebar-primary-foreground", "--sidebar-primary"],
];
const PAIRS: [string, string][] = [
  ...TEXT_ON_SURFACES.flatMap((t) =>
    SURFACES.map((s): [string, string] => [t, s]),
  ),
  ...FILLS,
];

describe("light palette contrast", () => {
  const palette = rootPalette();

  it("reads the light palette", () => {
    expect(palette.get("--background")).toMatch(/^#/);
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21);
  });

  it.each(PAIRS)("%s on %s reaches 4.5:1", (text, surface) => {
    const fg = palette.get(text);
    const bg = palette.get(surface);
    expect(fg, `${text} is not a hex colour in :root`).toBeDefined();
    expect(bg, `${surface} is not a hex colour in :root`).toBeDefined();
    expect(contrast(fg!, bg!)).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST);
  });
});
