import { describe, expect, it, vi } from "vitest";

const redirect = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({ redirect }));

import BackendBucketsPage from "./page";

describe("BackendBucketsPage", () => {
  // The bucket page's trail has a crumb here; it must lead somewhere.
  it("sends a backend's buckets to the backend's page", async () => {
    await BackendBucketsPage({
      params: Promise.resolve({ backendId: "a%20b" }),
    });
    expect(redirect).toHaveBeenCalledWith("/storage-backends/a%20b");
  });
});
