import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({
  listBucketGrants: vi.fn(),
  grantBucket: vi.fn(),
  revokeBucketGrant: vi.fn(),
  bumpRefresh: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: {
    listBucketGrants: h.listBucketGrants,
    grantBucket: h.grantBucket,
    revokeBucketGrant: h.revokeBucketGrant,
  },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => h.bumpRefresh,
}));

import { useBucketGrants } from "./useBucketGrants";

const BUCKET = "storageBackends/primary/buckets/pool";
const grant = (tenantId: string) => ({
  bucket: BUCKET,
  tenantId,
  grantedBy: "admin",
});

beforeEach(() => {
  h.listBucketGrants.mockReset();
  h.grantBucket.mockReset();
  h.revokeBucketGrant.mockReset();
  h.bumpRefresh.mockReset();
});

describe("useBucketGrants", () => {
  it("lists the grants of the bucket it is given", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-1")] });
    const { result } = renderHook(() => useBucketGrants(BUCKET));
    await act(async () => {
      await result.current.fetchGrants();
    });
    expect(h.listBucketGrants).toHaveBeenCalledWith({ name: BUCKET });
    expect(result.current.grants.map((g) => g.tenantId)).toEqual(["t-1"]);
    expect(result.current.error).toBeNull();
  });

  // Query contract: a failed list is reported in `error`, never thrown.
  it("reports a failed list through error and returns none", async () => {
    h.listBucketGrants.mockRejectedValue(new Error("denied by policy"));
    const { result } = renderHook(() => useBucketGrants(BUCKET));
    let out: unknown;
    await act(async () => {
      out = await result.current.fetchGrants();
    });
    expect(out).toEqual([]);
    expect(result.current.error).toMatch(/denied by policy/);
  });

  it("grants a tenant and adds the grant to the list", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-1")] });
    h.grantBucket.mockResolvedValue(grant("t-2"));
    const { result } = renderHook(() => useBucketGrants(BUCKET));
    await act(async () => {
      await result.current.fetchGrants();
      await result.current.grantBucket("t-2");
    });
    expect(h.grantBucket).toHaveBeenCalledWith({
      name: BUCKET,
      tenantId: "t-2",
    });
    expect(result.current.grants.map((g) => g.tenantId)).toEqual([
      "t-1",
      "t-2",
    ]);
    expect(h.bumpRefresh).toHaveBeenCalledWith("buckets");
  });

  it("revokes a grant and drops it from the list", async () => {
    h.listBucketGrants.mockResolvedValue({
      grants: [grant("t-1"), grant("t-2")],
    });
    h.revokeBucketGrant.mockResolvedValue({});
    const { result } = renderHook(() => useBucketGrants(BUCKET));
    await act(async () => {
      await result.current.fetchGrants();
      await result.current.revokeBucketGrant("t-1");
    });
    expect(h.revokeBucketGrant).toHaveBeenCalledWith({
      name: BUCKET,
      tenantId: "t-1",
    });
    expect(result.current.grants.map((g) => g.tenantId)).toEqual(["t-2"]);
  });

  // Mutation contract: a refused revoke throws the original error and keeps
  // the grant listed, since it is still in force.
  it("throws a refused revoke and keeps the grant", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-1")] });
    const refusal = new ConnectError("in use", Code.FailedPrecondition);
    h.revokeBucketGrant.mockRejectedValue(refusal);
    const { result } = renderHook(() => useBucketGrants(BUCKET));
    await act(async () => {
      await result.current.fetchGrants();
    });
    await act(async () => {
      await expect(result.current.revokeBucketGrant("t-1")).rejects.toBe(
        refusal,
      );
    });
    expect(result.current.grants.map((g) => g.tenantId)).toEqual(["t-1"]);
    expect(result.current.error).toBeNull();
  });
});
