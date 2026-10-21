import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { create } from "@bufbuild/protobuf";
import { timestampNow } from "@bufbuild/protobuf/wkt";

import { render, screen, waitFor, within } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
import type { UseOperationsArgs } from "@/hooks/useOperations";

const h = vi.hoisted(() => ({
  args: [] as unknown[],
  operations: [] as unknown[],
  error: null as string | null,
  refresh: vi.fn(),
  cancel: vi.fn(),
  dismiss: vi.fn(),
}));

vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("next/navigation", () => ({
  usePathname: () => "/operations",
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/hooks/useOperations", () => ({
  OPERATIONS_POLL_MS: 2_000,
  useOperations: (args: unknown) => (
    h.args.push(args),
    {
      operations: h.operations,
      loading: false,
      error: h.error,
      hasMore: false,
      refresh: h.refresh,
      loadMore: vi.fn(),
    }
  ),
}));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: {
    cancelOperation: h.cancel,
    dismissOperations: h.dismiss,
  },
}));
vi.mock("@/components/features/audit/ActorName", () => ({
  ActorName: ({ subject }: { subject: string }) => subject,
}));

import { LINKED_ROW_ATTR } from "@/components/ui/table";

import OperationsPage from "./page";

const running = create(OperationSchema, {
  name: "operations/run-1",
  type: "BatchCopy",
  state: OperationState.RUNNING,
  createdAt: timestampNow(),
  initiatorSubject: "0199-alice",
});
const finished = create(OperationSchema, {
  name: "operations/old-1",
  type: "BatchDelete",
  state: OperationState.SUCCEEDED,
  done: true,
  createdAt: timestampNow(),
});

const lastArgs = () => h.args.at(-1) as UseOperationsArgs;

beforeEach(() => {
  h.args = [];
  h.operations = [running, finished];
  h.error = null;
  h.refresh.mockReset().mockResolvedValue(undefined);
  h.cancel.mockReset().mockResolvedValue({});
  h.dismiss.mockReset().mockResolvedValue({});
});

describe("OperationsPage", () => {
  it("lists the user's own, live, with who started each", () => {
    render(<OperationsPage />);
    expect(lastArgs()).toEqual({
      view: "all",
      scope: "mine",
      includeDismissed: false,
      live: true,
    });
    const row = screen.getByRole("row", { name: /Copy objects, running/ });
    expect(within(row).getByText("0199-alice")).toBeInTheDocument();
  });

  it("asks the server for the view, the scope and the cleared", async () => {
    render(<OperationsPage />);
    await userEvent.click(screen.getByRole("button", { name: "Failed" }));
    expect(lastArgs().view).toBe("failed");
    await userEvent.click(screen.getByRole("switch", { name: "Show cleared" }));
    expect(lastArgs().includeDismissed).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Tenant" }));
    expect(lastArgs().scope).toBe("tenant");
    await userEvent.click(screen.getByRole("button", { name: "Live" }));
    expect(lastArgs().live).toBe(false);
  });

  it("clears finished work from the user's own list", async () => {
    render(<OperationsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Clear Move to trash" }),
    );
    expect(h.dismiss).toHaveBeenCalledWith({ names: ["operations/old-1"] });
    await userEvent.click(
      screen.getByRole("button", { name: /Clear finished/ }),
    );
    expect(h.dismiss).toHaveBeenLastCalledWith({ names: [] });
    await waitFor(() => expect(h.refresh).toHaveBeenCalled());
  });

  it("offers no clearing on the tenant's list, where it would do nothing", async () => {
    render(<OperationsPage />);
    await userEvent.click(screen.getByRole("button", { name: "Tenant" }));
    expect(screen.queryByRole("button", { name: /^Clear/ })).toBeNull();
    expect(screen.queryByRole("switch", { name: "Show cleared" })).toBeNull();
  });

  // A batch that ran to its end leaving objects behind reads as partly
  // failed, not done, in its row's name too.
  it("marks a batch that left objects behind as partly failed", () => {
    h.operations = [create(OperationSchema, { ...finished, failedCount: 1n })];
    render(<OperationsPage />);
    expect(
      screen.getByRole("row", { name: /Move to trash, partly failed/ }),
    ).toBeInTheDocument();
    expect(screen.getByText("partly failed")).toBeInTheDocument();
  });

  it("marks a cleared operation and offers no second clear", () => {
    h.operations = [
      create(OperationSchema, { ...finished, dismissedAt: timestampNow() }),
    ];
    render(<OperationsPage />);
    expect(screen.getByText("cleared")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Clear Move to trash" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: /Clear finished/ }),
    ).toBeDisabled();
  });

  it("cancels a running operation once confirmed", async () => {
    render(<OperationsPage />);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(h.cancel).not.toHaveBeenCalled();
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel operation" }),
    );
    await waitFor(() =>
      expect(h.cancel).toHaveBeenCalledWith({ name: "operations/run-1" }),
    );
  });

  it("narrows the loaded list by a search", async () => {
    render(<OperationsPage />);
    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search the operations" }),
      "trash",
    );
    expect(screen.queryByRole("row", { name: /Copy objects/ })).toBeNull();
    expect(
      screen.getByRole("row", { name: /Move to trash/ }),
    ).toBeInTheDocument();
  });

  it("says the list is unknown, not empty, when it cannot be read", () => {
    h.operations = [];
    h.error = "permission denied";
    render(<OperationsPage />);
    expect(screen.getByText("permission denied")).toBeInTheDocument();
    expect(screen.getByText(/unknown, not empty/)).toBeInTheDocument();
  });

  it("opens an operation's record from its row", async () => {
    render(<OperationsPage />);
    await userEvent.click(screen.getByRole("row", { name: /Move to trash/ }));
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "operations/old-1",
    );
  });

  it("puts the open record in the address, and opens one a link names", async () => {
    window.history.replaceState(null, "", "/operations");
    const { unmount } = render(<OperationsPage />);
    await userEvent.click(screen.getByRole("row", { name: /Move to trash/ }));
    expect(window.location.search).toBe(
      `?id=${encodeURIComponent("operations/old-1")}`,
    );
    unmount();
    window.history.replaceState(
      null,
      "",
      `/operations?id=${encodeURIComponent("operations/run-1")}`,
    );
    render(<OperationsPage />);
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "operations/run-1",
    );
    const linked = document.querySelectorAll(`tr[${LINKED_ROW_ATTR}]`);
    expect(linked).toHaveLength(1);
    // A row shows the id; the whole name is its tooltip and its record.
    expect(linked[0]).toHaveTextContent("run-1");
    expect(
      within(linked[0] as HTMLElement).getByTitle("operations/run-1"),
    ).toBeInTheDocument();
    window.history.replaceState(null, "", "/operations");
  });
});
