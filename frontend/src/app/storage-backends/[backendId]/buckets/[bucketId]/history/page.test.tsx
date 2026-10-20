import { describe, expect, it, vi } from "vitest";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(async () => ({ entries: [] })),
  listOperations: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { listAuditLog: h.listAuditLog },
  adminOperationClient: { listOperations: h.listOperations },
}));
vi.mock("../bucket-context", () => ({
  useBucket: () => ({
    bucket: { name: "storageBackends/primary/buckets/logs" },
  }),
}));

import BucketHistoryPage from "./page";

describe("BucketHistoryPage", () => {
  // A bucket's history is what names it or what it holds — its grants, its
  // quota — not every entry of its owner's trail; operations act on
  // collections, so none are read.
  it("reads the audit entries on the bucket and under it, and no operations", async () => {
    render(<BucketHistoryPage />);
    await waitFor(() =>
      expect(h.listAuditLog).toHaveBeenCalledWith(
        expect.objectContaining({
          tenantId: "",
          filter:
            'resource_name == "storageBackends/primary/buckets/logs" || resource_name.startsWith("storageBackends/primary/buckets/logs/")',
        }),
        expect.anything(),
      ),
    );
    expect(h.listOperations).not.toHaveBeenCalled();
    expect(
      await screen.findByText("No changes to this bucket yet."),
    ).toBeVisible();
  });
});
