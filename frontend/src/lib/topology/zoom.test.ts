import { describe, expect, it } from "vitest";

import { ZOOM_MAX, ZOOM_MIN, fitZoom, zoomBy, zoomPercent } from "./zoom";

describe("zoomBy", () => {
  it("steps in and out by a tenth", () => {
    expect(zoomBy(1, 1)).toBe(1.1);
    expect(zoomBy(1, -1)).toBe(0.9);
  });

  // Floating-point steps would otherwise leave 100 % as 99.99999 %.
  it("comes back to where it started", () => {
    let z = 1;
    for (let i = 0; i < 7; i++) z = zoomBy(z, -1);
    for (let i = 0; i < 7; i++) z = zoomBy(z, 1);
    expect(z).toBe(1);
  });

  it("stays within its bounds", () => {
    expect(zoomBy(ZOOM_MAX, 1)).toBe(ZOOM_MAX);
    expect(zoomBy(ZOOM_MIN, -1)).toBe(ZOOM_MIN);
  });
});

describe("fitZoom", () => {
  it("shrinks a graph wider than the viewport to fit", () => {
    expect(
      fitZoom({ width: 2000, height: 400 }, { width: 1000, height: 800 }),
    ).toBe(0.5);
  });

  it("fits the tighter of the two sides", () => {
    expect(
      fitZoom({ width: 1000, height: 1000 }, { width: 800, height: 300 }),
    ).toBe(0.3);
  });

  it("never blows a small graph up past its own size", () => {
    expect(
      fitZoom({ width: 100, height: 100 }, { width: 900, height: 900 }),
    ).toBe(1);
  });

  it("does not go below the smallest zoom", () => {
    expect(
      fitZoom({ width: 100_000, height: 100 }, { width: 100, height: 100 }),
    ).toBe(ZOOM_MIN);
  });

  it("is 100 % when there is nothing to fit or nowhere to fit it", () => {
    expect(fitZoom({ width: 0, height: 0 }, { width: 100, height: 100 })).toBe(
      1,
    );
    expect(fitZoom({ width: 10, height: 10 }, { width: 0, height: 0 })).toBe(1);
  });
});

describe("zoomPercent", () => {
  it.each([
    [1, "100%"],
    [0.25, "25%"],
    [1.1, "110%"],
  ])("%s → %s", (z, want) => expect(zoomPercent(z)).toBe(want));
});
