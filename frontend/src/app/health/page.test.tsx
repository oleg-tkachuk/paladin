import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

vi.mock("next/navigation", () => ({
  usePathname: () => "/health",
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/context/StatsContext", () => ({
  useStats: () => ({ stats: null }),
}));
// The page polls; one call on mount is all a render test needs.
vi.mock("@/hooks/useVisiblePolling", () => ({
  useVisiblePolling: (fn: () => void) => {
    useEffect(() => {
      fn();
    }, [fn]);
  },
}));

import HealthPage from "./page";

const component = (name: string, critical = false) => ({
  name,
  status: "healthy",
  latency_ms: 0,
  critical,
});
const payload = {
  roles: ["api", "admin", "worker", "dispatcher", "mcp"].map((role) => ({
    role,
    status: "healthy",
    components: [component("postgres", true), component("iam_listener", true)],
  })),
};

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(payload))),
  );
});
afterEach(() => vi.unstubAllGlobals());

const roleCard = async (role: string) =>
  (await screen.findByText(role)).closest('[data-slot="card"]')!;

describe("HealthPage role cards", () => {
  // A fixed column count squeezed five cards into four columns and cut
  // component names short ("capabili…", "iam_list…"). Columns now come from
  // the cards' minimum width, so a card is never narrower than its widest
  // row, and they fold to one on a narrow page.
  it("lays the cards out by their minimum width, not a column count", async () => {
    render(<HealthPage />);
    const grid = (await roleCard("dispatcher")).parentElement!;
    expect(grid.className).toContain(
      "grid-cols-[repeat(auto-fit,minmax(min(100%,23rem),1fr))]",
    );
    expect(grid.className).not.toMatch(/(^|\s)(md|lg|xl):grid-cols-/);
    expect(grid.children).toHaveLength(payload.roles.length);
  });

  // The card's default gap and padding stacked on the header's own left a
  // blank band between a role's name and its components.
  it("adds no card gap between the header and the rows", async () => {
    render(<HealthPage />);
    const card = await roleCard("api");
    expect(card.classList).toContain("gap-0");
    expect(card.classList).toContain("py-0");
  });
});
