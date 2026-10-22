import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  spend: vi.fn(),
  series: vi.fn(),
  roles: [] as string[],
  scope: "t-1" as string | undefined,
}));
vi.mock("@/lib/connect/client", () => ({
  billingClient: {
    listTenantSpend: h.spend,
    getPlatformTimeSeries: h.series,
    getBillingSettings: () => Promise.resolve({ currencyCode: "USD" }),
  },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { roles: h.roles } }),
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: h.scope }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ title }: { title?: React.ReactNode }) => <h1>{title}</h1>,
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
import BillingPage from "./page";

const usd = (nanos: bigint) => moneyFromNanos(nanos, "USD");
const spendRows = {
  truncated: false,
  tenants: [
    {
      tenantId: "t-2",
      slug: "globex",
      displayName: "Globex",
      deleted: false,
      spent: usd(2_500_000_000n),
      chargeCount: 3n,
    },
    {
      tenantId: "t-3",
      slug: "smoke",
      displayName: "Smoke",
      deleted: true,
      spent: usd(400_000_000n),
      chargeCount: 1n,
    },
  ],
};

describe("BillingPage", () => {
  beforeEach(() => {
    h.spend.mockReset();
    h.spend.mockResolvedValue(spendRows);
    h.series.mockReset().mockResolvedValue({ buckets: [] });
    h.roles = [];
    h.scope = "t-1";
    urlRouter.reset("/billing");
  });

  describe("for a platform admin", () => {
    beforeEach(() => {
      h.roles = ["platform.admin"];
    });

    it("lists every tenant's spend, the server's order kept", async () => {
      render(<BillingPage />);
      await screen.findByRole("link", { name: "Globex" });
      const card = screen
        .getByText("Tenants by spend")
        .closest("[data-slot=card]") as HTMLElement;
      expect(
        within(card)
          .getAllByRole("link")
          .map((a) => a.textContent),
      ).toEqual(["Globex", "Smoke · in trash"]);
    });

    // What the platform was charged in all, and each tenant's part of it.
    it("heads the page with the platform's totals, and each tenant's share", async () => {
      render(<BillingPage />);
      const spend = await screen.findByRole("group", { name: "Spend" });
      expect(spend).toHaveTextContent("2.9000");
      expect(screen.getByRole("group", { name: "Charges" })).toHaveTextContent(
        "4",
      );
      expect(screen.getByRole("group", { name: "Tenants" })).toHaveTextContent(
        "2",
      );
      const shares = screen
        .getAllByRole("progressbar", { name: "Share of the platform's spend" })
        .map((b) => b.getAttribute("aria-valuenow"));
      // 2.5 and 0.4 of 2.9.
      expect(shares).toEqual(["86", "14"]);
    });

    it("charts the platform's spend over the period, a day a bar", async () => {
      render(<BillingPage />);
      await waitFor(() => expect(h.series).toHaveBeenCalled());
      expect(h.series.mock.calls[0][0]).toMatchObject({ granularity: "day" });
      expect(
        await screen.findByText("Nothing was charged in this period."),
      ).toBeInTheDocument();
    });

    // A series that could not be read says so, rather than load for ever.
    it("says the spend over time could not be read", async () => {
      h.series.mockRejectedValue(new Error("unavailable"));
      render(<BillingPage />);
      expect(
        await screen.findByText(/Spend over time could not be loaded/),
      ).toBeInTheDocument();
    });

    it("links a row to that tenant's billing tab, with the period", async () => {
      urlRouter.reset("/billing", "period=7d");
      render(<BillingPage />);
      expect(
        await screen.findByRole("link", { name: "Globex" }),
      ).toHaveAttribute("href", "/tenants/globex/billing?period=7d");
    });

    it("says when no tenant spent anything", async () => {
      h.spend.mockResolvedValue({ truncated: false, tenants: [] });
      render(<BillingPage />);
      expect(
        await screen.findByText("No tenant spent anything in this period."),
      ).toBeInTheDocument();
    });

    it("takes an old ?tenant= link to that tenant's tab", async () => {
      urlRouter.reset("/billing", "tenant=t-2&period=90d");
      render(<BillingPage />);
      await waitFor(() =>
        expect(urlRouter.pathname).toBe("/tenants/t-2/billing"),
      );
      expect(urlRouter.search).toBe("period=90d");
    });
  });

  it("takes anyone else to their own tenant's billing tab", async () => {
    render(<BillingPage />);
    await waitFor(() =>
      expect(urlRouter.pathname).toBe("/tenants/t-1/billing"),
    );
    expect(h.spend).not.toHaveBeenCalled();
  });

  it("says so when there is no tenant to show", async () => {
    h.scope = undefined;
    render(<BillingPage />);
    expect(await screen.findByText("No tenant to show")).toBeInTheDocument();
  });
});
