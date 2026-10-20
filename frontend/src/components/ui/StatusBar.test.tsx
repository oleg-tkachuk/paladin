import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "./Tooltip";
import { StatusBar, statusSegments, statusSummary } from "./StatusBar";

const SEGMENTS = [
  { tone: "success" as const, label: "Healthy", count: 6 },
  { tone: "danger" as const, label: "Error", count: 2 },
  { tone: "suspended" as const, label: "Maintenance", count: 0 },
];

describe("statusSegments", () => {
  it("drops empty states and puts what needs attention first", () => {
    expect(statusSegments(SEGMENTS).map((s) => s.label)).toEqual([
      "Error",
      "Healthy",
    ]);
  });

  it("says the tally in words", () => {
    expect(statusSummary(SEGMENTS)).toBe("2 Error · 6 Healthy");
  });
});

describe("StatusBar", () => {
  it("draws each state as wide as its share and names the tally", () => {
    const { container } = render(
      <TooltipProvider>
        <StatusBar segments={SEGMENTS} />
      </TooltipProvider>,
    );
    expect(
      screen.getByRole("img", { name: "2 Error · 6 Healthy" }),
    ).toBeVisible();
    const danger = container.querySelector('[data-tone="danger"]');
    expect(danger).toHaveStyle({ width: "25%" });
  });

  it("draws nothing for a list of one", () => {
    const { container } = render(
      <StatusBar
        segments={[{ tone: "success", label: "Healthy", count: 1 }]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
