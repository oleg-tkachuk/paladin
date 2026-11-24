import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import { csvExport } from "@/lib/table/csv";

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

  it("sets the page's actions after the filters and the count", () => {
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
          onChange: () => {},
        }}
        count={{ shown: 1, total: 1 }}
        actions={<button type="button">New row</button>}
      >
        <button type="button">Plane</button>
      </TableToolbar>,
    );
    const order = screen.getAllByRole("button").map((b) => b.textContent);
    expect(order).toEqual(["Plane", "Active", "All", "New row"]);
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

  it("offers a menu per facet, after the page's own filters", () => {
    const facet = (id: string, label: string) => ({
      id,
      label,
      options: [],
      selected: [],
      onChange: () => {},
    });
    render(
      <TableToolbar
        query=""
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
        facets={[facet("state", "State"), facet("health", "Health")]}
      >
        <button type="button">Plane</button>
      </TableToolbar>,
    );
    expect(
      screen.getAllByRole("button").map((b) => b.getAttribute("aria-label")),
    ).toEqual([null, "Filter by state", "Filter by health"]);
  });

  describe("export", () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    const NAME = [{ header: "Name", text: (r: { n: string }) => r.n }];
    const toolbar = (rows: { n: string }[]) => (
      <TableToolbar
        query=""
        onQueryChange={() => {}}
        placeholder="Search…"
        searchLabel="Search rows"
        csv={csvExport("users", NAME, rows)}
      />
    );

    it("offers no export unless the page hands one over", () => {
      render(
        <TableToolbar
          query=""
          onQueryChange={() => {}}
          placeholder="Search…"
          searchLabel="Search rows"
        />,
      );
      expect(
        screen.queryByRole("button", { name: "Export as CSV" }),
      ).toBeNull();
    });

    it("cannot export a list that shows no rows", () => {
      render(toolbar([]));
      expect(
        screen.getByRole("button", { name: "Export as CSV" }),
      ).toBeDisabled();
    });

    it("saves the rows as a UTF-8 CSV named after the list", async () => {
      const blobs: Blob[] = [];
      URL.createObjectURL = vi.fn((b: Blob) => {
        blobs.push(b);
        return "blob:x";
      });
      URL.revokeObjectURL = vi.fn();
      const names: string[] = [];
      vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(
        function (this: HTMLAnchorElement) {
          names.push(this.download);
        },
      );
      render(toolbar([{ n: "alice" }, { n: "=bob" }]));
      fireEvent.click(screen.getByRole("button", { name: "Export as CSV" }));

      expect(names).toHaveLength(1);
      expect(names[0]).toMatch(/^users-\d{8}-\d{6}\.csv$/);
      expect(blobs[0].type).toBe("text/csv;charset=utf-8");
      const bytes = new Uint8Array(await blobs[0].arrayBuffer());
      // The byte order mark, then the text.
      expect([...bytes.slice(0, 3)]).toEqual([0xef, 0xbb, 0xbf]);
      expect(new TextDecoder().decode(bytes)).toBe(
        `"Name"\r\n"alice"\r\n"'=bob"\r\n`,
      );
    });
  });
});
