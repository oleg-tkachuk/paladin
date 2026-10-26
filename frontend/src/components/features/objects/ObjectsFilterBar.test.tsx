import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { FacetControl } from "@/hooks/useTableFacets";
import { OBJECT_FACETS } from "@/lib/objectFacets";
import { FACET_COUNTING } from "@/lib/table/facets";

import { ObjectsFilterBar, type FilterState } from "./ObjectsFilterBar";

const VIEW = { name: "Pending only", filters: { status: "pending" } };

// The page's two facets, uncounted, each with its own setter to assert on.
function facetControls(): FacetControl[] {
  return OBJECT_FACETS.map((f) => ({
    id: f.id,
    label: f.label,
    options: (f.options ?? []).map((o) => ({ ...o, count: 0 })),
    selected: [],
    onChange: vi.fn(),
    counting: FACET_COUNTING.withheld,
  }));
}

function bar(filter: Partial<FilterState> = {}) {
  const props = {
    filter: {
      search: "",
      tag: undefined,
      meta: undefined,
      recursive: false,
      ...filter,
    },
    onFilterChange: vi.fn(),
    facets: facetControls(),
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
  it("offers every content category as a facet, Other among them", async () => {
    const props = bar();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by content type" }),
    );
    const options = screen
      .getAllByRole("menuitemcheckbox")
      .map((o) => o.getAttribute("aria-label"));
    expect(options).toEqual([
      "Images",
      "Video",
      "Audio",
      "Text",
      "PDF",
      "JSON",
      "Other",
    ]);
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: "Images" }),
    );
    expect(props.facets[1]!.onChange).toHaveBeenLastCalledWith(["image"]);
    expect(props.onFilterChange).not.toHaveBeenCalled();
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

describe("ObjectsFilterBar status", () => {
  // A failed object is deleted, so there is no FAILED state to filter on.
  it("offers the states an object can be in, and no Failed", async () => {
    bar();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by status" }),
    );
    const options = screen
      .getAllByRole("menuitemcheckbox")
      .map((o) => o.getAttribute("aria-label"));
    expect(options).toEqual(["Pending", "Available", "Deleted"]);
  });
});

// The refresh sat last in the wrapping row of filters and fell onto a line
// of its own; it belongs with the search, on the line every width keeps.
describe("ObjectsFilterBar refresh", () => {
  it("sits in the search row, named, and refreshes", async () => {
    const props = bar();
    const row = screen.getByTestId("objects-search-row");
    expect(
      within(row).getByPlaceholderText(/Search objects/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(row).getByRole("button", { name: "Refresh the object list" }),
    );
    expect(props.onRefresh).toHaveBeenCalled();
  });
});
