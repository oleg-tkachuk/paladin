import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import { Checkbox } from "@/components/ui/checkbox";
import { TableScope } from "@/components/ui/TableScope";
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";

// The row keys, through the shared Table: every list in a TableScope gets
// them, and a table outside one does not.

const NAMES = ["alpha", "beta", "gamma"];

function Rows({
  onOpen = () => {},
  onSelect = () => {},
  keyboardRows,
}: {
  onOpen?: (name: string) => void;
  onSelect?: (name: string) => void;
  keyboardRows?: boolean;
}) {
  return (
    <Table keyboardRows={keyboardRows}>
      <TableBody>
        {NAMES.map((n) => (
          <TableRow key={n} aria-label={n}>
            <TableCell>
              <Checkbox
                aria-label={`Select ${n}`}
                onCheckedChange={() => onSelect(n)}
              />
            </TableCell>
            <TableCell>
              <a
                href={`/things/${n}`}
                onClick={(e) => {
                  e.preventDefault();
                  onOpen(n);
                }}
              >
                {n}
              </a>
            </TableCell>
          </TableRow>
        ))}
        {/* An empty state's row spans the table; the keys pass it by. */}
        <TableRow aria-label="note">
          <TableCell colSpan={2}>note</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  );
}

const inScope = (ui: React.ReactNode) =>
  render(<TableScope id="things">{ui}</TableScope>);
const press = (key: string, target: Element = document.body) =>
  fireEvent.keyDown(target, { key });
const row = (name: string) => screen.getByRole("row", { name });

describe("useTableKeyboard", () => {
  it("walks the rows with j and k, and not past either end", () => {
    inScope(<Rows />);
    press("j");
    expect(row("alpha")).toHaveFocus();
    press("j");
    press("j");
    press("j");
    expect(row("gamma")).toHaveFocus();
    press("k");
    expect(row("beta")).toHaveFocus();
    press("k");
    press("k");
    expect(row("alpha")).toHaveFocus();
  });

  it("takes the arrows only once a row has the focus", () => {
    inScope(<Rows />);
    // The page's own scrolling, until then.
    expect(press("ArrowDown")).toBe(true);
    expect(document.body).toHaveFocus();
    press("j");
    expect(press("ArrowDown", row("alpha"))).toBe(false);
    expect(row("beta")).toHaveFocus();
    press("ArrowUp", row("beta"));
    expect(row("alpha")).toHaveFocus();
  });

  it("opens the row's record on Enter", () => {
    const onOpen = vi.fn();
    inScope(<Rows onOpen={onOpen} />);
    press("j");
    press("j");
    press("Enter", row("beta"));
    expect(onOpen).toHaveBeenCalledExactlyOnceWith("beta");
  });

  it("leaves Enter on a control inside the row to the control", () => {
    const onOpen = vi.fn();
    inScope(<Rows onOpen={onOpen} />);
    press("Enter", screen.getByRole("checkbox", { name: "Select alpha" }));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("selects the row on x, and lets go of it on Esc", () => {
    const onSelect = vi.fn();
    inScope(<Rows onSelect={onSelect} />);
    press("j");
    press("x", row("alpha"));
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("alpha");
    press("Escape", row("alpha"));
    expect(row("alpha")).not.toHaveFocus();
  });

  it("is off while a field has the focus", () => {
    inScope(
      <>
        <input aria-label="search" />
        <Rows />
      </>,
    );
    const field = screen.getByLabelText("search");
    field.focus();
    press("j", field);
    expect(field).toHaveFocus();
  });

  it("is off inside a dialog or a menu over the list", () => {
    inScope(
      <>
        <div role="dialog">
          <button type="button">Close</button>
        </div>
        <Rows />
      </>,
    );
    const close = screen.getByRole("button", { name: "Close" });
    close.focus();
    press("j", close);
    expect(close).toHaveFocus();
  });

  it("gives way to a key another handler has claimed", () => {
    inScope(<Rows />);
    const claim = (e: KeyboardEvent) => e.preventDefault();
    document.addEventListener("keydown", claim);
    press("j");
    document.removeEventListener("keydown", claim);
    expect(document.body).toHaveFocus();
  });

  it("is off for a table outside a TableScope, unless asked", () => {
    const { unmount } = render(<Rows />);
    press("j");
    expect(document.body).toHaveFocus();
    unmount();
    render(<Rows keyboardRows />);
    press("j");
    expect(row("alpha")).toHaveFocus();
  });
});
