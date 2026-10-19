import { readFileSync } from "node:fs";
import { join } from "node:path";

import {
  blend,
  converter,
  formatHex,
  parse,
  wcagContrast,
  type Color,
} from "culori";
import { describe, expect, it } from "vitest";

import { PALETTES } from "@/lib/theme";
import {
  HOVER_SURFACE,
  SELECTED_SURFACE,
  SIDEBAR_HOVER_SURFACE,
  SIDEBAR_SELECTED_SURFACE,
} from "@/lib/ui/states";

// Text has to be readable at body size in every palette, and the states a
// control passes through — hovered, focused — have to stay readable and
// visible. The light scheme once put the accent at 3.2–3.7:1 against the
// ground; a dark palette had never been measured at all. These pairs are
// the ones the console draws.

const CSS = readFileSync(join(__dirname, "globals.css"), "utf8");

// WCAG 2.x AA for normal-size text.
const MIN_TEXT_CONTRAST = 4.5;
// WCAG 2.x non-text contrast, for the focus indicator against what it rings.
const MIN_NONTEXT_CONTRAST = 3;

/** The custom properties of the rule whose selector is exactly `selector`. */
function block(selector: string): Map<string, string> {
  const start = CSS.indexOf(`\n${selector} {`);
  if (start < 0) return new Map();
  const body = CSS.slice(start, CSS.indexOf("\n}", start));
  return new Map(
    [...body.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)].map((m) => [
      m[1],
      m[2].trim(),
    ]),
  );
}

/** A palette as it applies: :root, then its own class's overrides. */
function palette(theme: string): Map<string, string> {
  return new Map([...block(":root"), ...block(`.${theme}`)]);
}

/** A token's colour in a palette, following var() to the token it names. */
function colour(p: Map<string, string>, token: string): Color {
  const raw = p.get(token);
  if (raw === undefined) throw new Error(`${token} is not set`);
  const ref = raw.match(/^var\((--[a-z0-9-]+)\)$/);
  if (ref) return colour(p, ref[1]);
  const c = parse(raw);
  if (!c) throw new Error(`${token}: ${raw} is not a colour`);
  return c;
}

/**
 * A surface as a state paints it: `token` at `alpha` (a Tailwind `/50`)
 * laid over the surface under it.
 */
interface Paint {
  token: string;
  alpha?: number;
  over?: string;
}

function painted(p: Map<string, string>, paint: Paint): Color {
  const top = colour(p, paint.token);
  const under = colour(p, paint.over ?? "--background");
  // A colour's own alpha (oklch(… / 55%)) and a utility's (/50) both show the
  // surface through; contrast is measured on what the eye gets.
  const alpha = (top.alpha ?? 1) * (paint.alpha ?? 1);
  return alpha === 1 ? top : blend([under, { ...top, alpha }]);
}

const solid = (token: string): Paint => ({ token });

// [text, surface]: coloured text on the ground and the cards, and the
// foreground token on each solid fill.
const SURFACES = ["--background", "--card", "--band", "--muted", "--popover"];
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
const TEXT_PAIRS: [string, Paint][] = [
  ...TEXT_ON_SURFACES.flatMap((t) =>
    SURFACES.map((s): [string, Paint] => [t, solid(s)]),
  ),
  ...FILLS.map(([t, s]): [string, Paint] => [t, solid(s)]),
];

/** The surface a state's `bg-<token>/<alpha>` class paints. */
function stateSurface(cls: string, over: string): Paint {
  const m = cls.match(/bg-([a-z-]+)(?:\/(\d+))?/);
  if (!m) throw new Error(`${cls} paints no background`);
  return {
    token: `--${m[1]}`,
    alpha: m[2] === undefined ? undefined : Number(m[2]) / 100,
    over,
  };
}

// The hovered and selected surfaces as the shared states paint them
// (lib/ui/states), on each ground they appear on, with the text they carry.
const STATE_PAIRS: [string, Paint][] = [
  ...["--background", "--card"].flatMap((ground): [string, Paint][] => [
    ["--foreground", stateSurface(HOVER_SURFACE, ground)],
    ["--foreground", stateSurface(SELECTED_SURFACE, ground)],
  ]),
  ["--sidebar-foreground", stateSurface(SIDEBAR_HOVER_SURFACE, "--sidebar")],
  ["--sidebar-foreground", stateSurface(SIDEBAR_SELECTED_SURFACE, "--sidebar")],
  // The section labels and resting items on the sidebar's own ground.
  ["--muted-foreground", solid("--sidebar")],
  // The buttons' own hover fills.
  ["--foreground", solid("--muted")],
  ["--accent-foreground", solid("--accent")],
];

// The focus indicator against each surface a focusable control sits on.
const RING_ON = ["--background", "--card", "--muted", "--sidebar"];

describe.each(PALETTES)("the %s palette", (theme) => {
  const p = palette(theme);

  it("reads its colours", () => {
    expect(formatHex(colour(p, "--background"))).toMatch(/^#/);
  });

  it.each([...TEXT_PAIRS, ...STATE_PAIRS])(
    "%s on %j reaches 4.5:1",
    (text, surface) => {
      const ground = painted(p, surface);
      expect(
        wcagContrast(painted(p, { token: text, over: surface.token }), ground),
      ).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST);
    },
  );

  it.each(RING_ON)("the focus ring shows 3:1 against %s", (surface) => {
    expect(
      wcagContrast(
        painted(p, { token: "--ring", over: surface }),
        colour(p, surface),
      ),
    ).toBeGreaterThanOrEqual(MIN_NONTEXT_CONTRAST);
  });
});

// Depth: the console reads as layers only when each one is a visible step
// from the next. The sidebar sat on the card's own colour in two palettes,
// and a card stood 1.1:1 off the ground, which read as one flat sheet.
const toOklch = converter("oklch");
const lightness = (c: Color) => toOklch(c)?.l ?? 0;

// The least OKLCH lightness between two planes that the eye takes for one.
const MIN_PLANE_STEP = 0.02;
// The least contrast an edge needs to show from across a desk.
const MIN_EDGE_CONTRAST = 1.3;
// The least step between a card and its header band: a strip, not a plane.
const MIN_BAND_STEP = 0.01;

describe.each(PALETTES)("the %s palette's planes", (theme) => {
  const p = palette(theme);
  const l = (token: string) => lightness(colour(p, token));

  it("sets the sidebar a plane away from the ground", () => {
    expect(Math.abs(l("--sidebar") - l("--background"))).toBeGreaterThanOrEqual(
      MIN_PLANE_STEP,
    );
  });

  // What floats over a card is told apart by its shadow, not by this step:
  // a light card is already near white.
  it("lifts the cards a plane off the ground", () => {
    expect(l("--card") - l("--background")).toBeGreaterThanOrEqual(
      MIN_PLANE_STEP,
    );
  });

  it("rules the card's header band off the card", () => {
    expect(Math.abs(l("--band") - l("--card"))).toBeGreaterThanOrEqual(
      MIN_BAND_STEP,
    );
  });

  it.each([
    ["--border", "--card"],
    ["--border", "--background"],
    ["--sidebar-border", "--sidebar"],
  ])("draws %s visibly on %s", (edge, surface) => {
    expect(
      wcagContrast(
        painted(p, { token: edge, over: surface }),
        colour(p, surface),
      ),
    ).toBeGreaterThanOrEqual(MIN_EDGE_CONTRAST);
  });
});
