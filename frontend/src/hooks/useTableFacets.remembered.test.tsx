import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The router's view of the URL, which a test moves as a navigation would.
const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

const h = vi.hoisted(() => ({
  stored: {} as Record<string, unknown>,
  updateMine: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: {
    getMine: async () => ({ preferences: { ...h.stored } }),
    updateMine: h.updateMine,
  },
}));

import { useServerFacets, useTableFacets } from "./useTableFacets";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import type { Facet } from "@/lib/table/facets";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { USER_SETTINGS_QUERY_KEY } from "@/lib/theme";

interface Row {
  id: string;
  state: string;
}
const FACETS: readonly Facet<Row>[] = [
  {
    id: "state",
    label: "State",
    values: (r) => [r.state],
    options: [
      { value: "on", label: "On" },
      { value: "off", label: "Off" },
    ],
    initial: ["on"],
  },
];
const ROWS: Row[] = [
  { id: "a", state: "on" },
  { id: "b", state: "off" },
];
const TABLE = "things";

function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

/** The preferences as the shell leaves them after sign-in: loaded. */
function wrapperWith(tables: Record<string, unknown>) {
  h.stored = { [PREFERENCE_KEYS.tables]: tables };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(USER_SETTINGS_QUERY_KEY, { preferences: h.stored });
  return function Signedin({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

const savedOf = () =>
  (h.stored[PREFERENCE_KEYS.tables] as Record<string, { facets?: unknown }>)?.[
    TABLE
  ]?.facets;

beforeEach(() => {
  navigate("");
  h.updateMine.mockReset().mockImplementation(async ({ preferences }) => {
    h.stored = { ...preferences };
    return { preferences: { ...h.stored } };
  });
});

describe("remembered facet choices", () => {
  it("opens on the saved choices when the URL names no facet, and says so", () => {
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE),
      { wrapper: wrapperWith({ [TABLE]: { facets: { state: ["off"] } } }) },
    );
    expect(result.current.rows.map((r) => r.id)).toEqual(["b"]);
    expect(result.current.saved).not.toBeNull();
    // The view on screen is the URL's too, so a reload keeps it.
    expect(window.location.search).toBe("?state=off");
  });

  it("opens on the URL's choices when it names one", () => {
    navigate("?state=any");
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE),
      { wrapper: wrapperWith({ [TABLE]: { facets: { state: ["off"] } } }) },
    );
    expect(result.current.rows.map((r) => r.id)).toEqual(["a", "b"]);
  });

  it("saves a choice made, beside what the table already keeps", async () => {
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE),
      { wrapper: wrapperWith({ [TABLE]: { pageSize: 25 } }) },
    );
    expect(result.current.saved).toBeNull();
    act(() => result.current.controls[0].onChange(["off"]));
    expect(result.current.saved).not.toBeNull();
    await waitFor(() => expect(savedOf()).toEqual({ state: ["off"] }));
    expect(
      (h.stored[PREFERENCE_KEYS.tables] as Record<string, unknown>)[TABLE],
    ).toEqual({ pageSize: 25, facets: { state: ["off"] } });
  });

  it("forgets the saved choices when one goes back to where the list opens", async () => {
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE),
      { wrapper: wrapperWith({ [TABLE]: { facets: { state: ["off"] } } }) },
    );
    act(() => result.current.controls[0].onChange(["on"]));
    expect(result.current.saved).toBeNull();
    await waitFor(() => expect(h.updateMine).toHaveBeenCalled());
    expect(savedOf()).toBeUndefined();
  });

  it("clears the saved choices with the ones on screen", async () => {
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE),
      { wrapper: wrapperWith({ [TABLE]: { facets: { state: ["off"] } } }) },
    );
    act(() => result.current.saved?.clear());
    expect(result.current.active).toBe(0);
    expect(result.current.saved).toBeNull();
    await waitFor(() => expect(h.updateMine).toHaveBeenCalled());
    expect(savedOf()).toBeUndefined();
  });

  it("does not save the choices a link brought", () => {
    navigate("?state=off");
    renderHook(() => useTableFacets(ROWS, FACETS, PAGE_TABLE, TABLE), {
      wrapper: wrapperWith({}),
    });
    expect(h.updateMine).not.toHaveBeenCalled();
  });

  it("remembers nothing without a table id, or outside a query client", () => {
    const { result } = renderHook(
      () => useTableFacets(ROWS, FACETS, PAGE_TABLE),
      { wrapper: wrapperWith({ [TABLE]: { facets: { state: ["off"] } } }) },
    );
    expect(result.current.rows.map((r) => r.id)).toEqual(["a"]);
    act(() => result.current.controls[0].onChange(["off"]));
    expect(h.updateMine).not.toHaveBeenCalled();

    const bare = renderHook(() => useServerFacets(FACETS, PAGE_TABLE, TABLE));
    act(() => bare.result.current.choose("state", ["off"]));
    expect(bare.result.current.saved).toBeNull();
    expect(h.updateMine).not.toHaveBeenCalled();
  });
});
