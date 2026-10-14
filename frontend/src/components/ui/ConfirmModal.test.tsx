import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ConfirmModal } from "./ConfirmModal";

const baseProps = {
  isOpen: true,
  onClose: vi.fn(),
  onConfirm: vi.fn(),
  title: "Delete bucket?",
  message: "This cannot be undone.",
};

describe("ConfirmModal", () => {
  it("renders title + message with the alertdialog role when open", () => {
    render(
      <ConfirmModal {...baseProps} onClose={vi.fn()} onConfirm={vi.fn()} />,
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("Delete bucket?")).toBeInTheDocument();
    expect(screen.getByText("This cannot be undone.")).toBeInTheDocument();
  });

  it("renders nothing when closed", () => {
    render(
      <ConfirmModal
        {...baseProps}
        isOpen={false}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.queryByText("Delete bucket?")).not.toBeInTheDocument();
  });

  it("confirm runs onConfirm then onClose", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(
      <ConfirmModal
        {...baseProps}
        confirmText="Delete"
        onConfirm={onConfirm}
        onClose={onClose}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("cancel calls onClose only", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(
      <ConfirmModal {...baseProps} onConfirm={onConfirm} onClose={onClose} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("disables actions and shows progress while loading", () => {
    render(
      <ConfirmModal
        {...baseProps}
        loading
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Processing..." }),
    ).toBeDisabled();
  });

  it("renders a rich message and sets the warning apart as a note", () => {
    render(
      <ConfirmModal
        {...baseProps}
        message={
          <p>
            Restores <code>acme</code>.
          </p>
        }
        warning="Its slug may have been taken."
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.getByText("acme").tagName).toBe("CODE");
    expect(screen.getByRole("note")).toHaveTextContent(
      "Its slug may have been taken.",
    );
  });

  it("has no warning note unless one is given", () => {
    render(
      <ConfirmModal {...baseProps} onClose={vi.fn()} onConfirm={vi.fn()} />,
    );
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  // The object inspector (z-60) and a selection bar (z-50) open
  // confirmations; neither may cover one.
  it("stacks above the inspector panel and the selection bar", () => {
    render(
      <ConfirmModal {...baseProps} onClose={vi.fn()} onConfirm={vi.fn()} />,
    );
    const content = screen.getByRole("alertdialog");
    expect(content).toHaveClass("z-100");
    expect(content).not.toHaveClass("z-50");
    const overlay = document.querySelector(
      '[data-slot="alert-dialog-overlay"]',
    );
    expect(overlay).toHaveClass("z-100");
    expect(overlay).not.toHaveClass("z-50");
  });

  it("holds both buttons while the action runs", async () => {
    let finish!: () => void;
    const onConfirm = vi.fn(
      () => new Promise<void>((resolve) => (finish = resolve)),
    );
    const onClose = vi.fn();
    render(
      <ConfirmModal
        {...baseProps}
        confirmText="Delete"
        onConfirm={onConfirm}
        onClose={onClose}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(
      screen.getByRole("button", { name: "Processing..." }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(onClose).not.toHaveBeenCalled();
    finish();
    await vi.waitFor(() => expect(onClose).toHaveBeenCalledOnce());
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("renders a control it is given between the warning and the buttons", () => {
    render(
      <ConfirmModal
        {...baseProps}
        warning="No trash."
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      >
        <label>
          <input type="checkbox" /> Also delete the S3 bucket
        </label>
      </ConfirmModal>,
    );
    const box = screen.getByRole("checkbox", {
      name: "Also delete the S3 bucket",
    });
    expect(screen.getByRole("note").compareDocumentPosition(box)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(
      box.compareDocumentPosition(
        screen.getByRole("button", { name: "Cancel" }),
      ),
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("holds only the confirm button while confirmDisabled", () => {
    render(
      <ConfirmModal
        {...baseProps}
        confirmText="Revoke copy"
        confirmDisabled
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.getByRole("button", { name: "Revoke copy" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
  });
});
