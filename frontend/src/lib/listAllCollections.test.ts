import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ listCollections: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
}));

import { listAllCollections } from "./listAllCollections";

beforeEach(() => {
  h.listCollections.mockReset();
});

describe("listAllCollections", () => {
  it("follows the page token to the end, keeping the scope", async () => {
    h.listCollections
      .mockResolvedValueOnce({
        collections: [{ collection: "a" }],
        page: { nextPageToken: "t:a" },
      })
      .mockResolvedValueOnce({ collections: [{ collection: "b" }], page: {} });
    const got = await listAllCollections({
      parent: "",
      bucket: "storageBackends/primary/buckets/pool",
    });
    expect(got.map((c) => c.collection)).toEqual(["a", "b"]);
    const [first, second] = h.listCollections.mock.calls.map((c) => c[0]);
    expect(first.bucket).toBe("storageBackends/primary/buckets/pool");
    expect(second.page.pageToken).toBe("t:a");
    expect(second.bucket).toBe("storageBackends/primary/buckets/pool");
  });
});
