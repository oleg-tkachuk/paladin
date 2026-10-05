import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@/test/utils";

// The page is one RPC deep: mock adminSystemClient.getPlatformStats and assert
// on what the operator actually reads off the screen.
const h = vi.hoisted(() => ({ getPlatformStats: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  adminSystemClient: { getPlatformStats: h.getPlatformStats },
}));
// PageHeader renders Breadcrumbs, which reads the router — stub navigation
// rather than the header itself so the page's own title stays under test.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/stats",
  useSearchParams: () => new URLSearchParams(""),
}));

import StatsPage from "./page";

const state = (s: string, count: bigint, bytes: bigint) => ({
  $typeName: "paladin.admin.v1.ObjectStateStat",
  state: s,
  count,
  bytes,
});

function response(overrides: Record<string, unknown> = {}) {
  return {
    $typeName: "paladin.admin.v1.GetPlatformStatsResponse",
    tenants: {
      total: 4n,
      active: 3n,
      trashed: 1n,
      sharedLayout: 2n,
      dedicatedLayout: 1n,
      withoutDefaultBinding: 1n,
    },
    backends: {
      total: 2n,
      enabled: 1n,
      disabled: 1n,
      readOnly: 0n,
      maintenance: 0n,
      byKind: { "s3-compatible": 1n, "aws-s3": 1n },
    },
    buckets: {
      total: 5n,
      byProvisionState: { ready: 4n, failed: 1n },
      byBackend: { primary: 5n },
      tenantOwned: 2n,
      shared: 3n,
      versioningEnabled: 1n,
      objectLockEnabled: 0n,
      replicationEnabled: 0n,
    },
    collections: { total: 7n, byBackend: { primary: 7n }, unbound: 2n },
    users: { total: 9n, disabled: 1n },
    rls: rlsStats(),
    collectedAt: undefined,
    ...overrides,
  };
}

// The worker-proxied half. One availability flag covers all five censuses
// because they share a single source and therefore fail together.
function rlsStats(overrides: Record<string, unknown> = {}) {
  return {
    $typeName: "paladin.admin.v1.RLSStats",
    available: true,
    objects: {
      states: [state("AVAILABLE", 12n, 3000n), state("FAILED", 1n, 0n)],
      totalCount: 13n,
      totalBytes: 3000n,
      tenants: [
        {
          $typeName: "paladin.admin.v1.TenantObjectStats",
          tenantId: "11111111-1111-1111-1111-111111111111",
          slug: "acme-prod",
          displayName: "Acme Production",
          states: [state("AVAILABLE", 12n, 3000n)],
          totalCount: 12n,
          totalBytes: 3000n,
        },
      ],
      tenantsTruncated: 0n,
    },
    quotas: {
      total: 2n,
      tenantScoped: 2n,
      bucketScoped: 0n,
      withLimits: 2n,
      atLimit: 1n,
      nearLimit: 1n,
      usageObjectCount: 20n,
      usageTotalBytes: 5000n,
    },
    capabilities: {
      total: 6n,
      active: 3n,
      expired: 2n,
      revoked: 1n,
      delegated: 1n,
      expiringSoon: 1n,
      byPrincipalKind: { service_account: 3n },
    },
    apiTokens: {
      total: 4n,
      active: 2n,
      expired: 1n,
      revoked: 1n,
      expiringSoon: 1n,
      neverUsed: 1n,
    },
    subscriptions: {
      total: 3n,
      enabled: 2n,
      disabled: 1n,
      withFilter: 1n,
      bySinkKind: { http: 2n },
    },
    ...overrides,
  };
}

