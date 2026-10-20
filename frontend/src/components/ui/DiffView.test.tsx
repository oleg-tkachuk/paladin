import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

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

import { DiffView } from "./DiffView";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { USER_SETTINGS_QUERY_KEY } from "@/lib/theme";

function renderWith(ui: ReactElement, stored: Record<string, unknown> = {}) {
  h.stored = stored;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(USER_SETTINGS_QUERY_KEY, { preferences: stored });
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>,
  );
}

beforeEach(() => {
  h.updateMine.mockReset();
  h.updateMine.mockImplementation(async ({ preferences }) => {
    h.stored = { ...preferences };
    return { preferences: { ...h.stored } };
  });
});

const BEFORE = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join("\n");
const AFTER = BEFORE.replace("line 6", "line six");

/** The rows of the diff's table, each read as its cells' text. */
function rows(): string[][] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("row")
    .map((r) =>
      within(r)
        .queryAllByRole("cell")
        .map((c) => c.textContent ?? ""),
    );
}

describe("DiffView", () => {
  it("says there are no differences when the texts match", () => {
    renderWith(<DiffView before={"a\nb"} after={"a\r\nb\n"} />);
    expect(
      screen.getByText(/No differences between before and after/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows only the change and two lines either side when compact", () => {
    renderWith(<DiffView before={BEFORE} after={AFTER} />);
    const text = rows().map((cells) => cells.join("|"));
    expect(text).toEqual([
      "@@ −4,5 +4,5 @@",
      "4|4| |line 4",
      "5|5| |line 5",
      "6||−Removed|line 6",
      "|6|+Added|line six",
      "7|7| |line 7",
      "8|8| |line 8",
    ]);
  });

  it("marks lines by a sign and a spoken word, not by colour alone", () => {
    renderWith(<DiffView before={BEFORE} after={AFTER} />);
    const added = document.querySelector('[data-diff="add"]');
    const removed = document.querySelector('[data-diff="remove"]');
    expect(added).toHaveClass("bg-success/10");
    expect(removed).toHaveClass("bg-destructive/10");
    expect(within(added as HTMLElement).getByText("+")).toHaveAttribute(
      "aria-hidden",
    );
    expect(within(added as HTMLElement).getByText("Added")).toHaveClass(
      "sr-only",
    );
    expect(screen.getByText("1 added, 1 removed")).toBeInTheDocument();
  });

  it("shows every line when compact is off, and remembers it", async () => {
    renderWith(<DiffView before={BEFORE} after={AFTER} />);
    await userEvent.click(screen.getByRole("switch", { name: "Compact" }));
    expect(rows()).toHaveLength(13);
    expect(screen.queryByText(/^@@/)).toBeNull();
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.diff]).toEqual({
        compact: false,
        split: false,
      }),
    );
  });

  it("opens as the user left it", () => {
    renderWith(<DiffView before={BEFORE} after={AFTER} />, {
      [PREFERENCE_KEYS.diff]: { compact: false, split: true },
    });
    expect(screen.getByRole("switch", { name: "Compact" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "Side by side" })).toBeChecked();
    expect(rows()).toHaveLength(12);
  });

  it("sets the old and new lines side by side", async () => {
    renderWith(<DiffView before={"a\nb\nc"} after={"a\nB\nc\nd"} />);
    await userEvent.click(screen.getByRole("switch", { name: "Side by side" }));
    const text = rows().map((cells) => cells.join("|"));
    expect(text).toEqual([
      "@@ −1,3 +1,4 @@",
      "1| |a|1| |a",
      "2|−Removed|b|2|+Added|B",
      "3| |c|3| |c",
      // The added line faces an empty cell.
      "|4|+Added|d",
    ]);
  });

  it("names both sides by the labels it is given", () => {
    renderWith(
      <DiffView before="a" after="b" beforeLabel="Saved" afterLabel="Draft" />,
    );
    expect(
      screen.getByRole("table", { name: "Changes from saved to draft" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Saved")).toBeInTheDocument();
    expect(screen.getByText("Draft")).toBeInTheDocument();
  });

  it("reads a malformed preference as the default", () => {
    renderWith(<DiffView before="a" after="b" />, {
      [PREFERENCE_KEYS.diff]: "split please",
    });
    expect(screen.getByRole("switch", { name: "Compact" })).toBeChecked();
    expect(
      screen.getByRole("switch", { name: "Side by side" }),
    ).not.toBeChecked();
  });
});
