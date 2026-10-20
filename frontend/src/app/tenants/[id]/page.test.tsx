import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";

import { render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(async () => ({ entries: [] as unknown[] })),
  getQuota: vi.fn(),
  getEffectivePolicy: vi.fn(async () => ({
    layers: [{ source: "built-in", frozen: false }],
  })),
  trashed: false,
  listAccessibleBuckets: vi.fn(async () => ({
    buckets: [{ bucketId: "own" }, { bucketId: "granted" }],
  })),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { listAccessibleBuckets: h.listAccessibleBuckets },
  collectionClient: {
    listCollections: vi.fn(async () => ({ collections: [] })),
  },
  tenantBudgetClient: { get: vi.fn(async () => ({})) },
  auditClient: { listAuditLog: h.listAuditLog },
  quotaClient: { getQuota: h.getQuota },
  policyClient: { getEffectivePolicy: h.getEffectivePolicy },
  userSettingsClient: {
    getMine: vi.fn(async () => ({ preferences: {} })),
    updateMine: vi.fn(),
  },
}));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("./StorageMigrationCard", () => ({ StorageMigrationCard: () => null }));
vi.mock("./tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({
    tenantId: "t-1",
    slug: "acme",
    displayName: "Acme",
    trashed: h.trashed,
  }),
}));

import TenantOverviewPage from "./page";

beforeEach(() => {
  h.trashed = false;
  h.getQuota.mockReset().mockImplementation(async () => {
    throw new ConnectError("no quota", Code.NotFound);
  });
});

// Recent activity is the tenant's trail as the server selects it. A CEL
// filter over one page of the whole log showed nothing for a tenant whose
// entries were not among the newest, and missed its collections' rows.
describe("TenantOverviewPage recent activity", () => {
  it("asks for the tenant's trail, not a filtered page of the log", async () => {
    render(<TenantOverviewPage />);
    await waitFor(() => expect(h.listAuditLog).toHaveBeenCalled());
    const [req] = h.listAuditLog.mock.calls[0] as unknown as [
      { tenantId?: string; filter?: string },
    ];
    expect(req.tenantId).toBe("t-1");
    expect(req.filter ?? "").toBe("");
  });
});

// ADR-0032: the Buckets tab lists owned and granted buckets, so the tile
// counts the same set rather than only the ones the tenant owns.
describe("TenantOverviewPage bucket count", () => {
  it("counts the buckets the tenant may bind to", async () => {
    render(<TenantOverviewPage />);
    await waitFor(() =>
      expect(h.listAccessibleBuckets).toHaveBeenCalledWith(
        expect.objectContaining({ tenant: "tenants/t-1" }),
      ),
    );
    expect(await screen.findByText("owned or granted")).toBeInTheDocument();
    const tile = screen.getByText("owned or granted").closest("a")!;
    await waitFor(() => expect(tile).toHaveTextContent("2"));
  });
});

// A count that could not be read is unknown, not zero: "0 buckets" for a
// failed read tells an operator the tenant has none.
describe("TenantOverviewPage failed count", () => {
  it("shows no number for a list that failed", async () => {
    h.listAccessibleBuckets.mockRejectedValueOnce(new Error("unavailable"));
    render(<TenantOverviewPage />);
    const tile = (await screen.findByText("owned or granted")).closest("a")!;
    await waitFor(() => expect(tile).toHaveTextContent("—"));
    expect(tile).not.toHaveTextContent(/^0|\b0\b/);
  });

  it("counts every page of the list", async () => {
    h.listAccessibleBuckets
      .mockResolvedValueOnce({
        buckets: [{ bucketId: "a" }],
        page: { nextPageToken: "p2" },
      } as never)
      .mockResolvedValueOnce({
        buckets: [{ bucketId: "b" }, { bucketId: "c" }],
      });
    render(<TenantOverviewPage />);
    const tile = (await screen.findByText("owned or granted")).closest("a")!;
    await waitFor(() => expect(tile).toHaveTextContent("3"));
  });
});

// After Argo CD's status panel: the facts the page leads with, each in the
// shared status tones.
describe("TenantOverviewPage status panel", () => {
  const card = async (title: string) => {
    const panel = await screen.findByRole("region", { name: "Tenant status" });
    return within(panel)
      .getByText(title)
      .closest('[data-slot="status-card"]') as HTMLElement;
  };

  it("reads a frozen tenant as suspended, with the way out", async () => {
    h.trashed = true;
    render(<TenantOverviewPage />);
    const health = await card("Health");
    expect(health).toHaveAttribute("data-tone", "suspended");
    expect(within(health).getByText("Frozen")).toBeVisible();
    expect(within(health).getByRole("link")).toHaveAttribute("href", "/trash");
  });

  it("stands the quota by its fullest cap", async () => {
    h.getQuota.mockResolvedValueOnce({
      maxTotalBytes: 100n,
      maxObjectCount: 10n,
      usage: { totalBytes: 10n, objectCount: 10n },
    });
    render(<TenantOverviewPage />);
    const quota = await card("Quota");
    await waitFor(() => expect(quota).toHaveAttribute("data-tone", "danger"));
    expect(within(quota).getByText("At limit")).toBeVisible();
  });

  it("names the last change, and a frozen policy layer", async () => {
    h.listAuditLog.mockResolvedValueOnce({
      entries: [
        {
          entryId: "e1",
          action: "/paladin.admin.v1.TenantService/UpdateTenant",
          actorSubject: "system:bootstrap",
          actorTenantId: "",
          errorMessage: "",
        },
      ],
    });
    h.getEffectivePolicy.mockResolvedValueOnce({
      layers: [
        { source: "built-in", frozen: false },
        { source: "tenants/t-1", frozen: true },
      ],
    });
    render(<TenantOverviewPage />);
    const last = await card("Last change");
    await waitFor(() => expect(last).toHaveTextContent("Update tenant"));
    expect(last).toHaveAttribute("data-tone", "success");
    expect(within(last).getByRole("link")).toHaveAttribute(
      "href",
      "/tenants/acme/history",
    );
    const policy = await card("Policy");
    await waitFor(() => expect(policy).toHaveAttribute("data-tone", "danger"));
  });
});
