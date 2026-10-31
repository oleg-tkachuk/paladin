import { describe, expect, it, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  within,
  typeToConfirm,
} from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError } from "@connectrpc/connect";

// Protective net for decomposing the capabilities Issue dialog. The page talks
// to capabilityClient directly, so mock that + the tenant context. Covers the
// header, the issue-dialog open, the empty-subject guard, and the submit→issue
// wiring — the behaviors the extraction must preserve.
const h = vi.hoisted(() => ({
  tenantId: "t-1",
  list: vi.fn(),
  getUsage: vi.fn(),
  listOperationGrants: vi.fn(),
  issue: vi.fn(),
  revoke: vi.fn(),
  revokeBiscuit: vi.fn(),
  getBiscuitUsage: vi.fn(),
  get: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  billingClient: {
    getBillingSettings: () => Promise.resolve({ currencyCode: "EUR" }),
  },
  capabilityClient: {
    list: h.list,
    getUsage: h.getUsage,
    issue: h.issue,
    revoke: h.revoke,
    revokeBiscuit: h.revokeBiscuit,
    getBiscuitUsage: h.getBiscuitUsage,
    get: h.get,
    listOperationGrants: h.listOperationGrants,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({ tenantId: h.tenantId }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { LINKED_ROW_ATTR } from "@/components/ui/table";
import {
  BiscuitStatus,
  CapabilityDisposition,
  CapabilityGrantBasis,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";

import CapabilitiesPage from "./page";
import { MIN_MAX_REQUESTS } from "./_ceilings";

// The page has its own Subject box for browsing; this is the dialog's.
const dialogSubject = () =>
  within(screen.getByRole("dialog")).getByLabelText(/Subject/);
const issueButtons = () =>
  screen.getAllByRole("button", { name: /Issue capability/i });

// A listed capability, its subject an agent of the page's tenant.
const makeCap = (id: string) => ({
  id,
  issuer: "iss",
  parentId: "",
  subject: { kind: 2, tenantId: "t-1", subject: "agent-x" },
  audience: ["data"],
  caveats: {
    ops: ["get"],
    resourcePrefixes: [],
    resourceUris: [],
    sourceIpCidr: [],
    requestCeiling: { case: "unlimitedRequests", value: true },
    copyCeiling: { case: "unlimitedCopies", value: true },
    budgetCeiling: {
      case: "unlimitedBudget",
      value: { currencyCode: "XXX" },
    },
    allowTaintedRead: false,
    idempotencyKeyRequired: false,
  },
  issuedAt: undefined,
  notBefore: undefined,
  expiresAt: undefined,
});

beforeEach(() => {
  h.tenantId = "t-1";
  h.list.mockResolvedValue({ capabilities: [] });
  h.listOperationGrants.mockResolvedValue({
    grants: [
      {
        op: "get",
        basis: CapabilityGrantBasis.BUILTIN,
        actions: ["GetObject", "PresignGet"],
        pairedOps: [],
        procedures: [],
      },
    ],
  });
  h.getUsage.mockResolvedValue({
    requestCount: 0n,
    spent: { currencyCode: "XXX", units: 0n, nanos: 0 },
  });
  h.issue.mockReset();
  h.revoke.mockReset();
  h.showNotification.mockReset();
});

describe("CapabilitiesPage", () => {
  it("renders the header and an issue action", () => {
    render(<CapabilitiesPage />);
    expect(
      screen.getByRole("heading", { name: "Capabilities" }),
    ).toBeInTheDocument();
    expect(issueButtons().length).toBeGreaterThan(0);
  });

  // The page opens on the tenant's capabilities still in force, newest
  // first: an operator who knows no subject sees them all the same.
  it("lists the tenant's active capabilities, newest first, on opening", async () => {
    render(<CapabilitiesPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalled());
    expect(h.list.mock.calls[0][0]).toMatchObject({
      tenantId: "t-1",
      principalKind: PrincipalKind.UNSPECIFIED,
      subject: "",
      dispositions: [CapabilityDisposition.ACTIVE],
      sortOrder: SortOrder.DESC,
      pageToken: "",
    });
  });

  // A grant links here with its session: the list is what revoking it stops,
  // until the operator widens it to every session.
  it("lists one OAuth session's capabilities from the link, then every session's", async () => {
    window.history.replaceState(
      null,
      "",
      "/tenants/t-1/capabilities?session=s-1",
    );
    render(<CapabilitiesPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalled());
    expect(h.list.mock.calls[0][0]).toMatchObject({ session: "s-1" });
    expect(screen.getByText("s-1")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Every session" }));
    await waitFor(() =>
      expect(h.list.mock.lastCall?.[0]).toMatchObject({ session: "" }),
    );
    expect(window.location.search).toBe("");
    window.history.replaceState(null, "", "/");
  });

  // A capability links here with its run: the list is what revoking the run
  // stops, offered from the bar, until the operator widens it to every run.
  it("lists one agent run's capabilities from the link, offers to revoke it, then every run's", async () => {
    window.history.replaceState(null, "", "/tenants/t-1/capabilities?run=r-1");
    render(<CapabilitiesPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalled());
    expect(h.list.mock.calls[0][0]).toMatchObject({ runId: "r-1" });
    expect(screen.getByText("r-1")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Revoke this run" }));
    expect(
      await screen.findByRole("alertdialog", {
        name: "Revoke this agent run?",
      }),
    ).toBeInTheDocument();
    fireEvent.keyDown(document.activeElement ?? document.body, {
      key: "Escape",
    });

    fireEvent.click(screen.getByRole("button", { name: "Every run" }));
    await waitFor(() =>
      expect(h.list.mock.lastCall?.[0]).toMatchObject({ runId: "" }),
    );
    window.history.replaceState(null, "", "/");
  });

  // A list superseded by a newer filter is cancelled: that is no failure.
  it("says nothing of a list it cancelled", async () => {
    h.list.mockRejectedValue(new ConnectError("cancelled", Code.Canceled));
    render(<CapabilitiesPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeNull());
    expect(h.showNotification).not.toHaveBeenCalled();
  });

  it("toasts a list that failed", async () => {
    h.list.mockRejectedValue(new ConnectError("down", Code.Unavailable));
    render(<CapabilitiesPage />);
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", title: "Load failed" }),
      ),
    );
  });

  // One list once typing settles, not one per keystroke.
  it("lists by the subject once typing settles", async () => {
    render(<CapabilitiesPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalledTimes(1));
    await userEvent.type(
      screen.getByPlaceholderText(/agent-id, user subject/),
      "agent-x",
    );
    await waitFor(() =>
      expect(h.list).toHaveBeenLastCalledWith(
        expect.objectContaining({ subject: "agent-x" }),
        expect.anything(),
      ),
    );
    expect(h.list).toHaveBeenCalledTimes(2);
  });

  // A new filter keeps the rows on screen until its list answers. Without it
  // the table fell back to its skeleton for the round trip: rows unmounted
  // under an open row menu, which the e2e suite saw as its Revoke item
  // detached mid-click.
  it("keeps the rows, and an open row menu, while a new filter loads", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    const actions = await screen.findByRole("button", {
      name: /Actions for capability cap-1/,
    });
    // As the e2e suite does: type the subject, and open the row's menu before
    // the typing settles and the filtered list is asked for.
    h.list.mockReturnValue(new Promise(() => {}));
    await userEvent.type(
      screen.getByPlaceholderText(/agent-id, user subject/),
      "agent-x",
    );
    await userEvent.click(actions);
    expect(
      screen.getByRole("menuitem", { name: /Revoke/ }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(h.list).toHaveBeenLastCalledWith(
        expect.objectContaining({ subject: "agent-x" }),
        expect.anything(),
      ),
    );
    expect(
      screen.getByText("Actions for capability cap-1"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: /Revoke/ }),
    ).toBeInTheDocument();
  });

  // Never across tenants: another tenant's rows must not stand in for these
  // while this one's list loads.
  it("keeps no other tenant's rows while a tenant's list loads", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    const { rerender } = render(<CapabilitiesPage />);
    await screen.findByText("Actions for capability cap-1");
    h.list.mockReturnValue(new Promise(() => {}));
    h.tenantId = "t-2";
    rerender(<CapabilitiesPage />);
    await waitFor(() =>
      expect(h.list).toHaveBeenLastCalledWith(
        expect.objectContaining({ tenantId: "t-2" }),
        expect.anything(),
      ),
    );
    expect(screen.queryByText("Actions for capability cap-1")).toBeNull();
  });

  // The list spans the tenant's principals, so each row names its own.
  it("names each capability's principal", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    const row = (
      await screen.findByText("Actions for capability cap-1")
    ).closest("tr") as HTMLElement;
    expect(within(row).getByText("agent")).toBeInTheDocument();
    expect(within(row).getByTitle("agent-x")).toHaveTextContent("agent-x");
  });

  it("lists the revoked ones too once their box is checked", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(screen.getByRole("checkbox", { name: "revoked" }));
    await waitFor(() =>
      expect(h.list).toHaveBeenLastCalledWith(
        expect.objectContaining({
          dispositions: [
            CapabilityDisposition.ACTIVE,
            CapabilityDisposition.REVOKED,
          ],
        }),
        expect.anything(),
      ),
    );
  });

  // No status would ask the server for its default instead.
  it("keeps the last status checked", () => {
    render(<CapabilitiesPage />);
    expect(screen.getByRole("checkbox", { name: "active" })).toBeDisabled();
  });

  it("shows each capability's status as the list names it", async () => {
    h.list.mockResolvedValue({
      capabilities: [
        { ...makeCap("cap-1"), disposition: CapabilityDisposition.REVOKED },
      ],
    });
    render(<CapabilitiesPage />);
    const row = (
      await screen.findByText("Actions for capability cap-1")
    ).closest("tr") as HTMLElement;
    expect(within(row).getByText("revoked")).toBeInTheDocument();
  });

  it("opens the copy-revocation dialog from the header action", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /Revoke a copy/ }),
    );
    expect(
      screen.getByRole("alertdialog", { name: /Revoke one copy of a Biscuit/ }),
    ).toBeInTheDocument();
  });

  it("opens the copy-usage dialog from the header action", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(screen.getByRole("button", { name: /Copy usage/ }));
    expect(
      screen.getByRole("dialog", { name: /Usage of one Biscuit copy/ }),
    ).toBeInTheDocument();
  });

  it("opens the issue dialog from the header action", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(dialogSubject()).toBeInTheDocument();
  });

  it("does not call issue when the subject is empty", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    expect(h.issue).not.toHaveBeenCalled();
  });

  it("calls capabilityClient.issue when a subject is provided", async () => {
    // Reject so the success path's token-reveal render is skipped; we only
    // assert the submit wires through to the client.
    h.issue.mockRejectedValue(new Error("boom"));
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    await userEvent.type(dialogSubject(), "agent-x");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
  });

  // Left alone, the budget's unit is the server's to apply: the platform's
  // currency, which the form shows but does not send.
  it("sends unlimited ceilings explicitly, the budget's unit left to the server", async () => {
    h.issue.mockRejectedValue(new Error("boom"));
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    await userEvent.type(dialogSubject(), "agent-x");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
    const caveats = h.issue.mock.calls[0][0].caveats;
    expect(caveats.requestCeiling).toEqual({
      case: "unlimitedRequests",
      value: true,
    });
    expect(caveats.budgetCeiling.case).toBe("unlimitedBudget");
    expect(caveats.budgetCeiling.value).toMatchObject({ currencyCode: "" });
    expect(caveats.copyCeiling).toEqual({
      case: "unlimitedCopies",
      value: true,
    });
  });

  // A quota of Biscuit copies is a limit like the others: 0 allows no copy
  // with limits of its own.
  it("sends a typed quota of Biscuit copies", async () => {
    h.issue.mockRejectedValue(new Error("boom"));
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    const dialog = within(screen.getByRole("dialog"));
    await userEvent.type(dialogSubject(), "agent-x");
    const copies = dialog.getByLabelText(/Max Biscuit copies/);
    await userEvent.click(
      copies.parentElement!.querySelector("button[aria-pressed]")!,
    );
    await userEvent.type(copies, "3");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
    expect(h.issue.mock.calls[0][0].caveats.copyCeiling).toEqual({
      case: "maxCopies",
      value: 3n,
    });
  });

  // 0 is a limit now, not "none": a capability can be issued frozen.
  it("sends the least request limit as typed, and never a zero", async () => {
    h.issue.mockRejectedValue(new Error("boom"));
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    const dialog = within(screen.getByRole("dialog"));
    await userEvent.type(dialogSubject(), "agent-x");
    await userEvent.click(
      dialog.getAllByRole("button", { name: /Unlimited/ })[0],
    );
    const limit = dialog.getByLabelText(/Max requests/);
    await userEvent.type(limit, "0");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    expect(h.issue).not.toHaveBeenCalled();

    await userEvent.clear(limit);
    await userEvent.type(limit, String(MIN_MAX_REQUESTS));
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
    expect(h.issue.mock.calls[0][0].caveats.requestCeiling).toEqual({
      case: "maxRequests",
      value: MIN_MAX_REQUESTS,
    });
  });

  it("sends a typed budget exactly to the nano", async () => {
    h.issue.mockRejectedValue(new Error("boom"));
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    const dialog = within(screen.getByRole("dialog"));
    await userEvent.type(dialogSubject(), "agent-x");
    // The second Unlimited toggle is the budget's; the first is requests'.
    await userEvent.click(
      dialog.getAllByRole("button", { name: /Unlimited/ })[1],
    );
    await userEvent.type(dialog.getByLabelText(/Max budget/), "0.300000001");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
    const budget = h.issue.mock.calls[0][0].caveats.budgetCeiling;
    expect(budget.case).toBe("maxBudget");
    expect(budget.value).toMatchObject({
      currencyCode: "",
      units: 0n,
      nanos: 300_000_001,
    });
  });

  it("reveals the token and seeds the browse list on a successful issue", async () => {
    h.issue.mockResolvedValue({
      token: "cap-jwt-xyz",
      capability: {
        id: "cap-new-1",
        audience: ["data"],
        caveats: {
          ops: ["get"],
          resourcePrefixes: [],
          resourceUris: [],
          sourceIpCidr: [],
          requestCeiling: { case: "unlimitedRequests", value: true },
          copyCeiling: { case: "unlimitedCopies", value: true },
          budgetCeiling: {
            case: "unlimitedBudget",
            value: { currencyCode: "XXX" },
          },
          allowTaintedRead: false,
          idempotencyKeyRequired: false,
        },
        subject: { kind: 2, tenantId: "t-1", subject: "agent-x" },
      },
    });
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    await userEvent.type(dialogSubject(), "agent-x");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);

    // One-shot token reveal panel (the JWT is shown in a readOnly textarea).
    expect(await screen.findByDisplayValue("cap-jwt-xyz")).toBeInTheDocument();
    // Success toast.
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ type: "success", title: "Capability issued" }),
    );
    // The browse filter is synced to the just-issued principal so the list the
    // operator lands on shows it (the success path's setSubject + setItems).
    expect(screen.getByDisplayValue("agent-x")).toBeInTheDocument();
  });

  // The Biscuit is the only way to a copy: revoking one or reading its usage
  // pastes a copy, so it is shown beside the token, once — or why it is not.
  it.each([
    [BiscuitStatus.ISSUED, "cap-biscuit-abc", "cap-biscuit-abc", null],
    [BiscuitStatus.KEY_BOUND, "", null, /bound to a key/],
  ])(
    "shows the issued Biscuit, or why there is none (status %s)",
    async (biscuitStatus, biscuit, shown, why) => {
      h.issue.mockResolvedValue({
        token: "cap-jwt-xyz",
        biscuit,
        biscuitStatus,
        capability: {
          id: "cap-new-2",
          subject: { kind: 2, subject: "agent-y" },
        },
      });
      render(<CapabilitiesPage />);
      await userEvent.click(issueButtons()[0]);
      await userEvent.type(dialogSubject(), "agent-y");
      fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
      expect(
        await screen.findByDisplayValue("cap-jwt-xyz"),
      ).toBeInTheDocument();
      if (shown) {
        expect(screen.getByLabelText("Biscuit")).toHaveValue(shown);
      } else {
        expect(screen.queryByLabelText("Biscuit")).not.toBeInTheDocument();
        expect(screen.getByText(why!)).toBeInTheDocument();
      }
    },
  );

  // ── row-menu flows (Details + Revoke) ─────────────────────────────────
  it("opens the details dialog from the row menu", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByText("Actions for capability cap-1"),
    );
    await userEvent.click(screen.getByText("View details"));
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
  });

  it("opens the details beside the list from the row itself", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    const row = (
      await screen.findByText("Actions for capability cap-1")
    ).closest("tr") as HTMLElement;
    await userEvent.click(row);
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
    expect(window.location.search).toBe("?id=cap-1");
    // Revoke from the panel asks for the same confirmation as the menu's.
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();
    window.history.replaceState(null, "", "/");
  });

  it("puts the capability open in details in the address", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByText("Actions for capability cap-1"),
    );
    await userEvent.click(screen.getByText("View details"));
    expect(window.location.search).toBe("?id=cap-1");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
  });

  // The audit log links here by ?id=.
  it("opens the details of the capability a link names, marking its row", async () => {
    window.history.replaceState(null, "", "/tenants/t-1/capabilities?id=cap-2");
    h.list.mockResolvedValue({
      capabilities: [makeCap("cap-1"), makeCap("cap-2")],
    });
    render(<CapabilitiesPage />);
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
    await screen.findByText("Actions for capability cap-2");
    const row = (id: string) =>
      screen.getByText(`Actions for capability ${id}`).closest("tr");
    expect(row("cap-2")).toHaveAttribute(LINKED_ROW_ATTR);
    expect(row("cap-1")).not.toHaveAttribute(LINKED_ROW_ATTR);
    window.history.replaceState(null, "", "/");
  });

  it("reads a linked capability the browsed list does not hold", async () => {
    window.history.replaceState(null, "", "/tenants/t-1/capabilities?id=cap-9");
    h.get.mockResolvedValue({ capability: makeCap("cap-9") });
    render(<CapabilitiesPage />);
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
    expect(h.get).toHaveBeenCalledWith({ id: "cap-9" }, expect.anything());
    window.history.replaceState(null, "", "/");
  });

  it("reads a listed linked capability's record once, page and dialog together", async () => {
    window.history.replaceState(null, "", "/tenants/t-1/capabilities?id=cap-1");
    h.get.mockReset();
    h.get.mockResolvedValue({ capability: makeCap("cap-1") });
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
    await waitFor(() => expect(h.get).toHaveBeenCalled());
    expect(h.get).toHaveBeenCalledTimes(1);
    window.history.replaceState(null, "", "/");
  });

  it("revokes a capability from the row menu", async () => {
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    h.revoke.mockResolvedValue({});
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByText("Actions for capability cap-1"),
    );
    await userEvent.click(screen.getByText("Revoke"));
    await typeToConfirm("revoke");
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith(
        expect.objectContaining({ id: "cap-1" }),
      ),
    );
  });
});

