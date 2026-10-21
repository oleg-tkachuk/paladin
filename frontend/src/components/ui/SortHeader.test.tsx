import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Stand-ins that say which arrow was drawn.
vi.mock("@heroicons/react/24/outline", () => ({
  ArrowsUpDownIcon: () => <svg data-testid="unsorted" />,
  ArrowUpIcon: () => <svg data-testid="asc" />,
  ArrowDownIcon: () => <svg data-testid="desc" />,
}));

import { HEAD_LABEL, SortableHead, StaticHead } from "./SortHeader";
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

// A column that does not sort reads as a header beside the ones that do: the
// audit log's Request header was plain text in a row of capitals.
describe("StaticHead", () => {
  it("styles its label as a sortable header's", () => {
    head({ column: null, direction: null });
    render(
      <table>
        <thead>
          <tr>
            <StaticHead>Request</StaticHead>
          </tr>
        </thead>
      </table>,
    );
    const sortable = screen.getByRole("button", { name: /Name/ });
    const plain = screen.getByRole("columnheader", { name: "Request" });
    for (const cls of HEAD_LABEL.split(" ")) {
      expect(sortable).toHaveClass(cls);
      expect(plain).toHaveClass(cls);
    }
  });
});
