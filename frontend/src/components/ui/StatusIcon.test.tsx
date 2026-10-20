import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { STATUS_TONE, StatusTone } from "@/lib/ui/status";

import { StatusDot, StatusIcon, StatusLabel } from "./StatusIcon";

describe("StatusIcon", () => {
  it.each(Object.values(StatusTone))("draws the %s tone in its ink", (tone) => {
    const { container } = render(<StatusIcon tone={tone} />);
    const icon = container.querySelector('[data-slot="status-icon"]');
    expect(icon).toHaveClass(STATUS_TONE[tone].ink);
    expect(icon).toHaveAttribute("data-tone", tone);
  });

  it("gives every tone its own glyph, so no state rests on colour alone", () => {
    const glyphs = new Set(Object.values(STATUS_TONE).map((t) => t.icon));
    expect(glyphs.size).toBe(Object.values(StatusTone).length);
  });

  it("is decoration without a label and an image with one", () => {
    const { container, rerender } = render(<StatusIcon tone="success" />);
    expect(container.firstElementChild).toHaveAttribute("aria-hidden", "true");
    rerender(<StatusIcon tone="danger" label="Unreachable" />);
    expect(screen.getByRole("img", { name: "Unreachable" })).toBeVisible();
  });
});

describe("StatusDot", () => {
  it("fills the dot with its tone and hides it from a screen reader", () => {
    const { container } = render(<StatusDot tone="suspended" />);
    const dot = container.firstElementChild;
    expect(dot).toHaveClass(STATUS_TONE.suspended.fill);
    expect(dot).toHaveAttribute("aria-hidden", "true");
  });
});

describe("StatusLabel", () => {
  it("names the state beside its glyph", () => {
    render(<StatusLabel tone="progress">Draining</StatusLabel>);
    const label = screen.getByText("Draining");
    expect(label).toHaveClass(STATUS_TONE.progress.ink);
    expect(label.querySelector('[data-slot="status-icon"]')).not.toBeNull();
  });
});
