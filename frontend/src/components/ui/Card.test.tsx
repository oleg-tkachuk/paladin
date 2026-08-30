import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { Card, CardHeader, CardTitle } from "./Card";

// A card title is how the console names a section, so it has to be a heading:
// heading navigation is the primary way a screen-reader user moves through a
// page, and a `<div>` is invisible to it. These assertions are about the tag,
// not the text — getByText passed the whole time the bug was live.
describe("CardTitle", () => {
  it("is a heading, at h2 by default", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Preferences</CardTitle>
        </CardHeader>
      </Card>,
    );
    // getByRole("heading") is the assertion that matters: it is the same
    // lookup assistive technology performs, so it fails on a styled div.
    const heading = screen.getByRole("heading", { name: "Preferences" });
    expect(heading.tagName).toBe("H2");
  });

  it("lets the caller pick the level, for a card nested inside a section", () => {
    render(<CardTitle level={3}>Buckets on this backend</CardTitle>);
    expect(
      screen.getByRole("heading", {
        name: "Buckets on this backend",
        level: 3,
      }),
    ).toBeInTheDocument();
  });

  it("keeps its styling hook and forwards className", () => {
    render(<CardTitle className="text-sm">Password</CardTitle>);
    const heading = screen.getByRole("heading", { name: "Password" });
    expect(heading).toHaveAttribute("data-slot", "card-title");
    expect(heading).toHaveClass("text-sm", "font-heading");
  });
});
