import { describe, expect, it } from "vitest";

import { render, screen } from "@/test/utils";

import { formatCompactCount } from "@/lib/format/locale";

import { Count } from "./Count";

describe("Count", () => {
  it("shows a large count short, with the exact count on hover", () => {
    render(<Count value={12_345n} />);
    const count = screen.getByText(formatCompactCount(12_345n));
    expect(count.textContent).not.toBe("12,345");
    expect(count).toHaveAttribute("title", "12,345");
    expect(count).toHaveClass("tabular-nums");
  });

  it("shows a small count whole, with nothing to reveal", () => {
    render(<Count value={9_999} />);
    expect(screen.getByText("9,999")).not.toHaveAttribute("title");
  });
});
