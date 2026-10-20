import { beforeEach, describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import userEvent from "@testing-library/user-event";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({ summary: vi.fn(), series: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  billingClient: {
    getTenantSummary: h.summary,
    getTenantTimeSeries: h.series,
    getBillingSettings: () => Promise.resolve({ currencyCode: "EUR" }),
  },
}));
vi.mock("@/components/features/audit/ActorName", () => ({
  ActorName: ({ subject }: { subject: string }) => subject,
}));
vi.mock("next/navigation", async () => {
  const r = await import("@/test/urlRouter");
  return {
    useRouter: () => ({ replace: r.urlRouter.replace }),
    useSearchParams: r.useTestSearchParams,
    usePathname: r.useTestPathname,
  };
});

import { urlRouter } from "@/test/urlRouter";
import { moneyFromNanos } from "@/lib/format/money";
import { TenantBilling } from "./TenantBilling";
import { useBillingPeriod } from "./useBillingPeriod";
import { RANKED_VISIBLE } from "./rankedSpend";

const usd = (nanos: bigint) => moneyFromNanos(nanos, "USD");
const emptySummary = {
  total: usd(0n),
  budgetCeiling: { case: undefined },
  chargeCount: 0n,
  topCapabilities: [],
  topActors: [],
  topOps: [],
};

function Harness() {
  const state = useBillingPeriod();
  return (
    <TenantBilling
      tenantId="t-2"
      tenantHandle="globex"
      tenantName="Globex"
      state={state}
    />
  );
}

describe("TenantBilling", () => {
  beforeEach(() => {
    h.summary.mockReset();
    h.series.mockReset();
    h.series.mockResolvedValue({ buckets: [] });
    urlRouter.reset("/tenants/globex/billing");
  });

  it("reads the tenant it is given", async () => {
    h.summary.mockResolvedValue(emptySummary);
    render(<Harness />);
    expect(
      await screen.findByText(/Globex was not billed/),
    ).toBeInTheDocument();
    expect(h.summary).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-2" }),
      expect.anything(),
    );
  });

  it("ranks a breakdown as the server does, its first few shown, the rest on request", async () => {
    const ops = Array.from({ length: RANKED_VISIBLE + 2 }, (_, i) => ({
      label: `op-${i}`,
      spent: usd(BigInt(10 - i) * 1_000_000_000n),
      chargeCount: 1n,
    }));
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: usd(100_000_000_000n),
      chargeCount: 7n,
      topOps: ops,
    });
    render(<Harness />);
    await screen.findByText("op-0");
    const card = screen
      .getByText("Top operations")
      .closest("[data-slot=card]") as HTMLElement;
    const labels = () =>
      within(card)
        .getAllByRole("listitem")
        .map((li) => li.querySelector("span")?.textContent);
    expect(labels()).toEqual(ops.slice(0, RANKED_VISIBLE).map((o) => o.label));
    // Each with its share of the whole period.
    expect(within(card).getByText(/10\.0% of the period/)).toBeInTheDocument();
    await userEvent.click(
      within(card).getByRole("button", { name: `Show all ${ops.length}` }),
    );
    expect(labels()).toHaveLength(ops.length);
    await userEvent.click(
      within(card).getByRole("button", { name: "Show fewer" }),
    );
    expect(labels()).toHaveLength(RANKED_VISIBLE);
  });

  it("draws every day of the period, the quiet ones at zero", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: usd(2_000_000n),
      chargeCount: 2n,
    });
    const lastDay = new Date();
    lastDay.setUTCHours(0, 0, 0, 0);
    h.series.mockResolvedValue({
      buckets: [
        {
          start: { seconds: BigInt(lastDay.getTime() / 1000), nanos: 0 },
          spent: usd(2_000_000n),
          chargeCount: 2n,
        },
      ],
    });
    const { container } = render(<Harness />);
    const table = await screen.findByRole("table", { name: "Spend per day" });
    expect(
      container.querySelectorAll("svg rect").length,
    ).toBeGreaterThanOrEqual(30);
    expect(within(table).getAllByRole("row")).toHaveLength(2);
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
    render(<Harness />);
    expect(
      await screen.findByText("Billing is unavailable"),
    ).toBeInTheDocument();
    expect(screen.getByText("billing: handler not wired")).toBeInTheDocument();
    expect(screen.queryByText("Spend over time")).toBeNull();
  });

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
      ...emptySummary,
      total: usd(1_000_000_000n),
      budgetCeiling,
      chargeCount: 1n,
    });
    render(<Harness />);
    expect(await screen.findByText(helper)).toBeInTheDocument();
  });

  it("links to the tenant's budget, to set one or change it", async () => {
    h.summary.mockResolvedValue({ ...emptySummary, chargeCount: 1n });
    render(<Harness />);
    expect(
      await screen.findByRole("link", { name: "Set a budget" }),
    ).toHaveAttribute("href", "/tenants/globex/budget");
  });

  it("keeps a tile's action beside its label, and the value's qualifier on its line", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: usd(1_000_000_000n),
      budgetCeiling: { case: "maxBudget", value: usd(4_000_000_000n) },
      chargeCount: 1n,
    });
    render(<Harness />);
    const tile = await screen.findByRole("group", { name: "Budget" });
    const link = within(tile).getByRole("link", { name: "Change the budget" });
    expect(link.parentElement).toBe(
      within(tile).getByRole("heading", { name: "Budget" }).parentElement,
    );
    const left = within(tile).getByText("left");
    expect(left.parentElement?.textContent).toBe("US$3.0000left");
    expect(within(tile).getByRole("progressbar")).toHaveAttribute(
      "aria-valuenow",
      "25",
    );
  });

  it("names the actors, not their subjects only", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      chargeCount: 1n,
      topActors: [{ label: "0199-user", spent: usd(1n), chargeCount: 1n }],
    });
    render(<Harness />);
    const name = await screen.findByText("0199-user");
    expect(name.closest("[data-slot=card]")).toHaveTextContent("Top actors");
  });

  it("keeps the period in the URL, and says what is wrong with a custom one", async () => {
    h.summary.mockResolvedValue(emptySummary);
    render(<Harness />);
    await screen.findByText("No charges in this period");
    h.summary.mockClear();
    await userEvent.click(screen.getByRole("button", { name: "Custom" }));
    expect(urlRouter.search).toBe("period=custom");
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

  it("opens on the period its URL names", async () => {
    urlRouter.reset("/tenants/globex/billing", "period=7d");
    h.summary.mockResolvedValue(emptySummary);
    render(<Harness />);
    expect(
      await screen.findByRole("button", { name: "7 days" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("offers no hourly chart of 30 days, and draws it daily", async () => {
    h.summary.mockResolvedValue(emptySummary);
    render(<Harness />);
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
    render(<Harness />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Look at the last 90 days" }),
    );
    expect(screen.getByRole("button", { name: "90 days" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(urlRouter.search).toBe("period=90d");
  });

  it("writes amounts in the platform's currency when the ledger names none", async () => {
    h.summary.mockResolvedValue({
      ...emptySummary,
      total: { currencyCode: "", units: 2n, nanos: 0 },
      chargeCount: 1n,
    });
    render(<Harness />);
    expect(await screen.findByText("€2.0000")).toBeInTheDocument();
  });
});
