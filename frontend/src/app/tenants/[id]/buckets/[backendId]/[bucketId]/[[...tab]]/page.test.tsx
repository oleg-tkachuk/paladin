import { describe, expect, it, vi } from "vitest";

const redirect = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({ redirect }));

import TenantBucketRedirect from "./page";

describe("TenantBucketRedirect", () => {
  it("sends the old bucket page to the bucket's one page", async () => {
    await TenantBucketRedirect({
      params: Promise.resolve({ backendId: "primary", bucketId: "pool" }),
    });
    expect(redirect).toHaveBeenLastCalledWith(
      "/storage-backends/primary/buckets/pool",
    );
  });

  // A link to a tab keeps the tab.
  it("keeps the tab it was asked for", async () => {
    await TenantBucketRedirect({
      params: Promise.resolve({
        backendId: "primary",
        bucketId: "pool",
        tab: ["lifecycle"],
      }),
    });
    expect(redirect).toHaveBeenLastCalledWith(
      "/storage-backends/primary/buckets/pool/lifecycle",
    );
  });
});
