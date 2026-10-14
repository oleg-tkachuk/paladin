import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { NAV_JUMPS, NAV_PREFIX, jumpTarget } from "@/lib/navShortcuts";

import { KeyboardHelp, SHORTCUTS } from "./KeyboardHelp";

// The dialog used to advertise a "Tables" group (j/k, g g, ⇧G, Enter, Esc, /)
// and a "File browser" group (x, ⇧X, u, Delete) that no handler served. These
// tests tie every listed key to the code that answers it, so a shortcut
// cannot appear in the help again without one.

const DIALOG_TITLE = "Keyboard shortcuts";
// user-event does not add Shift for "?"; the component requires it.
const OPEN_HELP = "{Shift>}?{/Shift}";
const COMMAND_PALETTE_KEYS = ["⌘", "K"];
const HELP_KEYS = ["?"];
const UNSERVED_GROUPS = ["Tables", "File browser"];

describe("KeyboardHelp", () => {
  it("opens on Shift+?", async () => {
    render(<KeyboardHelp />);
    expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument();

    await userEvent.keyboard(OPEN_HELP);

    expect(screen.getByText(DIALOG_TITLE)).toBeInTheDocument();
  });

  it("stays closed while typing ? into an input", async () => {
    render(
      <>
        <input aria-label="search" />
        <KeyboardHelp />
      </>,
    );

    await userEvent.click(screen.getByLabelText("search"));
    await userEvent.keyboard(OPEN_HELP);

    expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument();
  });

  it("lists no Delete shortcut, since no page handles the key", async () => {
    render(<KeyboardHelp />);
    await userEvent.keyboard(OPEN_HELP);

    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).queryByText("Delete selected"),
    ).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Delete")).not.toBeInTheDocument();
  });

  it("shows no unserved group", async () => {
    render(<KeyboardHelp />);
    await userEvent.keyboard(OPEN_HELP);

    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Navigation")).toBeInTheDocument();
    for (const group of UNSERVED_GROUPS) {
      expect(within(dialog).queryByText(group)).not.toBeInTheDocument();
    }
  });

  it("lists only the navigation group", () => {
    expect(SHORTCUTS.map((g) => g.group)).toEqual(["Navigation"]);
  });

  it("lists every g-prefix jump the handler serves, and no other", () => {
    const jumps = SHORTCUTS.flatMap((g) => g.items).filter(
      (s) => s.keys[0] === NAV_PREFIX,
    );
    expect(jumps.map((s) => s.keys)).toEqual(
      NAV_JUMPS.map((j) => [NAV_PREFIX, j.key]),
    );
    for (const s of jumps) {
      expect(jumpTarget(s.keys[1], null)).not.toBeNull();
    }
  });

  it("lists nothing besides the jumps, the palette and the help itself", () => {
    const others = SHORTCUTS.flatMap((g) => g.items)
      .filter((s) => s.keys[0] !== NAV_PREFIX)
      .map((s) => s.keys);
    expect(others).toEqual([HELP_KEYS, COMMAND_PALETTE_KEYS]);
  });
});
