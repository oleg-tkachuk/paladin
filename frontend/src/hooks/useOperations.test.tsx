import { describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const h = vi.hoisted(() => ({
  listOperations: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  adminOperationClient: { listOperations: h.listOperations },
}));

import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { OP_BATCH_COPY, OP_FACET_STATE, OP_FACET_TYPE } from "@/lib/operations";

import {
  OPERATIONS_POLL_MS,
  useOperations,
  type UseOperationsArgs,
} from "./useOperations";

// Past the first poll, short of the second.
const PAST_ONE_POLL_MS = OPERATIONS_POLL_MS * 1.5;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const base: UseOperationsArgs = {
  facets: {},
  failuresOnly: false,
  scope: "mine",
  includeDismissed: false,
  live: false,
};

function page(names: string[], next = "") {
  return {
    operations: names.map((name) => ({ name })),
    page: { nextPageToken: next },
  };
}

describe("useOperations", () => {
  it("asks for the user's own, newest first, without the cleared", async () => {
    h.listOperations.mockReset().mockResolvedValue(page([]));
    renderHook(() => useOperations(base), { wrapper });
    await waitFor(() => expect(h.listOperations).toHaveBeenCalled());
    expect(h.listOperations).toHaveBeenCalledWith(
      expect.objectContaining({
        mine: true,
        includeDismissed: false,
        sortOrder: SortOrder.DESC,
        filter: "",
      }),
      expect.anything(),
    );
  });

  it("asks for only the work that left something undone", async () => {
    h.listOperations.mockReset().mockResolvedValue(page([]));
    renderHook(() => useOperations({ ...base, failuresOnly: true }), {
      wrapper,
    });
    await waitFor(() => expect(h.listOperations).toHaveBeenCalled());
    // Failed, or a batch that left objects behind: pushed into the query.
    expect(h.listOperations.mock.calls[0][0].filter).toBe("has_failures");
  });

  it("narrows by the facets, and has them counted with the first page only", async () => {
    h.listOperations
      .mockReset()
      .mockResolvedValueOnce({
        ...page(["operations/a"], "tok"),
        facetCounts: [
          { facet: OP_FACET_TYPE, value: OP_BATCH_COPY, count: 1n },
        ],
      })
      .mockResolvedValueOnce({ ...page(["operations/b"]), facetCounts: [] });
    const facets = {
      [OP_FACET_STATE]: ["FAILED"],
      [OP_FACET_TYPE]: [OP_BATCH_COPY],
    };
    const { result } = renderHook(() => useOperations({ ...base, facets }), {
      wrapper,
    });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    expect(h.listOperations.mock.calls[0][0]).toMatchObject({
      facets: { states: [OperationState.FAILED], types: [OP_BATCH_COPY] },
      countFacets: true,
    });
    await act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.operations).toHaveLength(2));
    expect(h.listOperations.mock.calls[1][0].countFacets).toBe(false);
    // The counts stay the first page's, which describe the whole listing.
    expect(result.current.facetCounts).toHaveLength(1);
  });

  it("asks again when a facet choice changes", async () => {
    h.listOperations.mockReset().mockResolvedValue(page([]));
    const { rerender } = renderHook(
      (facets: UseOperationsArgs["facets"]) =>
        useOperations({ ...base, facets }),
      { wrapper, initialProps: {} },
    );
    await waitFor(() => expect(h.listOperations).toHaveBeenCalledTimes(1));
    rerender({ [OP_FACET_TYPE]: [OP_BATCH_COPY] });
    await waitFor(() => expect(h.listOperations).toHaveBeenCalledTimes(2));
  });

  it("asks for the cleared only of the user's own list", async () => {
    h.listOperations.mockReset().mockResolvedValue(page([]));
    renderHook(
      () => useOperations({ ...base, scope: "tenant", includeDismissed: true }),
      { wrapper },
    );
    await waitFor(() => expect(h.listOperations).toHaveBeenCalled());
    expect(h.listOperations.mock.calls[0][0]).toMatchObject({
      mine: false,
      includeDismissed: false,
    });
  });

  it("pages on with the server's token", async () => {
    h.listOperations
      .mockReset()
      .mockResolvedValueOnce(page(["operations/a"], "tok"))
      .mockResolvedValueOnce(page(["operations/b"]));
    const { result } = renderHook(() => useOperations(base), { wrapper });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    await act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.operations).toHaveLength(2));
    expect(h.listOperations.mock.calls[1][0].page.pageToken).toBe("tok");
    expect(result.current.hasMore).toBe(false);
  });

  it("says why the list could not be read", async () => {
    h.listOperations.mockReset().mockRejectedValue(new Error("denied"));
    const { result } = renderHook(() => useOperations(base), { wrapper });
    await waitFor(() => expect(result.current.error).toMatch(/denied/));
    expect(result.current.operations).toEqual([]);
  });

  it.each([
    [true, 2],
    [false, 1],
  ])("live %s: asks %d time(s) within one poll", async (live, calls) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      h.listOperations.mockReset().mockResolvedValue(page([]));
      renderHook(() => useOperations({ ...base, live }), { wrapper });
      await waitFor(() => expect(h.listOperations).toHaveBeenCalledTimes(1));
      await act(() => vi.advanceTimersByTimeAsync(PAST_ONE_POLL_MS));
      await waitFor(() =>
        expect(h.listOperations).toHaveBeenCalledTimes(calls),
      );
    } finally {
      vi.useRealTimers();
    }
  });
});
