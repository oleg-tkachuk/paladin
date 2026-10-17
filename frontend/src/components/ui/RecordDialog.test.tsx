import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import {
  RecordBlock,
  RecordDialog,
  RecordField,
  openableRow,
} from "./RecordDialog";

function Row({ open }: { open: () => void }) {
  return (
    <table>
      <tbody>
        <tr {...openableRow(open)}>
          <td>
            r-1 <button type="button">copy</button>{" "}
            <a href="#r-1">
              <span>link</span>
            </a>
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
    const row = screen.getByRole("row", { name: /r-1/ });
    fireEvent.keyDown(row, { key });
    fireEvent.click(row);
    expect(open).toHaveBeenCalledTimes(2);
  });

  it("stays a row of its table that says it opens a dialog", () => {
    render(<Row open={vi.fn()} />);
    const row = screen.getByRole("row", { name: /r-1/ });
    expect(row).toHaveAttribute("aria-haspopup", "dialog");
    expect(row).toHaveAttribute("tabindex", "0");
  });

  it("ignores other keys", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.keyDown(screen.getByRole("row", { name: /r-1/ }), {
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

describe("openableRow clicks", () => {
  it.each(["copy", "link"])("leaves a click on the row's %s to it", (name) => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.click(screen.getByText(name));
    expect(open).not.toHaveBeenCalled();
  });

  it("opens on a click on the row's text", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.click(screen.getByRole("cell"));
    expect(open).toHaveBeenCalledOnce();
  });
});

describe("RecordBlock", () => {
  it("shows a long value under its label, wrapped", () => {
    render(<RecordBlock label="Before">{'{"a": 1}'}</RecordBlock>);
    expect(screen.getByText("Before")).toBeInTheDocument();
    expect(screen.getByText('{"a": 1}')).toHaveClass("whitespace-pre-wrap");
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
