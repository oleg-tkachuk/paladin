import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

const h = vi.hoisted(() => ({ stored: undefined as unknown }));
vi.mock("@/hooks/usePreference", () => ({
  usePreferenceSnapshot: () => h.stored,
}));

import { useTableViewMode } from "./useTableViewMode";

afterEach(() => {
  h.stored = undefined;
  window.history.replaceState(null, "", "/tenants");
});

describe("useTableViewMode", () => {
  it("opens as a table when nothing says otherwise", () => {
    const { result } = renderHook(() => useTableViewMode("tenants"));
    expect(result.current.view).toBe("table");
  });

  it("opens in the view the user saved for the table", () => {
    h.stored = { tenants: { view: "summary" }, other: { view: "tiles" } };
    const { result } = renderHook(() => useTableViewMode("tenants"));
    expect(result.current.view).toBe("summary");
  });

  // A link opens the list as it was, whatever the reader saved.
  it("takes the view a link names over the saved one", () => {
    h.stored = { tenants: { view: "summary" } };
    window.history.replaceState(null, "", "/tenants?view=tiles");
    const { result } = renderHook(() => useTableViewMode("tenants"));
    expect(result.current.view).toBe("tiles");
  });

  it("writes a change to the URL", () => {
    const { result } = renderHook(() => useTableViewMode("tenants"));
    act(() => result.current.setView("tiles"));
    expect(result.current.view).toBe("tiles");
    expect(window.location.search).toBe("?view=tiles");
    act(() => result.current.setView("table"));
    expect(window.location.search).toBe("");
  });
});
