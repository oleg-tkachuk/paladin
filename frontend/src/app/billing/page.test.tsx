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
import { moneyFromNanos } from "@/lib/format/money";

const usd = (nanos: bigint) => moneyFromNanos(nanos, "USD");

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
      total: usd(0n),
      budgetCeiling: { case: undefined },
      chargeCount: 0n,
      topCapabilities: [],
      topActors: [],
      topOps: [],
    });
    h.series.mockResolvedValue({ buckets: [] });

    render(<BillingPage />);

    expect(
      await screen.findByText("No charges in this period"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Billing is unavailable")).toBeNull();
  });

  // No budget row, an unlimited budget and a cap of zero used to read as the
  // same 0; each now says what it is.
  it.each([
    [{ case: undefined }, "no budget configured"],
    [
      { case: "unlimitedBudget", value: { currencyCode: "USD" } },
      "unlimited budget",
    ],
    [{ case: "maxBudget", value: usd(0n) }, "100.0% of budget"],
    [{ case: "maxBudget", value: usd(4_000_000_000n) }, "25.0% of budget"],
  ])("describes the budget %o as %s", async (budgetCeiling, helper) => {
    h.summary.mockResolvedValue({
      total: usd(1_000_000_000n),
      budgetCeiling,
      chargeCount: 1n,
      topCapabilities: [],
      topActors: [],
      topOps: [],
    });
    h.series.mockResolvedValue({ buckets: [] });

    render(<BillingPage />);

    expect(await screen.findByText(helper)).toBeInTheDocument();
  });
});
