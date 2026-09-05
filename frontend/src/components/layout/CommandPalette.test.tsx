import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

// The palette had no test at all, which is how it kept searching the first
// page of each resource and calling it a global search.
//
// It sent a DISJUNCTION for tenants and `filter: ""` for the other three.
// Neither narrows in SQL — the server's pushdown descends `&&` only, and an
// empty filter narrows nothing at all — so every query read one page and the
// browser filtered it. A tenant sorting past the ceiling could not be found by
// typing its name, and nothing on screen said the list was cut.
const h = vi.hoisted(() => ({
  listTenants: vi.fn(() => Promise.resolve({ tenants: [] })),
  listBuckets: vi.fn(() => Promise.resolve({ buckets: [] })),
  listCollections: vi.fn(() => Promise.resolve({ collections: [] })),
  listBackends: vi.fn(() => Promise.resolve({ backends: [] })),
  push: vi.fn(),
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: h.push }) }));
vi.mock("@/context/ActionsContext", () => ({
  useActions: () => ({ actions: [] }),
}));
vi.mock("@/context/ScopeContext", () => ({ useScope: () => ({}) }));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { listTenants: h.listTenants },
  bucketClient: { listBuckets: h.listBuckets },
  collectionClient: { listCollections: h.listCollections },
  backendClient: { listBackends: h.listBackends },
}));

import { CommandPalette } from "./CommandPalette";

function openAndType(value: string) {
  act(() => {
    fireEvent.keyDown(window, { key: "k", metaKey: true });
  });
  act(() => {
    fireEvent.change(screen.getByRole("textbox"), { target: { value } });
  });
}

describe("CommandPalette search", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    for (const fn of [
      h.listTenants,
      h.listBuckets,
      h.listCollections,
      h.listBackends,
    ])
      fn.mockClear();
  });
  afterEach(() => vi.useRealTimers());

  it("sends the same one-conjunct filter to all four planes", async () => {
    render(<CommandPalette />);
    openAndType("Acme");
    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    const want = 'search.contains("acme")';
    for (const [name, fn] of [
      ["tenants", h.listTenants],
      ["buckets", h.listBuckets],
      ["collections", h.listCollections],
      ["backends", h.listBackends],
    ] as const) {
      const arg = fn.mock.calls.at(-1)?.[0] as { filter?: string } | undefined;
      expect(arg?.filter, `${name} was not given the filter`).toBe(want);
      // A disjunction pushes nothing into SQL. This is the shape that made the
      // palette a first-page search.
      expect(arg?.filter).not.toContain("||");
    }
  });

  it("asks nothing at all for an empty query", () => {
    // performSearch returns early on a falsy query, so the assertion is that
    // NO request happens — not a conditional one that checks the filter only
    // if a call was made. That form passes when the palette is broken enough
    // to call nothing, which is exactly the failure it would be there to
    // catch, and it was how this test was first written.
    render(<CommandPalette />);
    openAndType("");
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(h.listTenants).not.toHaveBeenCalled();
    expect(h.listBuckets).not.toHaveBeenCalled();
  });
});
