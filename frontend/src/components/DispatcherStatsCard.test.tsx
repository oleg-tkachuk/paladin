import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ stats: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  adminSystemClient: { getDispatcherStats: h.stats },
}));

import { DispatcherStatsCard } from "./DispatcherStatsCard";

describe("DispatcherStatsCard", () => {
  beforeEach(() => {
    h.stats.mockReset();
  });

  it("renders the rollup and only the subscriptions that are behind", async () => {
    h.stats.mockResolvedValue({
      available: true,
      pending: 5n,
      failed: 2n,
      oldestPendingSeconds: 125n, // → "2m"
      subscriptions: [
        {
          subscriptionId: "sub-ok",
          tenantId: "t",
          pending: 0n,
          failed: 0n,
          lastError: "",
        },
        {
          subscriptionId: "sub-stuck",
          tenantId: "t",
          pending: 3n,
          failed: 2n,
          lastError: "connection refused",
        },
      ],
    });

    render(<DispatcherStatsCard />);

    expect(await screen.findByText("live")).toBeInTheDocument();
    // Rollup: oldest humanized to minutes.
    expect(screen.getByText("2m")).toBeInTheDocument();
    // The behind list shows the stuck sub + its error, and excludes the ok one.
    expect(await screen.findByText("sub-stuck")).toBeInTheDocument();
    expect(screen.getByText(/connection refused/)).toBeInTheDocument();
    expect(screen.queryByText("sub-ok")).toBeNull();
    expect(screen.getByText("3 pending")).toBeInTheDocument();
    expect(screen.getByText("2 failed")).toBeInTheDocument();
  });

  it("says all drained when nothing is behind", async () => {
    h.stats.mockResolvedValue({
      available: true,
      pending: 0n,
      failed: 0n,
      oldestPendingSeconds: 0n,
      subscriptions: [
        {
          subscriptionId: "sub-ok",
          tenantId: "t",
          pending: 0n,
          failed: 0n,
          lastError: "",
        },
      ],
    });
    render(<DispatcherStatsCard />);
    expect(
      await screen.findByText(/all subscriptions drained/i),
    ).toBeInTheDocument();
  });

  it("reports when the dispatcher stats source is unavailable", async () => {
    h.stats.mockResolvedValue({
      available: false,
      pending: 0n,
      failed: 0n,
      oldestPendingSeconds: 0n,
      subscriptions: [],
    });
    render(<DispatcherStatsCard />);
    expect(
      await screen.findByText(/unavailable in this deployment/i),
    ).toBeInTheDocument();
  });
});
