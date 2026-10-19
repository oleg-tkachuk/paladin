import { describe, expect, it } from "vitest";

import { render, screen } from "@/test/utils";

import { Confirmation } from "./Confirmation";

describe("Confirmation", () => {
  // The live region is there before the words, so they are announced.
  it("keeps its live region, and says the words once shown", () => {
    const { rerender } = render(
      <Confirmation shown={false}>Saved</Confirmation>,
    );
    const status = screen.getByRole("status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveTextContent("");
    rerender(<Confirmation shown>Saved</Confirmation>);
    expect(screen.getByRole("status")).toHaveTextContent("Saved");
  });
});
