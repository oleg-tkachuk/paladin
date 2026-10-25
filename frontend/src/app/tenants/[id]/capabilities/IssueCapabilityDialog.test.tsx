import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  issue: vi.fn(),
  listOperationGrants: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: {
    issue: h.issue,
    listOperationGrants: h.listOperationGrants,
  },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { IssueCapabilityDialog } from "./IssueCapabilityDialog";

// The server's table, as ListOperationGrants serves it.
const GRANTS = [
  { op: "get", actions: ["GetObject", "PresignGet"] },
  { op: "list", actions: ["GetObject", "PresignGet"] },
  { op: "put", actions: ["PutObject", "PresignPut"] },
  { op: "manage", actions: [] },
];
const UNLOCKS = "What these ops unlock";
const MANAGE_WARNING = /unlocks nothing on its own/;

function open() {
  render(
    <IssueCapabilityDialog
      open
      tenantId="t-1"
      onClose={vi.fn()}
      onIssued={vi.fn()}
    />,
  );
}

describe("IssueCapabilityDialog", () => {
  beforeEach(() => {
    h.issue.mockReset();
    h.showNotification.mockReset();
    h.listOperationGrants.mockReset();
    h.listOperationGrants.mockResolvedValue({ grants: GRANTS });
  });

  it("shows what the chosen ops unlock, as they are toggled", async () => {
    open();
    const unlocks = await screen.findByRole("group", { name: UNLOCKS });
    // The default ops, get and list, read.
    expect(within(unlocks).getAllByText("GetObject")).toHaveLength(2);
    expect(within(unlocks).queryByText("PutObject")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "put" }));
    expect(within(unlocks).getByText("PutObject")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "get" }));
    expect(within(unlocks).getAllByText("GetObject")).toHaveLength(1);
  });

  // manage unlocks nothing without a tenant policy: the dialog says so
  // before the capability is issued, rather than the call failing later.
  it("warns that manage needs a tenant policy, and stops when it is off", async () => {
    open();
    await screen.findByRole("group", { name: UNLOCKS });
    expect(screen.queryByText(MANAGE_WARNING)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "manage" }));
    expect(screen.getByRole("note")).toHaveTextContent(
      "manage unlocks nothing on its own: without a tenant policy that permits its actions, each call is denied by policy.",
    );

    fireEvent.click(screen.getByRole("button", { name: "manage" }));
    expect(screen.queryByText(MANAGE_WARNING)).not.toBeInTheDocument();
  });

  it("names an op the server's table does not list", async () => {
    open();
    await screen.findByRole("group", { name: UNLOCKS });
    fireEvent.click(screen.getByRole("button", { name: "share" }));
    expect(
      screen.getByText(/The server lists no built-in grant for/),
    ).toHaveTextContent(/share: a tenant policy decides what it may do\./);
  });

  // The table only informs the choice: a failed read holds nothing.
  it("says the table is unavailable and still lets the form submit", async () => {
    h.listOperationGrants.mockRejectedValue(new Error("down"));
    open();
    expect(
      await screen.findByText("What these ops unlock is unavailable."),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "agent-7" },
    });
    expect(
      screen.getByRole("button", { name: "Issue capability" }),
    ).toBeEnabled();
  });

  it("says it is reading the table while it loads", () => {
    h.listOperationGrants.mockReturnValue(new Promise(() => {}));
    open();
    expect(
      screen.getByText("Reading what these ops unlock…"),
    ).toBeInTheDocument();
  });

  // A limit left empty with Unlimited off is no limit anyone chose: the
  // submit waits for a number rather than sending one.
  it("holds the submit while a limit is on but empty, and says so", () => {
    open();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "agent-7" },
    });
    expect(
      screen.getByRole("button", { name: "Issue capability" }),
    ).toBeEnabled();

    // The Unlimited toggle beside the Max requests input.
    const maxRequests = screen.getByLabelText("Max requests");
    fireEvent.click(
      maxRequests.parentElement!.querySelector("button[aria-pressed]")!,
    );
    expect(
      screen.getByText("Set max requests, or make it Unlimited."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Issue capability" }),
    ).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Max requests"), {
      target: { value: "-3" },
    });
    expect(screen.getByLabelText("Max requests")).toHaveAccessibleDescription(
      "A whole number (0 allows none), or Unlimited.",
    );
  });
});
