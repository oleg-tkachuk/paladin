import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import { TableToolbar } from "./TableToolbar";

const FILTER_OPTIONS = [
  { value: "active", label: "Active" },
  { value: "all", label: "All" },
] as const;

describe("TableToolbar", () => {
  it("hands every edit of the search back, and clears it", () => {
    const onQueryChange = vi.fn();
    const { rerender } = render(
      <TableToolbar
        query=""
        onQueryChange={onQueryChange}
        placeholder="Search…"
        searchLabel="Search rows"
      />,
    );
    const box = screen.getByRole("searchbox", { name: "Search rows" });
    expect(
      screen.queryByRole("button", { name: "Clear the search" }),
    ).toBeNull();
    fireEvent.change(box, { target: { value: "abc" } });
    expect(onQueryChange).toHaveBeenLastCalledWith("abc");

    rerender(
      <TableToolbar
        query="abc"
        onQueryChange={onQueryChange}
        placeholder="Search…"
        searchLabel="Search rows"
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear the search" }));
    expect(onQueryChange).toHaveBeenLastCalledWith("");
  });

  it("marks the chosen filter and hands a new choice back", () => {
    const onChange = vi.fn();
    render(
      <TableToolbar
        query=""
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
        filter={{
          label: "State",
          options: FILTER_OPTIONS,
          value: "active",
          onChange,
        }}
      />,
    );
    const group = screen.getByRole("group", { name: "State" });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Active" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByRole("button", { name: "All" }));
    expect(onChange).toHaveBeenCalledWith("all");
  });

  it("sets the page's own filters in the bar", () => {
    render(
      <TableToolbar
        query=""
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
      >
        <button type="button">Plane</button>
      </TableToolbar>,
    );
    expect(screen.getByRole("button", { name: "Plane" })).toBeInTheDocument();
  });

  it("counts the rows showing out of those loaded", () => {
    const { rerender } = render(
      <TableToolbar
        query=""
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
        count={{ shown: 3, total: 3 }}
      />,
    );
    expect(screen.getByText("3 shown")).toBeInTheDocument();
    rerender(
      <TableToolbar
        query="x"
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
        count={{ shown: 1, total: 3 }}
      />,
    );
    expect(screen.getByText("1 of 3")).toBeInTheDocument();
  });
});
