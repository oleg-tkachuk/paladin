import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

import { LINKED_ROW_ATTR } from "@/components/ui/table";
import { TABLE_PARAMS, type TableUrl } from "@/lib/table/urlState";

import { useLinkedRow } from "./useLinkedRow";

const GRANTS: TableUrl = { namespace: "grants" };

function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

/** A row element whose scrolling a test can watch. */
function rowElement() {
  const el = document.createElement("tr");
  el.scrollIntoView = vi.fn();
  return el;
}

beforeEach(() => navigate(""));

describe("useLinkedRow", () => {
  it("names no row without the parameter, and marks none", () => {
    const { result } = renderHook(() => useLinkedRow());
    expect(result.current.linkedId).toBeNull();
    expect(result.current.rowProps("")).toEqual({});
    expect(result.current.rowProps("op-1")).toEqual({});
  });

  it("marks the row a link names and scrolls to it once", () => {
    navigate(`?${TABLE_PARAMS.row}=op-2`);
    const { result } = renderHook(() => useLinkedRow());
    expect(result.current.linkedId).toBe("op-2");
    expect(result.current.rowProps("op-1")).toEqual({});
    const props = result.current.rowProps("op-2");
    expect(props).toMatchObject({ [LINKED_ROW_ATTR]: true });
    const el = rowElement();
    if (!("ref" in props)) throw new Error("the linked row takes a ref");
    props.ref(el);
    props.ref(el);
    expect(el.scrollIntoView).toHaveBeenCalledTimes(1);
    expect(el.scrollIntoView).toHaveBeenCalledWith({ block: "center" });
  });

  it("puts a row opened here in the URL without scrolling to it", () => {
    navigate("?q=acme");
    const { result } = renderHook(() => useLinkedRow());
    act(() => result.current.open("op-3"));
    expect(window.location.search).toBe(`?q=acme&${TABLE_PARAMS.row}=op-3`);
    const props = result.current.rowProps("op-3");
    if (!("ref" in props)) throw new Error("the linked row takes a ref");
    const el = rowElement();
    props.ref(el);
    expect(el.scrollIntoView).not.toHaveBeenCalled();
    act(() => result.current.close());
    expect(window.location.search).toBe("?q=acme");
    expect(result.current.linkedId).toBeNull();
  });

  it("keeps an embedded table's row under its namespace", () => {
    navigate(`?${TABLE_PARAMS.row}=page-row&grants.${TABLE_PARAMS.row}=g-1`);
    const { result } = renderHook(() => useLinkedRow(GRANTS));
    expect(result.current.linkedId).toBe("g-1");
  });
});
