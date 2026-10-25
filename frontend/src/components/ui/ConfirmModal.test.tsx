import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { Code, ConnectError } from "@connectrpc/connect";

import { CONFIRM_FAILED, ConfirmModal } from "./ConfirmModal";

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

  // A refused action used to leave the dialog sitting there, unchanged, while
  // the error went unhandled: an upload that never finished could not be
  // trashed, and nothing said so.
  it("stays open and says why when the action is refused", async () => {
    const onClose = vi.fn();
    const refusal = new ConnectError(
      "the upload has not completed, so there is nothing to move to the trash",
      Code.FailedPrecondition,
    );
    render(
      <ConfirmModal
        {...baseProps}
        confirmText="Move to Trash"
        onConfirm={() => Promise.reject(refusal)}
        onClose={onClose}
      />,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Move to Trash" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "the upload has not completed",
    );
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Move to Trash" })).toBeEnabled();
  });

  it("names a failure that gave no reason", async () => {
    render(
      <ConfirmModal
        {...baseProps}
        confirmText="Delete"
        onConfirm={() => Promise.reject(undefined)}
        onClose={vi.fn()}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(CONFIRM_FAILED);
  });

  it("does not carry an old reason to another action", async () => {
    const props = { ...baseProps, confirmText: "Delete", onClose: vi.fn() };
    const { rerender } = render(
      <ConfirmModal
        {...props}
        onConfirm={() => Promise.reject(new Error("refused"))}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("refused");
    rerender(<ConfirmModal {...props} onConfirm={vi.fn()} />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("ConfirmModal typed confirmation", () => {
  const typed = {
    ...baseProps,
    type: "danger" as const,
    confirmText: "Delete bucket",
    typeToConfirm: "media-eu",
  };
  const field = () => screen.getByRole("textbox", { name: /to confirm/ });
  const confirmButton = () =>
    screen.getByRole("button", { name: "Delete bucket" });

  it("asks for nothing to be typed unless typeToConfirm is given", () => {
    render(
      <ConfirmModal {...baseProps} onClose={vi.fn()} onConfirm={vi.fn()} />,
    );
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("names what to type in the field's label and says how it is matched", () => {
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={vi.fn()} />);
    expect(field()).toHaveAccessibleName("Type media-eu to confirm");
    expect(field()).toHaveAccessibleDescription(
      "It has to match exactly, capitals included.",
    );
  });

  it("takes the focus when the dialog opens", () => {
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={vi.fn()} />);
    expect(field()).toHaveFocus();
  });

  it("holds the confirm button until the text matches exactly", async () => {
    const onConfirm = vi.fn();
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={onConfirm} />);
    expect(confirmButton()).toBeDisabled();
    await userEvent.type(field(), "media-e");
    expect(confirmButton()).toBeDisabled();
    await userEvent.type(field(), "u");
    expect(confirmButton()).toBeEnabled();
    await userEvent.click(confirmButton());
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("matches case-sensitively", async () => {
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={vi.fn()} />);
    await userEvent.type(field(), "MEDIA-EU");
    expect(confirmButton()).toBeDisabled();
  });

  // A name copied from the table often brings a space or a newline along.
  it("lets go of whitespace at either end, and only there", async () => {
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={vi.fn()} />);
    await userEvent.type(field(), "  media-eu ");
    expect(confirmButton()).toBeEnabled();
    await userEvent.clear(field());
    await userEvent.type(field(), "media -eu");
    expect(confirmButton()).toBeDisabled();
  });

  it("confirms on Enter only once the text matches", async () => {
    const onConfirm = vi.fn();
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={onConfirm} />);
    await userEvent.type(field(), "media{Enter}");
    expect(onConfirm).not.toHaveBeenCalled();
    await userEvent.type(field(), "-eu{Enter}");
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("does not confirm on Enter while the action runs", async () => {
    const onConfirm = vi.fn(() => new Promise<void>(() => {}));
    render(<ConfirmModal {...typed} onClose={vi.fn()} onConfirm={onConfirm} />);
    await userEvent.type(field(), "media-eu{Enter}");
    await userEvent.type(field(), "{Enter}");
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("still holds the button while confirmDisabled, whatever is typed", async () => {
    render(
      <ConfirmModal
        {...typed}
        confirmDisabled
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    await userEvent.type(field(), "media-eu");
    expect(confirmButton()).toBeDisabled();
  });

  it("starts empty each time it opens", async () => {
    const props = { ...typed, onClose: vi.fn(), onConfirm: vi.fn() };
    const { rerender } = render(<ConfirmModal {...props} />);
    await userEvent.type(field(), "media-eu");
    rerender(<ConfirmModal {...props} isOpen={false} />);
    rerender(<ConfirmModal {...props} />);
    expect(field()).toHaveValue("");
    expect(confirmButton()).toBeDisabled();
  });

  it("starts empty when it names another target", async () => {
    const props = { ...typed, onClose: vi.fn(), onConfirm: vi.fn() };
    const { rerender } = render(<ConfirmModal {...props} />);
    await userEvent.type(field(), "media-eu");
    rerender(<ConfirmModal {...props} typeToConfirm="media-us" />);
    expect(field()).toHaveValue("");
    expect(field()).toHaveAccessibleName("Type media-us to confirm");
  });
});

describe("ConfirmModal affected list", () => {
  const list = () => screen.getByRole("list", { name: "7 backends" });

  it("lists nothing unless affected is given", () => {
    render(
      <ConfirmModal {...baseProps} onClose={vi.fn()} onConfirm={vi.fn()} />,
    );
    expect(screen.queryByRole("list")).not.toBeInTheDocument();
  });

  it("names every item when there are few", () => {
    render(
      <ConfirmModal
        {...baseProps}
        affected={{ heading: "2 backends", items: ["b1", "b2"] }}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    const items = screen.getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual(["b1", "b2"]);
  });

  it("names the first five and counts the rest", () => {
    render(
      <ConfirmModal
        {...baseProps}
        affected={{
          heading: "7 backends",
          items: ["b1", "b2", "b3", "b4", "b5", "b6", "b7"],
        }}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    const items = within(list()).getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual([
      "b1",
      "b2",
      "b3",
      "b4",
      "b5",
      "and 2 more",
    ]);
  });

  // A purge removes several kinds, each listed under its own heading.
  it("lists several in the order given", () => {
    render(
      <ConfirmModal
        {...baseProps}
        affected={[
          { heading: "2 API tokens", items: ["ci", "deploy"] },
          { heading: "Also removed", items: ["Quotas"], mono: false },
        ]}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    const lists = screen.getAllByRole("list");
    expect(
      lists.map((l) => l.getAttribute("aria-labelledby") && l.textContent),
    ).toEqual(["cideploy", "Quotas"]);
    expect(
      screen.getByRole("list", { name: "2 API tokens" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("list", { name: "Also removed" }),
    ).toBeInTheDocument();
  });

  // A selection can reach past the rows the page has loaded.
  it("counts a total beyond the names it was given", () => {
    render(
      <ConfirmModal
        {...baseProps}
        affected={{ heading: "7 backends", items: ["b1", "b2"], total: 7 }}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(within(list()).getByText("and 5 more")).toBeInTheDocument();
  });

  it("sits between the warning and the buttons", () => {
    render(
      <ConfirmModal
        {...baseProps}
        warning="No trash."
        affected={{ heading: "7 backends", items: ["b1"] }}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.getByRole("note").compareDocumentPosition(list())).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(
      list().compareDocumentPosition(
        screen.getByRole("button", { name: "Cancel" }),
      ),
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });
});
