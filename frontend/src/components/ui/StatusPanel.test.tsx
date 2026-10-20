import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  stored: {} as Record<string, unknown>,
  getMine: vi.fn(),
  updateMine: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine, updateMine: h.updateMine },
}));

import { fireEvent, render, screen, waitFor, within } from "@/test/utils";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { StatusTone } from "@/lib/ui/status";

import { StatusCard, StatusPanel, type StatusCardSpec } from "./StatusPanel";

const settings = () => ({ theme: "dark", preferences: { ...h.stored } });

beforeEach(() => {
  h.stored = {};
  h.getMine.mockReset().mockImplementation(async () => settings());
  h.updateMine.mockReset().mockImplementation(async ({ preferences }) => {
    h.stored = { ...preferences };
    return settings();
  });
});

const CARDS: StatusCardSpec[] = [
  {
    title: "Health",
    tone: StatusTone.Suspended,
    value: "Frozen",
    detail: "In the trash: it takes no changes.",
    href: "/trash",
    linkLabel: "Open the trash",
  },
  { title: "Quota", tone: StatusTone.Warning, value: "Near limit" },
];

describe("StatusCard", () => {
  it("names the state in words beside its glyph, with the detail and the link", () => {
    render(<StatusCard {...CARDS[0]} help="tenantTrash" />);
    const card = screen
      .getByText("Health")
      .closest('[data-slot="status-card"]');
    expect(card).toHaveAttribute("data-tone", "suspended");
    expect(screen.getByText("Frozen")).toBeVisible();
    expect(
      screen.getByText("In the trash: it takes no changes."),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: /Open the trash/ }),
    ).toHaveAttribute("href", "/trash");
    expect(
      screen.getByRole("button", { name: "What is tenant trash?" }),
    ).toBeVisible();
  });

  // A state still being read is not shown as one: "Unknown" before the
  // answer would read as an answer.
  it("holds a placeholder while it is pending", () => {
    render(<StatusCard {...CARDS[1]} pending />);
    expect(screen.queryByText("Near limit")).toBeNull();
  });
});

describe("StatusPanel", () => {
  it("shows a card per fact, then collapses to one line and keeps the choice", async () => {
    render(<StatusPanel label="Tenant status" cards={CARDS} />);
    const panel = await screen.findByRole("region", { name: "Tenant status" });
    expect(within(panel).getAllByText(/Frozen|Near limit/)).toHaveLength(2);
    expect(panel.querySelectorAll('[data-slot="status-card"]')).toHaveLength(2);

    fireEvent.click(
      within(panel).getByRole("button", { name: "Collapse to one line" }),
    );
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.collapseStatusPanels]).toBe(true),
    );
    const row = await screen.findByRole("region", { name: "Tenant status" });
    expect(row).toHaveAttribute("data-collapsed");
    expect(row.querySelectorAll('[data-slot="status-card"]')).toHaveLength(0);
    // The one line still says every state.
    expect(within(row).getByText("Frozen")).toBeVisible();
    expect(within(row).getByText("Near limit")).toBeVisible();
  });

  it("opens collapsed for a user who collapsed it before", async () => {
    h.stored = { [PREFERENCE_KEYS.collapseStatusPanels]: true };
    render(<StatusPanel label="Bucket status" cards={CARDS} />);
    const button = await screen.findByRole("button", { name: "Show details" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(button);
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.collapseStatusPanels]).toBe(false),
    );
  });
});
