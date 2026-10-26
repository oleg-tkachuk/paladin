import { describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

// tag, metadata and search are read out of the QUERY STRING, so they are text
// a link can carry into a CEL expression the server compiles. This file exists
// because two of them were interpolated with an escape that handled the quote
// and not the backslash, and the status — `state == '${status}'` — was
// interpolated with no escaping at all. The status and the content type now
// go to the server as facets from a fixed vocabulary, never into the filter.
//
// Named .test.tsx, not .test.ts: vitest.config splits the two projects by
// extension — .ts runs in node, .tsx in jsdom — and renderHook needs a DOM.
//
// The page's own test suite drives this hook through stub controls and mocks
// useSearchParams to an empty set, so it cannot reach these values. Here the
// URL is the input.
const params = { current: new URLSearchParams("") };
vi.mock("next/navigation", () => ({
  useSearchParams: () => params.current,
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => "/tenants/t-1/collections/ok-1/objects",
}));

import { ObjectContentCategory } from "@/gen/paladin/data/v1/object_service_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

import { useObjectListState } from "./useObjectListState";

/** Opens the page at this query string, as a link would. */
function openAt(query: string) {
  params.current = new URLSearchParams(query);
  window.history.replaceState(null, "", `/objects?${query}`);
}

function filterFor(query: string): string {
  openAt(query);
  return renderHook(() => useObjectListState()).result.current.filter;
}

function facetsFor(query: string) {
  openAt(query);
  return renderHook(() => useObjectListState()).result.current.facets;
}

// One literal is two quote characters. Anything else means the value closed
// its own string and the rest of it is being read as expression syntax.
function unescapedQuotes(expr: string): number {
  return (expr.replace(/\\./g, "").match(/"/g) ?? []).length;
}

describe("useObjectListState filter escaping", () => {
  it("builds the ordinary filters as literals", () => {
    expect(filterFor("tag=env%3Dprod")).toContain('tags["env"] == "prod"');
  });

  it("escapes both halves of a tag facet, not just the key", () => {
    const filter = filterFor("tag=" + encodeURIComponent('k"=v" || true || "'));
    // The whole hostile value stays INSIDE the literals. Asserting
    // `not.toContain("|| true ||")` would be wrong: those characters
    // legitimately appear, as data, between escaped quotes. What
    // distinguishes data from syntax is the quote count and the exact shape.
    expect(unescapedQuotes(filter)).toBe(4); // two literals, key and value
    expect(filter).toBe('tags["k\\""] == "v\\" || true || \\""');
  });

  it("reads metadata from the URL, escaped", () => {
    const filter = filterFor(
      "meta=" + encodeURIComponent('k"=v" || true || "'),
    );
    expect(unescapedQuotes(filter)).toBe(4);
    expect(filter).toBe('metadata["k\\""] == "v\\" || true || \\""');
  });
});

describe("useObjectListState facets from the URL", () => {
  it("sends the status and content type as facets, not in the filter", () => {
    expect(facetsFor("status=AVAILABLE,PENDING&type=pdf")).toEqual({
      states: [ObjectState.PENDING, ObjectState.AVAILABLE],
      contentCategories: [ObjectContentCategory.PDF],
    });
    expect(filterFor("status=AVAILABLE&type=pdf")).toBe("");
  });

  it.each([
    "status=active",
    "status=" + encodeURIComponent('x" || true || "'),
    "status=" + encodeURIComponent("c:\\"),
    "type=" + encodeURIComponent('image" || true || "'),
  ])("sends nothing for %s, which no facet offers", (query) => {
    expect(facetsFor(query)).toEqual({ states: [], contentCategories: [] });
    expect(filterFor(query)).toBe("");
  });

  it("replaces every facet's choices, as a saved view does", () => {
    openAt("status=PENDING");
    const { result } = renderHook(() => useObjectListState());
    act(() =>
      result.current.setFacetSelection({
        status: [],
        type: ["image", "other"],
      }),
    );
    expect(result.current.facets).toEqual({
      states: [],
      contentCategories: [
        ObjectContentCategory.IMAGE,
        ObjectContentCategory.OTHER,
      ],
    });
    expect(new URLSearchParams(window.location.search).get("type")).toBe(
      "image,other",
    );
    expect(new URLSearchParams(window.location.search).has("status")).toBe(
      false,
    );
  });
});

describe("useObjectListState sort from the URL", () => {
  const sortFor = (query: string) => {
    openAt(query);
    return renderHook(() => useObjectListState()).result.current.sort;
  };

  it("keeps a sort the server applies", () => {
    expect(sortFor("sort=size_bytes&dir=desc")).toEqual({
      column: "size_bytes",
      direction: "desc",
    });
  });

  // The page once sent these and the server ignored them; it now refuses
  // them, so an old link would fail the list instead of loading it.
  it.each(["status", "object_tag", "anything"])(
    "drops a sort by %s",
    (column) => {
      expect(sortFor(`sort=${column}&dir=asc`)).toEqual({
        column: "",
        direction: null,
      });
    },
  );
});

describe("useObjectListState folder", () => {
  it.each([
    ["", ""],
    ["prefix=a%2Fb", "a/b/"],
    ["prefix=%2Fa%2F", "a/"],
  ])("opens %s at the folder %s", (query, want) => {
    openAt(query);
    expect(renderHook(() => useObjectListState()).result.current.prefix).toBe(
      want,
    );
  });

  it("writes the folder it opens, and the root as no parameter", () => {
    openAt("q=x");
    const { result, rerender } = renderHook(() => useObjectListState());
    act(() => result.current.handlePrefixChange("a/b"));
    rerender();
    expect(result.current.prefix).toBe("a/b/");
    expect(new URLSearchParams(window.location.search).get("prefix")).toBe(
      "a/b/",
    );
    act(() => result.current.handlePrefixChange(""));
    expect(window.location.search).toBe("?q=x");
  });
});
