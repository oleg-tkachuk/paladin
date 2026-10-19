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

import { useResourceLinks } from "./useResourceLinks";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";

const link = (n: number) => ({
  href: `/tenants/t${n}`,
  label: `t${n}`,
  kind: "Tenant",
});
const settings = () => ({ preferences: { ...h.stored } });

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  h.stored = { [PREFERENCE_KEYS.recent]: [link(1), link(2)] };
  // The settings answer late, as on a cold page load.
  h.getMine
    .mockReset()
    .mockImplementation(
      () => new Promise((r) => setTimeout(() => r(settings()), 20)),
    );
  h.updateMine.mockReset().mockImplementation(async ({ preferences }) => {
    h.stored = { ...preferences };
    return settings();
  });
});

describe("useResourceLinks", () => {
  // The first page of a session records its visit before the settings have
  // loaded; added to this render's empty copy, it replaced the history.
  it("adds a visit made before the settings loaded to the stored history", async () => {
    const { result } = renderHook(() => useResourceLinks(), { wrapper });
    act(() => result.current.visit(link(3)));
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.recent]).toEqual([
        link(3),
        link(1),
        link(2),
      ]),
    );
  });

  it("writes nothing for a visit that changes nothing", async () => {
    const { result } = renderHook(() => useResourceLinks(), { wrapper });
    await waitFor(() => expect(result.current.recent).toHaveLength(2));
    act(() => result.current.visit(link(1)));
    await new Promise((r) => setTimeout(r, 50));
    expect(h.updateMine).not.toHaveBeenCalled();
  });

  it("pins and unpins against what is stored", async () => {
    const { result } = renderHook(() => useResourceLinks(), { wrapper });
    await act(() => result.current.togglePin(link(5)));
    expect(h.stored[PREFERENCE_KEYS.pinned]).toEqual([link(5)]);
    await waitFor(() =>
      expect(result.current.isPinned(link(5).href)).toBe(true),
    );
    await act(() => result.current.togglePin(link(5)));
    expect(h.stored[PREFERENCE_KEYS.pinned]).toEqual([]);
  });
});
