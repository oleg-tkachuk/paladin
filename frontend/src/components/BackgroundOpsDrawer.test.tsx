import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// The drawer polls admin/v1.OperationService via adminOperationClient;
// mock listOperations (the poll) and cancelOperation (the row action).
const h = vi.hoisted(() => ({ list: vi.fn(), cancel: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: { listOperations: h.list, cancelOperation: h.cancel },
}));

import { create } from "@bufbuild/protobuf";
import { anyPack, StructSchema, ValueSchema } from "@bufbuild/protobuf/wkt";

import { BackgroundOpsDrawer } from "./BackgroundOpsDrawer";

// The wire carries metadata and failure details as Any-wrapped Structs; build
// them the same way the server does rather than hand-rolling the encoding.
function toStructFields(obj: Record<string, unknown>) {
  const out: Record<string, ReturnType<typeof create<typeof ValueSchema>>> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v === "number") {
      out[k] = create(ValueSchema, { kind: { case: "numberValue", value: v } });
    } else if (typeof v === "string") {
      out[k] = create(ValueSchema, { kind: { case: "stringValue", value: v } });
    } else {
      out[k] = create(ValueSchema, {
        kind: {
          case: "structValue",
          value: create(StructSchema, {
            fields: toStructFields(v as Record<string, unknown>),
          }),
        },
      });
    }
  }
  return out;
}

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

// A worker that dies mid-batch leaves the operation FAILED/WORKER_LOST, and
// "the outcome is unknown" is all the operator used to get. The reclaimer
// copies the last {processed, total} snapshot into the failure payload, so the
// row can say how far the work actually got before the worker stopped.
describe("BackgroundOpsDrawer progress", () => {
  beforeEach(() => {
    h.list.mockReset();
    h.cancel.mockReset();
  });

  function anyStruct(fields: Record<string, unknown>) {
    return anyPack(
      StructSchema,
      create(StructSchema, { fields: toStructFields(fields) }),
    );
  }

  it("reports how far a reclaimed operation got", async () => {
    h.list.mockResolvedValue({
      operations: [
        {
          name: "operations/lost-1",
          type: "BatchUpdateTags",
          done: true,
          createdAt: undefined,
          result: {
            case: "error",
            value: {
              code: 10,
              message: "the worker executing this operation stopped",
              details: [
                anyStruct({
                  code: "WORKER_LOST",
                  last_progress: { processed: 7, total: 9 },
                }),
              ],
            },
          },
        },
      ],
    });
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    expect(await screen.findByText(/7 of 9/)).toBeInTheDocument();
    expect(screen.getByText(/before the worker stopped/)).toBeInTheDocument();
  });

  it("says nothing about progress when the worker never reported any", async () => {
    h.list.mockResolvedValue({
      operations: [
        {
          name: "operations/lost-2",
          type: "BatchUpdateTags",
          done: true,
          createdAt: undefined,
          result: {
            case: "error",
            value: {
              code: 10,
              message: "the worker executing this operation stopped",
              // Died before its first report: metadata still held the args.
              details: [anyStruct({ code: "WORKER_LOST" })],
            },
          },
        },
      ],
    });
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    await screen.findByText(/the worker executing this operation stopped/);
    expect(screen.queryByText(/ of /)).not.toBeInTheDocument();
  });

  it("shows live progress for a running operation", async () => {
    h.list.mockResolvedValue({
      operations: [
        {
          name: "operations/run-1",
          type: "BatchCopy",
          done: false,
          createdAt: undefined,
          metadata: anyStruct({ processed: 3, total: 12 }),
          result: { case: undefined },
        },
      ],
    });
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    expect(await screen.findByText(/3 of 12 processed/)).toBeInTheDocument();
  });
});
