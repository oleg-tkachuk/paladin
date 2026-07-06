import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// The drawer polls admin/v1.OperationService via adminOperationClient;
// mock listOperations (the poll) and cancelOperation (the row action).
const h = vi.hoisted(() => ({ list: vi.fn(), cancel: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: { listOperations: h.list, cancelOperation: h.cancel },
}));

import { BackgroundOpsDrawer } from "./BackgroundOpsDrawer";

// Minimal Operation shape the component reads: name/type/done/result oneof.
const runningOp = {
  name: "operations/mig-1",
  type: "MigrateTenantStorageLayout",
  done: false,
  createdAt: undefined,
  result: { case: undefined },
};
const doneOp = {
  name: "operations/mig-0",
  type: "MigrateTenantStorageLayout",
  done: true,
  createdAt: undefined,
  result: { case: undefined },
};

async function openDrawer() {
  await userEvent.click(
    await screen.findByRole("button", { name: /background operations/i }),
  );
}

describe("BackgroundOpsDrawer cancel", () => {
  beforeEach(() => {
    h.list.mockReset();
    h.cancel.mockReset();
    h.cancel.mockResolvedValue({});
  });

  it("cancels an in-progress operation via CancelOperation", async () => {
    h.list.mockResolvedValue({ operations: [runningOp] });
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    const cancel = await screen.findByRole("button", { name: /^cancel$/i });
    await userEvent.click(cancel);

    await waitFor(() => expect(h.cancel).toHaveBeenCalledTimes(1));
    expect(h.cancel).toHaveBeenCalledWith({ name: "operations/mig-1" });
    // Refetch fires after a successful cancel (initial poll + post-cancel).
    await waitFor(() =>
      expect(h.list.mock.calls.length).toBeGreaterThanOrEqual(2),
    );
  });

  it("does not render a Cancel button for a completed operation", async () => {
    h.list.mockResolvedValue({ operations: [doneOp] });
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    // The done op renders — its 'done' badge is the anchor — but no Cancel.
    await screen.findByText("done");
    expect(screen.queryByRole("button", { name: /^cancel$/i })).toBeNull();
  });

  it("surfaces a cancel failure inline without throwing", async () => {
    h.list.mockResolvedValue({ operations: [runningOp] });
    h.cancel.mockRejectedValue(new Error("boom"));
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    await userEvent.click(
      await screen.findByRole("button", { name: /^cancel$/i }),
    );

    await waitFor(() => expect(h.cancel).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(/cancel failed/i)).toBeInTheDocument();
  });
});
