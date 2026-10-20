import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render as rtlRender, screen } from "@testing-library/react";

import { render } from "@/test/utils";

const h = vi.hoisted(() => ({ update: vi.fn() }));
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [{ tenants: { density: "compact" } }, vi.fn(), h.update],
}));

import { TableScope } from "./TableScope";
import { ViewModeSwitcher } from "./ViewModeSwitcher";

beforeEach(() => {
  h.update.mockReset();
  h.update.mockResolvedValue(undefined);
});

describe("ViewModeSwitcher", () => {
  it("says which view is on and changes it", () => {
    const onChange = vi.fn();
    rtlRender(<ViewModeSwitcher value="tiles" onChange={onChange} />);
    const group = screen.getByRole("group", { name: "View" });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Tiles" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Table" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByRole("button", { name: "Summary" }));
    expect(onChange).toHaveBeenCalledWith("summary");
  });

  // Saved as the table's view, beside its other preferences, so the list
  // opens in it next time; the default view is saved as none.
  it("saves the choice as the table's view inside a scope", () => {
    render(
      <TableScope id="tenants">
        <ViewModeSwitcher value="table" onChange={vi.fn()} />
      </TableScope>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Summary" }));
    const change = h.update.mock.calls[0][0] as (raw: unknown) => unknown;
    expect(change({ tenants: { density: "compact" } })).toEqual({
      tenants: { density: "compact", view: "summary" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Table" }));
    const back = h.update.mock.calls[1][0] as (raw: unknown) => unknown;
    expect(back({ tenants: { density: "compact", view: "summary" } })).toEqual({
      tenants: { density: "compact" },
    });
  });
});
