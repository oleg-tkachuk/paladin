import { beforeEach, describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  summary: vi.fn(),
  series: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  billingClient: {
    getTenantSummary: h.summary,
    getTenantTimeSeries: h.series,
  },
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));

import BillingPage from "./page";

describe("BillingPage", () => {
  beforeEach(() => {
    h.summary.mockReset();
    h.series.mockReset();
    h.showNotification.mockReset();
  });

  it("says billing is unavailable instead of reporting no charges", async () => {
    const err = new ConnectError(
      "billing: handler not wired",
      Code.Unavailable,
    );
    h.summary.mockRejectedValue(err);
    h.series.mockRejectedValue(err);

    render(<BillingPage />);

    expect(
      await screen.findByText("Billing is unavailable"),
    ).toBeInTheDocument();
    expect(screen.getByText("billing: handler not wired")).toBeInTheDocument();
    expect(screen.queryByText(/No capability charges/)).toBeNull();
    expect(screen.queryByText("Spend over time")).toBeNull();
  });

  it("shows the empty-period card when the period has no charges", async () => {
    h.summary.mockResolvedValue({
      totalMicros: 0n,
      unitCode: "USD",
      maxBudgetMicros: 0n,
      chargeCount: 0n,
      topCapabilities: [],
      topActors: [],
      topOps: [],
    });
    h.series.mockResolvedValue({ buckets: [], unitCode: "USD" });

    render(<BillingPage />);

    expect(
      await screen.findByText("No charges in this period"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Billing is unavailable")).toBeNull();
  });
});
