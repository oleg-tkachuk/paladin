import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
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
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  capabilityClient: {
    list: h.list,
    getUsage: h.getUsage,
    issue: h.issue,
    revoke: h.revoke,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import CapabilitiesPage from "./page";

const SUBJECT_PLACEHOLDER = /agent-id \/ user subject/i;
const issueButtons = () =>
  screen.getAllByRole("button", { name: /Issue capability/i });

beforeEach(() => {
  h.list.mockResolvedValue({ capabilities: [] });
  h.getUsage.mockResolvedValue({
    requestCount: 0n,
    spentAmount: 0,
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

  it("opens the issue dialog from the header action", async () => {
    render(<CapabilitiesPage />);
    await userEvent.click(issueButtons()[0]);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText(SUBJECT_PLACEHOLDER),
    ).toBeInTheDocument();
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
    await userEvent.type(
      screen.getByPlaceholderText(SUBJECT_PLACEHOLDER),
      "agent-x",
    );
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(h.issue).toHaveBeenCalled());
  });
});
