import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// The operations list arrives with the rest of the shell, so that is what the
// drawer reads; cancelOperation is still its own RPC.
const h = vi.hoisted(() => ({
  cancel: vi.fn(),
  refresh: vi.fn(),
  shell: {
    status: "ok" as "ok" | "unavailable" | "loading",
    reason: undefined as string | undefined,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    data: null as any[] | null,
  },
}));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: { cancelOperation: h.cancel },
}));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({
    operations: h.shell,
    version: { status: "ok", data: null },
    health: { status: "ok", data: null },
    error: null,
    lastUpdated: null,
    refresh: h.refresh,
  }),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function shellOps(ops: any[]) {
  h.shell.status = "ok";
  h.shell.reason = undefined;
  h.shell.data = ops;
}

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
    h.cancel.mockReset();
    h.refresh.mockReset();
    h.refresh.mockResolvedValue(undefined);
    h.cancel.mockResolvedValue({});
    shellOps([]);
  });

  it("cancels an in-progress operation via CancelOperation", async () => {
    shellOps([runningOp]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    const cancel = await screen.findByRole("button", { name: /^cancel$/i });
    await userEvent.click(cancel);
    // Stopping a batch part-way is not undone by restarting it, so the click
    // asks first.
    expect(h.cancel).not.toHaveBeenCalled();
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel operation" }),
    );

    await waitFor(() => expect(h.cancel).toHaveBeenCalledTimes(1));
    expect(h.cancel).toHaveBeenCalledWith({ name: "operations/mig-1" });
    // The shell is refreshed after a successful cancel so the row reflects
    // the new state without waiting for the next tick.
    await waitFor(() => expect(h.refresh).toHaveBeenCalled());
  });

  it("does not render a Cancel button for a completed operation", async () => {
    shellOps([doneOp]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    // The done op renders — its 'done' badge is the anchor — but no Cancel.
    await screen.findByText("done");
    expect(screen.queryByRole("button", { name: /^cancel$/i })).toBeNull();
  });

  it("surfaces a cancel failure inline without throwing", async () => {
    shellOps([runningOp]);
    h.cancel.mockRejectedValue(new Error("boom"));
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    await userEvent.click(
      await screen.findByRole("button", { name: /^cancel$/i }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel operation" }),
    );

    await waitFor(() => expect(h.cancel).toHaveBeenCalledTimes(1));
    // The thrown error's own message, not a generic "Cancel failed".
    expect(await screen.findByText(/boom/)).toBeInTheDocument();
  });
});

// A worker that dies mid-batch leaves the operation FAILED/WORKER_LOST, and
// "the outcome is unknown" is all the operator used to get. The reclaimer
// copies the last {processed, total} snapshot into the failure payload, so the
// row can say how far the work actually got before the worker stopped.
describe("BackgroundOpsDrawer progress", () => {
  beforeEach(() => {
    h.cancel.mockReset();
    h.refresh.mockReset();
    shellOps([]);
  });

  function anyStruct(fields: Record<string, unknown>) {
    return anyPack(
      StructSchema,
      create(StructSchema, { fields: toStructFields(fields) }),
    );
  }

  it("reports how far a reclaimed operation got", async () => {
    shellOps([
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
    ]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    expect(await screen.findByText(/7 of 9/)).toBeInTheDocument();
    expect(screen.getByText(/before the worker stopped/)).toBeInTheDocument();
  });

  it("says nothing about progress when the worker never reported any", async () => {
    shellOps([
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
    ]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    await screen.findByText(/the worker executing this operation stopped/);
    expect(screen.queryByText(/ of /)).not.toBeInTheDocument();
  });

  it("shows live progress for a running operation", async () => {
    shellOps([
      {
        name: "operations/run-1",
        type: "BatchCopy",
        done: false,
        createdAt: undefined,
        metadata: anyStruct({ processed: 3, total: 12 }),
        result: { case: undefined },
      },
    ]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();

    expect(await screen.findByText(/3 of 12 processed/)).toBeInTheDocument();
  });
});

// A chrome widget that cannot reach its service must say so. The shell
// reports each section separately for exactly this: an empty list here would
// read as "nothing is running" while a batch may well be running.
describe("BackgroundOpsDrawer unavailable section", () => {
  beforeEach(() => {
    h.cancel.mockReset();
    h.refresh.mockReset();
  });

  it("says the list is unknown rather than empty", async () => {
    h.shell.status = "unavailable";
    h.shell.reason = "UNAVAILABLE: connection refused";
    h.shell.data = null;

    render(<BackgroundOpsDrawer />);
    await openDrawer();

    expect(await screen.findByText(/unknown, not empty/i)).toBeInTheDocument();
    expect(
      screen.getByText(/UNAVAILABLE: connection refused/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/No background operations/)).toBeNull();
  });
});
