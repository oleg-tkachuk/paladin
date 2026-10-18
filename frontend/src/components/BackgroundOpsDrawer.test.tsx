import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// The operations list arrives with the rest of the shell, so that is what the
// drawer reads; cancelOperation is still its own RPC.
const h = vi.hoisted(() => ({
  cancel: vi.fn(),
  dismiss: vi.fn(),
  refresh: vi.fn(),
  shell: {
    status: "ok" as "ok" | "unavailable" | "loading",
    reason: undefined as string | undefined,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    data: null as any[] | null,
  },
}));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: {
    cancelOperation: h.cancel,
    dismissOperations: h.dismiss,
  },
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

import { create, fromJson } from "@bufbuild/protobuf";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
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

  // A finished batch keeps its last progress report; the row says how it
  // ended instead of showing a full bar that reads as still going.
  it("shows no progress for a finished operation", async () => {
    shellOps([
      {
        name: "operations/done-1",
        type: "BatchUpdateTags",
        done: true,
        createdAt: undefined,
        metadata: anyStruct({ processed: 1, total: 1 }),
        result: { case: undefined },
      },
    ]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    await screen.findByRole("listitem", { name: /Merge tags/ });
    expect(screen.queryByText(/processed/)).toBeNull();
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

// The drawer says what each operation did and to what, lets the list be
// narrowed by where operations stand, and opens one to its whole record.
describe("BackgroundOpsDrawer content", () => {
  const T1 = "tenants/0199-t/collections/e2e/docs";
  const resp = (json: Record<string, unknown>) => ({
    case: "response" as const,
    value: anyPack(StructSchema, fromJson(StructSchema, json as never)),
  });
  const tagsDone = {
    name: "operations/tags-1",
    type: "BatchUpdateTags",
    done: true,
    state: OperationState.SUCCEEDED,
    target: {
      collection: T1,
      destination: "",
      objectCount: 3,
      permanent: false,
      replace: false,
    },
    result: resp({
      total: 3,
      succeeded: 2,
      failed: 1,
      failures: [{ object_id: "obj-9", reason: "object is locked" }],
    }),
  };
  const deleteRunning = {
    name: "operations/del-1",
    type: "BatchDelete",
    done: false,
    state: OperationState.RUNNING,
    target: {
      collection: T1,
      destination: "",
      objectCount: 1,
      permanent: true,
      replace: false,
    },
    result: { case: undefined },
  };
  const cancelledCopy = {
    name: "operations/copy-1",
    type: "BatchCopy",
    done: true,
    state: OperationState.CANCELLED,
    result: {
      case: "error",
      value: { code: 1, message: "cancelled", details: [] },
    },
  };

  const failedRestore = {
    name: "operations/restore-1",
    type: "BatchRestoreObjects",
    done: true,
    state: OperationState.FAILED,
    result: {
      case: "error",
      value: { code: 13, message: "store unreachable", details: [] },
    },
  };

  beforeEach(() => {
    h.refresh.mockReset();
    shellOps([tagsDone, deleteRunning, cancelledCopy, failedRestore]);
  });

  it("names what each did and to which collection, active work first", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    const rows = await screen.findAllByRole("listitem");
    expect(rows[0]).toHaveAccessibleName("Delete permanently, running");
    const tags = screen.getByRole("listitem", { name: /Merge tags/ });
    expect(
      within(tags).getByRole("link", { name: "e2e/docs" }),
    ).toHaveAttribute("href", "/tenants/0199-t/collections/e2e%2Fdocs");
    expect(within(tags).getByText("3 objects")).toBeInTheDocument();
    expect(
      within(tags).getByText(/2 of 3 done · 1 failed/),
    ).toBeInTheDocument();
    // Cancelled is its own state, not a failure.
    expect(
      screen.getByRole("listitem", { name: "Copy objects, cancelled" }),
    ).toBeInTheDocument();
  });

  it("narrows the list by state, counting each", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    const filter = await screen.findByRole("group", { name: "Show" });
    expect(
      within(filter).getByRole("button", { name: /Active 1/ }),
    ).toBeInTheDocument();
    await userEvent.click(
      within(filter).getByRole("button", { name: /Done 2/ }),
    );
    const names = screen
      .getAllByRole("listitem")
      .map((r) => r.getAttribute("aria-label"));
    expect(names).toEqual(["Merge tags, done", "Copy objects, cancelled"]);
    await userEvent.click(
      within(filter).getByRole("button", { name: /Failed 1/ }),
    );
    expect(
      screen.getAllByRole("listitem").map((r) => r.getAttribute("aria-label")),
    ).toEqual(["Restore from trash, failed"]);
    expect(screen.getByText(/store unreachable/)).toBeInTheDocument();
  });

  it("opens an operation to its record, with each object that failed", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    await userEvent.click(
      await screen.findByRole("listitem", { name: /Merge tags/ }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Merge tags" });
    expect(dialog).toHaveAccessibleDescription("operations/tags-1");
    expect(
      within(dialog).getByText(/obj-9\s+object is locked/),
    ).toBeInTheDocument();
  });
});

// Clearing takes finished operations off the signed-in user's own list, on the
// server; the list is read again so they leave it.
describe("BackgroundOpsDrawer clearing", () => {
  const finishedOp = {
    name: "operations/old-1",
    type: "BatchDelete",
    done: true,
    state: OperationState.SUCCEEDED,
    result: { case: undefined },
  };
  const activeOp = {
    name: "operations/now-1",
    type: "BatchCopy",
    done: false,
    state: OperationState.RUNNING,
    result: { case: undefined },
  };

  beforeEach(() => {
    h.dismiss.mockReset().mockResolvedValue({ dismissedCount: 1 });
    h.refresh.mockReset().mockResolvedValue(undefined);
    shellOps([activeOp, finishedOp]);
  });

  it("clears every finished operation at once", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    await userEvent.click(
      await screen.findByRole("button", { name: /Clear finished 1/ }),
    );
    expect(h.dismiss).toHaveBeenCalledWith({ names: [] });
    await waitFor(() => expect(h.refresh).toHaveBeenCalled());
  });

  it("clears one finished operation, and offers nothing to clear on running work", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    const running = await screen.findByRole("listitem", {
      name: /Copy objects/,
    });
    expect(
      within(running).queryByRole("button", { name: /^Clear/ }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Clear Move to trash" }),
    );
    expect(h.dismiss).toHaveBeenCalledWith({ names: ["operations/old-1"] });
  });

  it("has nothing to clear when nothing has finished", async () => {
    shellOps([activeOp]);
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    expect(
      await screen.findByRole("button", { name: /Clear finished/ }),
    ).toBeDisabled();
  });

  it("says when clearing failed", async () => {
    h.dismiss.mockRejectedValue(new Error("nope"));
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    await userEvent.click(
      await screen.findByRole("button", { name: /Clear finished/ }),
    );
    expect(await screen.findByText(/nope/)).toBeInTheDocument();
  });
});

// The way to the page with room for detail: a link of its own, not the tail
// of the summary line it used to wrap in.
describe("BackgroundOpsDrawer operations page link", () => {
  beforeEach(() => {
    h.refresh.mockReset().mockResolvedValue(undefined);
    shellOps([]);
  });

  it("leads to the operations page and closes the drawer", async () => {
    render(<BackgroundOpsDrawer />);
    await openDrawer();
    const link = await screen.findByRole("link", {
      name: "Open the operations page",
    });
    expect(link).toHaveAttribute("href", "/operations");
    expect(screen.getByText(/^Yours only/)).not.toContainElement(link);
    await userEvent.click(link);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});
