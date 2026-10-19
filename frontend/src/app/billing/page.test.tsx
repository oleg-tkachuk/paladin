import { beforeEach, describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import userEvent from "@testing-library/user-event";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  tenantsError: null as string | null,
  summary: vi.fn(),
  series: vi.fn(),
  showNotification: vi.fn(),
  replace: vi.fn(),
  search: "",
}));
vi.mock("@/lib/connect/client", () => ({
  billingClient: {
    getTenantSummary: h.summary,
    getTenantTimeSeries: h.series,
    getBillingSettings: () => Promise.resolve({ currencyCode: "EUR" }),
  },
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ actions }: { actions?: React.ReactNode }) => (
    <div>{actions}</div>
  ),
}));
vi.mock("@/components/features/audit/ActorName", () => ({
  ActorName: ({ subject }: { subject: string }) => subject,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: h.replace }),
  useSearchParams: () => new URLSearchParams(h.search),
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: (set: string = "active") => ({
    error: h.tenantsError,
    refetch: vi.fn(),
    fetchedAt: 1,
    // Answers as ListTenants does: trashed tenants only when asked for.
    tenants: [
      { tenantId: "t-1", slug: "platform", displayName: "Platform" },
      { tenantId: "t-2", slug: "globex", displayName: "Globex" },
      {
        tenantId: "t-3",
        slug: "smoke",
        displayName: "Smoke",
        deletedAt: { seconds: 1n, nanos: 0 },
      },
    ].filter((t) => set === "all" || !t.deletedAt),
  }),
}));

import BillingPage from "./page";
import { moneyFromNanos } from "@/lib/format/money";

const usd = (nanos: bigint) => moneyFromNanos(nanos, "USD");