describe("StatsPage", () => {
  beforeEach(() => {
    h.getPlatformStats.mockReset();
  });

  it("renders the inventory census and the per-tenant object table", async () => {
    h.getPlatformStats.mockResolvedValue(response());
    render(<StatsPage />);

    // Headline strip: active tenants, not the total (trashed rows are a
    // separate, secondary number). findBy — the strip replaces skeletons
    // only once the RPC resolves.
    expect(await screen.findByText("1 in trash")).toBeInTheDocument();
    expect(screen.getByText("Platform Statistics")).toBeInTheDocument();
    expect(screen.getByText("2 unbound")).toBeInTheDocument();

    // Per-tenant row is labelled by slug, with the display name beneath.
    expect(screen.getByText("acme-prod")).toBeInTheDocument();
    expect(screen.getByText("Acme Production")).toBeInTheDocument();

    // Rollup row totals the fleet.
    expect(screen.getByText("All tenants")).toBeInTheDocument();
  });

  it("says the census is unavailable instead of rendering zeros", async () => {
    h.getPlatformStats.mockResolvedValue(
      response({
        rls: {
          $typeName: "paladin.admin.v1.RLSStats",
          available: false,
          objects: undefined,
          quotas: undefined,
          capabilities: undefined,
          apiTokens: undefined,
          subscriptions: undefined,
        },
      }),
    );
    render(<StatsPage />);

    // One flag, five censuses: every worker-fed card degrades together.
    expect(await screen.findAllByText("unavailable")).toHaveLength(5);
    expect(screen.getAllByText(/worker\.ops_url/).length).toBe(5);
    // The inventory half still renders — a dead worker must not blank the page.
    expect(screen.getByText("1 in trash")).toBeInTheDocument();
    expect(screen.queryByText("All tenants")).not.toBeInTheDocument();
  });

  it("renders the RLS'd censuses: quotas, capabilities, tokens, events", async () => {
    h.getPlatformStats.mockResolvedValue(response());
    render(<StatsPage />);

    await screen.findByText("1 in trash");

    // Quota cap proximity — the two numbers an operator acts on.
    expect(screen.getByText("At / over limit")).toBeInTheDocument();
    expect(screen.getByText("Near limit (≥90%)")).toBeInTheDocument();
    // Capability + token expiry windows differ on purpose (24h vs 7d).
    expect(screen.getByText("Expiring < 24h")).toBeInTheDocument();
    expect(screen.getByText("Expiring < 7d")).toBeInTheDocument();
    expect(screen.getByText("Never used")).toBeInTheDocument();
    // Subscription sink mix.
    expect(screen.getByText("With CEL filter")).toBeInTheDocument();
  });

  it("labels the enforced usage as lagging and states its coverage", async () => {
    h.getPlatformStats.mockResolvedValue(response());
    render(<StatsPage />);

    // Quotas are opt-in, so the page says how much of the fleet the
    // enforced number covers — without that, comparing it to the live
    // census means nothing.
    expect(
      await screen.findByText(/Covers 2 of 3 active tenants/),
    ).toBeInTheDocument();
    // And which of the two numbers wins when they disagree.
    expect(
      screen.getByText(/census is the authoritative count of what is stored/),
    ).toBeInTheDocument();
  });

  it("adds a column for an object state the frontend does not know yet", async () => {
    h.getPlatformStats.mockResolvedValue(
      response({
        rls: rlsStats({
          objects: {
            states: [state("ARCHIVED", 4n, 8n)],
            totalCount: 4n,
            totalBytes: 8n,
            tenants: [
              {
                $typeName: "paladin.admin.v1.TenantObjectStats",
                tenantId: "22222222-2222-2222-2222-222222222222",
                slug: "beta",
                displayName: "",
                states: [state("ARCHIVED", 4n, 8n)],
                totalCount: 4n,
                totalBytes: 8n,
              },
            ],
            tenantsTruncated: 0n,
          },
        }),
      }),
    );
    render(<StatsPage />);

    expect(
      await screen.findByRole("columnheader", { name: "archived" }),
    ).toBeInTheDocument();
  });

  it("pages the per-tenant table past the server's cap", async () => {
    const tenant = (id: string, slug: string, count: bigint) => ({
      $typeName: "paladin.admin.v1.TenantObjectStats",
      tenantId: id,
      slug,
      displayName: "",
      states: [state("AVAILABLE", count, 0n)],
      totalCount: count,
      totalBytes: 0n,
    });
    const page = (
      tenants: ReturnType<typeof tenant>[],
      truncated: bigint,
      next: string,
    ) =>
      response({
        rls: rlsStats({
          objects: {
            states: [state("AVAILABLE", 13n, 0n)],
            totalCount: 13n,
            totalBytes: 0n,
            tenants,
            tenantsTruncated: truncated,
            tenantsNextPageToken: next,
          },
        }),
      });
    const big = tenant("11111111-1111-1111-1111-111111111111", "big", 9n);
    const small = tenant("22222222-2222-2222-2222-222222222222", "small", 4n);
    h.getPlatformStats.mockImplementation(
      async (req: { tenantPage?: { pageToken: string } }) =>
        req.tenantPage?.pageToken === "p2"
          ? // A count that moved between requests repeats a row.
            page([big, small], 0n, "")
          : page([big], 1n, "p2"),
    );
    render(<StatsPage />);

    expect(await screen.findByText("big")).toBeInTheDocument();
    expect(screen.queryByText("small")).not.toBeInTheDocument();
    expect(
      screen.getByText(/1 smaller tenant\(s\) not shown/),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Show more" }));

    expect(await screen.findByText("small")).toBeInTheDocument();
    expect(screen.getAllByText("big")).toHaveLength(1);
    expect(h.getPlatformStats).toHaveBeenLastCalledWith({
      tenantPage: { pageSize: 0, pageToken: "p2" },
    });
    expect(
      screen.queryByRole("button", { name: "Show more" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/not shown/)).not.toBeInTheDocument();
  });

  it("keeps the note but offers no page when the server sends no token", async () => {
    h.getPlatformStats.mockResolvedValue(
      response({
        rls: rlsStats({
          objects: {
            ...rlsStats().objects,
            tenantsTruncated: 3n,
            tenantsNextPageToken: "",
          },
        }),
      }),
    );
    render(<StatsPage />);

    expect(
      await screen.findByText(/3 smaller tenant\(s\) not shown/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Show more" }),
    ).not.toBeInTheDocument();
  });
});
