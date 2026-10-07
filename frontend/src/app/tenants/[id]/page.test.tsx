import { describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(async () => ({ entries: [] })),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { listBuckets: vi.fn(async () => ({ buckets: [] })) },
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
