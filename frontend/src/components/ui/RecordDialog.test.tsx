import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import { RecordDialog, RecordField, openableRow } from "./RecordDialog";

function Row({ open }: { open: () => void }) {
  return (
    <table>
      <tbody>
        <tr {...openableRow(open, "Open record r-1")}>
          <td>
            r-1 <button type="button">copy</button>
          </td>
        </tr>
      </tbody>
    </table>
  );
}

describe("openableRow", () => {
  it.each(["Enter", " "])("opens on %j and on a click", (key) => {
    const open = vi.fn();
    render(<Row open={open} />);
    const row = screen.getByRole("button", { name: "Open record r-1" });
    fireEvent.keyDown(row, { key });
    fireEvent.click(row);
    expect(open).toHaveBeenCalledTimes(2);
  });

  it("ignores other keys", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Open record r-1" }), {
      key: "a",
    });
    expect(open).not.toHaveBeenCalled();
  });

  it("leaves a key pressed on a control inside the row to that control", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "copy" }), {
      key: "Enter",
    });
    expect(open).not.toHaveBeenCalled();
  });
});

describe("RecordDialog", () => {
  it("shows the title, the identifier and the fields", () => {
    render(
      <RecordDialog open onOpenChange={() => {}} title="Session" subtitle="s-1">
        <RecordField label="Agent" mono>
          agent-7
        </RecordField>
      </RecordDialog>,
    );
    const dialog = screen.getByRole("dialog", { name: "Session" });
    expect(dialog).toHaveAccessibleDescription("s-1");
    expect(screen.getByText("agent-7")).toHaveClass("break-all");
  });

  it("describes itself by its title when it has no identifier", () => {
    render(
      <RecordDialog open onOpenChange={() => {}} title="Session">
        <RecordField label="Agent">agent 7</RecordField>
      </RecordDialog>,
    );
    expect(screen.getByRole("dialog")).toHaveAccessibleDescription("Session");
    expect(screen.getByText("agent 7")).toHaveClass("break-words");
  });

  it("asks to close on Escape", () => {
    const onOpenChange = vi.fn();
    render(
      <RecordDialog open onOpenChange={onOpenChange} title="Session">
        <RecordField label="Agent">agent 7</RecordField>
      </RecordDialog>,
    );
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
