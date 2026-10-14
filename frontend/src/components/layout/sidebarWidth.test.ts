import { describe, expect, it } from "vitest";

import {
  CLEAR_SIDEBAR,
  SIDEBAR_COLLAPSED_ATTR,
  SIDEBAR_WIDTH,
} from "./sidebarWidth";

// Tailwind needs each class written out whole, so the offsets repeat the
// widths and the attribute; these keep the copies in step.
describe("sidebarWidth", () => {
  const size = (cls: string) => cls.split("-").pop();

  it("clears the expanded sidebar by its own width", () => {
    expect(CLEAR_SIDEBAR).toContain(`lg:left-${size(SIDEBAR_WIDTH.expanded)}`);
  });

  it("clears the collapsed sidebar by its own width, keyed on its attribute", () => {
    expect(CLEAR_SIDEBAR).toContain(
      `lg:[:root:has([${SIDEBAR_COLLAPSED_ATTR}])_&]:left-${size(SIDEBAR_WIDTH.collapsed)}`,
    );
  });
});
