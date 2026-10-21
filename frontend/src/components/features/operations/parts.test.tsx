import { beforeEach, describe, expect, it, vi } from "vitest";
import { create, fromJson } from "@bufbuild/protobuf";
import { anyPack, StructSchema } from "@bufbuild/protobuf/wkt";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

const h = vi.hoisted(() => ({
  batchDelete: vi.fn(),
  batchRestore: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", async (orig) => ({
  ...(await orig<typeof import("@/lib/connect/client")>()),
  batchClient: {
    batchDeleteObjects: h.batchDelete,
    batchRestoreObjects: h.batchRestore,
  },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));
import userEvent from "@testing-library/user-event";

import { render, screen, waitFor } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";

import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  CancelOperationButton,
  OperationProgressBar,
  OperationRecord,
  RetryFailedButton,
} from "./parts";

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

describe("OperationRecord", () => {
  it("says until when a finished operation is kept", () => {
    const op = create(OperationSchema, {
      name: "operations/a",
      type: "BatchCopy",
      done: true,
      expireTime: timestampFromDate(new Date("2026-11-01T12:00:00Z")),
    });
    render(<OperationRecord op={op} onClose={() => {}} />);
    expect(screen.getByText("Kept until")).toBeInTheDocument();
  });

  it("says nothing of it for one the reaper will not remove", () => {
    const op = create(OperationSchema, {
      name: "operations/b",
      type: "BatchCopy",
    });
    render(<OperationRecord op={op} onClose={() => {}} />);
    expect(screen.queryByText("Kept until")).toBeNull();
  });
});

describe("RetryFailedButton", () => {
  const COLLECTION = "tenants/t-1/collections/docs";
  const FAILED = "01a15875-a5aa-71f4-b0f9-4e7bb7b537b6";
  const partial = (type: string, permanent = false) =>
    create(OperationSchema, {
      name: "operations/p-1",
      type,
      state: OperationState.SUCCEEDED,
      failedCount: 1n,
      target: { collection: COLLECTION, permanent, objectCount: 2 },
      result: {
        case: "response",
        value: anyPack(
          StructSchema,
          fromJson(StructSchema, {
            total: 2,
            succeeded: 1,
            failed: 1,
            failures: [
              { object_id: FAILED, reason: "statemachine: version conflict" },
            ],
          }),
        ),
      },
    });

  beforeEach(() => {
    h.batchDelete.mockReset().mockResolvedValue({});
    h.batchRestore.mockReset().mockResolvedValue({});
    h.notify.mockReset();
  });

  it("starts the delete again for the objects it left, once confirmed", async () => {
    const onRetried = vi.fn();
    render(
      <RetryFailedButton op={partial("BatchDelete")} onRetried={onRetried} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /Retry failed/ }));
    expect(h.batchDelete).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(onRetried).toHaveBeenCalled());
    expect(h.batchDelete).toHaveBeenCalledWith({
      parent: COLLECTION,
      selector: { names: [`${COLLECTION}/objects/${FAILED}`], filter: "" },
      permanent: false,
    });
  });

  // Deleted for good, with no trash: the verb is typed first.
  it("asks for the verb before deleting for good again", async () => {
    render(<RetryFailedButton op={partial("BatchDelete", true)} />);
    await userEvent.click(screen.getByRole("button", { name: /Retry failed/ }));
    expect(screen.getByRole("button", { name: "Retry" })).toBeDisabled();
    expect(screen.getByText(/deleted for good/)).toBeInTheDocument();
  });

  it("says so when the retry does not start", async () => {
    h.batchRestore.mockRejectedValue(new Error("unavailable"));
    render(<RetryFailedButton op={partial("BatchRestoreObjects")} />);
    await userEvent.click(screen.getByRole("button", { name: /Retry failed/ }));
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "error",
          title: "Retry failed to start",
        }),
      ),
    );
  });

  it("offers nothing for a copy, whose request it cannot rebuild", () => {
    render(<RetryFailedButton op={partial("BatchCopy")} />);
    expect(screen.queryByRole("button", { name: /Retry failed/ })).toBeNull();
  });
});
