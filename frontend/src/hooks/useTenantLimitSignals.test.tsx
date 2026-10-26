import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  listPlatformStatsTenants: vi.fn(),
  summarize: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  adminSystemClient: { listPlatformStatsTenants: h.listPlatformStatsTenants },
  tenantBudgetClient: { summarize: h.summarize },
}));

import { PlatformStatsSignal } from "@/gen/paladin/admin/v1/system_service_pb";
import { MAX_LIST_PAGES } from "@/lib/queries/paging";
import { BUDGET_ALERT_THRESHOLD } from "@/lib/tenant/limits";

import { useTenantLimitSignals } from "./useTenantLimitSignals";

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

const signalPage = (ids: string[], nextPageToken = "") => ({
  tenants: ids.map((tenantId) => ({ tenantId, count: 1n })),
  page: { nextPageToken },
});
const budgetPage = (ids: string[], nextPageToken = "") => ({
  summaries: ids.map((tenantId) => ({
    tenantId,
    budget: { tenantId },
    utilisationPct: 90,
  })),
  nextPageToken,
});

/** Answers each quota signal from its own pages. */
function quotaBySignal(
  pages: Partial<Record<PlatformStatsSignal, ReturnType<typeof signalPage>[]>>,
) {
  h.listPlatformStatsTenants.mockImplementation(
    (req: { signal: PlatformStatsSignal; page: { pageToken: string } }) => {
      const list = pages[req.signal] ?? [signalPage([])];
      const at = req.page.pageToken ? Number(req.page.pageToken) : 0;
      return Promise.resolve(list[at]);
    },
  );
}

beforeEach(() => {
  h.listPlatformStatsTenants.mockReset();
  h.summarize.mockReset();
});

describe("useTenantLimitSignals", () => {
  it("joins every page of each read per tenant", async () => {
    quotaBySignal({
      [PlatformStatsSignal.QUOTA_AT_LIMIT]: [
        signalPage(["a"], "1"),
        signalPage(["b"]),
      ],
      [PlatformStatsSignal.QUOTA_NEAR_LIMIT]: [signalPage(["c"])],
    });
    h.summarize
      .mockResolvedValueOnce(budgetPage(["c"], "tok-2"))
      .mockResolvedValueOnce(budgetPage(["d"]));
    const { result } = renderHook(() => useTenantLimitSignals(), {
      wrapper: wrap(),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    const { signals } = result.current;
    expect([...signals.keys()].sort()).toEqual(["a", "b", "c", "d"]);
    expect(signals.get("b")?.quota).toBe("at-limit");
    expect(signals.get("c")?.quota).toBe("near-limit");
    expect(signals.get("c")?.budget).toBeDefined();
    expect(result.current.error).toBeNull();
    // The budget read asks for the dashboard's threshold, active tenants only,
    // and follows the token.
    expect(h.summarize.mock.calls[0][0]).toMatchObject({
      thresholdPct: BUDGET_ALERT_THRESHOLD,
      excludeInactive: true,
      unlimitedOnly: false,
      pageToken: "",
    });
    expect(h.summarize.mock.calls[1][0].pageToken).toBe("tok-2");
  });

  it("stops at the page ceiling instead of following a token forever", async () => {
    h.listPlatformStatsTenants.mockResolvedValue(signalPage(["x"], "more"));
    h.summarize.mockResolvedValue(budgetPage([]));
    const { result } = renderHook(() => useTenantLimitSignals(), {
      wrapper: wrap(),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    // Two quota signals, each to the ceiling.
    expect(h.listPlatformStatsTenants).toHaveBeenCalledTimes(
      2 * MAX_LIST_PAGES,
    );
  });

  it("keeps what the other reads said when one is refused", async () => {
    quotaBySignal({
      [PlatformStatsSignal.QUOTA_NEAR_LIMIT]: [signalPage(["a"])],
    });
    h.summarize.mockRejectedValue(new Error("capabilities are off"));
    const { result } = renderHook(() => useTenantLimitSignals(), {
      wrapper: wrap(),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("capabilities are off");
    expect([...result.current.signals.keys()]).toEqual(["a"]);
    expect(result.current.signals.get("a")).toEqual({ quota: "near-limit" });
  });

  it("is loading and empty until the reads land", () => {
    h.listPlatformStatsTenants.mockReturnValue(new Promise(() => {}));
    h.summarize.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useTenantLimitSignals(), {
      wrapper: wrap(),
    });
    expect(result.current.loading).toBe(true);
    expect(result.current.signals.size).toBe(0);
  });
});
