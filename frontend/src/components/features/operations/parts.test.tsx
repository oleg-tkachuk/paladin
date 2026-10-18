import { describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import userEvent from "@testing-library/user-event";

import { render, screen, waitFor } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";

import { CancelOperationButton, OperationProgressBar } from "./parts";

describe("OperationProgressBar", () => {
  it("says how far the work got, to a screen reader too", () => {
    render(<OperationProgressBar progress={{ processed: 3, total: 12 }} />);
    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "3");
    expect(bar).toHaveAttribute("aria-valuemax", "12");
    expect(screen.getByText("3 of 12 processed")).toBeInTheDocument();
  });
});

describe("CancelOperationButton", () => {
  const op = create(OperationSchema, {
    name: "operations/a",
    type: "BatchCopy",
  });

  it("cancels only once confirmed, and clears an earlier error first", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    const onError = vi.fn();
    render(
      <CancelOperationButton op={op} onCancel={onCancel} onError={onError} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).not.toHaveBeenCalled();
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel operation" }),
    );
    await waitFor(() => expect(onCancel).toHaveBeenCalledWith("operations/a"));
    expect(onError).toHaveBeenCalledWith(null);
    expect(onError).not.toHaveBeenCalledWith(expect.any(String));
  });

  it("hands back why the cancel failed", async () => {
    const onError = vi.fn();
    render(
      <CancelOperationButton
        op={op}
        onCancel={vi.fn().mockRejectedValue(new Error("gone"))}
        onError={onError}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel operation" }),
    );
    await waitFor(() =>
      expect(onError).toHaveBeenCalledWith(expect.stringContaining("gone")),
    );
  });
});
