import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Stand-ins that say which arrow was drawn.
vi.mock("@heroicons/react/24/outline", () => ({
  ArrowsUpDownIcon: () => <svg data-testid="unsorted" />,
  ArrowUpIcon: () => <svg data-testid="asc" />,
  ArrowDownIcon: () => <svg data-testid="desc" />,
}));

import { SortableHead } from "./SortHeader";
import type { SortState } from "@/hooks/useTableSort";

type Col = "name" | "size";

function head(current: SortState<Col>, onSort = vi.fn()) {
  render(
    <table>
      <thead>
        <tr>
          <SortableHead<Col>
            label="Name"
            column="name"
            current={current}
            onSort={onSort}
          />
        </tr>
      </thead>
    </table>,
  );
  return onSort;
}

describe("SortableHead", () => {
  // aria-sort is read from the header cell; without it a screen reader could
  // not tell which column the table was sorted on, or which way.
  it.each([
    ["unsorted", "none", { column: null, direction: null }],
    ["unsorted", "none", { column: "size", direction: "asc" }],
    ["asc", "ascending", { column: "name", direction: "asc" }],
    ["desc", "descending", { column: "name", direction: "desc" }],
  ] as const)("draws %s and says %s for %o", (icon, ariaSort, current) => {
    head(current);
    expect(screen.getByTestId(icon)).toBeInTheDocument();
    expect(screen.getByRole("columnheader")).toHaveAttribute(
      "aria-sort",
      ariaSort,
    );
  });

  it("hands its column back on click", async () => {
    const onSort = head({ column: null, direction: null });
    await userEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(onSort).toHaveBeenCalledWith("name");
  });
});
