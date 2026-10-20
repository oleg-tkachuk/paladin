import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  spend: vi.fn(),
  roles: [] as string[],
  scope: "t-1" as string | undefined,
}));
vi.mock("@/lib/connect/client", () => ({
  billingClient: { listTenantSpend: h.spend },
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
