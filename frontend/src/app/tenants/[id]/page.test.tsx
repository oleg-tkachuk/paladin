import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(async () => ({ entries: [] })),
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
}));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("./StorageMigrationCard", () => ({ StorageMigrationCard: () => null }));
vi.mock("./tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));

import TenantOverviewPage from "./page";

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
