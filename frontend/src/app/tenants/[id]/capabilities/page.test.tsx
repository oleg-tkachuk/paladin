import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Protective net for decomposing the capabilities Issue dialog. The page talks
// to capabilityClient directly, so mock that + the tenant context. Covers the
// header, the issue-dialog open, the empty-subject guard, and the submit→issue
// wiring — the behaviors the extraction must preserve.
const h = vi.hoisted(() => ({
  list: vi.fn(),
  getUsage: vi.fn(),
  issue: vi.fn(),
  revoke: vi.fn(),
  revokeBiscuit: vi.fn(),
  getBiscuitUsage: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  capabilityClient: {
    list: h.list,
    getUsage: h.getUsage,
    issue: h.issue,
    revoke: h.revoke,
    revokeBiscuit: h.revokeBiscuit,
    getBiscuitUsage: h.getBiscuitUsage,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import CapabilitiesPage from "./page";

// The page has its own Subject box for browsing; this is the dialog's.
const dialogSubject = () =>
  within(screen.getByRole("dialog")).getByLabelText(/Subject/);
const issueButtons = () =>
  screen.getAllByRole("button", { name: /Issue capability/i });

beforeEach(() => {
  h.list.mockResolvedValue({ capabilities: [] });
  h.getUsage.mockResolvedValue({
    requestCount: 0n,
    spentMicros: 0n,
    unitCode: "UNIT",
  });
  h.issue.mockReset();
  h.revoke.mockReset();
  h.showNotification.mockReset();
  localStorage.clear();
});

describe("CapabilitiesPage", () => {
  it("renders the header and an issue action", () => {
    render(<CapabilitiesPage />);
    expect(
      screen.getByRole("heading", { name: "Capabilities" }),
    ).toBeInTheDocument();
    expect(issueButtons().length).toBeGreaterThan(0);
  });

  // react-query's refetch ignores `enabled`: with no principal chosen, closing
  // the issue dialog or submitting the filter sent List an empty subject,
  // which the server refuses, and the page showed the refusal as a load error.
  it("never lists without a principal", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    await userEvent.keyboard("{Escape}");
    fireEvent.submit(
      screen.getByRole("button", { name: /^Browse$/i }).closest("form")!,
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(h.list).not.toHaveBeenCalled();
    expect(screen.queryByText(/could not be loaded/i)).not.toBeInTheDocument();
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
          maxRequests: 0n,
          maxBudgetMicros: 0n,
          unitCode: "UNIT",
          allowTaintedRead: false,
          idempotencyKeyRequired: false,
        },
        subject: { kind: 2, tenantId: "t-1", subject: "agent-x" },
        generation: 0n,
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

  // ── row-menu flows (Details + Revoke) ─────────────────────────────────
  // Seed the last-browsed principal so the page hydrates + lists on mount,
  // giving us a row to drive the kebab menu against.
  const makeCap = (id: string) => ({
    id,
    issuer: "iss",
    parentId: "",
    generation: 0n,
    subject: { kind: 2, tenantId: "t-1", subject: "agent-x" },
    audience: ["data"],
    caveats: {
      ops: ["get"],
      resourcePrefixes: [],
      resourceUris: [],
      sourceIpCidr: [],
      maxRequests: 0n,
      maxBudgetMicros: 0n,
      unitCode: "UNIT",
      allowTaintedRead: false,
      idempotencyKeyRequired: false,
    },
    issuedAt: undefined,
    notBefore: undefined,
    expiresAt: undefined,
  });
  const seedBrowse = () =>
    localStorage.setItem(
      "paladin:capabilities:lastBrowse:t-1",
      JSON.stringify({ kind: "2", subject: "agent-x" }),
    );

  it("opens the details dialog from the row menu", async () => {
    seedBrowse();
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByText("Actions for capability cap-1"),
    );
    await userEvent.click(screen.getByText("View details"));
    expect(await screen.findByText("Capability details")).toBeInTheDocument();
  });

  it("revokes a capability from the row menu", async () => {
    seedBrowse();
    h.list.mockResolvedValue({ capabilities: [makeCap("cap-1")] });
    h.revoke.mockResolvedValue({});
    render(<CapabilitiesPage />);
    await userEvent.click(
      await screen.findByText("Actions for capability cap-1"),
    );
    await userEvent.click(screen.getByText("Revoke"));
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith(
        expect.objectContaining({ id: "cap-1" }),
      ),
    );
  });
});

// A failed browse said "No capabilities for this principal".
describe("CapabilitiesPage failed list", () => {
  it("says the list could not be loaded, not that there are none", async () => {
    window.localStorage.setItem(
      "paladin:capabilities:lastBrowse:t-1",
      JSON.stringify({ subject: "agent-1" }),
    );
    h.list.mockRejectedValue(new Error("unavailable"));
    render(<CapabilitiesPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Capabilities could not be loaded/,
    );
    expect(screen.queryByText(/No capabilities for this principal/)).toBeNull();
    window.localStorage.clear();
  });
});
