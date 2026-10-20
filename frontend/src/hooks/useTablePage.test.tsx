import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The router's view of the URL, which a test moves as a navigation would.
const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

import { useTablePage } from "./useTablePage";
import { PAGE_TABLE } from "@/lib/table/urlState";

const ROWS = Array.from({ length: 120 }, (_, i) => `row-${i}`);

function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

beforeEach(() => navigate(""));

describe("useTablePage", () => {
  it("shows the first fifty rows until told otherwise", () => {
    const { result } = renderHook(() => useTablePage(ROWS));
    expect(result.current.rows).toEqual(ROWS.slice(0, 50));
    expect(result.current.pagination).toMatchObject({
      page: 1,
      pageCount: 3,
      pageSize: 50,
      total: 120,
      first: 1,
      last: 50,
    });
  });

  it("moves between pages, never past the last", () => {
    const { result } = renderHook(() => useTablePage(ROWS));
    act(() => result.current.pagination.onPage(3));
    expect(result.current.rows).toEqual(ROWS.slice(100));
    act(() => result.current.pagination.onPage(4));
    expect(result.current.pagination.page).toBe(3);
  });

  it("goes back to the first page on a new size", () => {
    const { result } = renderHook(() => useTablePage(ROWS));
    act(() => result.current.pagination.onPage(2));
    act(() => result.current.pagination.onPageSize(25));
    expect(result.current.pagination).toMatchObject({ page: 1, pageCount: 5 });
    act(() => result.current.pagination.onPageSize("all"));
    expect(result.current.rows).toBe(ROWS);
  });

  it("keeps the page in the URL, and opens on the one a link names", () => {
    navigate("?page=2&tab=x");
    const { result } = renderHook(() =>
      useTablePage(ROWS, { url: PAGE_TABLE }),
    );
    expect(result.current.rows[0]).toBe("row-50");
    act(() => result.current.pagination.onPage(1));
    expect(window.location.search).toBe("?tab=x");
  });

  it("shows the last page for a link to a page since emptied", () => {
    navigate("?page=9");
    const { result } = renderHook(() =>
      useTablePage(ROWS, { url: PAGE_TABLE }),
    );
    expect(result.current.pagination.page).toBe(3);
  });

  it("goes back to the first page when the search changes", () => {
    const { result, rerender } = renderHook(
      ({ query }) => useTablePage(ROWS, { resetOn: [query] }),
      { initialProps: { query: "" } },
    );
    act(() => result.current.pagination.onPage(3));
    rerender({ query: "" });
    expect(result.current.pagination.page).toBe(3);
    rerender({ query: "a" });
    expect(result.current.pagination.page).toBe(1);
  });

  it("opens on the page holding a linked row, once its rows arrive", () => {
    const key = (r: string) => r;
    const { result, rerender } = renderHook(
      ({ rows, id }) => useTablePage(rows, { focus: { id, key } }),
      { initialProps: { rows: [] as string[], id: "row-75" as string | null } },
    );
    expect(result.current.pagination.page).toBe(1);
    rerender({ rows: ROWS, id: "row-75" });
    expect(result.current.pagination.page).toBe(2);
    // The reader may move on from it; the same link does not pull them back.
    act(() => result.current.pagination.onPage(1));
    rerender({ rows: ROWS, id: "row-75" });
    expect(result.current.pagination.page).toBe(1);
  });
});