// A failed list said there were no capabilities.
describe("CapabilitiesPage failed list", () => {
  it("says the list could not be loaded, not that there are none", async () => {
    h.list.mockRejectedValue(new Error("unavailable"));
    render(<CapabilitiesPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Capabilities could not be loaded/,
    );
    expect(screen.queryByText(/No capabilities match/)).toBeNull();
  });
});

// jsdom has no layout, so these pin the structure that keeps the table inside
// its card: column visibility keyed to the card's width, and full-width rows
// that really span every column.
describe("CapabilitiesPage table layout", () => {
  const VIEWPORT_BREAKPOINT = /(^|\s)(sm|md|lg|xl|2xl):/;
  const headers = () => Array.from(document.querySelectorAll("thead th"));

  it("spans every column with the placeholder row", async () => {
    render(<CapabilitiesPage />);
    const cell = (await screen.findByText(/No capabilities match/)).closest(
      "td",
    )!;
    expect(cell.colSpan).toBe(headers().length);
  });

  // The sidebar takes a fixed share of the window, so a viewport breakpoint
  // showed every column on a card too narrow for them and pushed Status and
  // the actions menu off its right edge.
  it("shows optional columns by the card's width, not the viewport's", () => {
    render(<CapabilitiesPage />);
    const card = document
      .querySelector('[data-slot="table-container"]')!
      .closest(".\\@container");
    expect(card).not.toBeNull();
    for (const th of headers()) {
      expect(th.className).not.toMatch(VIEWPORT_BREAKPOINT);
    }
  });

  it("never hides the status column or the actions menu", () => {
    render(<CapabilitiesPage />);
    const always = headers().filter((th) =>
      /^(Status|Actions)$/.test(th.textContent!.trim()),
    );
    expect(always).toHaveLength(2);
    for (const th of always) expect(th.className).not.toMatch(/\bhidden\b/);
  });
});

describe("CapabilitiesPage paging", () => {
  // A tenant's capabilities past the first page are a click away, so they
  // can be revoked from the page.
  it("reads the next page on Load more", async () => {
    h.list
      .mockResolvedValueOnce({
        capabilities: [makeCap("cap-1")],
        nextPageToken: "p2",
      })
      .mockResolvedValueOnce({
        capabilities: [makeCap("cap-2")],
        nextPageToken: "",
      });
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Load more" }),
    );
    await screen.findByText("Actions for capability cap-2");
    expect(h.list.mock.calls[1][0].pageToken).toBe("p2");
    expect(
      screen.getByText("Actions for capability cap-1"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });
});
