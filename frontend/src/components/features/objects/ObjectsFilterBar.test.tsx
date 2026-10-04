import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ObjectsFilterBar, type FilterState } from "./ObjectsFilterBar";

const VIEW = { name: "Pending only", filters: { status: "pending" } };

function bar(filter: Partial<FilterState> = {}) {
  const props = {
    filter: {
      search: "",
      status: undefined,
      tag: undefined,
      type: undefined,
      meta: undefined,
      recursive: false,
      ...filter,
    },
    onFilterChange: vi.fn(),
    tagOptions: [],
    visibleColumns: new Set<string>(),
    onToggleColumn: vi.fn(),
    savedViews: [VIEW],
    onApplyView: vi.fn(),
    onDeleteView: vi.fn(),
    onSaveView: vi.fn(),
    loading: false,
    onRefresh: vi.fn(),
  };
  render(<ObjectsFilterBar {...props} />);
  return props;
}

describe("ObjectsFilterBar saved views", () => {
  // The click handler sat on a div inside the menu item, so Enter on the item
  // did nothing: a saved view could be applied with a mouse only.
  it("applies a view from the keyboard", async () => {
    const props = bar();
    await userEvent.click(screen.getByRole("button", { name: /Views/ }));
    screen.getByRole("menuitem", { name: /Pending only/ }).focus();
    await userEvent.keyboard("{Enter}");
    expect(props.onApplyView).toHaveBeenCalledWith(VIEW);
  });

  it("names the delete button, and deleting does not apply the view", async () => {
    const props = bar();
    await userEvent.click(screen.getByRole("button", { name: /Views/ }));
    await userEvent.click(
      screen.getByRole("button", { name: "Delete view Pending only" }),
    );
    expect(props.onDeleteView).toHaveBeenCalledWith("Pending only");
    expect(props.onApplyView).not.toHaveBeenCalled();
  });
});

describe("ObjectsFilterBar content type and metadata", () => {
  it("offers the content-type categories and clears with All Types", async () => {
    const props = bar();
    const select = screen.getByRole("combobox", {
      name: "Filter by content type",
    });
    await userEvent.click(select);
    await userEvent.click(screen.getByRole("option", { name: "Images" }));
    expect(props.onFilterChange).toHaveBeenLastCalledWith({ type: "image" });
  });

  it("applies a metadata facet on Enter, not per keystroke", async () => {
    const props = bar();
    const input = screen.getByRole("textbox", { name: "Filter by metadata" });
    await userEvent.type(input, " owner =ops");
    expect(props.onFilterChange).not.toHaveBeenCalled();
    await userEvent.keyboard("{Enter}");
    expect(props.onFilterChange).toHaveBeenCalledTimes(1);
    expect(props.onFilterChange).toHaveBeenCalledWith({ meta: "owner=ops" });
  });

  it("leaves text without a key unapplied, and clears on an empty field", async () => {
    const props = bar({ meta: "owner=ops" });
    const input = screen.getByRole("textbox", { name: "Filter by metadata" });
    expect(input).toHaveValue("owner=ops");
    await userEvent.clear(input);
    await userEvent.type(input, "no-separator");
    await userEvent.tab();
    expect(props.onFilterChange).not.toHaveBeenCalled();
    await userEvent.clear(input);
    await userEvent.tab();
    expect(props.onFilterChange).toHaveBeenCalledWith({ meta: undefined });
  });
});

describe("ObjectsFilterBar layout", () => {
  // The status, type and metadata filters sat in a row that could not wrap,
  // so on a narrow screen the metadata field ran past the card and the window.
  // jsdom has no layout, so this pins the class that lets the row wrap; the
  // overflow itself was measured in a browser.
  it("lets the filter row wrap", () => {
    bar();
    const row = screen.getByLabelText("Filter by metadata").parentElement!;
    expect(row).toHaveClass("flex-wrap");
  });
});
