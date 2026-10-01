import { ConnectError, Code } from "@connectrpc/connect";
import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render } from "@/test/utils";
import { formatTime, setDisplayTimeZone } from "@/lib/format/locale";

const h = vi.hoisted(() => ({ getMine: vi.fn(), setTheme: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine },
}));
vi.mock("next-themes", () => ({ useTheme: () => ({ setTheme: h.setTheme }) }));

import { SettingsSync } from "./SettingsSync";

// 12:00 UTC, which is 21:00 in Tokyo and 12:00 in UTC.
const NOON_UTC = new Date(Date.UTC(2026, 9, 1, 12, 0, 0));
const SAVED = { seconds: 1n };

/** A page that writes a time, as the children of the gate do. */
function Clock() {
  return <p>at {formatTime(NOON_UTC)}</p>;
}

describe("SettingsSync", () => {
  beforeEach(() => {
    h.getMine.mockReset();
    h.setTheme.mockReset();
  });
  afterEach(() => {
    setDisplayTimeZone(undefined);
  });

  it("applies the saved theme", async () => {
    h.getMine.mockResolvedValue({ theme: "light", updatedAt: SAVED });
    render(<SettingsSync>page</SettingsSync>);
    await waitFor(() => expect(h.setTheme).toHaveBeenCalledWith("light"));
  });

  // The page's first render is already in the saved zone, not the browser's.
  it("renders the page in the saved time zone", async () => {
    h.getMine.mockResolvedValue({
      theme: "dark",
      timezone: "Asia/Tokyo",
      updatedAt: SAVED,
    });
    render(
      <SettingsSync>
        <Clock />
      </SettingsSync>,
    );
    expect(await screen.findByText("at 21:00:00")).toBeInTheDocument();
  });

  // GetMine serves UTC to a user who never saved; that is not their choice.
  it("keeps the browser's zone for settings never saved", async () => {
    h.getMine.mockResolvedValue({ theme: "system", timezone: "Asia/Tokyo" });
    render(
      <SettingsSync>
        <Clock />
      </SettingsSync>,
    );
    expect(
      await screen.findByText(`at ${formatTime(NOON_UTC)}`),
    ).toBeInTheDocument();
    setDisplayTimeZone("Asia/Tokyo");
    expect(screen.queryByText("at 21:00:00")).toBeNull();
  });

  it("renders nothing until the read settles", () => {
    h.getMine.mockReturnValue(new Promise(() => {}));
    render(<SettingsSync>page</SettingsSync>);
    expect(screen.queryByText("page")).toBeNull();
  });

  it.each([
    [
      "an unknown theme",
      () => h.getMine.mockResolvedValue({ theme: "midnight" }),
    ],
    [
      "no saved settings",
      () => h.getMine.mockRejectedValue(new ConnectError("", Code.NotFound)),
    ],
    [
      "a failed read",
      () => h.getMine.mockRejectedValue(new ConnectError("", Code.Unavailable)),
    ],
  ])(
    "keeps the current theme on %s and still renders the page",
    async (_, arrange) => {
      arrange();
      render(<SettingsSync>page</SettingsSync>);
      expect(await screen.findByText("page")).toBeInTheDocument();
      expect(h.setTheme).not.toHaveBeenCalled();
    },
  );
});
