import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ObjectsFilterBar } from "./ObjectsFilterBar";

const VIEW = { name: "Pending only", filters: { status: "pending" } };

function bar() {
  const props = {
    filter: {
      search: "",
      status: undefined,
      tag: undefined,
      recursive: false,
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
