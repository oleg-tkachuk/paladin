import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import {
  PAGE_TRANSITION_CLASS,
  pageTransitionsOn,
  prefersReducedMotion,
  REDUCED_MOTION_QUERY,
  supportsViewTransitions,
} from "./motion";

const withTransitions = {
  startViewTransition: (() => {}) as unknown as Document["startViewTransition"],
};
const withoutTransitions = {} as Pick<Document, "startViewTransition">;

/** A window whose media query answers `reduced` for the reduced-motion query. */
const windowWith = (reduced: boolean) => ({
  matchMedia: (q: string) =>
    ({ matches: q === REDUCED_MOTION_QUERY && reduced }) as MediaQueryList,
});

// The stylesheet spells the class and reads the tokens; a rename on one side
// would leave page changes untimed with nothing failing.
describe("the stylesheet's motion", () => {
  const css = readFileSync(join(__dirname, "../../app/globals.css"), "utf8");
  const reduced = css.slice(css.indexOf(`@media ${REDUCED_MOTION_QUERY}`));

  it("times the page change by its token, under the class it runs as", () => {
    expect(css).toContain(`::view-transition-old(.${PAGE_TRANSITION_CLASS})`);
    expect(css).toContain(`::view-transition-new(.${PAGE_TRANSITION_CLASS})`);
    expect(css).toMatch(/--motion-page:\s*\d+ms;/);
    expect(css).toContain("animation-duration: var(--motion-page)");
  });

  it("eases a state by its token", () => {
    expect(css).toMatch(/--motion-state:\s*\d+ms;/);
    expect(css).toContain("transition-duration: var(--motion-state)");
  });

  it("turns every transition off for less motion", () => {
    expect(reduced.length).toBeLessThan(css.length);
    expect(reduced).toContain("::view-transition-group(*)");
    expect(reduced).toContain("transition: none !important");
  });

  it("names no duration outside the tokens", () => {
    const motion = css.slice(css.indexOf("Motion (lib/ui/motion.ts)"));
    const durations = motion.match(/\b\d+m?s\b/g) ?? [];
    expect(durations).toHaveLength(2);
  });
});

describe("motion", () => {
  it.each([
    ["a document that runs them", withTransitions, true],
    ["a document without the API", withoutTransitions, false],
    ["no document", undefined, false],
  ])("view transitions on %s: %s", (_, doc, want) => {
    expect(supportsViewTransitions(doc)).toBe(want);
  });

  it.each([
    ["asked for less motion", windowWith(true), true],
    ["not asked", windowWith(false), false],
    ["no window", undefined, false],
    ["no matchMedia", {} as Pick<Window, "matchMedia">, false],
  ])("reduced motion when %s: %s", (_, win, want) => {
    expect(prefersReducedMotion(win)).toBe(want);
  });

  it.each([
    ["supported and wanted", withTransitions, windowWith(false), true],
    ["supported, less motion asked", withTransitions, windowWith(true), false],
    ["unsupported", withoutTransitions, windowWith(false), false],
    ["rendered without a document", undefined, undefined, false],
  ])("a page change animates when %s: %s", (_, doc, win, want) => {
    expect(pageTransitionsOn(doc, win)).toBe(want);
  });
});
