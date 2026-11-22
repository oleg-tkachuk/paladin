import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  issue: vi.fn(),
  listOperationGrants: vi.fn(),
  getDefaults: vi.fn(),
  showNotification: vi.fn(),
  listCollections: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
  capabilityClient: {
    issue: h.issue,
    listOperationGrants: h.listOperationGrants,
    getDefaults: h.getDefaults,
  },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { CapabilityGrantBasis } from "@/gen/paladin/admin/v1/capability_service_pb";
import { IssueCapabilityDialog } from "./IssueCapabilityDialog";

const NO_FIELDS = { actions: [], pairedOps: [], procedures: [] };
// The server's table, as ListOperationGrants serves it, with no row for
// object:tag so the dialog's fallback shows. object:presign is paired with
// put and delete rather than the server's get and put: the line must name
// the ops the row carries, not ones the console assumes.
const GRANTS = [
  {
    ...NO_FIELDS,
    op: "get",
    basis: CapabilityGrantBasis.BUILTIN,
    actions: ["GetObject", "PresignGet"],
  },
  {
    ...NO_FIELDS,
    op: "list",
    basis: CapabilityGrantBasis.BUILTIN,
    actions: ["GetObject", "PresignGet"],
  },
  {
    ...NO_FIELDS,
    op: "put",
    basis: CapabilityGrantBasis.BUILTIN,
    actions: ["PutObject", "PresignPut"],
  },
  { ...NO_FIELDS, op: "manage", basis: CapabilityGrantBasis.TENANT_POLICY },
  {
    ...NO_FIELDS,
    op: "object:presign",
    basis: CapabilityGrantBasis.PAIRED,
    pairedOps: ["put", "delete"],
  },
  {
    ...NO_FIELDS,
    op: "share",
    basis: CapabilityGrantBasis.CAVEATS_ONLY,
    procedures: ["paladin.admin.v1.CapabilityService.Delegate"],
  },
];
const UNLOCKS = "What these ops unlock";
// GetDefaults' answer: a quarter of an hour.
const SERVER_DEFAULT_TTL = { seconds: BigInt(15 * 60), nanos: 0 };
const MANAGE_WARNING = /unlocks nothing on its own:/;
const PRESIGN_LINE = /adds a check beside/;
const SHARE_LINE = /is decided by the caveats alone/;

// A tenant id, as the route resolves it: a resource is built from the id.
const TENANT = "0190a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b";
const NESTED = "invoices/2026/q1";

function open() {
  render(
    <IssueCapabilityDialog
      open
      tenantId={TENANT}
      onClose={vi.fn()}
      onIssued={vi.fn()}
    />,
  );
}

const issueButton = () =>
  screen.getByRole("button", { name: "Issue capability" });

async function addResource(n: number, collection: string, key: string) {
  fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
  const row = screen.getByRole("listitem", { name: `Resource ${n}` });
  fireEvent.click(within(row).getByLabelText("Collection"));
  fireEvent.click(await screen.findByRole("option", { name: collection }));
  fireEvent.change(within(row).getByLabelText("Key prefix"), {
    target: { value: key },
  });
}

describe("IssueCapabilityDialog", () => {
  beforeEach(() => {
    h.issue.mockReset();
    h.showNotification.mockReset();
    h.listOperationGrants.mockReset();
    h.listOperationGrants.mockResolvedValue({ grants: GRANTS });
    h.getDefaults.mockReset();
    h.getDefaults.mockResolvedValue({ defaultTtl: SERVER_DEFAULT_TTL });
    h.listCollections.mockReset();
    h.listCollections.mockResolvedValue({
      collections: [
        { tenantId: TENANT, collection: "docs", displayName: "" },
        { tenantId: TENANT, collection: NESTED, displayName: "" },
      ],
      page: { nextPageToken: "" },
    });
  });

  // The old field took free text, and its example was no resource at all.
  it("takes no free-text resource", () => {
    open();
    expect(screen.queryByText(/Resource prefixes/)).not.toBeInTheDocument();
    expect(
      screen.queryByPlaceholderText("objects/contracts/2026/"),
    ).not.toBeInTheDocument();
  });

  it("holds the submit while a resource row is refused, and says which", async () => {
    open();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "agent-7" },
    });
    expect(issueButton()).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    expect(
      screen.getByText("Resource 1: Choose a collection."),
    ).toBeInTheDocument();
    expect(issueButton()).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Remove resource 1" }));
    await addResource(1, "docs", "a//b");
    expect(
      screen.getByText(
        "Resource 1: The key prefix is refused: it holds an empty segment.",
      ),
    ).toBeInTheDocument();
    expect(issueButton()).toBeDisabled();
  });

  // The resources go out exactly as the SDKs build them: the nested
  // collection is one segment, not collection "invoices" with key "2026/…".
  it("sends the picked resources as the SDKs build them", async () => {
    h.issue.mockRejectedValue(new Error("boom"));
    open();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "agent-7" },
    });
    await addResource(1, "docs", "contracts/2026/");
    await addResource(2, NESTED, "");
    fireEvent.click(issueButton());
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
    expect(h.issue.mock.calls[0][0].caveats.resourcePrefixes).toEqual([
      `object://${TENANT}/docs/contracts/2026/`,
      `object://${TENANT}/invoices.2026.q1/`,
    ]);
  });

  // "Server default" sends no TTL; the label says what the server gives one.
  it("labels the server's default lifetime with its length", async () => {
    open();
    fireEvent.click(screen.getByLabelText("Expires after"));
    expect(
      await screen.findByRole("option", { name: "Server default (15 min)" }),
    ).toBeInTheDocument();
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

  // object:presign grants no action: the dialog says it only adds a check
  // beside the ops its row names.
  it("says object:presign adds a check beside its paired ops", async () => {
    open();
    const unlocks = await screen.findByRole("group", { name: UNLOCKS });
    expect(within(unlocks).queryByText(PRESIGN_LINE)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "object:presign" }));
    expect(within(unlocks).getByText(PRESIGN_LINE)).toHaveTextContent(
      "object:presign adds a check beside put or delete; it unlocks nothing on its own.",
    );
    // A check, not a warning: the manage note is the only one.
    expect(screen.queryByRole("note")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "object:presign" }));
    expect(within(unlocks).queryByText(PRESIGN_LINE)).not.toBeInTheDocument();
  });

  it("says share is decided by the caveats alone, and what it opens", async () => {
    open();
    const unlocks = await screen.findByRole("group", { name: UNLOCKS });
    fireEvent.click(screen.getByRole("button", { name: "share" }));
    expect(within(unlocks).getByText(SHARE_LINE)).toHaveTextContent(
      "share is decided by the caveats alone: it opens CapabilityService.Delegate.",
    );
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("names an op the server's table does not list", async () => {
    open();
    await screen.findByRole("group", { name: UNLOCKS });
    fireEvent.click(screen.getByRole("button", { name: "object:tag" }));
    expect(
      screen.getByText(/The server's table does not say what/),
    ).toHaveTextContent(
      "The server's table does not say what object:tag unlocks; it is not counted as granted.",
    );
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

    // A negative limit, and a limit no request could use, are refused here
    // as the server would refuse them.
    for (const value of ["-3", "0"]) {
      fireEvent.change(screen.getByLabelText("Max requests"), {
        target: { value },
      });
      expect(screen.getByLabelText("Max requests")).toHaveAccessibleDescription(
        "A whole number, at least 1, or Unlimited.",
      );
      expect(
        screen.getByRole("button", { name: "Issue capability" }),
      ).toBeDisabled();
    }
  });
});
