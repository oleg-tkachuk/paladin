import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { KeyboardHelp } from "./KeyboardHelp";

const DIALOG_TITLE = "Keyboard shortcuts";
// user-event does not add Shift for "?"; the component requires it.
const OPEN_HELP = "{Shift>}?{/Shift}";

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
});
