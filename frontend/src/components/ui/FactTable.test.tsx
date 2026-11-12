import { describe, expect, it } from "vitest";

import { render, screen, within } from "@/test/utils";

import { FactTable, NO_FACT } from "./FactTable";

const matrix = {
  columns: ["every", "last run", "took"],
  rows: [
    { label: "api_token_purger", cells: ["1h0m0s", "30m24s ago", "139ms"] },
    { label: "lifecycle", cells: ["30m0s", "not yet", undefined] },
  ],
};

describe("FactTable", () => {
  it("sets out a row to a thing, its label as the row's header, a fact to a column", () => {
    render(<FactTable matrix={matrix} />);
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual(["", "every", "last run", "took"]);
    const row = screen.getByRole("row", { name: /api_token_purger/ });
    expect(within(row).getByRole("rowheader")).toHaveTextContent(
      "api_token_purger",
    );
    expect(
      within(row)
        .getAllByRole("cell")
        .map((c) => c.textContent),
    ).toEqual(["1h0m0s", "30m24s ago", "139ms"]);
  });

  it("marks a fact a row does not report, muted", () => {
    render(<FactTable matrix={matrix} />);
    const row = screen.getByRole("row", { name: /lifecycle/ });
    const missing = within(row).getAllByRole("cell")[2];
    expect(missing).toHaveTextContent(NO_FACT);
    expect(missing).toHaveClass("text-muted-foreground");
  });

  it("never wraps a cell, and scrolls rather than breaking a row", () => {
    const { container } = render(<FactTable matrix={matrix} />);
    for (const cell of screen.getAllByRole("cell")) {
      expect(cell).toHaveClass("whitespace-nowrap");
    }
    for (const h of screen.getAllByRole("rowheader")) {
      expect(h).toHaveClass("whitespace-nowrap");
    }
    expect(container.firstChild).toHaveClass("overflow-x-auto");
  });
});
