import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@/test/utils";

import type { Pagination } from "@/hooks/useTablePage";

import { TablePagination } from "./TablePagination";

const props = (over: Partial<Pagination> = {}): Pagination => ({
  page: 2,
  pageCount: 3,
  pageSize: 50,
  total: 120,
  first: 51,
  last: 100,
  onPage: vi.fn(),
  onPageSize: vi.fn(),
  ...over,
});

describe("TablePagination", () => {
  it("says which rows show, and moves a page either way", async () => {
    const p = props();
    render(<TablePagination {...p} />);
    expect(screen.getByRole("navigation", { name: "Pages" })).toHaveTextContent(
      "51–100 of 120",
    );
    expect(screen.getByText("Page 2 of 3")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(p.onPage).toHaveBeenCalledWith(3);
    await userEvent.click(
      screen.getByRole("button", { name: "Previous page" }),
    );
    expect(p.onPage).toHaveBeenCalledWith(1);
  });

  it("offers no step past either end", () => {
    const { rerender } = render(<TablePagination {...props({ page: 1 })} />);
    expect(
      screen.getByRole("button", { name: "Previous page" }),
    ).toBeDisabled();
    rerender(<TablePagination {...props({ page: 3 })} />);
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  });

  it("hands a new page size back", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const p = props();
    render(<TablePagination {...p} />);
    await user.click(screen.getByRole("combobox", { name: "Rows per page" }));
    await user.click(await screen.findByRole("option", { name: "All" }));
    expect(p.onPageSize).toHaveBeenCalledWith("all");
  });

  it("shows no page steps when one page holds every row", () => {
    render(
      <TablePagination
        {...props({ page: 1, pageCount: 1, total: 30, first: 1, last: 30 })}
      />,
    );
    expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
    expect(screen.getByText("1–30 of 30")).toBeInTheDocument();
  });

  it("is not there for a list the smallest page holds", () => {
    render(
      <TablePagination
        {...props({ page: 1, pageCount: 1, total: 8, first: 1, last: 8 })}
      />,
    );
    expect(screen.queryByRole("navigation")).toBeNull();
  });
});
