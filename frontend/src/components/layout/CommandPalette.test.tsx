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
// The request argument is declared, not inferred: `vi.fn(() => …)` has an
// empty parameter tuple, so `calls.at(-1)?.[0]` is a type error — and the
// filter this file exists to assert lives in exactly that argument.
type ListReq = { filter?: string };
// The row type is declared too, not inferred from `[]`. An empty literal infers
// never[], so a test returning a populated list — which the partial-failure
// cases below must — does not type-check against its own mock.
type Rows = Record<string, unknown>[];
const h = vi.hoisted(() => ({
  listTenants: vi.fn(
    (_req: ListReq): Promise<{ tenants: Record<string, unknown>[] }> =>
      Promise.resolve({ tenants: [] }),
  ),
  listBuckets: vi.fn(
    (_req: ListReq): Promise<{ buckets: Record<string, unknown>[] }> =>
      Promise.resolve({ buckets: [] }),
  ),
  listCollections: vi.fn(
    (_req: ListReq): Promise<{ collections: Record<string, unknown>[] }> =>
      Promise.resolve({ collections: [] }),
  ),
  listBackends: vi.fn(
    (_req: ListReq): Promise<{ backends: Record<string, unknown>[] }> =>
      Promise.resolve({ backends: [] }),
  ),
  push: vi.fn(),
}));
export type { Rows };

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
      const arg = fn.mock.calls.at(-1)?.[0];
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

// A source that falls over must not look like a source that found nothing.
//
// Each call carries its own .catch() so one broken plane cannot take the whole
// palette down — that part is right. Returning an empty list from it was not:
// an operator typing a bucket name they can see in another tab was told, in
// silence, that it does not exist.
describe("CommandPalette partial failures", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    h.listTenants.mockClear();
    h.listBuckets.mockClear();
    h.listCollections.mockClear();
    h.listBackends.mockClear();
  });
  afterEach(() => {
    vi.useRealTimers();
    h.listBackends.mockImplementation(() => Promise.resolve({ backends: [] }));
  });

  it("names the source that did not answer", async () => {
    h.listBackends.mockImplementation(() =>
      Promise.reject(new Error("unavailable")),
    );
    render(<CommandPalette />);
    openAndType("acme");
    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    expect(screen.getByRole("status")).toHaveTextContent(
      /backends did not respond/i,
    );
  });

  it("says so even when the other sources returned rows", async () => {
    // The harder case: results on screen read as "the search worked", so a
    // failure with no banner is the same lie, only harder to notice.
    h.listBackends.mockImplementation(() =>
      Promise.reject(new Error("unavailable")),
    );
    h.listTenants.mockImplementation(() =>
      Promise.resolve({
        tenants: [{ tenantId: "t-1", slug: "acme", displayName: "Acme" }],
      }),
    );
    render(<CommandPalette />);
    openAndType("acme");
    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/did not respond/i);
    h.listTenants.mockImplementation(() => Promise.resolve({ tenants: [] }));
  });

  it("shows nothing when every source answered", async () => {
    render(<CommandPalette />);
    openAndType("acme");
    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    expect(screen.queryByRole("status")).toBeNull();
  });
});
