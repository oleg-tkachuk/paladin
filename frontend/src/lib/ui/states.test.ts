import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

import { PALETTES } from "@/lib/theme";

import { FOCUS_RING, STICKY_BELOW_TOPBAR } from "./states";

// One focus indicator, one press, three levels of elevation: a component
// that styles its own drifts from the rest, and the contrast tests measure
// only the shared ones.

const SRC = join(__dirname, "..", "..");
const STATES = join(__dirname, "states.ts");
const CSS = readFileSync(join(SRC, "app", "globals.css"), "utf8");

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
    return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
  });
}

const files = sources(SRC).filter((f) => f !== STATES);

/** Where a pattern appears, as file:line. */
function found(pattern: RegExp): string[] {
  return files.flatMap((file) =>
    code(readFileSync(file, "utf8"))
      .split("\n")
      .flatMap((line, i) =>
        pattern.test(line) ? [`${relative(SRC, file)}:${i + 1}`] : [],
      ),
  );
}

/** A source with its comments left out, so prose naming a class is not one. */
function code(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

/** The elevation levels globals.css defines, as their utilities. */
const LEVELS = [...CSS.matchAll(/--shadow-([a-z]+):/g)].map((m) => m[1]);

describe("interaction states", () => {
  it("styles focus only through lib/ui/states", () => {
    expect(found(/focus-visible\]?:(ring|border|outline)/)).toEqual([]);
  });

  it("presses only through lib/ui/states", () => {
    expect(found(/active:(scale|translate)-/)).toEqual([]);
  });

  // The primitives every page builds from, each with the shared ring.
  it.each([
    "button",
    "badge",
    "checkbox",
    "input",
    "textarea",
    "Select",
    "switch",
    "tabs",
  ])("ui/%s takes the shared focus ring", (name) => {
    const src = readFileSync(
      join(SRC, "components", "ui", `${name}.tsx`),
      "utf8",
    );
    expect(src).toContain("FOCUS_RING");
  });

  it("shows the ring even where a control turns its outline off", () => {
    expect(FOCUS_RING).toContain("focus-visible:outline-solid");
  });
});

describe("elevation", () => {
  it("defines its levels in the theme", () => {
    expect(LEVELS).toEqual(["raised", "floating", "overlay"]);
  });

  it("casts no shadow outside the levels", () => {
    const allowed = new Set([...LEVELS, "none"]);
    const offenders = files.flatMap((file) =>
      [
        ...code(readFileSync(file, "utf8")).matchAll(
          /(?<![\w-])shadow(?:-([\w/[\]().,%-]+))?(?![\w-])/g,
        ),
      ]
        .filter(([, level]) => {
          if (level === undefined) return true;
          // The selected row's rail is an inset line, not an elevation.
          if (level.startsWith("[inset")) return false;
          return !allowed.has(level);
        })
        .map(([cls]) => `${relative(SRC, file)}: ${cls}`),
    );
    expect(offenders).toEqual([]);
  });

  it.each(PALETTES)("gives the %s palette its shadow colours", (theme) => {
    const own = theme === "light" ? ":root" : `.${theme}`;
    const start = CSS.indexOf(`\n${own} {`);
    const body = CSS.slice(start, CSS.indexOf("\n}", start));
    expect(body).toContain("--elevation-shadow:");
    expect(body).toContain("--elevation-shadow-strong:");
    expect(body).toContain("--elevation-highlight:");
  });
});

describe("sticky bars", () => {
  const topBar = readFileSync(
    join(SRC, "components", "layout", "TopBar.tsx"),
    "utf8",
  );

  // A bar stuck at another offset slides under the top bar or leaves a gap.
  it("stick just under the top bar, at its height", () => {
    const height = topBar.match(/\bh-(\d+)\b/)?.[1];
    expect(height).toBeDefined();
    expect(STICKY_BELOW_TOPBAR).toContain(`top-${height}`);
  });

  it("are placed only through STICKY_BELOW_TOPBAR, besides the shell's own", () => {
    const shell = new Set([
      "components/layout/TopBar.tsx",
      "components/layout/Sidebar.tsx",
    ]);
    const off = found(/(?<![\w-])sticky(?![\w-])/).filter(
      (at) => !shell.has(at.split(":")[0]),
    );
    expect(off).toEqual([]);
  });
});
