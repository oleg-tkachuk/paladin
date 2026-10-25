import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { CONTENT_FITS_WIDTH, ScrollArea } from "./scroll-area";

// Radix wraps the content in a display: table box, which grows to its widest
// line, so a long sidebar name pushed the controls beside it past the edge.
describe("ScrollArea", () => {
  it("holds its content to the viewport's width", () => {
    render(
      <ScrollArea>
        <p>a very long resource name that would otherwise widen the area</p>
      </ScrollArea>,
    );
    const viewport = screen
      .getByText(/very long resource name/)
      .closest("[data-slot=scroll-area-viewport]");
    expect(viewport?.className).toContain(CONTENT_FITS_WIDTH);
  });
});