describe("BillingPage", () => {
  beforeEach(() => {
    h.summary.mockReset();
    h.series.mockReset();
    h.showNotification.mockReset();
    h.replace.mockReset();
    h.search = "";
    h.tenantsError = null;
  });

  const emptySummary = {
    total: usd(0n),
    budgetCeiling: { case: undefined },
    chargeCount: 0n,
    topCapabilities: [],
    topActors: [],
    topOps: [],
  };

  it("sorts a breakdown by spend as an amount, not as text", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: usd(12_000_000_000n),
      chargeCount: 3n,
      // Ranked by the server; "9" outsorts "10" as text but not as money.
      topOps: [
        { label: "put", spent: usd(10_000_000_000n), chargeCount: 1n },
        { label: "get", spent: usd(9_000_000_000n), chargeCount: 1n },
        { label: "list", spent: usd(500_000_000n), chargeCount: 1n },
      ],
    });
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    // The card shows before the summary does: wait for its rows.
    await screen.findByText("list");
    const card = screen
      .getByText("Top operations")
      .closest("[data-slot=card]") as HTMLElement;
    const labels = () =>
      within(card)
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[0].textContent);
    expect(labels()).toEqual(["put", "get", "list"]);
    fireEvent.click(within(card).getByRole("button", { name: /^Spend/ }));
    expect(labels()).toEqual(["list", "get", "put"]);
  });

  // The console's scope is usually the platform tenant, which is billed
  // nothing; the page reads whichever tenant ?tenant= names.
  it("reads the tenant the URL names, over the scope's", async () => {
    h.search = "tenant=t-2";
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });

    render(<BillingPage />);

    await screen.findByText("No charges in this period");
    expect(h.summary).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-2" }),
      expect.anything(),
    );
    expect(h.series).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-2" }),
      expect.anything(),
    );
    expect(
      await screen.findByText(/Globex was not billed/),
    ).toBeInTheDocument();
  });

  // A picker whose options failed to load reads as "there is no one else".
  it("says when the tenant list could not be loaded", async () => {
    h.tenantsError = "permission denied";
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Tenants could not be loaded",
    );
  });

  it("falls back to the scope's tenant with no ?tenant=", async () => {
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    await screen.findByText("No charges in this period");
    expect(h.summary).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-1" }),
      expect.anything(),
    );
  });

  it("offers trashed tenants too, marked as such", async () => {
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    fireEvent.click(await screen.findByRole("combobox", { name: "Tenant" }));
    expect(
      await screen.findByRole("option", { name: "Smoke · in trash" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("option", { name: "Globex" }));
    expect(h.replace).toHaveBeenCalledWith("?tenant=t-2");
  });

  it("draws every day of the period, the quiet ones at zero", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: usd(2_000_000n),
      chargeCount: 2n,
    });
    const day = (iso: string) => ({
      seconds: BigInt(Date.parse(iso) / 1000),
      nanos: 0,
    });
    const lastDay = new Date();
    lastDay.setUTCHours(0, 0, 0, 0);
    h.series.mockResolvedValue({
      buckets: [
        {
          start: day(lastDay.toISOString()),
          spent: usd(2_000_000n),
          chargeCount: 2n,
        },
      ],
    });
    h.search = "tenant=t-2";

    const { container } = render(<BillingPage />);

    const table = await screen.findByRole("table", { name: "Spend per day" });
    // A column per day of the 30-day period (31 when it spans a midnight).
    expect(
      container.querySelectorAll("svg rect").length,
    ).toBeGreaterThanOrEqual(30);
    // Only the billed day is a row of the accessible table; the chart's axis
    // spans the whole 30-day period.
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    // A sub-cent average reads in four places, not as US$0.00.
    expect(
      screen.getByText(/Average: US\$0\.0001 per day/),
    ).toBeInTheDocument();
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

  it("links to the tenant's budget, to set one or change it", async () => {
    h.search = "tenant=t-2";
    h.summary.mockResolvedValue({ ...emptySummary, chargeCount: 1n });
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    expect(
      await screen.findByRole("link", { name: "Set a budget" }),
    ).toHaveAttribute("href", "/tenants/globex/budget");
  });

  it("names the actors, not their subjects only", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      chargeCount: 1n,
      topActors: [{ label: "0199-user", spent: usd(1n), chargeCount: 1n }],
    });
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    const name = await screen.findByText("0199-user");
    expect(name.closest("[data-slot=card]")).toHaveTextContent("Top actors");
  });

  it("says what is wrong with a custom range, and asks for nothing", async () => {
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    await screen.findByText("No charges in this period");
    h.summary.mockClear();
    await userEvent.click(screen.getByRole("button", { name: "Custom" }));
    expect(screen.getByText("Choose both dates.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Period start"), {
      target: { value: "2026-10-05" },
    });
    fireEvent.change(screen.getByLabelText("Period end"), {
      target: { value: "2026-10-01" },
    });
    expect(screen.getByText("The start is after the end.")).toBeInTheDocument();
    expect(h.summary).not.toHaveBeenCalled();
  });

  it("offers no hourly chart of 30 days, and draws it daily", async () => {
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    const group = await screen.findByRole("group", { name: "Granularity" });
    expect(
      within(group).getByRole("button", { name: "Hourly" }),
    ).toBeDisabled();
    expect(
      within(group).getByRole("button", { name: "Daily" }),
    ).toHaveAttribute("aria-pressed", "true");
    await waitFor(() =>
      expect(h.series).toHaveBeenCalledWith(
        expect.objectContaining({ granularity: "day" }),
        expect.anything(),
      ),
    );
  });

  it("offers a longer period from an empty one", async () => {
    h.summary.mockResolvedValue(emptySummary);
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Look at the last 90 days" }),
    );
    expect(screen.getByRole("button", { name: "90 days" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("writes amounts in the platform's currency when the ledger names none", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: { currencyCode: "", units: 2n, nanos: 0 },
      chargeCount: 1n,
    });
    h.series.mockResolvedValue({ buckets: [] });
    render(<BillingPage />);
    expect(await screen.findByText("€2.0000")).toBeInTheDocument();
  });
});
