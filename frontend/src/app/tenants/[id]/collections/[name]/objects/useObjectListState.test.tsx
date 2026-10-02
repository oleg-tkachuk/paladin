import { describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

// status, tag and search are read out of the QUERY STRING, so they are text a
// link can carry into a CEL expression the server compiles. This file exists
// because two of the three were interpolated with an escape that handled the
// quote and not the backslash, and the third — `state == '${status}'` — was
// interpolated with no escaping at all.
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

import { useObjectListState } from "./useObjectListState";

function filterFor(query: string): string {
  params.current = new URLSearchParams(query);
  return renderHook(() => useObjectListState()).result.current.filter;
}

// One literal is two quote characters. Anything else means the value closed
// its own string and the rest of it is being read as expression syntax.
function unescapedQuotes(expr: string): number {
  return (expr.replace(/\\./g, "").match(/"/g) ?? []).length;
}

describe("useObjectListState filter escaping", () => {
  it("builds the ordinary filters as literals", () => {
    expect(filterFor("status=active")).toBe('state == "active"');
    expect(filterFor("tag=env%3Dprod")).toContain('tags["env"] == "prod"');
  });

  it("does not let a crafted status write its own conjunct", () => {
    const filter = filterFor("status=" + encodeURIComponent('x" || true || "'));
    // The whole hostile value stays INSIDE one literal. Asserting
    // `not.toContain("|| true ||")` would be wrong and did fail here: those
    // characters legitimately appear, as data, between escaped quotes. What
    // distinguishes data from syntax is the quote count and the exact shape.
    expect(unescapedQuotes(filter)).toBe(2);
    expect(filter).toBe('state == "x\\" || true || \\""');
  });

  it("survives a trailing backslash, which used to escape the closing quote", () => {
    const filter = filterFor("status=" + encodeURIComponent("c:\\"));
    expect(unescapedQuotes(filter)).toBe(2);
    expect(filter).toBe('state == "c:\\\\"');
  });

  it("escapes both halves of a tag facet, not just the key", () => {
    const filter = filterFor("tag=" + encodeURIComponent('k"=v" || true || "'));
    expect(unescapedQuotes(filter)).toBe(4); // two literals, key and value
    expect(filter).toBe('tags["k\\""] == "v\\" || true || \\""');
  });

  it("reads content type and metadata from the URL, escaped or ignored", () => {
    expect(filterFor("type=image")).toBe('content_type.startsWith("image/")');
    expect(filterFor("type=" + encodeURIComponent('image" || true || "'))).toBe(
      "",
    );
    const filter = filterFor(
      "meta=" + encodeURIComponent('k"=v" || true || "'),
    );
    expect(unescapedQuotes(filter)).toBe(4);
    expect(filter).toBe('metadata["k\\""] == "v\\" || true || \\""');
  });
});
