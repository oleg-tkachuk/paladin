import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { onlineManager } from "@tanstack/react-query";
import { render, screen } from "@/test/utils";
import { Code, ConnectError } from "@connectrpc/connect";
import { CAPABILITIES_DOCS_URL } from "@/constants";

const h = vi.hoisted(() => ({
  audit: vi.fn(),
  ops: vi.fn(),
  budget: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { listAuditLog: h.audit },
  adminOperationClient: { listOperations: h.ops },
  tenantBudgetClient: { summarize: h.budget },
}));

import { DashboardWidgets } from "./DashboardWidgets";

const EMPTY_STATES = [
  "No recent activity.",
  "No failures in the recent window.",
  "No tenants are approaching their cap.",
];

describe("DashboardWidgets", () => {
  beforeEach(() => {
    h.audit.mockReset();
    h.ops.mockReset();
    h.budget.mockReset();
  });
  afterEach(() => onlineManager.setOnline(true));

  it("shows the empty states when every query answers with nothing", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.ops.mockResolvedValue({ operations: [] });
    h.budget.mockResolvedValue({ summaries: [] });

    render(<DashboardWidgets />);

    for (const text of EMPTY_STATES) {
      expect(await screen.findByText(text)).toBeInTheDocument();
    }
  });

  // A paused query (offline, or a retry held back while the window is
  // unfocused) has no answer yet; "no failures" would be a claim.
  it("does not claim an empty result while the queries are paused", () => {
    onlineManager.setOnline(false);

    render(<DashboardWidgets />);

    for (const text of EMPTY_STATES) {
      expect(screen.queryByText(text)).toBeNull();
    }
  });

  it("says a widget is unavailable when its query fails", async () => {
    h.audit.mockRejectedValue(new Error("fetch failed"));
    h.ops.mockRejectedValue(new Error("fetch failed"));
    h.budget.mockRejectedValue(new Error("unavailable"));

    render(<DashboardWidgets />);

    expect(
      await screen.findByText("Activity unavailable."),
    ).toBeInTheDocument();
    expect(
      await screen.findByText("Operations unavailable."),
    ).toBeInTheDocument();
    for (const text of EMPTY_STATES) {
      expect(screen.queryByText(text)).toBeNull();
    }
  });

  // It used to guess "capability subsystem may be disabled" for any failure.
  it("says why the budget summary is unavailable and how to enable it", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.ops.mockResolvedValue({ operations: [] });
    h.budget.mockRejectedValue(
      new ConnectError(
        "capability subsystem disabled; tenant budget unavailable",
        Code.Unavailable,
      ),
    );

    render(<DashboardWidgets />);

    expect(
      await screen.findByText(
        "Summary unavailable: capability subsystem disabled; tenant budget unavailable",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "How to enable capabilities" }),
    ).toHaveAttribute("href", CAPABILITIES_DOCS_URL);
  });
});
