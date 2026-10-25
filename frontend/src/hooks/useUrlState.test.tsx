import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The router's view of the URL, which a test moves as a navigation would.
const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

import { textParam, useUrlState } from "./useUrlState";
import { useTableQuery } from "./useTableUrl";
import { useTableSort } from "./useTableSort";
import { useTableView } from "./useTableView";
import { PAGE_TABLE, type TableUrl } from "@/lib/table/urlState";

const GRANTS: TableUrl = { namespace: "grants" };
const COLUMNS = ["name"] as const;

/** Moves the address bar as a link to this page would, router included. */
function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

beforeEach(() => {
  navigate("");
});

describe("useTableQuery", () => {
  it("starts from the URL and writes each change back, leaving the rest", () => {
    navigate("?q=acme&tab=x");
    const { result } = renderHook(() => useTableQuery(PAGE_TABLE));
    expect(result.current[0]).toBe("acme");
    act(() => result.current[1]("beta"));
    expect(result.current[0]).toBe("beta");
    expect(window.location.search).toBe("?q=beta&tab=x");
    act(() => result.current[1](""));
    expect(window.location.search).toBe("?tab=x");
  });

  it("takes up a navigation to another search", () => {
    const { result, rerender } = renderHook(() => useTableQuery(PAGE_TABLE));
    navigate("?q=linked");
    rerender();
    expect(result.current[0]).toBe("linked");
  });

  // Typing runs ahead of the router: its view may still hold an earlier
  // keystroke when it re-renders, and that must not undo the later one.
  it("keeps the latest keystroke when the router catches up late", () => {
    const { result, rerender } = renderHook(() => useTableQuery(PAGE_TABLE));
    act(() => result.current[1]("a"));
    act(() => result.current[1]("ab"));
    nav.search = "?q=a";
    rerender();
    expect(result.current[0]).toBe("ab");
  });

  it("stays out of the URL without a TableUrl", () => {
    navigate("?q=acme");
    const { result } = renderHook(() => useTableQuery());
    expect(result.current[0]).toBe("");
    act(() => result.current[1]("x"));
    expect(window.location.search).toBe("?q=acme");
  });

  it("reads and writes under its namespace only", () => {
    navigate("?q=page");
    const { result } = renderHook(() => useTableQuery(GRANTS));
    expect(result.current[0]).toBe("");
    act(() => result.current[1]("g"));
    expect(window.location.search).toBe("?q=page&grants.q=g");
  });
});

describe("useTableSort in the URL", () => {
  const inUrl = { url: PAGE_TABLE, columns: COLUMNS };
  it("restores a sort, cycles it, and clears it from the URL", () => {
    navigate("?sort=name&dir=asc");
    const { result } = renderHook(() => useTableSort(undefined, inUrl));
    expect(result.current.sort).toEqual({ column: "name", direction: "asc" });
    act(() => result.current.toggleSort("name"));
    expect(window.location.search).toBe("?sort=name&dir=desc");
    act(() => result.current.toggleSort("name"));
    expect(result.current.sort.column).toBeNull();
    expect(window.location.search).toBe("");
  });
});

describe("useTableView in the URL", () => {
  const rows = [{ name: "b" }, { name: "a" }, { name: "c" }];
  const text = (r: { name: string }) => [r.name];
  const accessors = { name: (r: { name: string }) => r.name };
  it("opens a shared link with its search and sort applied", () => {
    navigate("?q=a&sort=name&dir=desc");
    const { result } = renderHook(() =>
      useTableView(rows, { text, accessors, url: PAGE_TABLE }),
    );
    expect(result.current.query).toBe("a");
    expect(result.current.rows.map((r) => r.name)).toEqual(["a"]);
    act(() => result.current.setQuery(""));
    expect(result.current.rows.map((r) => r.name)).toEqual(["c", "b", "a"]);
  });
});

// A link to the same page (next/link) moves the router first: the page
// re-renders with the new parameters before Next.js writes them to the
// address bar. The capability panel's link to an ancestor changed ?id= and
// left the panel where it was.
describe("useUrlState", () => {
  const ID = textParam("id");

  it("takes up a soft navigation the address bar has not caught up with", () => {
    navigate("?id=first");
    const { result, rerender } = renderHook(() => useUrlState(ID, ""));
    expect(result.current[0]).toBe("first");
    nav.search = "?id=next";
    rerender();
    expect(result.current[0]).toBe("next");
  });

  // A link back to a row opened before: the router reported both writes, so
  // a query string it already passed is a navigation, not it catching up.
  it("takes up a link back to a value this page wrote before", () => {
    const { result, rerender } = renderHook(() => useUrlState(ID, ""));
    act(() => result.current[1]("parent"));
    nav.search = "?id=parent";
    rerender();
    act(() => result.current[1]("child"));
    nav.search = "?id=child";
    rerender();
    nav.search = "?id=parent";
    rerender();
    expect(result.current[0]).toBe("parent");
  });

  // The router catching up with one hook's write must not undo another's
  // later one: the registry of writes is the page's, not each hook's.
  it("keeps a later write when the router catches up with another hook's", () => {
    const TAB = textParam("tab");
    const { result, rerender } = renderHook(() => ({
      id: useUrlState(ID, ""),
      tab: useUrlState(TAB, ""),
    }));
    act(() => result.current.id[1]("a"));
    act(() => result.current.tab[1]("x"));
    act(() => result.current.id[1]("ab"));
    nav.search = "?id=a&tab=x";
    rerender();
    expect(result.current.id[0]).toBe("ab");
    expect(result.current.tab[0]).toBe("x");
  });
});
