import { ConnectError, Code } from "@connectrpc/connect";
import { waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render } from "@/test/utils";

const h = vi.hoisted(() => ({ getMine: vi.fn(), setTheme: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine },
}));
vi.mock("next-themes", () => ({ useTheme: () => ({ setTheme: h.setTheme }) }));

import { ThemeSync } from "./ThemeSync";

describe("ThemeSync", () => {
  beforeEach(() => {
    h.getMine.mockReset();
    h.setTheme.mockReset();
  });

  it("applies the saved theme", async () => {
    h.getMine.mockResolvedValue({ theme: "violet" });
    render(<ThemeSync />);
    await waitFor(() => expect(h.setTheme).toHaveBeenCalledWith("violet"));
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
  ])("keeps the current theme on %s", async (_, arrange) => {
    arrange();
    render(<ThemeSync />);
    await waitFor(() => expect(h.getMine).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(h.setTheme).not.toHaveBeenCalled();
  });
});
