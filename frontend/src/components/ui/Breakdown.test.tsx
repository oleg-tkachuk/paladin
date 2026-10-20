import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { TagIcon } from "@heroicons/react/24/outline";

import { Breakdown, stateEntries } from "./Breakdown";

describe("Breakdown", () => {
  it("lists each value as a legend reads, with its count", () => {
    render(
      <Breakdown
        title="Provider"
        icon={TagIcon}
        entries={[
          { key: "aws", count: 3 },
          { key: "garage", count: 1200 },
        ]}
      />,
    );
    expect(
      screen.getByRole("heading", { name: "Provider" }),
    ).toBeInTheDocument();
    const items = screen.getAllByRole("listitem").map((li) => li.textContent);
    expect(items).toEqual(["aws (3)", "garage (1,200)"]);
  });

  it("marks a state with its tone's dot", () => {
    const { container } = render(
      <Breakdown
        title="Health"
        icon={TagIcon}
        entries={[{ key: "Probe failed", count: 1, tone: "danger" }]}
      />,
    );
    expect(
      container.querySelector('[data-slot="status-dot"][data-tone="danger"]'),
    ).not.toBeNull();
  });
});

describe("stateEntries", () => {
  it("puts what needs attention first and drops empty states", () => {
    expect(
      stateEntries([
        { tone: "success", label: "Healthy", count: 4 },
        { tone: "unknown", label: "Untested", count: 0 },
        { tone: "danger", label: "Probe failed", count: 1 },
      ]),
    ).toEqual([
      { key: "Probe failed", count: 1, tone: "danger" },
      { key: "Healthy", count: 4, tone: "success" },
    ]);
  });
});
