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
});
