import { beforeEach, describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({ getUsage: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { getUsage: h.getUsage },
}));

import { fetchCapabilityUsage } from "./details";

describe("fetchCapabilityUsage", () => {
  beforeEach(() => {
    h.getUsage.mockReset();
  });

  it("is the capability's requests and spend", async () => {
    h.getUsage.mockResolvedValue({
      requestCount: 7n,
      spent: undefined,
      extra: 1,
    });
    expect(await fetchCapabilityUsage("c")).toEqual({
      requestCount: 7n,
      spent: undefined,
    });
  });

  it("is never when it has no usage row", async () => {
    h.getUsage.mockImplementation(async () => {
      throw new ConnectError("none", Code.NotFound);
    });
    expect(await fetchCapabilityUsage("c")).toBe("never");
  });

  it("fails on any other error", async () => {
    h.getUsage.mockImplementation(async () => {
      throw new ConnectError("down", Code.Unavailable);
    });
    await expect(fetchCapabilityUsage("c")).rejects.toThrow("down");
  });
});
