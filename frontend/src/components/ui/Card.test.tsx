import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import {
  CARD_HEADER_BAND,
  CARD_PLANE,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "./Card";

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

// A card is a plane above the page: without its edge and shadow the console
// read as one flat sheet. The flat variants are the exception, by name.
describe("Card", () => {
  const classes = (list: string) => list.split(" ");

  it("stands off the page with an edge and a raised shadow", () => {
    render(<Card data-testid="card" />);
    expect(screen.getByTestId("card")).toHaveClass(...classes(CARD_PLANE));
  });

  it.each(["outline", "subtle", "glass"] as const)(
    "lies flat as the %s variant",
    (variant) => {
      render(<Card data-testid="card" variant={variant} />);
      expect(screen.getByTestId("card")).not.toHaveClass("shadow-raised");
    },
  );

  it("puts the header that opens it on a band of its own", () => {
    render(
      <Card>
        <CardHeader data-testid="header">
          <CardTitle>Recent activity</CardTitle>
        </CardHeader>
        <CardContent>rows</CardContent>
      </Card>,
    );
    expect(screen.getByTestId("header")).toHaveClass(
      ...classes(CARD_HEADER_BAND),
    );
  });
});

describe("CardTitle icon", () => {
  const Glyph = () => <svg data-testid="glyph" />;

  it("puts the icon on a chip before the title, named by the title alone", () => {
    render(
      <CardTitle icon={Glyph} tone="info">
        Specs
      </CardTitle>,
    );
    const heading = screen.getByRole("heading", { name: "Specs" });
    const chip = heading.querySelector('[data-slot="icon-chip"]');
    expect(chip).toHaveAttribute("data-tone", "info");
    expect(chip).toContainElement(screen.getByTestId("glyph"));
    expect(heading.firstElementChild).toBe(chip);
  });

  it("draws no chip without an icon", () => {
    render(<CardTitle>Specs</CardTitle>);
    expect(
      screen
        .getByRole("heading", { name: "Specs" })
        .querySelector('[data-slot="icon-chip"]'),
    ).toBeNull();
  });
});
