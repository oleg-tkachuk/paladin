import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { SelectionBar } from "./SelectionBar";

describe("SelectionBar", () => {
  it("renders nothing while nothing is selected", () => {
    render(
      <SelectionBar count={0} onClear={vi.fn()}>
        <button>Delete</button>
      </SelectionBar>,
    );
    expect(screen.queryByRole("toolbar")).not.toBeInTheDocument();
  });

  it("names the count and carries the actions in one toolbar", () => {
    render(
      <SelectionBar count={3} onClear={vi.fn()}>
        <button>Delete</button>
      </SelectionBar>,
    );
    const bar = screen.getByRole("toolbar", { name: "Selection actions" });
    expect(bar).toHaveTextContent("3 selected");
    expect(bar).toContainElement(
      screen.getByRole("button", { name: "Delete" }),
    );
  });

  it("drops the selection from its clear button", async () => {
    const onClear = vi.fn();
    render(
      <SelectionBar count={1} onClear={onClear}>
        <button>Delete</button>
      </SelectionBar>,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Clear selection" }),
    );
    expect(onClear).toHaveBeenCalledOnce();
  });

  // The object inspector is z-60: a bar above it would cover the
  // inspector's own actions.
  it("stays below the inspector panel", () => {
    render(
      <SelectionBar count={1} onClear={vi.fn()}>
        <button>Delete</button>
      </SelectionBar>,
    );
    expect(screen.getByRole("toolbar").parentElement).toHaveClass("z-50");
  });
});
