import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { Badge } from "./badge";

// A fixed h-5 held a 20px line plus 2px padding, so a badge given text-sm
// (20px line height) clipped its own text. The height is a floor now.
describe("Badge", () => {
  it("grows with its text instead of clipping it", () => {
    render(<Badge className="text-sm">active</Badge>);
    const badge = screen.getByText("active");
    expect(badge.classList).toContain("min-h-5");
    expect(badge.classList).not.toContain("h-5");
  });
});
