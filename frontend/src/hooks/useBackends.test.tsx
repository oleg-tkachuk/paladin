import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({ listBackends: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  backendClient: { listBackends: h.listBackends },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
  useRefreshSignal: () => 0,
}));

import { useBackends } from "./useBackends";

function page(ids: string[], nextPageToken = "") {
  return {
    backends: ids.map((backendId) => ({ backendId })),
    page: { nextPageToken },
  };
}

describe("useBackends pagination", () => {
  beforeEach(() => h.listBackends.mockReset());

  // A backend past the first page was invisible and unselectable everywhere it
  // is offered — the scope picker, the collection and bucket dialogs, the
  // tables — with nothing on screen saying the list was cut. Same defect the
  // bucket picker had; there are simply fewer backends, so it had not bitten.
  it("follows nextPageToken so backends past the first page are reachable", async () => {
    h.listBackends
      .mockResolvedValueOnce(page(["a", "b"], "tok-2"))
      .mockResolvedValueOnce(page(["z"]));

    const { result } = renderHook(() => useBackends(false));
    await act(async () => {
      await result.current.fetchBackends();
    });

    expect(h.listBackends).toHaveBeenCalledTimes(2);
    expect(h.listBackends.mock.calls[1][0].page.pageToken).toBe("tok-2");
    expect(result.current.backends.map((b) => b.backendId)).toEqual([
      "a",
      "b",
      "z",
    ]);
  });

  it("stops at the page ceiling instead of following a token forever", async () => {
    h.listBackends.mockResolvedValue(page(["x"], "always-more"));

    const { result } = renderHook(() => useBackends(false));
    await act(async () => {
      await result.current.fetchBackends();
    });

    expect(h.listBackends).toHaveBeenCalledTimes(20);
  });
});
