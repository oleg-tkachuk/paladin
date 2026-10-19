import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  stored: {} as Record<string, unknown>,
  getMine: vi.fn(),
  updateMine: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine, updateMine: h.updateMine },
}));

import { usePreference } from "./usePreference";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { readLinks } from "@/lib/preferences/resourceLinks";

const settings = () => ({ theme: "dark", preferences: { ...h.stored } });

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const link = (n: number) => ({
  href: `/tenants/t${n}`,
  label: `t${n}`,
  kind: "Tenant",
});

beforeEach(() => {
  h.stored = { "other.client": { keep: true } };
  h.getMine.mockReset().mockImplementation(async () => settings());
  // The server replaces preferences whole, as UpdateMine does.
  h.updateMine.mockReset().mockImplementation(async ({ preferences }) => {
    await new Promise((r) => setTimeout(r, 5));
    h.stored = { ...preferences };
    return settings();
  });
});

describe("usePreference", () => {
  it("writes its key and leaves the others as they were", async () => {
    const { result } = renderHook(
      () => usePreference(PREFERENCE_KEYS.pinned, readLinks),
      { wrapper },
    );
    await waitFor(() => expect(h.getMine).toHaveBeenCalled());
    await act(() => result.current[1]([link(1)]));
    expect(h.stored["other.client"]).toEqual({ keep: true });
    await waitFor(() => expect(result.current[0]).toEqual([link(1)]));
  });

  // Two writes at once each sent a copy without the other's key.
  it("loses neither of two writes made together", async () => {
    const { result } = renderHook(
      () => ({
        pinned: usePreference(PREFERENCE_KEYS.pinned, readLinks),
        recent: usePreference(PREFERENCE_KEYS.recent, readLinks),
      }),
      { wrapper },
    );
    await waitFor(() => expect(h.getMine).toHaveBeenCalled());
    await act(() =>
      Promise.all([
        result.current.pinned[1]([link(1)]),
        result.current.recent[1]([link(2)]),
      ]),
    );
    expect(h.stored[PREFERENCE_KEYS.pinned]).toEqual([link(1)]);
    expect(h.stored[PREFERENCE_KEYS.recent]).toEqual([link(2)]);
  });

  it("reads a value of the wrong shape as none", async () => {
    h.stored[PREFERENCE_KEYS.recent] = "not a list";
    const { result } = renderHook(
      () => usePreference(PREFERENCE_KEYS.recent, readLinks),
      { wrapper },
    );
    await waitFor(() => expect(h.getMine).toHaveBeenCalled());
    expect(result.current[0]).toEqual([]);
  });
});
