import { describe, expect, it } from "vitest";

import { backendHref, bucketHref, parseBucketName, tenantHref } from "./hrefs";

describe("hrefs", () => {
  it("builds each entity's one URL", () => {
    expect(backendHref("primary")).toBe("/storage-backends/primary");
    expect(bucketHref("primary", "b1")).toBe(
      "/storage-backends/primary/buckets/b1",
    );
    expect(bucketHref("primary", "b1", "lifecycle")).toBe(
      "/storage-backends/primary/buckets/b1/lifecycle",
    );
    expect(tenantHref("acme")).toBe("/tenants/acme");
  });

  it("encodes what a path segment cannot hold", () => {
    expect(bucketHref("a b", "c/d")).toBe(
      "/storage-backends/a%20b/buckets/c%2Fd",
    );
  });
});

describe("parseBucketName", () => {
  it("splits a bucket resource name", () => {
    expect(parseBucketName("storageBackends/primary/buckets/b1")).toEqual({
      backendId: "primary",
      bucketId: "b1",
    });
  });

  it.each([
    "",
    "storageBackends/primary",
    "buckets/b1",
    "storageBackends/a/buckets/b/c",
  ])("refuses %o", (name) => {
    expect(parseBucketName(name)).toBeNull();
  });
});
