import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";

import { SpendChart } from "./SpendChart";
import type { SpendPoint } from "./spendSeries";

const day = (d: number): Date => new Date(Date.UTC(2026, 9, d));
const points: SpendPoint[] = [
  { start: day(1), value: 0, chargeCount: 0 },
  { start: day(2), value: 0.002, chargeCount: 2 },
  { start: day(3), value: 0, chargeCount: 0 },
  { start: day(4), value: 0.01, chargeCount: 7 },
];
const fmt = (v: number) => `$${v.toFixed(3)}`;

function chart() {
  return render(
    <SpendChart points={points} granularity="day" formatValue={fmt} />,
  );
}

describe("SpendChart", () => {
  it("gives every bucket a column and only billed ones a bar", () => {
    const { container } = chart();
    // One hit target per bucket, quiet buckets included.
    expect(container.querySelectorAll("rect")).toHaveLength(points.length);
    expect(container.querySelectorAll("path")).toHaveLength(2);
  });

  it("tops the axis at a round value above the peak", () => {
    const { container } = chart();
    const ticks = [...container.querySelectorAll("svg text")].map(
      (t) => t.textContent,
    );
    expect(ticks).toEqual(
      expect.arrayContaining(["$0.000", "$0.005", "$0.010"]),
    );
  });

  it("answers hover with the bucket, its spend and its charges", () => {
    const { container } = chart();
    fireEvent.mouseEnter(container.querySelectorAll("rect")[3]);
    const tip = screen.getByRole("status");
    expect(tip).toHaveTextContent("4 Oct 2026");
    expect(tip).toHaveTextContent("$0.010");
    expect(tip).toHaveTextContent("7 charges");
  });

  it("lets a keyboard reach the billed buckets only", () => {
    const { container } = chart();
    const focusable = [...container.querySelectorAll("rect")].map((r) =>
      r.getAttribute("tabindex"),
    );
    expect(focusable).toEqual(["-1", "0", "-1", "0"]);
  });

  it("lists the billed buckets in a table for screen readers", () => {
    chart();
    const table = screen.getByRole("table", { name: "Spend per day" });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(
      within(table).getByText("1 charge".replace("1 charge", "2")),
    ).toBeInTheDocument();
  });
});
