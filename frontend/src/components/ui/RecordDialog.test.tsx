import type React from "react";
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
  it("shows a long value under its label, wrapped, and copies it", () => {
    render(<RecordBlock label="Before">{'{"a": 1}'}</RecordBlock>);
    expect(screen.getByText("Before")).toBeInTheDocument();
    expect(screen.getByText('{"a": 1}')).toHaveClass("whitespace-pre-wrap");
    expect(
      screen.getByRole("button", { name: "Copy before" }),
    ).toBeInTheDocument();
  });
});

// The dialog sizes itself to what it holds, by attributes its contents carry
// and classes its content reads them with. Tailwind finds a class only as
// literal text, so the two spellings are held together here.
describe("RecordDialog width", () => {
  const dialogFor = (body: React.ReactNode) => {
    render(
      <RecordDialog open onOpenChange={() => {}} title="Entry">
        {body}
      </RecordDialog>,
    );
    return screen.getByRole("dialog");
  };

  it("widens for a long field and for a block, by the attributes they carry", () => {
    const dialog = dialogFor(
      <>
        <RecordField label="Resource" mono>
          {
            "storageBackends/primary/buckets/paladin-e2e/tenants/0199/collections/docs"
          }
        </RecordField>
        <RecordBlock label="After">{"{}"}</RecordBlock>
      </>,
    );
    for (const attr of ["data-record-wide", "data-record-block"]) {
      expect(dialog.querySelector(`[${attr}]`)).not.toBeNull();
      expect(dialog.className).toContain(`has-[[${attr}]]`);
    }
  });

  it("keeps a short field beside its label, in the shared column", () => {
    const dialog = dialogFor(
      <RecordField label="Audience">paladin-iam</RecordField>,
    );
    expect(dialog.querySelector("[data-record-wide]")).toBeNull();
    expect(screen.getByText("Audience").parentElement).toHaveClass(
      "sm:grid-cols-subgrid",
    );
  });

  it("puts a value under its label when told to, whatever its length", () => {
    const dialog = dialogFor(
      <RecordField label="Note" wide>
        short
      </RecordField>,
    );
    expect(dialog.querySelector("[data-record-wide]")).not.toBeNull();
  });
});

describe("RecordField copy", () => {
  it("copies a plain identifier, and says it did", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(
      <RecordDialog open onOpenChange={() => {}} title="Entry">
        <RecordField label="Request" mono>
          req-42
        </RecordField>
        <RecordField label="Actor">
          <span>Alice</span>
        </RecordField>
      </RecordDialog>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Copy request" }));
    expect(writeText).toHaveBeenCalledWith("req-42");
    expect(
      await screen.findByRole("button", { name: "Copied" }),
    ).toBeInTheDocument();
    // A value that is not plain text — a name, a link — has nothing to copy.
    expect(screen.queryByRole("button", { name: "Copy actor" })).toBeNull();
  });

  it("copies the record's identifier from under the title", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(
      <RecordDialog
        open
        onOpenChange={() => {}}
        title="Audit entry"
        subtitle="01a1-id"
      >
        <RecordField label="Action">x</RecordField>
      </RecordDialog>,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Copy audit entry id" }),
    );
    expect(writeText).toHaveBeenCalledWith("01a1-id");
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
