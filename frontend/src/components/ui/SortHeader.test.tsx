import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Stand-ins that say which arrow was drawn.
vi.mock("@heroicons/react/24/outline", () => ({
  ArrowsUpDownIcon: () => <svg data-testid="unsorted" />,
  ArrowUpIcon: () => <svg data-testid="asc" />,
  ArrowDownIcon: () => <svg data-testid="desc" />,
}));

import { SortHeader } from "./SortHeader";
import type { SortState } from "@/hooks/useTableSort";

type Col = "name" | "size";

function header(current: SortState<Col>, onSort = vi.fn()) {
  render(
    <SortHeader<Col>
      label="Name"
      column="name"
      current={current}
      onSort={onSort}
    />,
  );
  return onSort;
}

describe("SortHeader", () => {
  it.each([
    ["unsorted", { column: null, direction: null }],
    ["unsorted", { column: "size", direction: "asc" }],
    ["asc", { column: "name", direction: "asc" }],
    ["desc", { column: "name", direction: "desc" }],
  ] as const)("draws %s for %o", (icon, current) => {
    header(current);
    expect(screen.getByTestId(icon)).toBeInTheDocument();
  });

  it("hands its column back on click", async () => {
    const onSort = header({ column: null, direction: null });
    await userEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(onSort).toHaveBeenCalledWith("name");
  });
});
