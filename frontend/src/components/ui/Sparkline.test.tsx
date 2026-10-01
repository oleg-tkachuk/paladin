import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";

import { Sparkline } from "./Sparkline";

// The sparkline drew in fixed hex colours that ignored the theme, and keyed its
// gradient on the colour name, so two sparklines on one page shared an id.
describe("Sparkline", () => {
  it("draws in the text colour, not a fixed one", () => {
    const { container } = render(<Sparkline data={[1, 3, 2]} />);
    const line = container.querySelector("path[stroke]");
    expect(line?.getAttribute("stroke")).toBe("currentColor");
    expect(container.querySelector("svg")?.getAttribute("class")).toContain(
      "text-primary",
    );
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{6}|rgba\(/i);
  });

  it("gives every instance its own gradient", () => {
    const { container } = render(
      <>
        <Sparkline data={[1, 2]} />
        <Sparkline data={[2, 1]} />
      </>,
    );
    const ids = [...container.querySelectorAll("linearGradient")].map(
      (g) => g.id,
    );
    expect(new Set(ids).size).toBe(2);
    const fills = [...container.querySelectorAll("path[fill^='url']")].map(
      (p) => p.getAttribute("fill"),
    );
    expect(fills).toEqual(ids.map((id) => `url(#${id})`));
  });

  it("draws a single point without dividing by zero", () => {
    const { container } = render(<Sparkline data={[5]} />);
    expect(container.innerHTML).not.toContain("NaN");
  });
});
